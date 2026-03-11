package agent

import (
	"fmt"
	"io"
	"net/http"
	"time"
)

// providerEndpoint maps provider names to their lightweight test endpoints.
var providerEndpoint = map[string]struct {
	URL     string
	AuthFn  func(key string) (string, string) // returns header name, value
}{
	"openai": {
		URL:    "https://api.openai.com/v1/models",
		AuthFn: func(key string) (string, string) { return "Authorization", "Bearer " + key },
	},
	"anthropic": {
		URL:    "https://api.anthropic.com/v1/models",
		AuthFn: func(key string) (string, string) { return "x-api-key", key },
	},
	"openrouter": {
		URL:    "https://openrouter.ai/api/v1/models",
		AuthFn: func(key string) (string, string) { return "Authorization", "Bearer " + key },
	},
}

// TestProviderKey makes a lightweight API call (GET /models) to verify a key.
// Returns nil if valid, an error if the key is rejected.
// Network errors return a warning-style error (key might still be valid).
func TestProviderKey(provider, key string) error {
	ep, ok := providerEndpoint[provider]
	if !ok {
		return fmt.Errorf("unknown provider %q", provider)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", ep.URL, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	hdr, val := ep.AuthFn(key)
	req.Header.Set(hdr, val)
	if provider == "anthropic" {
		req.Header.Set("anthropic-version", "2023-06-01")
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("network error (key not verified): %v", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	switch {
	case resp.StatusCode == 200:
		return nil
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		return fmt.Errorf("%s key rejected (HTTP %d)", provider, resp.StatusCode)
	default:
		return fmt.Errorf("%s returned HTTP %d (key not verified)", provider, resp.StatusCode)
	}
}

// TestAgentKeys tests all configured provider keys for an agent.
// Returns a map of provider → error (nil means valid).
// Only tests providers that have a non-empty key.
func TestAgentKeys(home, name string) map[string]error {
	dir := Dir(home, name)
	results := make(map[string]error)

	for _, pair := range []struct {
		configKey string
		provider  string
	}{
		{"openai_key", "openai"},
		{"anthropic_key", "anthropic"},
		{"openrouter_key", "openrouter"},
	} {
		val, err := ConfigGet(dir, pair.configKey)
		if err != nil {
			continue
		}
		key, ok := val.(string)
		if !ok || key == "" {
			continue
		}
		results[pair.provider] = TestProviderKey(pair.provider, key)
	}

	return results
}
