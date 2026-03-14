package kube

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// buildFrame constructs a raw WebSocket frame (server → client, unmasked).
func buildFrame(opcode byte, payload []byte) []byte {
	var buf bytes.Buffer

	// FIN + opcode.
	buf.WriteByte(0x80 | opcode)

	// Length (unmasked — server frames are not masked).
	l := len(payload)
	switch {
	case l <= 125:
		buf.WriteByte(byte(l))
	case l <= 65535:
		buf.WriteByte(126)
		b := make([]byte, 2)
		binary.BigEndian.PutUint16(b, uint16(l))
		buf.Write(b)
	default:
		buf.WriteByte(127)
		b := make([]byte, 8)
		binary.BigEndian.PutUint64(b, uint64(l))
		buf.Write(b)
	}

	buf.Write(payload)
	return buf.Bytes()
}

func TestWsReadFrame_SmallPayload(t *testing.T) {
	data := []byte{channelStdout, 'h', 'e', 'l', 'l', 'o'}
	frame := buildFrame(0x2, data) // binary frame
	br := bufio.NewReader(bytes.NewReader(frame))

	payload, err := wsReadFrame(br)
	if err != nil {
		t.Fatalf("wsReadFrame: %v", err)
	}
	if !bytes.Equal(payload, data) {
		t.Errorf("payload = %q, want %q", payload, data)
	}
}

func TestWsReadFrame_CloseFrame(t *testing.T) {
	frame := buildFrame(0x8, []byte{0x03, 0xe8}) // close frame
	br := bufio.NewReader(bytes.NewReader(frame))

	_, err := wsReadFrame(br)
	if err != io.EOF {
		t.Errorf("expected io.EOF for close frame, got %v", err)
	}
}

func TestWsReadFrame_EmptyPayload(t *testing.T) {
	frame := buildFrame(0x2, nil)
	br := bufio.NewReader(bytes.NewReader(frame))

	payload, err := wsReadFrame(br)
	if err != nil {
		t.Fatalf("wsReadFrame: %v", err)
	}
	if len(payload) != 0 {
		t.Errorf("expected empty payload, got %d bytes", len(payload))
	}
}

func TestWsReadFrame_ExtendedLength16(t *testing.T) {
	// Payload > 125 bytes → uses 16-bit extended length.
	data := bytes.Repeat([]byte{0x42}, 200)
	frame := buildFrame(0x2, data)
	br := bufio.NewReader(bytes.NewReader(frame))

	payload, err := wsReadFrame(br)
	if err != nil {
		t.Fatalf("wsReadFrame: %v", err)
	}
	if len(payload) != 200 {
		t.Errorf("payload length = %d, want 200", len(payload))
	}
}

func TestWsReadFrame_ExtendedLength64(t *testing.T) {
	// Payload > 65535 bytes → uses 64-bit extended length.
	data := bytes.Repeat([]byte{0x42}, 70000)
	frame := buildFrame(0x2, data)
	br := bufio.NewReader(bytes.NewReader(frame))

	payload, err := wsReadFrame(br)
	if err != nil {
		t.Fatalf("wsReadFrame: %v", err)
	}
	if len(payload) != 70000 {
		t.Errorf("payload length = %d, want 70000", len(payload))
	}
}

func TestWsReadFrame_MaskedFrame(t *testing.T) {
	// Build a masked frame manually.
	data := []byte{channelStdout, 'A', 'B', 'C'}
	maskKey := [4]byte{0x12, 0x34, 0x56, 0x78}

	var buf bytes.Buffer
	buf.WriteByte(0x82) // FIN + binary
	buf.WriteByte(0x80 | byte(len(data))) // masked + length
	buf.Write(maskKey[:])

	// XOR payload with mask.
	for i, b := range data {
		buf.WriteByte(b ^ maskKey[i%4])
	}

	br := bufio.NewReader(&buf)
	payload, err := wsReadFrame(br)
	if err != nil {
		t.Fatalf("wsReadFrame: %v", err)
	}
	if !bytes.Equal(payload, data) {
		t.Errorf("payload = %x, want %x", payload, data)
	}
}

func TestWsReadFrame_TooLarge(t *testing.T) {
	// Craft a frame header claiming > maxFrameSize.
	var buf bytes.Buffer
	buf.WriteByte(0x82) // FIN + binary
	buf.WriteByte(127)  // 64-bit length follows
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, maxFrameSize+1)
	buf.Write(b)
	// Don't need to write actual payload — should error before reading it.

	br := bufio.NewReader(&buf)
	_, err := wsReadFrame(br)
	if err == nil {
		t.Fatal("expected error for oversized frame")
	}
}

func TestWsReadFrame_MultipleFrames(t *testing.T) {
	// Verify bufio.Reader is properly reused across frames.
	frame1 := buildFrame(0x2, []byte{channelStdout, '1'})
	frame2 := buildFrame(0x2, []byte{channelStdout, '2'})
	frame3 := buildFrame(0x8, nil) // close

	var buf bytes.Buffer
	buf.Write(frame1)
	buf.Write(frame2)
	buf.Write(frame3)

	br := bufio.NewReaderSize(&buf, 4096)

	p1, err := wsReadFrame(br)
	if err != nil {
		t.Fatalf("frame 1: %v", err)
	}
	if p1[1] != '1' {
		t.Errorf("frame 1 data = %q, want '1'", string(p1[1:]))
	}

	p2, err := wsReadFrame(br)
	if err != nil {
		t.Fatalf("frame 2: %v", err)
	}
	if p2[1] != '2' {
		t.Errorf("frame 2 data = %q, want '2'", string(p2[1:]))
	}

	_, err = wsReadFrame(br)
	if err != io.EOF {
		t.Errorf("frame 3: expected EOF, got %v", err)
	}
}

func TestWsReadFrame_TruncatedHeader(t *testing.T) {
	// Only 1 byte — not enough for a complete header.
	br := bufio.NewReader(bytes.NewReader([]byte{0x82}))
	_, err := wsReadFrame(br)
	if err == nil {
		t.Fatal("expected error for truncated header")
	}
}

func TestWsReadFrame_ChannelDispatch(t *testing.T) {
	// Verify Exec dispatches stdout vs stderr vs error channels correctly.
	// Build frames: stdout "out", stderr "err", error with Success, close.
	frames := bytes.Buffer{}
	frames.Write(buildFrame(0x2, []byte{channelStdout, 'o', 'u', 't'}))
	frames.Write(buildFrame(0x2, []byte{channelStderr, 'e', 'r', 'r'}))
	frames.Write(buildFrame(0x2, []byte{channelError, '{', '"', 'S', 'u', 'c', 'c', 'e', 's', 's', '"', '}'}))
	frames.Write(buildFrame(0x8, nil))

	br := bufio.NewReaderSize(&frames, 4096)

	// Simulate Exec's channel dispatch logic.
	var stdout, stderr bytes.Buffer
	for {
		payload, err := wsReadFrame(br)
		if err != nil {
			break
		}
		if len(payload) == 0 {
			continue
		}
		switch payload[0] {
		case channelStdout:
			stdout.Write(payload[1:])
		case channelStderr:
			stderr.Write(payload[1:])
		}
	}

	if stdout.String() != "out" {
		t.Errorf("stdout = %q, want %q", stdout.String(), "out")
	}
	if stderr.String() != "err" {
		t.Errorf("stderr = %q, want %q", stderr.String(), "err")
	}
}

func TestWsReadFrame_PingSkipped(t *testing.T) {
	// A ping frame (opcode 0x9) should return empty payload, not error.
	frames := bytes.Buffer{}
	frames.Write(buildFrame(0x9, []byte("ping-data")))            // ping
	frames.Write(buildFrame(0x2, []byte{channelStdout, 'h', 'i'})) // data
	frames.Write(buildFrame(0x8, nil))                              // close

	br := bufio.NewReaderSize(&frames, 4096)

	// First read: ping frame — should return nil payload (skipped).
	payload, err := wsReadFrame(br)
	if err != nil {
		t.Fatalf("ping frame: unexpected error: %v", err)
	}
	if payload != nil {
		t.Errorf("ping frame: expected nil payload, got %v", payload)
	}

	// Second read: data frame with actual content.
	payload, err = wsReadFrame(br)
	if err != nil {
		t.Fatalf("data frame: unexpected error: %v", err)
	}
	if string(payload) != string([]byte{channelStdout, 'h', 'i'}) {
		t.Errorf("data frame: payload = %v", payload)
	}

	// Third read: close → EOF.
	_, err = wsReadFrame(br)
	if err != io.EOF {
		t.Errorf("close frame: expected EOF, got %v", err)
	}
}

func TestWsReadFrame_PongSkipped(t *testing.T) {
	frames := bytes.Buffer{}
	frames.Write(buildFrame(0xA, nil))                              // pong (no payload)
	frames.Write(buildFrame(0x2, []byte{channelStdout, 'o', 'k'})) // data
	frames.Write(buildFrame(0x8, nil))                              // close

	br := bufio.NewReaderSize(&frames, 4096)

	// Pong: nil payload.
	payload, err := wsReadFrame(br)
	if err != nil {
		t.Fatalf("pong frame: unexpected error: %v", err)
	}
	if payload != nil {
		t.Errorf("pong frame: expected nil payload, got %v", payload)
	}

	// Data frame.
	payload, err = wsReadFrame(br)
	if err != nil {
		t.Fatalf("data frame: unexpected error: %v", err)
	}
	if len(payload) != 3 || payload[0] != channelStdout {
		t.Errorf("data frame: unexpected payload %v", payload)
	}
}

func TestWsReadFrame_NonZeroRSVBits(t *testing.T) {
	// A frame with RSV1 set (bit 6 of first byte) should be rejected.
	var buf bytes.Buffer
	buf.WriteByte(0xC2) // FIN=1, RSV1=1, opcode=binary
	buf.WriteByte(0x03) // length 3
	buf.Write([]byte("abc"))
	br := bufio.NewReader(&buf)

	_, err := wsReadFrame(br)
	if err == nil {
		t.Fatal("expected error for non-zero RSV bits")
	}
	if !strings.Contains(err.Error(), "non-zero RSV") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestWsReadFrame_ControlFrameTooLarge(t *testing.T) {
	// A ping frame claiming > 125 bytes of payload should be rejected.
	var buf bytes.Buffer
	buf.WriteByte(0x89) // FIN + ping
	buf.WriteByte(126)  // 16-bit extended length follows
	ext := make([]byte, 2)
	binary.BigEndian.PutUint16(ext, 200)
	buf.Write(ext)
	// Don't write actual payload — should error before reading it.

	br := bufio.NewReader(&buf)
	_, err := wsReadFrame(br)
	if err == nil {
		t.Fatal("expected error for oversized control frame")
	}
	if !strings.Contains(err.Error(), "control frame payload too large") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestExec_Integration tests the full Exec() function end-to-end by running a
// test HTTP server that performs the WebSocket upgrade handshake and sends
// K8s exec protocol frames back to the client.
func TestExec_Integration(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Validate the exec URL structure.
		if !strings.Contains(r.URL.Path, "/exec") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("stdout") != "true" {
			t.Error("expected stdout=true")
		}
		cmds := r.URL.Query()["command"]
		if len(cmds) == 0 {
			t.Error("expected at least one command")
		}

		// Validate WebSocket upgrade headers.
		if r.Header.Get("Upgrade") != "websocket" {
			t.Error("expected Upgrade: websocket")
		}
		wsKey := r.Header.Get("Sec-WebSocket-Key")
		if wsKey == "" {
			t.Error("expected Sec-WebSocket-Key")
		}

		// Compute accept key.
		const magic = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
		h := sha1.New()
		h.Write([]byte(wsKey + magic))
		acceptKey := base64.StdEncoding.EncodeToString(h.Sum(nil))

		// Hijack the connection.
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("server does not support hijacking")
		}
		conn, buf, err := hj.Hijack()
		if err != nil {
			t.Fatalf("hijack: %v", err)
		}
		defer conn.Close()

		// Send upgrade response.
		buf.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
		buf.WriteString("Upgrade: websocket\r\n")
		buf.WriteString("Connection: Upgrade\r\n")
		buf.WriteString("Sec-WebSocket-Accept: " + acceptKey + "\r\n")
		buf.WriteString("Sec-WebSocket-Protocol: v4.channel.k8s.io\r\n")
		buf.WriteString("\r\n")
		buf.Flush()

		// Send stdout frame: channel byte + data.
		stdoutData := []byte{channelStdout}
		stdoutData = append(stdoutData, []byte("hello from exec")...)
		conn.Write(buildFrame(0x2, stdoutData))

		// Send stderr frame.
		stderrData := []byte{channelStderr}
		stderrData = append(stderrData, []byte("some stderr")...)
		conn.Write(buildFrame(0x2, stderrData))

		// Send success status on error channel.
		errData := []byte{channelError}
		errData = append(errData, []byte(`{"status":"Success"}`)...)
		conn.Write(buildFrame(0x2, errData))

		// Close frame.
		conn.Write(buildFrame(0x8, nil))
	}))
	defer srv.Close()

	c := NewFromConfig(srv.URL, "test-token", "default", srv.Client())

	stdout, err := c.Exec(context.Background(), "test-pod", []string{"echo", "hello"})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if stdout != "hello from exec" {
		t.Errorf("stdout = %q, want %q", stdout, "hello from exec")
	}
}

// TestExec_ErrorStatus tests that Exec returns an error when the K8s exec
// error channel reports a non-Success status.
func TestExec_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wsKey := r.Header.Get("Sec-WebSocket-Key")
		const magic = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
		h := sha1.New()
		h.Write([]byte(wsKey + magic))
		acceptKey := base64.StdEncoding.EncodeToString(h.Sum(nil))

		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("no hijacker")
		}
		conn, buf, err := hj.Hijack()
		if err != nil {
			t.Fatalf("hijack: %v", err)
		}
		defer conn.Close()

		buf.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
		buf.WriteString("Upgrade: websocket\r\n")
		buf.WriteString("Connection: Upgrade\r\n")
		buf.WriteString("Sec-WebSocket-Accept: " + acceptKey + "\r\n")
		buf.WriteString("Sec-WebSocket-Protocol: v4.channel.k8s.io\r\n")
		buf.WriteString("\r\n")
		buf.Flush()

		// Send error status (non-Success).
		errData := []byte{channelError}
		errData = append(errData, []byte(`{"status":"Failure","message":"command not found"}`)...)
		conn.Write(buildFrame(0x2, errData))

		conn.Write(buildFrame(0x8, nil))
	}))
	defer srv.Close()

	c := NewFromConfig(srv.URL, "test-token", "default", srv.Client())

	_, err := c.Exec(context.Background(), "test-pod", []string{"nonexistent"})
	if err == nil {
		t.Fatal("expected error for non-Success status")
	}
	if !strings.Contains(err.Error(), "exec error") {
		t.Errorf("error should mention exec error: %v", err)
	}
}

// TestExec_UpgradeFailure tests that Exec returns an error when the server
// rejects the WebSocket upgrade.
func TestExec_UpgradeFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("forbidden"))
	}))
	defer srv.Close()

	c := NewFromConfig(srv.URL, "test-token", "default", srv.Client())

	_, err := c.Exec(context.Background(), "test-pod", []string{"echo"})
	if err == nil {
		t.Fatal("expected error for failed WebSocket upgrade")
	}
	if !strings.Contains(err.Error(), "websocket") {
		t.Errorf("error should mention websocket: %v", err)
	}
}

// ---------------------------------------------------------------------------
// wsWriteFrame tests
// ---------------------------------------------------------------------------

func TestWsWriteFrame_SmallPayload(t *testing.T) {
	payload := []byte{channelStdout, 'h', 'e', 'l', 'l', 'o'}

	var buf bytes.Buffer
	if err := wsWriteFrame(&buf, payload); err != nil {
		t.Fatalf("wsWriteFrame: %v", err)
	}

	raw := buf.Bytes()

	// Verify FIN + binary opcode.
	if raw[0] != 0x82 {
		t.Errorf("first byte = 0x%02x, want 0x82 (FIN + binary)", raw[0])
	}

	// Verify mask bit set and correct length.
	if raw[1]&0x80 == 0 {
		t.Error("mask bit not set")
	}
	length := int(raw[1] & 0x7F)
	if length != len(payload) {
		t.Errorf("length = %d, want %d", length, len(payload))
	}

	// Verify 4-byte mask key exists (bytes 2..5).
	maskKey := raw[2:6]
	maskedPayload := raw[6:]

	if len(maskedPayload) != len(payload) {
		t.Fatalf("masked payload length = %d, want %d", len(maskedPayload), len(payload))
	}

	// Unmask and verify payload.
	for i, b := range maskedPayload {
		unmasked := b ^ maskKey[i%4]
		if unmasked != payload[i] {
			t.Errorf("byte %d: unmasked 0x%02x, want 0x%02x", i, unmasked, payload[i])
		}
	}

	// Roundtrip: wsReadFrame should read it back correctly.
	br := bufio.NewReader(bytes.NewReader(buf.Bytes()))
	got, err := wsReadFrame(br)
	if err != nil {
		t.Fatalf("wsReadFrame roundtrip: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("roundtrip payload = %q, want %q", got, payload)
	}
}

func TestWsWriteFrame_ExtendedLength16(t *testing.T) {
	payload := bytes.Repeat([]byte{0xAB}, 200) // >125, triggers 16-bit extended length

	var buf bytes.Buffer
	if err := wsWriteFrame(&buf, payload); err != nil {
		t.Fatalf("wsWriteFrame: %v", err)
	}

	raw := buf.Bytes()

	// Verify FIN + binary opcode.
	if raw[0] != 0x82 {
		t.Errorf("first byte = 0x%02x, want 0x82", raw[0])
	}

	// Length byte should be 0x80 | 126 (mask bit + 16-bit indicator).
	if raw[1] != (0x80 | 126) {
		t.Errorf("length byte = 0x%02x, want 0x%02x", raw[1], 0x80|126)
	}

	// 16-bit extended length in bytes 2..3.
	extLen := binary.BigEndian.Uint16(raw[2:4])
	if extLen != 200 {
		t.Errorf("extended length = %d, want 200", extLen)
	}

	// Roundtrip via wsReadFrame.
	br := bufio.NewReader(bytes.NewReader(buf.Bytes()))
	got, err := wsReadFrame(br)
	if err != nil {
		t.Fatalf("wsReadFrame roundtrip: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("roundtrip: payload length = %d, want %d", len(got), len(payload))
	}
}

func TestWsWriteFrame_ExtendedLength64(t *testing.T) {
	payload := bytes.Repeat([]byte{0xCD}, 70000) // >65535, triggers 64-bit extended length

	var buf bytes.Buffer
	if err := wsWriteFrame(&buf, payload); err != nil {
		t.Fatalf("wsWriteFrame: %v", err)
	}

	// Roundtrip via wsReadFrame.
	br := bufio.NewReader(bytes.NewReader(buf.Bytes()))
	got, err := wsReadFrame(br)
	if err != nil {
		t.Fatalf("wsReadFrame roundtrip: %v", err)
	}
	if len(got) != 70000 {
		t.Errorf("roundtrip: payload length = %d, want 70000", len(got))
	}
	if !bytes.Equal(got, payload) {
		t.Error("roundtrip: payload content mismatch")
	}
}

func TestWsWriteFrame_EmptyPayload(t *testing.T) {
	var buf bytes.Buffer
	if err := wsWriteFrame(&buf, nil); err != nil {
		t.Fatalf("wsWriteFrame: %v", err)
	}

	raw := buf.Bytes()

	// FIN + binary opcode.
	if raw[0] != 0x82 {
		t.Errorf("first byte = 0x%02x, want 0x82", raw[0])
	}

	// Mask bit set, length 0.
	if raw[1] != 0x80 {
		t.Errorf("length byte = 0x%02x, want 0x80 (masked, length 0)", raw[1])
	}

	// Total frame: 2 header + 4 mask key + 0 payload = 6 bytes.
	if len(raw) != 6 {
		t.Errorf("frame size = %d, want 6", len(raw))
	}

	// Roundtrip via wsReadFrame.
	br := bufio.NewReader(bytes.NewReader(buf.Bytes()))
	got, err := wsReadFrame(br)
	if err != nil {
		t.Fatalf("wsReadFrame roundtrip: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("roundtrip: expected empty payload, got %d bytes", len(got))
	}
}

// ---------------------------------------------------------------------------
// ExecWithStdin integration tests
// ---------------------------------------------------------------------------

func TestExecWithStdin_Integration(t *testing.T) {
	const stdinData = "hello stdin"
	const stdoutReply = "got your stdin"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Validate stdin=true in query.
		if r.URL.Query().Get("stdin") != "true" {
			t.Error("expected stdin=true")
		}

		// Perform WebSocket upgrade.
		wsKey := r.Header.Get("Sec-WebSocket-Key")
		const magic = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
		h := sha1.New()
		h.Write([]byte(wsKey + magic))
		acceptKey := base64.StdEncoding.EncodeToString(h.Sum(nil))

		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("server does not support hijacking")
		}
		conn, bw, err := hj.Hijack()
		if err != nil {
			t.Fatalf("hijack: %v", err)
		}
		defer conn.Close()

		bw.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
		bw.WriteString("Upgrade: websocket\r\n")
		bw.WriteString("Connection: Upgrade\r\n")
		bw.WriteString("Sec-WebSocket-Accept: " + acceptKey + "\r\n")
		bw.WriteString("Sec-WebSocket-Protocol: v4.channel.k8s.io\r\n")
		bw.WriteString("\r\n")
		bw.Flush()

		// Read stdin frames from the client (client frames are masked).
		br := bufio.NewReaderSize(conn, 4096)
		var received bytes.Buffer
		for {
			payload, err := wsReadFrame(br)
			if err != nil {
				break
			}
			if len(payload) == 0 {
				continue
			}
			if payload[0] != channelStdin {
				t.Errorf("expected channel %d, got %d", channelStdin, payload[0])
				continue
			}
			received.Write(payload[1:])
			// We know the test sends a small payload, so one frame is enough.
			break
		}

		if received.String() != stdinData {
			t.Errorf("server received stdin = %q, want %q", received.String(), stdinData)
		}

		// Send stdout reply.
		stdoutFrame := []byte{channelStdout}
		stdoutFrame = append(stdoutFrame, []byte(stdoutReply)...)
		conn.Write(buildFrame(0x2, stdoutFrame))

		// Send Success status.
		errFrame := []byte{channelError}
		errFrame = append(errFrame, []byte(`{"status":"Success"}`)...)
		conn.Write(buildFrame(0x2, errFrame))

		// Close.
		conn.Write(buildFrame(0x8, nil))
	}))
	defer srv.Close()

	c := NewFromConfig(srv.URL, "test-token", "default", srv.Client())

	stdout, err := c.ExecWithStdin(
		context.Background(),
		"test-pod",
		[]string{"cat"},
		strings.NewReader(stdinData),
	)
	if err != nil {
		t.Fatalf("ExecWithStdin: %v", err)
	}
	if stdout != stdoutReply {
		t.Errorf("stdout = %q, want %q", stdout, stdoutReply)
	}
}

func TestExecWithStdin_LargeStdin(t *testing.T) {
	// 100KB of stdin data — tests chunking at the 32KB boundary.
	const totalSize = 100 * 1024
	inputData := bytes.Repeat([]byte{0x5A}, totalSize)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wsKey := r.Header.Get("Sec-WebSocket-Key")
		const magic = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
		h := sha1.New()
		h.Write([]byte(wsKey + magic))
		acceptKey := base64.StdEncoding.EncodeToString(h.Sum(nil))

		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("server does not support hijacking")
		}
		conn, bw, err := hj.Hijack()
		if err != nil {
			t.Fatalf("hijack: %v", err)
		}
		defer conn.Close()

		bw.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
		bw.WriteString("Upgrade: websocket\r\n")
		bw.WriteString("Connection: Upgrade\r\n")
		bw.WriteString("Sec-WebSocket-Accept: " + acceptKey + "\r\n")
		bw.WriteString("Sec-WebSocket-Protocol: v4.channel.k8s.io\r\n")
		bw.WriteString("\r\n")
		bw.Flush()

		// Collect all stdin frames until no more data.
		br := bufio.NewReaderSize(conn, 65536)
		var collected bytes.Buffer
		for {
			payload, err := wsReadFrame(br)
			if err != nil {
				break
			}
			if len(payload) == 0 {
				continue
			}
			if payload[0] != channelStdin {
				continue
			}
			collected.Write(payload[1:])
			if collected.Len() >= totalSize {
				break
			}
		}

		if collected.Len() != totalSize {
			t.Errorf("server received %d bytes of stdin, want %d", collected.Len(), totalSize)
		}

		// Send stdout confirming receipt size.
		reply := fmt.Sprintf("received:%d", collected.Len())
		stdoutFrame := []byte{channelStdout}
		stdoutFrame = append(stdoutFrame, []byte(reply)...)
		conn.Write(buildFrame(0x2, stdoutFrame))

		// Success status + close.
		errFrame := []byte{channelError}
		errFrame = append(errFrame, []byte(`{"status":"Success"}`)...)
		conn.Write(buildFrame(0x2, errFrame))
		conn.Write(buildFrame(0x8, nil))
	}))
	defer srv.Close()

	c := NewFromConfig(srv.URL, "test-token", "default", srv.Client())

	stdout, err := c.ExecWithStdin(
		context.Background(),
		"test-pod",
		[]string{"cat"},
		bytes.NewReader(inputData),
	)
	if err != nil {
		t.Fatalf("ExecWithStdin: %v", err)
	}

	expected := fmt.Sprintf("received:%d", totalSize)
	if stdout != expected {
		t.Errorf("stdout = %q, want %q", stdout, expected)
	}
}
