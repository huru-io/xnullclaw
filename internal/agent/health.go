package agent

import (
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
// This is used after container start to wait for the nullclaw gateway
// to finish initializing before sending messages.
func WaitForHealthy(port int, timeout time.Duration) error {
	if port <= 0 {
		return nil // no port mapped, skip health check
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/health", port)
	deadline := time.Now().Add(timeout)
	interval := 500 * time.Millisecond

	for time.Now().Before(deadline) {
		if checkHealth(url) {
			return nil
		}
		time.Sleep(interval)
	}

	return fmt.Errorf("gateway health check timed out after %s (port %d)", timeout, port)
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
