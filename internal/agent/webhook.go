package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// webhookClient is used for sending messages to agent gateways.
var webhookClient = &http.Client{Timeout: 60 * time.Second}

// maxWebhookMessageSize is the maximum allowed outbound message size (256KB).
const maxWebhookMessageSize = 256 * 1024

// WebhookResponse is the parsed response from POST /webhook.
type WebhookResponse struct {
	Status   string `json:"status"`
	Response string `json:"response"`
}

// SendWebhook sends a message to an agent's gateway via POST /webhook.
// Returns the agent's response text, or an error.
func SendWebhook(ctx context.Context, port int, token, message string) (*WebhookResponse, error) {
	if len(message) > maxWebhookMessageSize {
		return nil, fmt.Errorf("message too large (%d bytes, max %d)", len(message), maxWebhookMessageSize)
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/webhook", port)

	body, err := json.Marshal(map[string]string{"message": message})
	if err != nil {
		return nil, fmt.Errorf("marshal webhook body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := webhookClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("webhook request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20)) // 2MB cap
	if err != nil {
		return nil, fmt.Errorf("read webhook response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("webhook returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var result WebhookResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse webhook response: %w", err)
	}

	return &result, nil
}

// FriendlyWebhookError translates common webhook errors into user-oriented messages.
func FriendlyWebhookError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection refused"):
		return "agent gateway is not reachable — try 'xnc restart <agent>'"
	case strings.Contains(msg, "HTTP 401"):
		return "authentication failed — token may be missing or mismatched"
	case strings.Contains(msg, "context deadline exceeded") || strings.Contains(msg, "Client.Timeout"):
		return "agent did not respond within timeout — check 'xnc logs <agent>'"
	case strings.Contains(msg, "too large"):
		return msg
	case strings.Contains(msg, "read token"):
		return "could not read auth token — agent may need re-setup"
	default:
		return msg
	}
}

// TrySendWebhook attempts to send a message via webhook if a port is mapped.
// Returns (nil, nil) if port <= 0 (caller should use fallback).
// Returns (response, nil) on success, or (nil, err) on failure.
func TrySendWebhook(ctx context.Context, port int, home, agentName, message string) (*WebhookResponse, error) {
	if port <= 0 {
		return nil, nil
	}
	agentDir := Dir(home, agentName)
	token, err := ReadToken(agentDir)
	if err != nil {
		return nil, fmt.Errorf("read token for %s: %w", agentName, err)
	}
	return SendWebhook(ctx, port, token, message)
}
