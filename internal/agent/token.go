package agent

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// tokenPrefix is the standard prefix for nullclaw bearer tokens.
const tokenPrefix = "zc_"

// tokenFile is the filename where the plaintext token is stored
// in the agent's data directory for mux-side use.
const tokenFile = ".auth_token"

// GenerateToken creates a new bearer token with 256 bits of entropy.
// Format: "zc_" + 64 hex chars = 67 chars total.
func GenerateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return tokenPrefix + hex.EncodeToString(b), nil
}

// HashToken computes the SHA-256 hash of a token, returning a hex string.
// The nullclaw gateway stores only hashes and compares using constant-time.
func HashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// WriteToken stores the plaintext bearer token in the agent's data directory.
// This is read by the mux when making HTTP requests to the agent.
func WriteToken(agentDir string, token string) error {
	p := filepath.Join(agentDir, "data", tokenFile)
	return os.WriteFile(p, []byte(token), 0600)
}

// ReadToken reads the plaintext bearer token from the agent's data directory.
// Returns empty string and nil error if the file doesn't exist.
func ReadToken(agentDir string) (string, error) {
	p := filepath.Join(agentDir, "data", tokenFile)
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read token: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// SetupWebhookAuth generates a token, writes it to the agent's data dir,
// and injects the SHA-256 hash into the agent's config.json under
// gateway.paired_tokens so the gateway accepts it immediately on startup.
func SetupWebhookAuth(agentDir string) (string, error) {
	token, err := GenerateToken()
	if err != nil {
		return "", err
	}

	// Write plaintext for mux-side use.
	if err := WriteToken(agentDir, token); err != nil {
		return "", fmt.Errorf("write token: %w", err)
	}

	// Inject hash into config.json gateway.paired_tokens.
	hash := HashToken(token)
	if err := injectTokenHash(agentDir, hash); err != nil {
		return "", fmt.Errorf("inject token hash: %w", err)
	}

	return token, nil
}

// injectTokenHash adds a token hash to the gateway.paired_tokens array
// in the agent's config.json.
func injectTokenHash(agentDir string, hash string) error {
	doc, err := ConfigGetAll(agentDir)
	if err != nil {
		return err
	}

	// Navigate/create gateway section.
	gw, ok := doc["gateway"].(map[string]any)
	if !ok {
		gw = make(map[string]any)
		doc["gateway"] = gw
	}

	// Set paired_tokens (replace any existing tokens — one token per agent).
	gw["paired_tokens"] = []any{hash}

	// Ensure pairing is required.
	gw["require_pairing"] = true

	return writeConfig(agentDir, doc)
}

// writeConfig marshals and writes the config.json file.
func writeConfig(agentDir string, doc map[string]any) error {
	return ConfigSetAll(agentDir, doc)
}
