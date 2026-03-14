package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// safeContainerNameRe validates that a container name contains only safe
// characters for embedding in a URL hostname (prevents SSRF/URL injection).
var safeContainerNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

// SafeContainerName returns true if the name is safe for embedding in URLs.
func SafeContainerName(name string) bool {
	return safeContainerNameRe.MatchString(name)
}

// webhookClient is used for sending messages to agent gateways.
var webhookClient = &http.Client{Timeout: 60 * time.Second}

// maxWebhookMessageSize is the maximum allowed outbound message size (256KB).
const maxWebhookMessageSize = 256 * 1024

// WebhookResponse is the parsed response from POST /webhook.
type WebhookResponse struct {
	Status   string `json:"status"`
	Response string `json:"response"`
}

// AgentBaseURL returns the base URL for reaching an agent's gateway.
//   - mode "local": http://127.0.0.1:<port> (host port mapping)
//   - mode "docker": http://<containerName>:3000 (Docker network DNS)
//   - mode "kubernetes": http://<containerName>:3000 (K8s Service DNS)
//
// If containerName fails validation, falls back to localhost to prevent
// URL injection / SSRF.
func AgentBaseURL(mode string, port int, containerName string) string {
	if (mode == "docker" || mode == "kubernetes") && containerName != "" && SafeContainerName(containerName) {
		return fmt.Sprintf("http://%s:3000", containerName)
	}
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}

// SendWebhook sends a message to an agent's gateway via POST /webhook.
// baseURL is the agent's base URL (e.g. "http://127.0.0.1:49823" or "http://xnc-abc-alice:3000").
// Returns the agent's response text, or an error.
func SendWebhook(ctx context.Context, baseURL, token, message string) (*WebhookResponse, error) {
	if len(message) > maxWebhookMessageSize {
		return nil, fmt.Errorf("message too large (%d bytes, max %d)", len(message), maxWebhookMessageSize)
	}

	url := baseURL + "/webhook"

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

// TrySendWebhook attempts to send a message via webhook.
// In "local" mode, requires port > 0. In "docker"/"kubernetes" mode, uses container/service DNS.
// Returns (nil, nil) if not reachable (caller should use fallback).
// Returns (response, nil) on success, or (nil, err) on failure.
//
// tokenReader is an optional function to read the auth token by agent name.
// If nil, falls back to reading from the filesystem (local/docker mode).
func TrySendWebhook(ctx context.Context, mode string, port int, containerName, home, agentName, message string, tokenReader ...func(string) (string, error)) (*WebhookResponse, error) {
	// Docker/K8s mode with a container name uses DNS — no port needed.
	// Local mode requires a mapped port; skip if unavailable.
	usesDNS := (mode == "docker" || mode == "kubernetes") && containerName != ""
	if !usesDNS && port <= 0 {
		return nil, nil
	}
	baseURL := AgentBaseURL(mode, port, containerName)

	var token string
	var err error
	if len(tokenReader) > 0 && tokenReader[0] != nil {
		token, err = tokenReader[0](agentName)
	} else {
		agentDir := Dir(home, agentName)
		token, err = ReadToken(agentDir)
	}
	if err != nil {
		return nil, fmt.Errorf("read token for %s: %w", agentName, err)
	}
	return SendWebhook(ctx, baseURL, token, message)
}
