// bridge.go manages persistent WebSocket connections to agent web channels.
// It replaces the outbox drainer for agents with web channel support,
// providing real-time bidirectional communication.
package mux

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jotavich/xnullclaw/internal/agent"
	"github.com/jotavich/xnullclaw/internal/config"
	"github.com/jotavich/xnullclaw/internal/docker"
	"github.com/jotavich/xnullclaw/internal/logging"
	"github.com/jotavich/xnullclaw/internal/media"
	"github.com/jotavich/xnullclaw/internal/memory"
	"github.com/jotavich/xnullclaw/internal/telegram"
	"github.com/jotavich/xnullclaw/internal/tools"
)

// Bridge manages WebSocket connections to agent web channels.
// It implements tools.AgentSender for synchronous message delivery.
//
// Created in two phases: base fields are set at construction, then bot/turnMu/chatID
// are wired after the Telegram bot and turn mutex are created. No connections are
// established until Connect is called, which happens after wiring is complete.
type Bridge struct {
	home        string
	mediaTmpDir string // host path for retrieved container files
	backend     agent.Backend
	store       *memory.Store
	bot         telegram.Sender
	cfg         *config.Config
	logger      *logging.Logger
	mode        string
	docker      docker.Ops
	chatID      *int64 // pointer to mux's lastChatID
	turnMu      *sync.Mutex

	// done is closed on CloseAll to stop reconnect goroutines.
	done chan struct{}

	mu    sync.Mutex
	conns map[string]*agentConn
}

// agentConn holds the WebSocket connection state for a single agent.
type agentConn struct {
	name  string
	ws    *wsConn
	token string
	done  chan struct{} // closed when readLoop exits

	mu     sync.Mutex
	waiter chan string // non-nil when a Send() is waiting for a response
}

// sessionID is the session identifier used for all mux connections.
// Nullclaw routes assistant_final events to connections with matching session_id.
const sessionID = "mux"

// maxReconnectAttempts caps how many times reconnect will retry before giving up.
const maxReconnectAttempts = 20


// Send sends a message to an agent via WebSocket and waits for the response.
// Implements tools.AgentSender.
func (b *Bridge) Send(ctx context.Context, name, message string) (string, error) {
	ac, err := b.getOrConnect(ctx, name)
	if err != nil {
		return "", err
	}

	// Register response waiter.
	responseCh := make(chan string, 1)
	ac.mu.Lock()
	if ac.waiter != nil {
		ac.mu.Unlock()
		return "", fmt.Errorf("bridge: another send to %s is in progress", name)
	}
	ac.waiter = responseCh
	ac.mu.Unlock()

	if err := b.writeUserMessage(ac, message); err != nil {
		ac.mu.Lock()
		ac.waiter = nil
		ac.mu.Unlock()
		return "", err
	}

	// Wait for response, timeout, or connection close.
	select {
	case resp, ok := <-responseCh:
		if !ok {
			// Channel closed — connection was lost after the message was
			// successfully written. The agent received and is processing it,
			// but we lost the response. Callers must NOT re-send.
			return "", fmt.Errorf("%w (agent: %s)", tools.ErrResponseLost, name)
		}
		return resp, nil
	case <-ctx.Done():
		ac.mu.Lock()
		ac.waiter = nil
		ac.mu.Unlock()
		return "", fmt.Errorf("bridge: timeout waiting for response from %s", name)
	}
}

// SendAsync sends a message to an agent without waiting for a response.
// The agent's response will arrive as an unsolicited message via the bridge
// reader loop, which delivers it directly to Telegram with an identity header.
func (b *Bridge) SendAsync(ctx context.Context, name, message string) error {
	ac, err := b.getOrConnect(ctx, name)
	if err != nil {
		return err
	}
	return b.writeUserMessage(ac, message)
}

// writeUserMessage builds and sends a user_message to the agent via WebSocket.
func (b *Bridge) writeUserMessage(ac *agentConn, message string) error {
	msg := map[string]any{
		"v":          1,
		"type":       "user_message",
		"session_id": sessionID,
		"payload": map[string]any{
			"auth_token": ac.token,
			"content":    message,
			"sender_id":  "mux",
		},
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if err := ac.ws.WriteText(data); err != nil {
		return fmt.Errorf("bridge: ws write to %s: %w", ac.name, err)
	}
	return nil
}

// IsConnected returns true if there is an active WebSocket connection to the agent.
// Implements tools.AgentSender.
func (b *Bridge) IsConnected(name string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.conns[name]
	return ok
}

// Connect establishes a WebSocket connection to an agent's web channel.
// Safe for concurrent calls — uses check-dial-recheck pattern to prevent
// duplicate connections.
func (b *Bridge) Connect(ctx context.Context, name string) error {
	b.mu.Lock()
	if _, ok := b.conns[name]; ok {
		b.mu.Unlock()
		return nil // already connected
	}
	b.mu.Unlock()

	ac, err := b.dial(ctx, name)
	if err != nil {
		return err
	}

	// Re-check under lock — another goroutine may have connected while we dialed.
	b.mu.Lock()
	if _, ok := b.conns[name]; ok {
		b.mu.Unlock()
		// Someone else connected first — close our connection, use theirs.
		ac.ws.Close()
		return nil
	}
	b.conns[name] = ac
	b.mu.Unlock()

	// Start read loop in background.
	go b.readLoop(ac)

	b.logger.Info("bridge: connected", "agent", name)
	return nil
}

// Disconnect closes the WebSocket connection to an agent.
func (b *Bridge) Disconnect(name string) {
	b.mu.Lock()
	ac, ok := b.conns[name]
	if ok {
		delete(b.conns, name)
	}
	b.mu.Unlock()

	if ok {
		ac.ws.Close()
		// Wait for readLoop to exit before draining the waiter.
		// This prevents readLoop from spawning a reconnect goroutine
		// after we've explicitly disconnected.
		<-ac.done
		// Drain any pending waiter with close (Send detects closed channel).
		ac.mu.Lock()
		if ac.waiter != nil {
			close(ac.waiter)
			ac.waiter = nil
		}
		ac.mu.Unlock()
		b.logger.Info("bridge: disconnected", "agent", name)
	}
}

// CloseAll closes all WebSocket connections and stops reconnect goroutines.
func (b *Bridge) CloseAll() {
	// Signal all reconnect goroutines to stop.
	select {
	case <-b.done:
		// Already closed.
	default:
		close(b.done)
	}

	b.mu.Lock()
	names := make([]string, 0, len(b.conns))
	for name := range b.conns {
		names = append(names, name)
	}
	b.mu.Unlock()

	for _, name := range names {
		b.Disconnect(name)
	}
}

// dial creates a new WebSocket connection to an agent.
func (b *Bridge) dial(ctx context.Context, name string) (*agentConn, error) {
	// Get auth token.
	token, err := b.backend.ReadToken(name)
	if err != nil {
		return nil, fmt.Errorf("bridge: read token for %s: %w", name, err)
	}

	// Build WebSocket URL.
	wsURL, err := b.webSocketURL(ctx, name, token)
	if err != nil {
		return nil, err
	}

	ws, err := wsDial(ctx, wsURL)
	if err != nil {
		return nil, fmt.Errorf("bridge: connect to %s: %w", name, err)
	}

	return &agentConn{
		name:  name,
		ws:    ws,
		token: token,
		done:  make(chan struct{}),
	}, nil
}

// webSocketURL constructs the WebSocket URL for an agent's web channel.
func (b *Bridge) webSocketURL(ctx context.Context, name, token string) (string, error) {
	cn := agent.ContainerName(b.home, name)

	var host string
	var port int

	switch b.mode {
	case "docker", "kubernetes":
		// Use container/service DNS.
		if !agent.SafeContainerName(cn) {
			return "", fmt.Errorf("bridge: unsafe container name: %s", cn)
		}
		host = cn
		port = docker.WebChannelPort
	default:
		// Local mode: use localhost with mapped port.
		p, err := b.docker.WebPort(ctx, cn)
		if err != nil || p == 0 {
			return "", fmt.Errorf("bridge: no web channel port for %s", name)
		}
		host = "127.0.0.1"
		port = p
	}

	// URL-encode query parameters to prevent injection from malformed tokens.
	params := url.Values{}
	params.Set("token", token)
	params.Set("session_id", sessionID)
	return fmt.Sprintf("ws://%s:%d/ws?%s", host, port, params.Encode()), nil
}

// pingInterval controls how often we send WebSocket pings as keepalive.
// If no pong is received within readDeadline, the connection is considered dead.
const pingInterval = 30 * time.Second

// readDeadline is how long we wait for any data (message or pong) before
// considering the connection dead. Must be > pingInterval.
const readDeadline = 90 * time.Second

// readLoop continuously reads WebSocket frames from an agent connection.
// Handles response routing (to synchronous waiters) and unsolicited message
// delivery (to Telegram for autonomous/cron output).
//
// Starts a ping keepalive goroutine to detect dead connections faster than
// the read deadline alone.
func (b *Bridge) readLoop(ac *agentConn) {
	defer close(ac.done)

	// Start ping keepalive — sends periodic pings to detect dead connections.
	// Stops when ac.done is closed (deferred above) or on write error.
	go func() {
		ticker := time.NewTicker(pingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := ac.ws.Ping(); err != nil {
					return // write failed — readLoop will detect via read error
				}
			case <-ac.done:
				return
			}
		}
	}()

	for {
		// Read deadline detects connections where pongs stop arriving.
		// Pong frames refresh the deadline inside ReadMessage, preventing
		// false timeouts on idle-but-alive connections.
		ac.ws.SetReadDeadline(readDeadline)

		payload, err := ac.ws.ReadMessage()
		if err != nil {
			b.logger.Info("bridge: connection closed", "agent", ac.name, "error", err)
			// Remove from pool. Track whether we were still the active connection
			// (vs. being removed by an explicit Disconnect call).
			b.mu.Lock()
			wasActive := b.conns[ac.name] == ac
			if wasActive {
				delete(b.conns, ac.name)
			}
			b.mu.Unlock()

			// Drain pending waiter — close channel so Send returns error.
			ac.mu.Lock()
			if ac.waiter != nil {
				close(ac.waiter)
				ac.waiter = nil
			}
			ac.mu.Unlock()

			// Only reconnect if we were still the active connection.
			// If Disconnect removed us first, reconnection is unwanted.
			if wasActive {
				go b.reconnect(ac.name)
			}
			return
		}

		// Parse event.
		var event struct {
			V         int    `json:"v"`
			Type      string `json:"type"`
			SessionID string `json:"session_id"`
			Payload   struct {
				Content string `json:"content"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(payload, &event); err != nil {
			continue
		}

		// Only handle assistant_final events for our session.
		if event.Type != "assistant_final" || event.SessionID != sessionID {
			continue
		}

		content := event.Payload.Content
		if content == "" {
			continue
		}

		// Check for synchronous waiter first.
		ac.mu.Lock()
		waiter := ac.waiter
		ac.waiter = nil
		ac.mu.Unlock()

		if waiter != nil {
			// Synchronous response — send to waiter.
			select {
			case waiter <- content:
			default:
			}
			continue
		}

		// No waiter — unsolicited message (cron, autonomous).
		b.deliverUnsolicited(ac.name, content)
	}
}

// deliverUnsolicited handles autonomous/cron messages from agents.
// Same delivery logic as the Drainer: identity header + Telegram + memory store.
func (b *Bridge) deliverUnsolicited(name, content string) {
	if b.bot == nil || b.turnMu == nil || b.chatID == nil {
		return // bridge not fully wired yet
	}

	chatID := muxTargetChatID(b.cfg, b.chatID)
	if chatID == 0 {
		return
	}

	// Coordinate with turn sends. If a turn is active, queue for delivery
	// after the turn completes rather than dropping.
	if !b.turnMu.TryLock() {
		go func() {
			b.turnMu.Lock()
			defer b.turnMu.Unlock()
			b.sendAgentResponse(name, content)
		}()
		return
	}
	defer b.turnMu.Unlock()
	b.sendAgentResponse(name, content)
}

// sendAgentResponse sends an agent's response to Telegram with an identity
// header and stores it in memory. Must be called with turnMu held.
func (b *Bridge) sendAgentResponse(name, content string) {
	chatID := muxTargetChatID(b.cfg, b.chatID)
	if chatID == 0 {
		return
	}

	header := agentIdentityHeader(b.cfg, name)

	// Parse media attachments.
	cleanText, attachments := media.Parse(content)

	// Resolve container paths → host paths by retrieving files.
	if len(attachments) > 0 && b.mediaTmpDir != "" {
		retrieveCtx, retrieveCancel := context.WithTimeout(context.Background(), 60*time.Second)
		attachments = resolveContainerAttachments(retrieveCtx, b.docker, b.home, name, b.mediaTmpDir, attachments, b.logger.Error)
		retrieveCancel()
	}

	if cleanText != "" {
		if err := b.bot.Send(chatID, header+cleanText); err != nil {
			b.logger.Error("bridge: telegram send", "agent", name, "error", err)
			return
		}
	}

	caption := strings.TrimSpace(header)
	for _, att := range attachments {
		sendAttachment(b.bot, b.logger, chatID, att, caption)
	}

	// Store in memory.
	sanitized := config.SanitizeText(content, 300)
	agentName := name
	if err := b.store.AddMessage(memory.Message{
		Role:    "assistant",
		Content: fmt.Sprintf("[%s]: %s", name, sanitized),
		Agent:   &agentName,
		Stream:  "bridge",
	}); err != nil {
		b.logger.Error("bridge: store message failed", "agent", name, "error", err)
	}

	b.logger.Info("bridge: delivered agent response", "agent", name, "len", len(content))
}

// reconnect attempts to re-establish a WebSocket connection with exponential backoff.
// Stops when: the agent is not running, someone else reconnected, max attempts reached,
// or the bridge is shutting down.
func (b *Bridge) reconnect(name string) {
	backoff := time.Second
	const maxBackoff = 30 * time.Second

	for attempt := 0; attempt < maxReconnectAttempts; attempt++ {
		// Check for bridge shutdown.
		select {
		case <-b.done:
			b.logger.Info("bridge: reconnect stopped (shutdown)", "agent", name)
			return
		default:
		}

		// Sleep with shutdown awareness.
		select {
		case <-b.done:
			b.logger.Info("bridge: reconnect stopped (shutdown)", "agent", name)
			return
		case <-time.After(backoff):
		}

		// Check if agent is still running (bounded to prevent hanging on Docker API).
		cn := agent.ContainerName(b.home, name)
		isRunCtx, isRunCancel := context.WithTimeout(context.Background(), 10*time.Second)
		running, err := b.docker.IsRunning(isRunCtx, cn)
		isRunCancel()
		if err != nil || !running {
			b.logger.Info("bridge: agent not running, stopping reconnect", "agent", name)
			return
		}

		// Check if someone else already reconnected.
		b.mu.Lock()
		if _, ok := b.conns[name]; ok {
			b.mu.Unlock()
			return
		}
		b.mu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err = b.Connect(ctx, name)
		cancel()

		if err == nil {
			b.logger.Info("bridge: reconnected", "agent", name)
			return
		}

		b.logger.Info("bridge: reconnect failed", "agent", name, "attempt", attempt+1, "backoff", backoff, "error", err)
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}

	b.logger.Error("bridge: reconnect abandoned after max attempts", "agent", name, "attempts", maxReconnectAttempts)
}

// getOrConnect returns an existing connection or establishes a new one.
func (b *Bridge) getOrConnect(ctx context.Context, name string) (*agentConn, error) {
	b.mu.Lock()
	ac, ok := b.conns[name]
	b.mu.Unlock()
	if ok {
		return ac, nil
	}

	if err := b.Connect(ctx, name); err != nil {
		return nil, err
	}

	b.mu.Lock()
	ac = b.conns[name]
	b.mu.Unlock()
	if ac == nil {
		return nil, fmt.Errorf("bridge: connection lost immediately for %s", name)
	}
	return ac, nil
}
