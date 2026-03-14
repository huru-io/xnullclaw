package kube

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// K8s exec stream channels (SPDY/WebSocket sub-protocol).
const (
	channelStdin  = 0
	channelStdout = 1
	channelStderr = 2
	channelError  = 3
)

// Exec runs a command inside a pod and returns its stdout.
// Uses the K8s exec API with a minimal WebSocket client (no external deps).
func (c *Client) Exec(ctx context.Context, pod string, cmd []string) (string, error) {
	// Build exec URL with query params.
	u, err := url.Parse(fmt.Sprintf("%s/api/v1/namespaces/%s/pods/%s/exec",
		c.host, c.namespace, pod))
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("stdout", "true")
	q.Set("stderr", "true")
	q.Set("container", "agent")
	for _, arg := range cmd {
		q.Add("command", arg)
	}
	u.RawQuery = q.Encode()

	// Switch to WebSocket scheme.
	wsURL := strings.Replace(u.String(), "https://", "wss://", 1)
	wsURL = strings.Replace(wsURL, "http://", "ws://", 1)

	conn, err := c.wsConnect(ctx, wsURL)
	if err != nil {
		return "", fmt.Errorf("exec websocket connect: %w", err)
	}
	defer conn.Close()

	// Read all frames, collecting stdout.
	var stdout strings.Builder
	var stderr strings.Builder
	br := bufio.NewReaderSize(conn, 4096)

	var readErr error
	for {
		payload, err := wsReadFrame(br)
		if err != nil {
			if err != io.EOF {
				readErr = fmt.Errorf("websocket read: %w", err)
			}
			break
		}
		if len(payload) == 0 {
			continue
		}

		channel := payload[0]
		data := payload[1:]

		switch channel {
		case channelStdout:
			if stdout.Len()+len(data) > maxFrameSize {
				return stdout.String(), fmt.Errorf("exec stdout too large (>%d bytes)", maxFrameSize)
			}
			stdout.Write(data)
		case channelStderr:
			if stderr.Len()+len(data) > maxFrameSize {
				break // discard excess stderr silently
			}
			stderr.Write(data)
		case channelError:
			// K8s sends a JSON status on channel 3. If it contains
			// "Success" we ignore it; otherwise treat as error.
			msg := string(data)
			if !strings.Contains(msg, `"Success"`) {
				return stdout.String(), fmt.Errorf("exec error: %s", msg)
			}
		}
	}

	return stdout.String(), readErr
}

// wsConnect performs a WebSocket upgrade handshake.
func (c *Client) wsConnect(ctx context.Context, wsURL string) (io.ReadWriteCloser, error) {
	// Convert wss:// back to https:// for the HTTP request.
	httpURL := strings.Replace(wsURL, "wss://", "https://", 1)
	httpURL = strings.Replace(httpURL, "ws://", "http://", 1)

	// Generate WebSocket key.
	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		return nil, err
	}
	wsKey := base64.StdEncoding.EncodeToString(keyBytes)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, httpURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.bearerToken())
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", wsKey)
	req.Header.Set("Sec-WebSocket-Protocol", "v4.channel.k8s.io")

	// Use raw transport to hijack the connection.
	transport := c.httpClient.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}

	resp, err := transport.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusSwitchingProtocols {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("websocket upgrade failed: %s: %s", resp.Status, body)
	}

	// Validate WebSocket accept header.
	expectedAccept := wsAcceptKey(wsKey)
	if resp.Header.Get("Sec-WebSocket-Accept") != expectedAccept {
		resp.Body.Close()
		return nil, fmt.Errorf("invalid Sec-WebSocket-Accept header")
	}

	rwc, ok := resp.Body.(io.ReadWriteCloser)
	if !ok {
		resp.Body.Close()
		return nil, fmt.Errorf("websocket connection does not support writing")
	}
	return rwc, nil
}

// wsAcceptKey computes the expected Sec-WebSocket-Accept value.
func wsAcceptKey(key string) string {
	const magic = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	h := sha1.New()
	h.Write([]byte(key + magic))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// maxFrameSize limits WebSocket frame payloads to 10MB to prevent OOM.
const maxFrameSize = 10 << 20

// wsReadFrame reads a single WebSocket frame and returns its payload.
func wsReadFrame(br *bufio.Reader) ([]byte, error) {
	// Read first 2 bytes: FIN/opcode + mask/length.
	header := make([]byte, 2)
	if _, err := io.ReadFull(br, header); err != nil {
		return nil, err
	}

	// Reject non-zero RSV bits (no extensions negotiated).
	if rsv := (header[0] >> 4) & 0x07; rsv != 0 {
		return nil, fmt.Errorf("websocket: non-zero RSV bits (0x%x)", rsv)
	}

	opcode := header[0] & 0x0f
	masked := (header[1] & 0x80) != 0
	length := uint64(header[1] & 0x7f)

	// Parse extended length for all frame types.
	switch length {
	case 126:
		ext := make([]byte, 2)
		if _, err := io.ReadFull(br, ext); err != nil {
			return nil, err
		}
		length = uint64(binary.BigEndian.Uint16(ext))
	case 127:
		ext := make([]byte, 8)
		if _, err := io.ReadFull(br, ext); err != nil {
			return nil, err
		}
		length = binary.BigEndian.Uint64(ext)
	}

	// Handle control frames after proper length parsing.
	switch opcode {
	case 0x8: // close frame — discard payload (status code + reason) before returning EOF
		if length > 125 {
			return nil, fmt.Errorf("websocket: close frame payload too large: %d", length)
		}
		if length > 0 {
			discard := make([]byte, length)
			if _, err := io.ReadFull(br, discard); err != nil {
				return nil, err
			}
		}
		return nil, io.EOF
	case 0x9, 0xA: // ping, pong — skip (read and discard payload)
		if length > 125 {
			return nil, fmt.Errorf("websocket: control frame payload too large: %d", length)
		}
		if length > 0 {
			discard := make([]byte, length)
			if _, err := io.ReadFull(br, discard); err != nil {
				return nil, err
			}
		}
		return nil, nil // caller will `continue` on empty payload
	}

	if length > maxFrameSize {
		return nil, fmt.Errorf("websocket frame too large: %d bytes (max %d)", length, maxFrameSize)
	}

	var maskKey [4]byte
	if masked {
		if _, err := io.ReadFull(br, maskKey[:]); err != nil {
			return nil, err
		}
	}

	payload := make([]byte, length)
	if _, err := io.ReadFull(br, payload); err != nil {
		return nil, err
	}

	if masked {
		for i := range payload {
			payload[i] ^= maskKey[i%4]
		}
	}

	return payload, nil
}
