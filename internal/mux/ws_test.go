package mux

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ---------- wsAcceptValue ----------

func TestWsAcceptValue_RFC6455(t *testing.T) {
	// RFC 6455 §4.2.2 example: the spec mandates this exact output.
	key := "dGhlIHNhbXBsZSBub25jZQ=="
	want := "s3pPLMBiTxaQ9kYGzzhZRbK+xOo="
	got := wsAcceptValue(key)
	if got != want {
		t.Fatalf("wsAcceptValue(%q) = %q, want %q", key, got, want)
	}
}

func TestWsAcceptValue_DeterministicAndUnique(t *testing.T) {
	// Same input always produces same output.
	a := wsAcceptValue("abc")
	b := wsAcceptValue("abc")
	if a != b {
		t.Fatal("wsAcceptValue is not deterministic")
	}
	// Different inputs produce different outputs.
	c := wsAcceptValue("xyz")
	if a == c {
		t.Fatal("wsAcceptValue produced same output for different inputs")
	}
}

// ---------- helpers ----------

// writeServerFrame writes an unmasked WebSocket frame (server-to-client) to w.
// Safe to call from goroutines — does not use testing.T for failure reporting.
func writeServerFrame(_ *testing.T, w io.Writer, opcode byte, payload []byte) {
	var buf []byte
	buf = append(buf, 0x80|opcode) // FIN + opcode

	length := len(payload)
	switch {
	case length <= 125:
		buf = append(buf, byte(length)) // no mask bit
	case length <= 65535:
		buf = append(buf, 126)
		buf = append(buf, byte(length>>8), byte(length))
	default:
		buf = append(buf, 127)
		lenBytes := make([]byte, 8)
		binary.BigEndian.PutUint64(lenBytes, uint64(length))
		buf = append(buf, lenBytes...)
	}
	buf = append(buf, payload...)
	// Write errors on pipes indicate the other end closed — not a test concern.
	w.Write(buf)
}

// readMaskedFrame reads a masked client-to-server frame from r and returns
// the unmasked payload and opcode.
func readMaskedFrame(t *testing.T, r io.Reader) ([]byte, byte) {
	t.Helper()
	br := bufio.NewReader(r)

	header := make([]byte, 2)
	if _, err := io.ReadFull(br, header); err != nil {
		t.Fatalf("readMaskedFrame header: %v", err)
	}
	opcode := header[0] & 0x0f
	masked := (header[1] & 0x80) != 0
	if !masked {
		t.Fatal("readMaskedFrame: expected masked frame from client")
	}
	length := uint64(header[1] & 0x7f)
	switch length {
	case 126:
		ext := make([]byte, 2)
		if _, err := io.ReadFull(br, ext); err != nil {
			t.Fatalf("readMaskedFrame ext16: %v", err)
		}
		length = uint64(binary.BigEndian.Uint16(ext))
	case 127:
		ext := make([]byte, 8)
		if _, err := io.ReadFull(br, ext); err != nil {
			t.Fatalf("readMaskedFrame ext64: %v", err)
		}
		length = binary.BigEndian.Uint64(ext)
	}

	maskKey := make([]byte, 4)
	if _, err := io.ReadFull(br, maskKey); err != nil {
		t.Fatalf("readMaskedFrame mask: %v", err)
	}

	payload := make([]byte, length)
	if _, err := io.ReadFull(br, payload); err != nil {
		t.Fatalf("readMaskedFrame payload: %v", err)
	}

	// Unmask.
	for i := range payload {
		payload[i] ^= maskKey[i%4]
	}
	return payload, opcode
}

// ---------- writeFrame + readFrame round-trip ----------

func TestRoundTrip_SmallPayload(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ws := &wsConn{conn: client, br: bufio.NewReader(client)}
	msg := []byte("hello websocket")

	// Server writes an unmasked frame; client reads it via ReadMessage.
	go writeServerFrame(t, server, 0x1, msg)

	got, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if !bytes.Equal(got, msg) {
		t.Fatalf("payload = %q, want %q", got, msg)
	}
}

func TestRoundTrip_MediumPayload(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ws := &wsConn{conn: client, br: bufio.NewReader(client)}
	msg := bytes.Repeat([]byte("A"), 1000) // 1000 bytes — uses 16-bit extended length

	go writeServerFrame(t, server, 0x1, msg)

	got, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if !bytes.Equal(got, msg) {
		t.Fatalf("payload length = %d, want %d", len(got), len(msg))
	}
}

func TestRoundTrip_EmptyPayload(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ws := &wsConn{conn: client, br: bufio.NewReader(client)}

	go writeServerFrame(t, server, 0x1, nil)

	got, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("payload length = %d, want 0", len(got))
	}
}

func TestWriteText_SmallPayload(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ws := &wsConn{conn: client, br: bufio.NewReader(client)}
	msg := []byte("from client")

	go func() {
		if err := ws.WriteText(msg); err != nil {
			t.Errorf("WriteText: %v", err)
		}
	}()

	payload, opcode := readMaskedFrame(t, server)
	if opcode != 0x1 {
		t.Fatalf("opcode = 0x%x, want 0x1", opcode)
	}
	if !bytes.Equal(payload, msg) {
		t.Fatalf("payload = %q, want %q", payload, msg)
	}
}

func TestWriteText_MediumPayload(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ws := &wsConn{conn: client, br: bufio.NewReader(client)}
	msg := bytes.Repeat([]byte("B"), 500) // 16-bit extended length

	go func() {
		if err := ws.WriteText(msg); err != nil {
			t.Errorf("WriteText: %v", err)
		}
	}()

	payload, opcode := readMaskedFrame(t, server)
	if opcode != 0x1 {
		t.Fatalf("opcode = 0x%x, want 0x1", opcode)
	}
	if !bytes.Equal(payload, msg) {
		t.Fatalf("payload length = %d, want %d", len(payload), len(msg))
	}
}

func TestWriteText_LargePayload_64bitLength(t *testing.T) {
	// Payload > 65535 triggers the 64-bit length encoding path.
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ws := &wsConn{conn: client, br: bufio.NewReader(client)}
	msg := bytes.Repeat([]byte("C"), 70000)

	go func() {
		if err := ws.WriteText(msg); err != nil {
			t.Errorf("WriteText: %v", err)
		}
	}()

	payload, opcode := readMaskedFrame(t, server)
	if opcode != 0x1 {
		t.Fatalf("opcode = 0x%x, want 0x1", opcode)
	}
	if !bytes.Equal(payload, msg) {
		t.Fatalf("payload length = %d, want %d", len(payload), len(msg))
	}
}

// ---------- ReadMessage opcode dispatch ----------

func TestReadMessage_TextFrame(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ws := &wsConn{conn: client, br: bufio.NewReader(client)}
	msg := []byte("text frame")

	go writeServerFrame(t, server, 0x1, msg)

	got, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if !bytes.Equal(got, msg) {
		t.Fatalf("payload = %q, want %q", got, msg)
	}
}

func TestReadMessage_BinaryFrame(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ws := &wsConn{conn: client, br: bufio.NewReader(client)}
	msg := []byte{0x00, 0x01, 0x02, 0xff}

	go writeServerFrame(t, server, 0x2, msg)

	got, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if !bytes.Equal(got, msg) {
		t.Fatalf("payload = %x, want %x", got, msg)
	}
}

func TestReadMessage_CloseFrame(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ws := &wsConn{conn: client, br: bufio.NewReader(client)}

	go func() {
		// Send close frame.
		writeServerFrame(t, server, 0x8, nil)
		// Drain the close echo that ReadMessage sends back (net.Pipe is synchronous).
		readMaskedFrame(t, server)
	}()

	_, err := ws.ReadMessage()
	if err != io.EOF {
		t.Fatalf("err = %v, want io.EOF", err)
	}
}

func TestReadMessage_PingSendsPong(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ws := &wsConn{conn: client, br: bufio.NewReader(client)}
	pingPayload := []byte("ping-data")
	textPayload := []byte("after-ping")

	go func() {
		// Send a ping frame.
		writeServerFrame(t, server, 0x9, pingPayload)

		// The client will respond with a pong before reading the next frame.
		// We must read the pong here (net.Pipe is synchronous) before
		// sending the text frame, otherwise both sides deadlock.
		pongData, opcode := readMaskedFrame(t, server)
		if opcode != 0xA {
			t.Errorf("expected pong opcode 0xA, got 0x%x", opcode)
		}
		if !bytes.Equal(pongData, pingPayload) {
			t.Errorf("pong payload = %q, want %q", pongData, pingPayload)
		}

		// Now send the text frame that ReadMessage will return.
		writeServerFrame(t, server, 0x1, textPayload)
	}()

	// ReadMessage should skip the ping (sending pong) and return the text frame.
	got, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if !bytes.Equal(got, textPayload) {
		t.Fatalf("payload = %q, want %q", got, textPayload)
	}
}

func TestReadMessage_PongIgnored(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ws := &wsConn{conn: client, br: bufio.NewReader(client)}
	textPayload := []byte("after-pong")

	go func() {
		// Send an unsolicited pong followed by a text frame.
		writeServerFrame(t, server, 0xA, []byte("pong-data"))
		writeServerFrame(t, server, 0x1, textPayload)
	}()

	got, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if !bytes.Equal(got, textPayload) {
		t.Fatalf("payload = %q, want %q", got, textPayload)
	}
}

func TestReadMessage_UnknownOpcodeSkipped(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ws := &wsConn{conn: client, br: bufio.NewReader(client)}
	textPayload := []byte("real-data")

	go func() {
		// Send a frame with an unknown opcode, then a text frame.
		writeServerFrame(t, server, 0xF, []byte("unknown"))
		writeServerFrame(t, server, 0x1, textPayload)
	}()

	got, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if !bytes.Equal(got, textPayload) {
		t.Fatalf("payload = %q, want %q", got, textPayload)
	}
}

// ---------- WriteText size limit ----------

func TestWriteText_ExceedsMaxOutboundSize(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ws := &wsConn{conn: client, br: bufio.NewReader(client)}
	big := make([]byte, maxOutboundFrameSize+1)

	err := ws.WriteText(big)
	if err == nil {
		t.Fatal("expected error for oversized frame")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWriteText_ExactlyMaxOutboundSize(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ws := &wsConn{conn: client, br: bufio.NewReader(client)}
	msg := make([]byte, maxOutboundFrameSize) // exactly at limit — should succeed

	go func() {
		// Drain the frame from the other end so the write doesn't block.
		readMaskedFrame(t, server)
	}()

	if err := ws.WriteText(msg); err != nil {
		t.Fatalf("WriteText at max size failed: %v", err)
	}
}

// ---------- Ping payload too large ----------

func TestReadMessage_PingPayloadTooLarge(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ws := &wsConn{conn: client, br: bufio.NewReader(client)}
	oversizedPing := bytes.Repeat([]byte("x"), maxControlPayload+1) // 126 bytes

	go writeServerFrame(t, server, 0x9, oversizedPing)

	_, err := ws.ReadMessage()
	if err == nil {
		t.Fatal("expected error for oversized ping payload")
	}
	if !strings.Contains(err.Error(), "RFC 6455") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReadMessage_PingExactlyMaxControlPayload(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ws := &wsConn{conn: client, br: bufio.NewReader(client)}
	pingPayload := bytes.Repeat([]byte("x"), maxControlPayload) // exactly 125 bytes — valid
	textPayload := []byte("after-big-ping")

	go func() {
		writeServerFrame(t, server, 0x9, pingPayload)
		// Read the pong before sending next frame (net.Pipe is synchronous).
		readMaskedFrame(t, server)
		writeServerFrame(t, server, 0x1, textPayload)
	}()

	got, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if !bytes.Equal(got, textPayload) {
		t.Fatalf("payload = %q, want %q", got, textPayload)
	}
}

// ---------- Frame too large ----------

func TestReadFrame_ExceedsMaxInboundSize(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ws := &wsConn{conn: client, br: bufio.NewReader(client)}

	// Craft a frame header that claims a payload larger than maxInboundFrameSize
	// without actually sending that much data.
	go func() {
		var buf []byte
		buf = append(buf, 0x81) // FIN + text
		buf = append(buf, 127) // 64-bit length
		lenBytes := make([]byte, 8)
		binary.BigEndian.PutUint64(lenBytes, uint64(maxInboundFrameSize+1))
		buf = append(buf, lenBytes...)
		server.Write(buf)
	}()

	_, err := ws.ReadMessage()
	if err == nil {
		t.Fatal("expected error for oversized frame")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------- readFrame length encoding branches ----------

func TestReadFrame_16BitLength(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ws := &wsConn{conn: client, br: bufio.NewReader(client)}
	msg := bytes.Repeat([]byte("D"), 300) // triggers 16-bit encoding

	go writeServerFrame(t, server, 0x1, msg)

	got, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if !bytes.Equal(got, msg) {
		t.Fatalf("payload length = %d, want %d", len(got), len(msg))
	}
}

func TestReadFrame_64BitLength(t *testing.T) {
	// Use a payload just over 65535 to trigger the 64-bit length path.
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ws := &wsConn{conn: client, br: bufio.NewReader(client)}
	msg := bytes.Repeat([]byte("E"), 65536)

	go writeServerFrame(t, server, 0x1, msg)

	got, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if !bytes.Equal(got, msg) {
		t.Fatalf("payload length = %d, want %d", len(got), len(msg))
	}
}

// ---------- readFrame with masked data ----------

func TestReadFrame_MaskedServerFrame(t *testing.T) {
	// Verify readFrame correctly unmasks a masked frame (unusual for server
	// but the code handles it generically).
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ws := &wsConn{conn: client, br: bufio.NewReader(client)}
	msg := []byte("masked-server-frame")
	maskKey := [4]byte{0x12, 0x34, 0x56, 0x78}

	go func() {
		var buf []byte
		buf = append(buf, 0x81)                   // FIN + text
		buf = append(buf, 0x80|byte(len(msg)))     // mask bit set + length
		buf = append(buf, maskKey[:]...)
		masked := make([]byte, len(msg))
		for i, b := range msg {
			masked[i] = b ^ maskKey[i%4]
		}
		buf = append(buf, masked...)
		server.Write(buf)
	}()

	got, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if !bytes.Equal(got, msg) {
		t.Fatalf("payload = %q, want %q", got, msg)
	}
}

// ---------- Close ----------

func TestClose(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()

	ws := &wsConn{conn: client, br: bufio.NewReader(client)}

	go func() {
		// Read the close frame the client sends.
		payload, opcode := readMaskedFrame(t, server)
		if opcode != 0x8 {
			t.Errorf("expected close opcode 0x8, got 0x%x", opcode)
		}
		if len(payload) != 0 {
			t.Errorf("close payload length = %d, want 0", len(payload))
		}
	}()

	if err := ws.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// ---------- wsDial via httptest ----------

func TestWsDial_SuccessfulHandshake(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "websocket" {
			t.Errorf("missing Upgrade header")
		}
		if r.Header.Get("Sec-WebSocket-Version") != "13" {
			t.Errorf("wrong websocket version")
		}

		key := r.Header.Get("Sec-WebSocket-Key")
		accept := wsAcceptValue(key)

		w.Header().Set("Upgrade", "websocket")
		w.Header().Set("Connection", "Upgrade")
		w.Header().Set("Sec-WebSocket-Accept", accept)
		w.WriteHeader(http.StatusSwitchingProtocols)
	}))
	defer srv.Close()

	// Convert http://... to ws://...
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ws, err := wsDial(ctx, wsURL)
	if err != nil {
		t.Fatalf("wsDial: %v", err)
	}
	ws.Close()
}

func TestWsDial_WrongAcceptKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Upgrade", "websocket")
		w.Header().Set("Connection", "Upgrade")
		w.Header().Set("Sec-WebSocket-Accept", "wrong-accept-key")
		w.WriteHeader(http.StatusSwitchingProtocols)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := wsDial(ctx, wsURL)
	if err == nil {
		t.Fatal("expected error for wrong accept key")
	}
	if !strings.Contains(err.Error(), "invalid accept key") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWsDial_Non101Status(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := wsDial(ctx, wsURL)
	if err == nil {
		t.Fatal("expected error for non-101 status")
	}
	if !strings.Contains(err.Error(), "HTTP 400") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWsDial_InvalidURL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := wsDial(ctx, "ws://127.0.0.1:0/bad")
	if err == nil {
		t.Fatal("expected error for unreachable host")
	}
}

func TestWsDial_HandshakeRequestPath(t *testing.T) {
	// Verify the request path is sent correctly.
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		key := r.Header.Get("Sec-WebSocket-Key")
		accept := wsAcceptValue(key)
		w.Header().Set("Upgrade", "websocket")
		w.Header().Set("Connection", "Upgrade")
		w.Header().Set("Sec-WebSocket-Accept", accept)
		w.WriteHeader(http.StatusSwitchingProtocols)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/my/path"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ws, err := wsDial(ctx, wsURL)
	if err != nil {
		t.Fatalf("wsDial: %v", err)
	}
	ws.Close()

	if gotPath != "/my/path" {
		t.Fatalf("request path = %q, want %q", gotPath, "/my/path")
	}
}

// ---------- wsDial full round-trip ----------

func TestWsDial_FullRoundTrip(t *testing.T) {
	// Hijack the connection to do real WebSocket framing after the handshake.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Sec-WebSocket-Key")
		accept := wsAcceptValue(key)

		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("server doesn't support hijacking")
			return
		}

		// Write the upgrade response manually.
		conn, bufrw, err := hj.Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		defer conn.Close()

		resp := fmt.Sprintf("HTTP/1.1 101 Switching Protocols\r\n"+
			"Upgrade: websocket\r\n"+
			"Connection: Upgrade\r\n"+
			"Sec-WebSocket-Accept: %s\r\n"+
			"\r\n", accept)
		bufrw.WriteString(resp)
		bufrw.Flush()

		// Read a client frame (masked).
		header := make([]byte, 2)
		if _, err := io.ReadFull(conn, header); err != nil {
			t.Errorf("server read header: %v", err)
			return
		}
		length := int(header[1] & 0x7f)
		maskKey := make([]byte, 4)
		io.ReadFull(conn, maskKey)
		payload := make([]byte, length)
		io.ReadFull(conn, payload)
		for i := range payload {
			payload[i] ^= maskKey[i%4]
		}

		// Echo it back as an unmasked server frame.
		echo := append([]byte("echo:"), payload...)
		var frame []byte
		frame = append(frame, 0x81) // FIN + text
		frame = append(frame, byte(len(echo)))
		frame = append(frame, echo...)
		conn.Write(frame)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ws, err := wsDial(ctx, wsURL)
	if err != nil {
		t.Fatalf("wsDial: %v", err)
	}
	defer ws.Close()

	if err := ws.WriteText([]byte("hello")); err != nil {
		t.Fatalf("WriteText: %v", err)
	}

	got, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if string(got) != "echo:hello" {
		t.Fatalf("got = %q, want %q", got, "echo:hello")
	}
}

// ---------- wsAcceptValue internals ----------

func TestWsAcceptValue_MatchesManualComputation(t *testing.T) {
	key := "testkey123"
	magic := "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	h := sha1.New()
	h.Write([]byte(key + magic))
	want := base64.StdEncoding.EncodeToString(h.Sum(nil))

	got := wsAcceptValue(key)
	if got != want {
		t.Fatalf("wsAcceptValue(%q) = %q, want %q", key, got, want)
	}
}

// ---------- Connection closed during read ----------

func TestReadMessage_ConnectionClosed(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()

	ws := &wsConn{conn: client, br: bufio.NewReader(client)}

	// Close the server side immediately — read should fail.
	server.Close()

	_, err := ws.ReadMessage()
	if err == nil {
		t.Fatal("expected error when connection is closed")
	}
}

// ---------- wss scheme rejected ----------

func TestWsDial_WssSchemeRejected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := wsDial(ctx, "wss://127.0.0.1:443/ws")
	if err == nil {
		t.Fatal("expected error for wss scheme")
	}
	if !strings.Contains(err.Error(), "wss scheme not supported") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------- fragmented messages ----------

// writeServerFragments writes a message as multiple WebSocket frames (no FIN on
// intermediate frames, FIN on the last). This tests ReadMessage's accumulation.
func writeServerFragments(w io.Writer, opcode byte, parts [][]byte) {
	for i, part := range parts {
		var header byte
		if i == 0 {
			header = opcode // first frame: actual opcode, no FIN
		} else {
			header = 0x0 // continuation frame, no FIN
		}
		if i == len(parts)-1 {
			header |= 0x80 // last frame: set FIN
		}

		var buf []byte
		buf = append(buf, header)
		length := len(part)
		switch {
		case length <= 125:
			buf = append(buf, byte(length))
		case length <= 65535:
			buf = append(buf, 126)
			buf = append(buf, byte(length>>8), byte(length))
		default:
			buf = append(buf, 127)
			lenBytes := make([]byte, 8)
			binary.BigEndian.PutUint64(lenBytes, uint64(length))
			buf = append(buf, lenBytes...)
		}
		buf = append(buf, part...)
		w.Write(buf)
	}
}

func TestReadMessage_FragmentedMessage(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ws := &wsConn{conn: client, br: bufio.NewReader(client)}

	go writeServerFragments(server, 0x1, [][]byte{
		[]byte("hello "),
		[]byte("world"),
		[]byte("!"),
	})

	got, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if string(got) != "hello world!" {
		t.Fatalf("payload = %q, want 'hello world!'", got)
	}
}

func TestReadMessage_FragmentedTwoFrames(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ws := &wsConn{conn: client, br: bufio.NewReader(client)}

	go writeServerFragments(server, 0x1, [][]byte{
		[]byte("part1"),
		[]byte("part2"),
	})

	got, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if string(got) != "part1part2" {
		t.Fatalf("payload = %q, want 'part1part2'", got)
	}
}

func TestReadMessage_PingDuringFragmentation(t *testing.T) {
	// RFC 6455 §5.4: control frames can be injected between fragments.
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ws := &wsConn{conn: client, br: bufio.NewReader(client)}

	go func() {
		// First fragment (no FIN).
		var buf []byte
		buf = append(buf, 0x01) // text, no FIN
		buf = append(buf, byte(5))
		buf = append(buf, []byte("hello")...)
		server.Write(buf)

		// Inject a ping between fragments.
		writeServerFrame(t, server, 0x9, []byte("p"))

		// Read the pong reply (net.Pipe is synchronous).
		readMaskedFrame(t, server)

		// Final continuation fragment (FIN).
		buf = buf[:0]
		buf = append(buf, 0x80) // continuation + FIN
		buf = append(buf, byte(6))
		buf = append(buf, []byte(" world")...)
		server.Write(buf)
	}()

	got, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if string(got) != "hello world" {
		t.Fatalf("payload = %q, want 'hello world'", got)
	}
}

func TestReadMessage_ContinuationWithoutInitial(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ws := &wsConn{conn: client, br: bufio.NewReader(client)}

	// Send a continuation frame without a preceding initial frame.
	go func() {
		var buf []byte
		buf = append(buf, 0x80) // continuation + FIN
		buf = append(buf, byte(3))
		buf = append(buf, []byte("bad")...)
		server.Write(buf)
	}()

	_, err := ws.ReadMessage()
	if err == nil {
		t.Fatal("expected error for continuation without initial frame")
	}
	if !strings.Contains(err.Error(), "continuation frame without initial") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReadMessage_NewMessageDuringFragmentation(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ws := &wsConn{conn: client, br: bufio.NewReader(client)}

	// Send a non-FIN text frame, then another text frame (not continuation).
	go func() {
		// First fragment (text, no FIN).
		var buf []byte
		buf = append(buf, 0x01) // text opcode, no FIN
		buf = append(buf, byte(4))
		buf = append(buf, []byte("part")...)
		server.Write(buf)

		// New text frame (opcode 0x1, FIN) — violates RFC 6455.
		buf = buf[:0]
		buf = append(buf, 0x81) // text opcode + FIN
		buf = append(buf, byte(3))
		buf = append(buf, []byte("new")...)
		server.Write(buf)
	}()

	_, err := ws.ReadMessage()
	if err == nil {
		t.Fatal("expected error for new message during fragmentation")
	}
	if !strings.Contains(err.Error(), "new message started before previous message completed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------- RSV bit validation ----------

func TestReadMessage_NonZeroRSVBits(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ws := &wsConn{conn: client, br: bufio.NewReader(client)}

	// Send a frame with RSV1 set (bit 6 of first byte).
	go func() {
		var buf []byte
		buf = append(buf, 0xC1) // FIN=1, RSV1=1, opcode=text (0x80|0x40|0x01)
		buf = append(buf, byte(3))
		buf = append(buf, []byte("bad")...)
		server.Write(buf)
	}()

	_, err := ws.ReadMessage()
	if err == nil {
		t.Fatal("expected error for non-zero RSV bits")
	}
	if !strings.Contains(err.Error(), "non-zero RSV bits") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------- SetReadDeadline ----------

func TestSetReadDeadline(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ws := &wsConn{conn: client, br: bufio.NewReader(client)}

	// Set a very short deadline so the read times out.
	if err := ws.SetReadDeadline(time.Now().Add(10 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}

	_, err := ws.ReadMessage()
	if err == nil {
		t.Fatal("expected timeout error")
	}
}
