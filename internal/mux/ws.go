// ws.go — minimal WebSocket client for connecting to agent web channels.
// Pure RFC 6455 implementation with no external dependencies.
package mux

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Frame size limits.
const (
	maxInboundFrameSize  = 10 << 20   // 10 MB — inbound from agents
	maxOutboundFrameSize = 256 * 1024 // 256 KB — consistent with webhook limit
	maxControlPayload    = 125        // RFC 6455 §5.5: control frame max
)

// Timeouts for write operations and close handshakes.
const (
	writeTimeout = 10 * time.Second // deadline for each write operation
	closeTimeout = 5 * time.Second  // deadline for close frame + TCP close
)

// wsConn wraps a raw TCP connection with WebSocket framing.
type wsConn struct {
	conn            net.Conn
	br              *bufio.Reader
	mu              sync.Mutex     // protects writes
	readDeadlineDur time.Duration  // if >0, refreshed on pong receipt
}

// handshakeTimeout is the maximum time allowed for the WebSocket upgrade handshake,
// applied unconditionally regardless of the caller's context deadline.
const handshakeTimeout = 30 * time.Second

// wsDial connects to a WebSocket server and performs the upgrade handshake.
// Only ws:// URLs are supported — agents are on internal networks.
func wsDial(ctx context.Context, rawURL string) (*wsConn, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}

	if u.Scheme == "wss" {
		return nil, fmt.Errorf("ws: wss scheme not supported (agents use internal network)")
	}

	host := u.Host
	if !strings.Contains(host, ":") {
		host += ":80"
	}

	// Dial with context for timeout/cancellation.
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", host)
	if err != nil {
		return nil, fmt.Errorf("ws dial %s: %w", host, err)
	}

	// Apply handshake deadline: use the shorter of context deadline or handshakeTimeout.
	hsDeadline := time.Now().Add(handshakeTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(hsDeadline) {
		hsDeadline = ctxDeadline
	}
	conn.SetDeadline(hsDeadline)

	// Generate WebSocket key.
	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		conn.Close()
		return nil, err
	}
	wsKey := base64.StdEncoding.EncodeToString(keyBytes)

	// Build HTTP upgrade request.
	path := u.RequestURI()
	reqStr := fmt.Sprintf("GET %s HTTP/1.1\r\n"+
		"Host: %s\r\n"+
		"Connection: Upgrade\r\n"+
		"Upgrade: websocket\r\n"+
		"Sec-WebSocket-Version: 13\r\n"+
		"Sec-WebSocket-Key: %s\r\n"+
		"\r\n", path, u.Host, wsKey)

	if _, err := conn.Write([]byte(reqStr)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("ws handshake write: %w", err)
	}

	// Read response.
	br := bufio.NewReaderSize(conn, 4096)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("ws handshake read: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusSwitchingProtocols {
		conn.Close()
		return nil, fmt.Errorf("ws upgrade failed: HTTP %d", resp.StatusCode)
	}

	// Validate accept key.
	expectedAccept := wsAcceptValue(wsKey)
	if resp.Header.Get("Sec-WebSocket-Accept") != expectedAccept {
		conn.Close()
		return nil, fmt.Errorf("ws invalid accept key")
	}

	// Clear deadline — managed per-operation from here.
	conn.SetDeadline(time.Time{})

	return &wsConn{conn: conn, br: br}, nil
}

// WriteText sends a text WebSocket frame. Client-to-server frames are masked per RFC 6455.
func (c *wsConn) WriteText(data []byte) error {
	if len(data) > maxOutboundFrameSize {
		return fmt.Errorf("ws outbound frame too large: %d bytes (max %d)", len(data), maxOutboundFrameSize)
	}
	return c.writeFrame(0x1, data)
}

// writeFrame writes a masked WebSocket frame with the given opcode and payload.
// The entire frame (header + masked payload) is written in a single Write call
// to prevent partial frames on connection failure.
func (c *wsConn) writeFrame(opcode byte, payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	length := len(payload)

	// Calculate total frame size for single-buffer assembly.
	headerSize := 2 + 4 // base header + mask key
	switch {
	case length <= 125:
		// headerSize already correct
	case length <= 65535:
		headerSize += 2
	default:
		headerSize += 8
	}

	// Assemble complete frame in a single buffer.
	frame := make([]byte, 0, headerSize+length)

	// FIN + opcode.
	frame = append(frame, 0x80|opcode)

	// Encode length with mask bit set.
	switch {
	case length <= 125:
		frame = append(frame, 0x80|byte(length))
	case length <= 65535:
		frame = append(frame, 0x80|126)
		frame = append(frame, byte(length>>8), byte(length))
	default:
		frame = append(frame, 0x80|127)
		lenBytes := make([]byte, 8)
		binary.BigEndian.PutUint64(lenBytes, uint64(length))
		frame = append(frame, lenBytes...)
	}

	// Random mask key.
	maskKey := make([]byte, 4)
	if _, err := rand.Read(maskKey); err != nil {
		return err
	}
	frame = append(frame, maskKey...)

	// Mask payload and append.
	for i, b := range payload {
		frame = append(frame, b^maskKey[i%4])
	}

	// Set write deadline to prevent blocking on dead connections.
	c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	_, err := c.conn.Write(frame)
	c.conn.SetWriteDeadline(time.Time{}) // clear deadline
	return err
}

// ReadMessage reads the next complete WebSocket message, accumulating continuation
// frames per RFC 6455 §5.4. Handles ping/pong/close frames transparently.
func (c *wsConn) ReadMessage() ([]byte, error) {
	var buf []byte // accumulation buffer for fragmented messages
	var fragmented bool

	for {
		payload, opcode, fin, err := c.readFrame()
		if err != nil {
			return nil, err
		}

		switch {
		case opcode == 0x8: // close — echo close frame per RFC 6455 §5.5.1
			c.writeFrame(0x8, payload) // best-effort echo
			return nil, io.EOF
		case opcode == 0x9: // ping — respond with pong (control frames can interleave)
			if len(payload) > maxControlPayload {
				return nil, fmt.Errorf("ws ping payload exceeds RFC 6455 limit: %d", len(payload))
			}
			if err := c.writeFrame(0xA, payload); err != nil {
				return nil, fmt.Errorf("ws pong write failed: %w", err)
			}
			continue
		case opcode == 0xA: // pong — connection is alive, refresh read deadline
			if c.readDeadlineDur > 0 {
				c.conn.SetReadDeadline(time.Now().Add(c.readDeadlineDur))
			}
			continue
		case opcode == 0x1 || opcode == 0x2: // text or binary — start of message
			if fragmented {
				return nil, fmt.Errorf("ws: new message started before previous message completed")
			}
			if fin {
				return payload, nil // single-frame message (common case)
			}
			// First fragment.
			if int64(len(payload)) > maxInboundFrameSize {
				return nil, fmt.Errorf("ws fragmented message too large: %d", len(payload))
			}
			buf = append(buf[:0], payload...)
			fragmented = true
		case opcode == 0x0: // continuation frame
			if !fragmented {
				return nil, fmt.Errorf("ws: continuation frame without initial frame")
			}
			if int64(len(buf))+int64(len(payload)) > maxInboundFrameSize {
				return nil, fmt.Errorf("ws fragmented message too large: %d", int64(len(buf))+int64(len(payload)))
			}
			buf = append(buf, payload...)
			if fin {
				fragmented = false
				result := make([]byte, len(buf))
				copy(result, buf)
				return result, nil
			}
		default:
			continue
		}
	}
}

// readFrame reads a single WebSocket frame and returns its payload, opcode, and FIN bit.
func (c *wsConn) readFrame() ([]byte, byte, bool, error) {
	// Read first 2 bytes.
	header := make([]byte, 2)
	if _, err := io.ReadFull(c.br, header); err != nil {
		return nil, 0, false, err
	}

	fin := (header[0] & 0x80) != 0
	rsv := (header[0] >> 4) & 0x07
	if rsv != 0 {
		return nil, 0, false, fmt.Errorf("ws: non-zero RSV bits (0x%x) without negotiated extension", rsv)
	}
	opcode := header[0] & 0x0f
	masked := (header[1] & 0x80) != 0
	length := uint64(header[1] & 0x7f)

	switch length {
	case 126:
		ext := make([]byte, 2)
		if _, err := io.ReadFull(c.br, ext); err != nil {
			return nil, 0, false, err
		}
		length = uint64(binary.BigEndian.Uint16(ext))
	case 127:
		ext := make([]byte, 8)
		if _, err := io.ReadFull(c.br, ext); err != nil {
			return nil, 0, false, err
		}
		length = binary.BigEndian.Uint64(ext)
	}

	if length > maxInboundFrameSize {
		return nil, 0, false, fmt.Errorf("ws frame too large: %d", length)
	}

	var maskKey [4]byte
	if masked {
		if _, err := io.ReadFull(c.br, maskKey[:]); err != nil {
			return nil, 0, false, err
		}
	}

	payload := make([]byte, length)
	if _, err := io.ReadFull(c.br, payload); err != nil {
		return nil, 0, false, err
	}

	if masked {
		for i := range payload {
			payload[i] ^= maskKey[i%4]
		}
	}

	return payload, opcode, fin, nil
}

// Close sends a close frame and closes the underlying connection.
// Sets a deadline so the close doesn't block on a dead connection.
func (c *wsConn) Close() error {
	// Set a deadline so the close frame write and TCP close don't hang.
	c.conn.SetDeadline(time.Now().Add(closeTimeout))
	c.writeFrame(0x8, nil) // best-effort close frame
	return c.conn.Close()
}

// Ping sends a WebSocket ping frame. Used as a keepalive to detect dead connections.
func (c *wsConn) Ping() error {
	return c.writeFrame(0x9, nil)
}

// SetReadDeadline sets the read deadline on the underlying connection.
// The duration is remembered so that pong frames can refresh the deadline
// (preventing false timeouts on idle-but-alive connections).
func (c *wsConn) SetReadDeadline(d time.Duration) error {
	c.readDeadlineDur = d
	return c.conn.SetReadDeadline(time.Now().Add(d))
}

// wsAcceptValue computes the expected Sec-WebSocket-Accept value per RFC 6455.
func wsAcceptValue(key string) string {
	const magic = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	h := sha1.New()
	h.Write([]byte(key + magic))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}
