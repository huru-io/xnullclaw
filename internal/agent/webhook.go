package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// webhookClient is used for sending messages to agent gateways.
var webhookClient = &http.Client{Timeout: 60 * time.Second}

// WebhookResponse is the parsed response from POST /webhook.
type WebhookResponse struct {
	Status   string `json:"status"`
	Response string `json:"response"`
}

// SendWebhook sends a message to an agent's gateway via POST /webhook.
// Returns the agent's response text, or an error.
func SendWebhook(port int, token, message string) (*WebhookResponse, error) {
	url := fmt.Sprintf("http://127.0.0.1:%d/webhook", port)

	body, err := json.Marshal(map[string]string{"message": message})
	if err != nil {
		return nil, fmt.Errorf("marshal webhook body: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
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
