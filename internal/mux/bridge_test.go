package mux

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jotavich/xnullclaw/internal/agent"
	"github.com/jotavich/xnullclaw/internal/config"
	"github.com/jotavich/xnullclaw/internal/docker"
	"github.com/jotavich/xnullclaw/internal/tools"
)

// --- fake WebSocket server helpers ---

// fakeWSServer listens on a random port and performs the WebSocket upgrade handshake.
type fakeWSServer struct {
	ln   net.Listener
	port int
}

func newFakeWSServer(t *testing.T) *fakeWSServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	t.Cleanup(func() { ln.Close() })
	return &fakeWSServer{ln: ln, port: port}
}

// accept accepts one connection, performs WebSocket upgrade, and returns the
// server-side net.Conn. The caller owns the conn and must close it.
func (s *fakeWSServer) accept(t *testing.T) net.Conn {
	t.Helper()
	conn, err := s.ln.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	br := bufio.NewReader(conn)
	req, err := http.ReadRequest(br)
	if err != nil {
		conn.Close()
		t.Fatalf("read upgrade request: %v", err)
	}
	req.Body.Close()

	if req.Header.Get("Upgrade") != "websocket" {
		conn.Close()
		t.Fatalf("expected websocket upgrade, got: %s", req.Header.Get("Upgrade"))
	}

	wsKey := req.Header.Get("Sec-WebSocket-Key")
	if wsKey == "" {
		conn.Close()
		t.Fatalf("missing Sec-WebSocket-Key")
	}

	const magic = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	h := sha1.New()
	h.Write([]byte(wsKey + magic))
	acceptVal := base64.StdEncoding.EncodeToString(h.Sum(nil))

	resp := fmt.Sprintf("HTTP/1.1 101 Switching Protocols\r\n"+
		"Connection: Upgrade\r\n"+
		"Upgrade: websocket\r\n"+
		"Sec-WebSocket-Accept: %s\r\n"+
		"\r\n", acceptVal)
	if _, err := conn.Write([]byte(resp)); err != nil {
		conn.Close()
		t.Fatalf("write upgrade response: %v", err)
	}

	conn.SetDeadline(time.Time{})
	return conn
}

// --- bridge test helper ---

// newTestBridge creates a Bridge with all dependencies wired for testing.
// The mode is set to "local" by default (so webSocketURL uses WebPort from docker mock).
func newTestBridge(t *testing.T, bot *mockSender, dops *docker.MockOps) *Bridge {
	t.Helper()

	home := t.TempDir()
	store := testStore(t)
	logger := testLogger(t)
	cfg := config.DefaultConfig()

	var chatID int64 = 42
	var mu sync.Mutex

	backend := &agent.MockBackend{
		ReadTokenFn: func(name string) (string, error) {
			return "test-token-" + name, nil
		},
	}

	b := &Bridge{
		home:    home,
		backend: backend,
		store:   store,
		bot:     bot,
		cfg:     cfg,
		logger:  logger,
		mode:    "local",
		docker:  dops,
		chatID:  &chatID,
		turnMu:  &mu,
		done:    make(chan struct{}),
		conns:   make(map[string]*agentConn),
	}
	t.Cleanup(func() { b.CloseAll() })
	return b
}

// bridgeReadClientFrame reads one masked client-to-server frame from a net.Conn
// and returns the unmasked payload. Uses readMaskedFrame from ws_test.go.
func bridgeReadClientFrame(t *testing.T, conn net.Conn) []byte {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	payload, _ := readMaskedFrame(t, conn)
	return payload
}

// --- tests ---

func TestBridge_IsConnected(t *testing.T) {
	srv := newFakeWSServer(t)

	dops := &docker.MockOps{
		WebPortFn: func(_ context.Context, _ string) (int, error) {
			return srv.port, nil
		},
	}
	bot := &mockSender{}
	b := newTestBridge(t, bot, dops)

	// Before Connect: not connected.
	if b.IsConnected("alice") {
		t.Fatal("expected not connected before Connect")
	}

	// Connect in background, accept on server.
	errCh := make(chan error, 1)
	go func() {
		errCh <- b.Connect(context.Background(), "alice")
	}()

	conn := srv.accept(t)
	defer conn.Close()

	if err := <-errCh; err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// After Connect: connected.
	if !b.IsConnected("alice") {
		t.Fatal("expected connected after Connect")
	}

	// Disconnect: not connected.
	b.Disconnect("alice")
	if b.IsConnected("alice") {
		t.Fatal("expected not connected after Disconnect")
	}
}

func TestBridge_SendHappyPath(t *testing.T) {
	srv := newFakeWSServer(t)

	dops := &docker.MockOps{
		WebPortFn: func(_ context.Context, _ string) (int, error) {
			return srv.port, nil
		},
	}
	bot := &mockSender{}
	b := newTestBridge(t, bot, dops)

	// Connect.
	errCh := make(chan error, 1)
	go func() {
		errCh <- b.Connect(context.Background(), "alice")
	}()

	conn := srv.accept(t)
	defer conn.Close()

	if err := <-errCh; err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Send in background.
	type sendResult struct {
		resp string
		err  error
	}
	resCh := make(chan sendResult, 1)
	go func() {
		resp, err := b.Send(context.Background(), "alice", "hello agent")
		resCh <- sendResult{resp, err}
	}()

	// Read the user_message from server side.
	payload := bridgeReadClientFrame(t, conn)

	// Verify user_message structure.
	var msg struct {
		V       int    `json:"v"`
		Type    string `json:"type"`
		Session string `json:"session_id"`
		Payload struct {
			Content   string `json:"content"`
			AuthToken string `json:"auth_token"`
			SenderID  string `json:"sender_id"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(payload, &msg); err != nil {
		t.Fatalf("unmarshal user_message: %v", err)
	}
	if msg.Type != "user_message" {
		t.Errorf("type = %q, want user_message", msg.Type)
	}
	if msg.Session != "mux" {
		t.Errorf("session_id = %q, want mux", msg.Session)
	}
	if msg.Payload.Content != "hello agent" {
		t.Errorf("content = %q, want 'hello agent'", msg.Payload.Content)
	}

	// Send assistant_final response.
	resp := map[string]any{
		"v":          1,
		"type":       "assistant_final",
		"session_id": "mux",
		"payload": map[string]any{
			"content": "hello from alice",
		},
	}
	respData, _ := json.Marshal(resp)
	writeServerFrame(t, conn, 0x1, respData)

	// Wait for Send result.
	select {
	case res := <-resCh:
		if res.err != nil {
			t.Fatalf("Send error: %v", res.err)
		}
		if res.resp != "hello from alice" {
			t.Errorf("Send response = %q, want 'hello from alice'", res.resp)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Send timed out")
	}
}

func TestBridge_SendConcurrentRejection(t *testing.T) {
	srv := newFakeWSServer(t)

	dops := &docker.MockOps{
		WebPortFn: func(_ context.Context, _ string) (int, error) {
			return srv.port, nil
		},
	}
	bot := &mockSender{}
	b := newTestBridge(t, bot, dops)

	// Connect.
	errCh := make(chan error, 1)
	go func() {
		errCh <- b.Connect(context.Background(), "alice")
	}()

	conn := srv.accept(t)
	defer conn.Close()

	if err := <-errCh; err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// First Send -- will block waiting for response.
	type sendResult struct {
		resp string
		err  error
	}
	res1Ch := make(chan sendResult, 1)
	go func() {
		resp, err := b.Send(context.Background(), "alice", "first")
		res1Ch <- sendResult{resp, err}
	}()

	// Wait for the first Send to register its waiter by reading the client frame.
	bridgeReadClientFrame(t, conn)

	// Second Send should be rejected immediately.
	_, err := b.Send(context.Background(), "alice", "second")
	if err == nil {
		t.Fatal("expected error for concurrent Send")
	}
	if !strings.Contains(err.Error(), "another send to") {
		t.Errorf("unexpected error: %v", err)
	}

	// Unblock the first Send.
	resp := map[string]any{
		"v": 1, "type": "assistant_final", "session_id": "mux",
		"payload": map[string]any{"content": "response"},
	}
	respData, _ := json.Marshal(resp)
	writeServerFrame(t, conn, 0x1, respData)

	select {
	case res := <-res1Ch:
		if res.err != nil {
			t.Fatalf("first Send error: %v", res.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first Send timed out")
	}
}

func TestBridge_SendContextCancellation(t *testing.T) {
	srv := newFakeWSServer(t)

	dops := &docker.MockOps{
		WebPortFn: func(_ context.Context, _ string) (int, error) {
			return srv.port, nil
		},
	}
	bot := &mockSender{}
	b := newTestBridge(t, bot, dops)

	// Connect.
	errCh := make(chan error, 1)
	go func() {
		errCh <- b.Connect(context.Background(), "alice")
	}()

	conn := srv.accept(t)
	defer conn.Close()

	if err := <-errCh; err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Send with a context that will be cancelled.
	ctx, cancel := context.WithCancel(context.Background())

	type sendResult struct {
		resp string
		err  error
	}
	resCh := make(chan sendResult, 1)
	go func() {
		resp, err := b.Send(ctx, "alice", "will be cancelled")
		resCh <- sendResult{resp, err}
	}()

	// Read the user_message so we know the Send has registered its waiter.
	bridgeReadClientFrame(t, conn)

	// Cancel the context.
	cancel()

	select {
	case res := <-resCh:
		if res.err == nil {
			t.Fatal("expected error from cancelled context")
		}
		if !strings.Contains(res.err.Error(), "timeout waiting for response") {
			t.Errorf("unexpected error: %v", res.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Send did not return after context cancellation")
	}
}

func TestBridge_ReadLoopRoutesToWaiter(t *testing.T) {
	srv := newFakeWSServer(t)

	dops := &docker.MockOps{
		WebPortFn: func(_ context.Context, _ string) (int, error) {
			return srv.port, nil
		},
	}
	bot := &mockSender{}
	b := newTestBridge(t, bot, dops)

	// Connect.
	errCh := make(chan error, 1)
	go func() {
		errCh <- b.Connect(context.Background(), "alice")
	}()

	conn := srv.accept(t)
	defer conn.Close()

	if err := <-errCh; err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Send in background -- this registers a waiter.
	type sendResult struct {
		resp string
		err  error
	}
	resCh := make(chan sendResult, 1)
	go func() {
		resp, err := b.Send(context.Background(), "alice", "test msg")
		resCh <- sendResult{resp, err}
	}()

	// Read the user_message.
	bridgeReadClientFrame(t, conn)

	// Server sends assistant_final -- should route to the waiter.
	event := map[string]any{
		"v": 1, "type": "assistant_final", "session_id": "mux",
		"payload": map[string]any{"content": "routed response"},
	}
	data, _ := json.Marshal(event)
	writeServerFrame(t, conn, 0x1, data)

	select {
	case res := <-resCh:
		if res.err != nil {
			t.Fatalf("Send error: %v", res.err)
		}
		if res.resp != "routed response" {
			t.Errorf("response = %q, want 'routed response'", res.resp)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Send timed out")
	}

	// Bot should not have received the message (it was routed to the waiter).
	if len(bot.sent()) != 0 {
		t.Errorf("expected no unsolicited sends, got %d", len(bot.sent()))
	}
}

func TestBridge_ReadLoopDeliversUnsolicited(t *testing.T) {
	srv := newFakeWSServer(t)

	dops := &docker.MockOps{
		WebPortFn: func(_ context.Context, _ string) (int, error) {
			return srv.port, nil
		},
	}
	bot := &mockSender{}
	b := newTestBridge(t, bot, dops)

	// Connect.
	errCh := make(chan error, 1)
	go func() {
		errCh <- b.Connect(context.Background(), "alice")
	}()

	conn := srv.accept(t)
	defer conn.Close()

	if err := <-errCh; err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// No Send in progress -- message should be delivered as unsolicited.
	event := map[string]any{
		"v": 1, "type": "assistant_final", "session_id": "mux",
		"payload": map[string]any{"content": "unsolicited output"},
	}
	data, _ := json.Marshal(event)
	writeServerFrame(t, conn, 0x1, data)

	// Wait for delivery.
	deadline := time.After(5 * time.Second)
	for {
		msgs := bot.sent()
		if len(msgs) > 0 {
			found := false
			for _, m := range msgs {
				if strings.Contains(m.text, "unsolicited output") {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected 'unsolicited output' in sends, got %+v", msgs)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("unsolicited delivery timed out")
		case <-time.After(10 * time.Millisecond):
		}
	}

	// Verify memory store got the message.
	msgs, err := b.store.RecentMessages("bridge", 10)
	if err != nil {
		t.Fatalf("RecentMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 stored message, got %d", len(msgs))
	}
	if !strings.Contains(msgs[0].Content, "[alice]") {
		t.Errorf("stored content should contain [alice], got %q", msgs[0].Content)
	}
	if msgs[0].Stream != "bridge" {
		t.Errorf("stream = %q, want 'bridge'", msgs[0].Stream)
	}
}

func TestBridge_ReadLoopIgnoresWrongSessionID(t *testing.T) {
	srv := newFakeWSServer(t)

	dops := &docker.MockOps{
		WebPortFn: func(_ context.Context, _ string) (int, error) {
			return srv.port, nil
		},
	}
	bot := &mockSender{}
	b := newTestBridge(t, bot, dops)

	// Connect.
	errCh := make(chan error, 1)
	go func() {
		errCh <- b.Connect(context.Background(), "alice")
	}()

	conn := srv.accept(t)
	defer conn.Close()

	if err := <-errCh; err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Send assistant_final with wrong session_id (should be ignored).
	event := map[string]any{
		"v": 1, "type": "assistant_final", "session_id": "wrong-session",
		"payload": map[string]any{"content": "should be ignored"},
	}
	data, _ := json.Marshal(event)
	writeServerFrame(t, conn, 0x1, data)

	// Send a sentinel with correct session_id — when this arrives, the previous
	// frame has certainly been processed by readLoop.
	sentinel := map[string]any{
		"v": 1, "type": "assistant_final", "session_id": "mux",
		"payload": map[string]any{"content": "sentinel"},
	}
	sentinelData, _ := json.Marshal(sentinel)
	writeServerFrame(t, conn, 0x1, sentinelData)

	// Wait for sentinel delivery.
	deadline := time.After(5 * time.Second)
	for {
		msgs := bot.sent()
		if len(msgs) > 0 {
			// Only the sentinel should have been delivered.
			if len(msgs) != 1 {
				t.Errorf("expected 1 send (sentinel only), got %d", len(msgs))
			}
			if !strings.Contains(msgs[0].text, "sentinel") {
				t.Errorf("expected sentinel, got %q", msgs[0].text)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("sentinel delivery timed out")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestBridge_ReadLoopIgnoresNonAssistantFinal(t *testing.T) {
	srv := newFakeWSServer(t)

	dops := &docker.MockOps{
		WebPortFn: func(_ context.Context, _ string) (int, error) {
			return srv.port, nil
		},
	}
	bot := &mockSender{}
	b := newTestBridge(t, bot, dops)

	// Connect.
	errCh := make(chan error, 1)
	go func() {
		errCh <- b.Connect(context.Background(), "alice")
	}()

	conn := srv.accept(t)
	defer conn.Close()

	if err := <-errCh; err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Send various non-assistant_final event types (should all be ignored).
	for _, evType := range []string{"tool_call", "user_message", "assistant_chunk", "system"} {
		event := map[string]any{
			"v": 1, "type": evType, "session_id": "mux",
			"payload": map[string]any{"content": "should be ignored"},
		}
		data, _ := json.Marshal(event)
		writeServerFrame(t, conn, 0x1, data)
	}

	// Sentinel: a valid assistant_final that confirms all previous frames were processed.
	sentinel := map[string]any{
		"v": 1, "type": "assistant_final", "session_id": "mux",
		"payload": map[string]any{"content": "sentinel"},
	}
	sentinelData, _ := json.Marshal(sentinel)
	writeServerFrame(t, conn, 0x1, sentinelData)

	deadline := time.After(5 * time.Second)
	for {
		msgs := bot.sent()
		if len(msgs) > 0 {
			if len(msgs) != 1 {
				t.Errorf("expected 1 send (sentinel only), got %d", len(msgs))
			}
			if !strings.Contains(msgs[0].text, "sentinel") {
				t.Errorf("expected sentinel, got %q", msgs[0].text)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("sentinel delivery timed out")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestBridge_DisconnectDrainsPendingWaiter(t *testing.T) {
	srv := newFakeWSServer(t)

	dops := &docker.MockOps{
		WebPortFn: func(_ context.Context, _ string) (int, error) {
			return srv.port, nil
		},
	}
	bot := &mockSender{}
	b := newTestBridge(t, bot, dops)

	// Connect.
	errCh := make(chan error, 1)
	go func() {
		errCh <- b.Connect(context.Background(), "alice")
	}()

	conn := srv.accept(t)
	defer conn.Close()

	if err := <-errCh; err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Start a Send that will block.
	type sendResult struct {
		resp string
		err  error
	}
	resCh := make(chan sendResult, 1)
	go func() {
		resp, err := b.Send(context.Background(), "alice", "pending msg")
		resCh <- sendResult{resp, err}
	}()

	// Read the user_message so the Send is waiting.
	bridgeReadClientFrame(t, conn)

	// Disconnect while Send is pending.
	b.Disconnect("alice")

	select {
	case res := <-resCh:
		if res.err == nil {
			t.Fatal("expected error when connection disconnected during Send")
		}
		if !errors.Is(res.err, tools.ErrResponseLost) {
			t.Errorf("unexpected error: %v", res.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Send did not return after Disconnect")
	}
}

func TestBridge_CloseAll(t *testing.T) {
	srv1 := newFakeWSServer(t)
	srv2 := newFakeWSServer(t)

	ports := map[string]int{}
	dops := &docker.MockOps{
		WebPortFn: func(_ context.Context, name string) (int, error) {
			p, ok := ports[name]
			if !ok {
				return 0, fmt.Errorf("unknown: %s", name)
			}
			return p, nil
		},
	}
	bot := &mockSender{}
	b := newTestBridge(t, bot, dops)

	cn1 := agent.ContainerName(b.home, "alice")
	cn2 := agent.ContainerName(b.home, "bob")
	ports[cn1] = srv1.port
	ports[cn2] = srv2.port

	// Connect alice.
	errCh := make(chan error, 1)
	go func() {
		errCh <- b.Connect(context.Background(), "alice")
	}()
	conn1 := srv1.accept(t)
	defer conn1.Close()
	if err := <-errCh; err != nil {
		t.Fatalf("Connect alice: %v", err)
	}

	// Connect bob.
	go func() {
		errCh <- b.Connect(context.Background(), "bob")
	}()
	conn2 := srv2.accept(t)
	defer conn2.Close()
	if err := <-errCh; err != nil {
		t.Fatalf("Connect bob: %v", err)
	}

	if !b.IsConnected("alice") || !b.IsConnected("bob") {
		t.Fatal("both should be connected")
	}

	b.CloseAll()

	if b.IsConnected("alice") {
		t.Error("alice should be disconnected after CloseAll")
	}
	if b.IsConnected("bob") {
		t.Error("bob should be disconnected after CloseAll")
	}
}

func TestBridge_WebSocketURL_DockerMode(t *testing.T) {
	dops := &docker.MockOps{}
	bot := &mockSender{}
	b := newTestBridge(t, bot, dops)
	b.mode = "docker"

	wsURL, err := b.webSocketURL(context.Background(), "alice", "mytoken")
	if err != nil {
		t.Fatalf("webSocketURL: %v", err)
	}

	cn := agent.ContainerName(b.home, "alice")
	expectedPrefix := fmt.Sprintf("ws://%s:%d/ws?", cn, docker.WebChannelPort)
	if !strings.HasPrefix(wsURL, expectedPrefix) {
		t.Errorf("url = %q, want prefix %q", wsURL, expectedPrefix)
	}
	if !strings.Contains(wsURL, "token=mytoken") {
		t.Errorf("url should contain token=mytoken: %q", wsURL)
	}
	if !strings.Contains(wsURL, "session_id=mux") {
		t.Errorf("url should contain session_id=mux: %q", wsURL)
	}
}

func TestBridge_WebSocketURL_KubernetesMode(t *testing.T) {
	dops := &docker.MockOps{}
	bot := &mockSender{}
	b := newTestBridge(t, bot, dops)
	b.mode = "kubernetes"

	wsURL, err := b.webSocketURL(context.Background(), "alice", "k8s-token")
	if err != nil {
		t.Fatalf("webSocketURL: %v", err)
	}

	cn := agent.ContainerName(b.home, "alice")
	expectedPrefix := fmt.Sprintf("ws://%s:%d/ws?", cn, docker.WebChannelPort)
	if !strings.HasPrefix(wsURL, expectedPrefix) {
		t.Errorf("url = %q, want prefix %q", wsURL, expectedPrefix)
	}
}

func TestBridge_WebSocketURL_LocalMode(t *testing.T) {
	const localPort = 54321

	dops := &docker.MockOps{
		WebPortFn: func(_ context.Context, _ string) (int, error) {
			return localPort, nil
		},
	}
	bot := &mockSender{}
	b := newTestBridge(t, bot, dops)
	b.mode = "local"

	wsURL, err := b.webSocketURL(context.Background(), "alice", "local-token")
	if err != nil {
		t.Fatalf("webSocketURL: %v", err)
	}

	expectedPrefix := fmt.Sprintf("ws://127.0.0.1:%d/ws?", localPort)
	if !strings.HasPrefix(wsURL, expectedPrefix) {
		t.Errorf("url = %q, want prefix %q", wsURL, expectedPrefix)
	}
}

func TestBridge_WebSocketURL_LocalMode_NoPort(t *testing.T) {
	dops := &docker.MockOps{
		WebPortFn: func(_ context.Context, _ string) (int, error) {
			return 0, nil
		},
	}
	bot := &mockSender{}
	b := newTestBridge(t, bot, dops)
	b.mode = "local"

	_, err := b.webSocketURL(context.Background(), "alice", "tok")
	if err == nil {
		t.Fatal("expected error when WebPort returns 0")
	}
	if !strings.Contains(err.Error(), "no web channel port") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBridge_ConnectTOCTOUSafety(t *testing.T) {
	srv := newFakeWSServer(t)

	dops := &docker.MockOps{
		WebPortFn: func(_ context.Context, _ string) (int, error) {
			return srv.port, nil
		},
	}
	bot := &mockSender{}
	b := newTestBridge(t, bot, dops)

	// First Connect.
	errCh := make(chan error, 1)
	go func() {
		errCh <- b.Connect(context.Background(), "alice")
	}()

	conn := srv.accept(t)
	defer conn.Close()

	if err := <-errCh; err != nil {
		t.Fatalf("first Connect: %v", err)
	}

	// Second Connect should return nil immediately (already connected).
	if err := b.Connect(context.Background(), "alice"); err != nil {
		t.Fatalf("second Connect: %v", err)
	}

	// Should still have exactly one connection.
	b.mu.Lock()
	count := len(b.conns)
	b.mu.Unlock()
	if count != 1 {
		t.Errorf("expected 1 connection, got %d", count)
	}
}

func TestBridge_ConnectTOCTOU_RaceConnect(t *testing.T) {
	srv := newFakeWSServer(t)

	dops := &docker.MockOps{
		WebPortFn: func(_ context.Context, _ string) (int, error) {
			return srv.port, nil
		},
	}
	bot := &mockSender{}
	b := newTestBridge(t, bot, dops)

	// Race two Connects.
	const goroutines = 2
	errCh := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			errCh <- b.Connect(context.Background(), "alice")
		}()
	}

	// Accept connections (may be 1 or 2 depending on timing).
	accepted := 0
	for accepted < goroutines {
		connCh := make(chan net.Conn, 1)
		go func() {
			c, err := srv.ln.Accept()
			if err == nil {
				br := bufio.NewReader(c)
				req, err := http.ReadRequest(br)
				if err != nil {
					c.Close()
					return
				}
				req.Body.Close()
				wsKey := req.Header.Get("Sec-WebSocket-Key")
				const magic = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
				h := sha1.New()
				h.Write([]byte(wsKey + magic))
				acceptVal := base64.StdEncoding.EncodeToString(h.Sum(nil))
				resp := fmt.Sprintf("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Accept: %s\r\n\r\n", acceptVal)
				c.Write([]byte(resp))
				c.SetDeadline(time.Time{})
				connCh <- c
			}
		}()

		select {
		case c := <-connCh:
			defer c.Close()
			accepted++
		case <-time.After(2 * time.Second):
			accepted = goroutines // break
		}
	}

	// Collect results.
	for i := 0; i < goroutines; i++ {
		select {
		case err := <-errCh:
			if err != nil {
				t.Errorf("Connect error: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Connect timed out")
		}
	}

	// Should have exactly one connection in the map.
	b.mu.Lock()
	count := len(b.conns)
	b.mu.Unlock()
	if count != 1 {
		t.Errorf("expected 1 connection after race, got %d", count)
	}
}

func TestBridge_ReconnectStopsOnShutdown(t *testing.T) {
	dops := &docker.MockOps{
		IsRunningFn: func(_ context.Context, _ string) (bool, error) {
			return true, nil
		},
		WebPortFn: func(_ context.Context, _ string) (int, error) {
			return 1, nil // unreachable port
		},
	}
	bot := &mockSender{}
	b := newTestBridge(t, bot, dops)

	// Close done channel immediately -- reconnect should bail.
	close(b.done)

	done := make(chan struct{})
	go func() {
		b.reconnect("alice")
		close(done)
	}()

	select {
	case <-done:
		// OK
	case <-time.After(3 * time.Second):
		t.Fatal("reconnect did not stop on shutdown")
	}
}

func TestBridge_ReadLoopIgnoresEmptyContent(t *testing.T) {
	srv := newFakeWSServer(t)

	dops := &docker.MockOps{
		WebPortFn: func(_ context.Context, _ string) (int, error) {
			return srv.port, nil
		},
	}
	bot := &mockSender{}
	b := newTestBridge(t, bot, dops)

	errCh := make(chan error, 1)
	go func() {
		errCh <- b.Connect(context.Background(), "alice")
	}()

	conn := srv.accept(t)
	defer conn.Close()

	if err := <-errCh; err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Empty content should be ignored.
	event := map[string]any{
		"v": 1, "type": "assistant_final", "session_id": "mux",
		"payload": map[string]any{"content": ""},
	}
	data, _ := json.Marshal(event)
	writeServerFrame(t, conn, 0x1, data)

	// Sentinel confirms readLoop processed the empty-content frame.
	sentinel := map[string]any{
		"v": 1, "type": "assistant_final", "session_id": "mux",
		"payload": map[string]any{"content": "sentinel"},
	}
	sentinelData, _ := json.Marshal(sentinel)
	writeServerFrame(t, conn, 0x1, sentinelData)

	deadline := time.After(5 * time.Second)
	for {
		msgs := bot.sent()
		if len(msgs) > 0 {
			if len(msgs) != 1 {
				t.Errorf("expected 1 send (sentinel only), got %d", len(msgs))
			}
			if !strings.Contains(msgs[0].text, "sentinel") {
				t.Errorf("expected sentinel, got %q", msgs[0].text)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("sentinel delivery timed out")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestBridge_ReadLoopIgnoresInvalidJSON(t *testing.T) {
	srv := newFakeWSServer(t)

	dops := &docker.MockOps{
		WebPortFn: func(_ context.Context, _ string) (int, error) {
			return srv.port, nil
		},
	}
	bot := &mockSender{}
	b := newTestBridge(t, bot, dops)

	errCh := make(chan error, 1)
	go func() {
		errCh <- b.Connect(context.Background(), "alice")
	}()

	conn := srv.accept(t)
	defer conn.Close()

	if err := <-errCh; err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Invalid JSON should be silently skipped.
	writeServerFrame(t, conn, 0x1, []byte("not json at all {{"))

	// Sentinel confirms readLoop processed the invalid JSON frame.
	sentinel := map[string]any{
		"v": 1, "type": "assistant_final", "session_id": "mux",
		"payload": map[string]any{"content": "sentinel"},
	}
	sentinelData, _ := json.Marshal(sentinel)
	writeServerFrame(t, conn, 0x1, sentinelData)

	deadline := time.After(5 * time.Second)
	for {
		msgs := bot.sent()
		if len(msgs) > 0 {
			if len(msgs) != 1 {
				t.Errorf("expected 1 send (sentinel only), got %d", len(msgs))
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("sentinel delivery timed out")
		case <-time.After(10 * time.Millisecond):
		}
	}

	if !b.IsConnected("alice") {
		t.Error("connection should still be alive after invalid JSON")
	}
}

func TestBridge_DeliverUnsolicited_NilBot(t *testing.T) {
	dops := &docker.MockOps{}
	b := newTestBridge(t, &mockSender{}, dops)
	b.bot = nil
	b.deliverUnsolicited("alice", "test")
}

func TestBridge_DeliverUnsolicited_NilTurnMu(t *testing.T) {
	dops := &docker.MockOps{}
	b := newTestBridge(t, &mockSender{}, dops)
	b.turnMu = nil
	b.deliverUnsolicited("alice", "test")
}

func TestBridge_DeliverUnsolicited_NilChatID(t *testing.T) {
	dops := &docker.MockOps{}
	b := newTestBridge(t, &mockSender{}, dops)
	b.chatID = nil
	b.deliverUnsolicited("alice", "test")
}

func TestBridge_DeliverUnsolicited_ZeroChatID(t *testing.T) {
	dops := &docker.MockOps{}
	bot := &mockSender{}
	b := newTestBridge(t, bot, dops)
	var zero int64
	b.chatID = &zero

	b.deliverUnsolicited("alice", "test content")

	if len(bot.sent()) != 0 {
		t.Errorf("expected no sends with zero chatID, got %d", len(bot.sent()))
	}
}

func TestBridge_DeliverUnsolicited_TurnMuLocked(t *testing.T) {
	dops := &docker.MockOps{}
	bot := &mockSender{}
	b := newTestBridge(t, bot, dops)

	b.turnMu.Lock()
	b.deliverUnsolicited("alice", "deferred content")
	b.turnMu.Unlock()

	if len(bot.sent()) != 0 {
		t.Errorf("expected no sends when turnMu locked, got %d", len(bot.sent()))
	}
}

func TestBridge_DeliverUnsolicited_StoresInMemory(t *testing.T) {
	dops := &docker.MockOps{}
	bot := &mockSender{}
	b := newTestBridge(t, bot, dops)

	b.deliverUnsolicited("alice", "remember this bridge msg")

	msgs, err := b.store.RecentMessages("bridge", 10)
	if err != nil {
		t.Fatalf("RecentMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 stored message, got %d", len(msgs))
	}
	if msgs[0].Role != "assistant" {
		t.Errorf("role = %q, want 'assistant'", msgs[0].Role)
	}
	if !strings.Contains(msgs[0].Content, "[alice]") {
		t.Errorf("stored content should contain [alice], got %q", msgs[0].Content)
	}
	if msgs[0].Agent == nil || *msgs[0].Agent != "alice" {
		t.Errorf("agent = %v, want 'alice'", msgs[0].Agent)
	}
}

func TestBridge_ConnectionLostDuringSend(t *testing.T) {
	srv := newFakeWSServer(t)

	dops := &docker.MockOps{
		WebPortFn: func(_ context.Context, _ string) (int, error) {
			return srv.port, nil
		},
		IsRunningFn: func(_ context.Context, _ string) (bool, error) {
			return false, nil // prevent reconnect
		},
	}
	bot := &mockSender{}
	b := newTestBridge(t, bot, dops)

	errCh := make(chan error, 1)
	go func() {
		errCh <- b.Connect(context.Background(), "alice")
	}()

	conn := srv.accept(t)
	defer conn.Close()

	if err := <-errCh; err != nil {
		t.Fatalf("Connect: %v", err)
	}

	type sendResult struct {
		resp string
		err  error
	}
	resCh := make(chan sendResult, 1)
	go func() {
		resp, err := b.Send(context.Background(), "alice", "will be lost")
		resCh <- sendResult{resp, err}
	}()

	bridgeReadClientFrame(t, conn)

	// Server closes connection abruptly.
	conn.Close()

	select {
	case res := <-resCh:
		if res.err == nil {
			t.Fatal("expected error when connection lost during Send")
		}
		if !errors.Is(res.err, tools.ErrResponseLost) {
			t.Errorf("unexpected error: %v", res.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Send did not return after connection loss")
	}
}

func TestBridge_GetOrConnect_ConnectsLazily(t *testing.T) {
	srv := newFakeWSServer(t)

	dops := &docker.MockOps{
		WebPortFn: func(_ context.Context, _ string) (int, error) {
			return srv.port, nil
		},
	}
	bot := &mockSender{}
	b := newTestBridge(t, bot, dops)

	if b.IsConnected("alice") {
		t.Fatal("should not be connected initially")
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := b.getOrConnect(context.Background(), "alice")
		errCh <- err
	}()

	conn := srv.accept(t)
	defer conn.Close()

	if err := <-errCh; err != nil {
		t.Fatalf("getOrConnect: %v", err)
	}

	if !b.IsConnected("alice") {
		t.Fatal("should be connected after getOrConnect")
	}
}

func TestBridge_CloseAll_Idempotent(t *testing.T) {
	dops := &docker.MockOps{}
	bot := &mockSender{}
	b := newTestBridge(t, bot, dops)

	b.CloseAll()
	b.CloseAll()
	b.CloseAll()
}

func TestBridge_WebSocketURL_TokenURLEncoded(t *testing.T) {
	dops := &docker.MockOps{}
	bot := &mockSender{}
	b := newTestBridge(t, bot, dops)
	b.mode = "docker"

	token := "my token/with&special=chars"
	wsURL, err := b.webSocketURL(context.Background(), "alice", token)
	if err != nil {
		t.Fatalf("webSocketURL: %v", err)
	}

	if strings.Contains(wsURL, "my token/with") {
		t.Errorf("token should be URL-encoded, got: %q", wsURL)
	}
	if !strings.Contains(wsURL, "token=") {
		t.Errorf("url should contain token parameter: %q", wsURL)
	}
}

func TestBridge_ReadToken_Error(t *testing.T) {
	dops := &docker.MockOps{
		WebPortFn: func(_ context.Context, _ string) (int, error) {
			return 9999, nil
		},
	}
	bot := &mockSender{}
	b := newTestBridge(t, bot, dops)

	b.backend = &agent.MockBackend{
		ReadTokenFn: func(name string) (string, error) {
			return "", fmt.Errorf("token not found")
		},
	}

	err := b.Connect(context.Background(), "alice")
	if err == nil {
		t.Fatal("expected error when ReadToken fails")
	}
	if !strings.Contains(err.Error(), "read token") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBridge_DisconnectNonExistent(t *testing.T) {
	dops := &docker.MockOps{}
	bot := &mockSender{}
	b := newTestBridge(t, bot, dops)

	b.Disconnect("nonexistent")
}

func TestBridge_SendAfterDisconnect(t *testing.T) {
	srv := newFakeWSServer(t)

	connectCount := 0
	dops := &docker.MockOps{
		WebPortFn: func(_ context.Context, _ string) (int, error) {
			connectCount++
			if connectCount > 1 {
				return 0, fmt.Errorf("no port")
			}
			return srv.port, nil
		},
	}
	bot := &mockSender{}
	b := newTestBridge(t, bot, dops)

	errCh := make(chan error, 1)
	go func() {
		errCh <- b.Connect(context.Background(), "alice")
	}()
	conn := srv.accept(t)
	conn.Close()
	<-errCh

	b.Disconnect("alice")

	_, err := b.Send(context.Background(), "alice", "after disconnect")
	if err == nil {
		t.Fatal("expected error when sending after disconnect with no server")
	}
}

func TestBridge_DeliverUnsolicited_WithIdentityEmoji(t *testing.T) {
	dops := &docker.MockOps{}
	bot := &mockSender{}
	b := newTestBridge(t, bot, dops)
	b.cfg.Agents.Identities = map[string]config.AgentIdentity{
		"alice": {Emoji: "\U0001F916"},
	}

	b.deliverUnsolicited("alice", "hello with emoji")

	msgs := bot.sent()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 send, got %d", len(msgs))
	}
	if !strings.Contains(msgs[0].text, "\U0001F916") {
		t.Errorf("expected emoji in message, got %q", msgs[0].text)
	}
	if !strings.Contains(msgs[0].text, "hello with emoji") {
		t.Errorf("expected content in message, got %q", msgs[0].text)
	}
}

func TestBridge_ReconnectStopsWhenAgentNotRunning(t *testing.T) {
	dops := &docker.MockOps{
		IsRunningFn: func(_ context.Context, _ string) (bool, error) {
			return false, nil
		},
	}
	bot := &mockSender{}
	b := newTestBridge(t, bot, dops)

	done := make(chan struct{})
	go func() {
		b.reconnect("alice")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("reconnect did not stop when agent not running")
	}
}

func TestBridge_ReconnectStopsWhenAlreadyReconnected(t *testing.T) {
	srv := newFakeWSServer(t)

	dops := &docker.MockOps{
		IsRunningFn: func(_ context.Context, _ string) (bool, error) {
			return true, nil
		},
		WebPortFn: func(_ context.Context, _ string) (int, error) {
			return srv.port, nil
		},
	}
	bot := &mockSender{}
	b := newTestBridge(t, bot, dops)

	// Pre-populate conns to simulate someone else reconnecting.
	// Use a real net.Pipe so Disconnect won't nil-deref on ws.Close().
	pipeServer, pipeClient := net.Pipe()
	defer pipeServer.Close()
	fakeWs := &wsConn{conn: pipeClient, br: bufio.NewReaderSize(pipeClient, 64)}
	fakeDone := make(chan struct{})
	close(fakeDone) // no readLoop running — mark as already done
	b.mu.Lock()
	b.conns["alice"] = &agentConn{name: "alice", ws: fakeWs, done: fakeDone}
	b.mu.Unlock()

	done := make(chan struct{})
	go func() {
		b.reconnect("alice")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("reconnect did not stop when already reconnected")
	}
}

func TestBridge_ReadLoopReconnectsOnClose(t *testing.T) {
	srv := newFakeWSServer(t)

	dops := &docker.MockOps{
		WebPortFn: func(_ context.Context, _ string) (int, error) {
			return srv.port, nil
		},
		IsRunningFn: func(_ context.Context, _ string) (bool, error) {
			return false, nil // prevent actual reconnect
		},
	}
	bot := &mockSender{}
	b := newTestBridge(t, bot, dops)

	errCh := make(chan error, 1)
	go func() {
		errCh <- b.Connect(context.Background(), "alice")
	}()

	conn := srv.accept(t)
	defer conn.Close()

	if err := <-errCh; err != nil {
		t.Fatalf("Connect: %v", err)
	}

	if !b.IsConnected("alice") {
		t.Fatal("should be connected")
	}

	// Server sends close frame.
	writeServerFrame(t, conn, 0x8, nil)
	conn.Close()

	// Wait for readLoop to process and remove the connection.
	deadline := time.After(5 * time.Second)
	for b.IsConnected("alice") {
		select {
		case <-deadline:
			t.Fatal("connection was not cleaned up after server close")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestBridge_DeliverUnsolicited_MemoryContent(t *testing.T) {
	dops := &docker.MockOps{}
	bot := &mockSender{}
	b := newTestBridge(t, bot, dops)

	longContent := strings.Repeat("x", 500)
	b.deliverUnsolicited("alice", longContent)

	msgs, err := b.store.RecentMessages("bridge", 10)
	if err != nil {
		t.Fatalf("RecentMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	// Content should be sanitized (truncated to 300 chars by config.SanitizeText) + "[alice]: " prefix.
	stored := msgs[0].Content
	maxExpected := 300 + len("[alice]: ")
	if len(stored) > maxExpected {
		t.Errorf("stored content should be at most %d chars, got %d", maxExpected, len(stored))
	}
}
