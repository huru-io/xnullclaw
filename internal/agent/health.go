package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// healthClient is used exclusively for gateway health checks.
var healthClient = &http.Client{Timeout: 5 * time.Second}

// WaitForHealthy polls the agent's gateway /health endpoint until it
// returns {"status":"ok"} or the timeout expires. Returns nil on success.
//
// baseURL is the agent's base URL (e.g. "http://127.0.0.1:49823" or
// "http://xnc-abc-alice:3000"). Pass empty string to skip the check.
func WaitForHealthy(ctx context.Context, baseURL string, timeout time.Duration) error {
	if baseURL == "" {
		return nil // no URL available, skip health check
	}

	url := baseURL + "/health"
	deadline := time.Now().Add(timeout)
	interval := 500 * time.Millisecond

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if checkHealth(url) {
			return nil
		}
		time.Sleep(interval)
	}

	return fmt.Errorf("gateway health check timed out after %s (%s)", timeout, baseURL)
}

// checkHealth performs a single GET /health and returns true if the
// response is HTTP 200 with status "ok" or "degraded".
func checkHealth(url string) bool {
	resp, err := healthClient.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false
	}

	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return false
	}

	return body.Status == "ok" || body.Status == "degraded"
}
