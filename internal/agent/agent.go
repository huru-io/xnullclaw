// Package agent handles agent directory management on disk.
// It is Docker-agnostic — no Docker imports here.
package agent

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// DefaultHome returns the default XNC_HOME directory.
func DefaultHome() string {
	if h := os.Getenv("XNC_HOME"); h != "" {
		return h
	}
	if h := os.Getenv("XNULLCLAW_HOME"); h != "" {
		return h
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".xnc")
}

// DefaultImage returns the Docker image name for nullclaw containers.
func DefaultImage() string {
	if img := os.Getenv("XNC_IMAGE"); img != "" {
		return img
	}
	if img := os.Getenv("XNULLCLAW_IMAGE"); img != "" {
		return img
	}
	return "nullclaw:latest"
}

// nameRe validates agent names: starts with a letter, ends with letter/digit,
// at most one hyphen or one underscore in the middle.
var nameRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9]*([_-][a-zA-Z0-9]+)?$`)

// reservedNames are CLI commands that conflict with agent names.
var reservedNames = map[string]bool{
	"mux": true, "help": true, "version": true, "list": true,
	"running": true, "image": true, "config": true, "send": true,
	"init": true, "skill": true,
}

// ValidateName checks that name is a valid, pronounceable agent name.
func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("agent name cannot be empty")
	}
	if len(name) > 20 {
		return fmt.Errorf("agent name %q too long (max 20 characters)", name)
	}
	if !nameRe.MatchString(name) {
		return fmt.Errorf("invalid agent name %q: must start with a letter, end with a letter or digit, at most one hyphen or underscore", name)
	}
	// Canonical form must be at least 3 characters (pronounceable).
	if len(CanonicalName(name)) < 3 {
		return fmt.Errorf("agent name %q is too short (need at least 3 letters/digits)", name)
	}
	if reservedNames[strings.ToLower(name)] {
		return fmt.Errorf("agent name %q is reserved", name)
	}
	return nil
}

// CanonicalName returns the canonical form of an agent name: lowercase
// with hyphens and underscores stripped. This ensures names that sound
// the same when spoken (e.g. Perez1, Perez-1, perez_1) are treated as
// identical.
func CanonicalName(name string) string {
	s := strings.ToLower(name)
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, "_", "")
	return s
}

// ConflictsWith scans all existing agents under home and returns the
// name of any agent whose canonical form matches the given name.
// Returns ("", false) if no conflict.
func ConflictsWith(home, name string) (string, bool) {
	canon := CanonicalName(name)
	agentsDir := AgentsDir(home)
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		existing := e.Name()
		// Skip exact match (handled by Exists check separately).
		if existing == canon {
			continue
		}
		// Check if this is an agent directory.
		if _, err := os.Stat(filepath.Join(agentsDir, existing, "config.json")); err != nil {
			continue
		}
		if CanonicalName(existing) == canon {
			return existing, true
		}
	}
	return "", false
}

// IsXNCHome checks if a directory looks like an xnc home directory.
// Returns true if the directory contains a .instance_id file or a mux/ directory.
func IsXNCHome(home string) bool {
	if _, err := os.Stat(filepath.Join(home, ".instance_id")); err == nil {
		return true
	}
	if fi, err := os.Stat(filepath.Join(home, "mux")); err == nil && fi.IsDir() {
		return true
	}
	return false
}

// SetupComplete checks if xnc has been fully initialized:
// the home is an xnc home, at least one agent exists, and at least
// one agent has a provider API key configured.
func SetupComplete(home string) bool {
	if !IsXNCHome(home) {
		return false
	}
	agents, _ := ListAll(home)
	if len(agents) == 0 {
		return false
	}
	for _, info := range agents {
		if HasProviderKey(home, info.Name) {
			return true
		}
	}
	return false
}

// HasProviderKey checks if an agent has at least one non-empty API key
// configured (openai, anthropic, or openrouter).
func HasProviderKey(home, name string) bool {
	dir := Dir(home, name)
	for _, key := range []string{"openai_key", "anthropic_key", "openrouter_key"} {
		val, err := ConfigGet(dir, key)
		if err != nil {
			continue
		}
		if s, ok := val.(string); ok && s != "" {
			return true
		}
	}
	return false
}

// IsEmptyDir checks if a directory is empty or doesn't exist.
func IsEmptyDir(path string) bool {
	entries, err := os.ReadDir(path)
	if err != nil {
		return true // doesn't exist = empty
	}
	return len(entries) == 0
}

// ValidateHome checks that a home directory is either a valid xnc home
// or empty/non-existent. Returns an error if the directory exists and
// contains non-xnc files (to prevent accidentally using the wrong directory).
func ValidateHome(home string, allowInit bool) error {
	if IsXNCHome(home) {
		return nil
	}
	if IsEmptyDir(home) {
		if !allowInit {
			return fmt.Errorf("%s is empty — run 'xnc init' first", home)
		}
		return nil
	}
	// Directory exists with content but no .instance_id — suspicious.
	return fmt.Errorf("%s does not look like an xnc home (no .instance_id found). Use --home to specify the correct path or 'xnc init --home %s' to initialize it", home, home)
}

// AgentsDir returns the directory containing all agent subdirectories.
func AgentsDir(home string) string {
	return filepath.Join(home, "agents")
}

// Dir returns the agent's directory path using canonical name.
func Dir(home, name string) string {
	return filepath.Join(AgentsDir(home), CanonicalName(name))
}

// Exists checks whether an agent directory with a config.json exists.
func Exists(home, name string) bool {
	dir := Dir(home, name)
	info, err := os.Stat(filepath.Join(dir, "config.json"))
	return err == nil && !info.IsDir()
}

// InstanceID returns the 6-hex-char instance ID for this xnullclaw install.
// Stored at ~/.xnc/.instance_id. Auto-generated on first call.
func InstanceID(home string) string {
	path := filepath.Join(home, ".instance_id")
	data, err := os.ReadFile(path)
	if err == nil {
		id := strings.TrimSpace(string(data))
		if len(id) == 6 {
			return id
		}
	}

	// Generate new ID.
	b := make([]byte, 3) // 3 bytes = 6 hex chars
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failure indicates a broken system.
		panic("agent: crypto/rand failed: " + err.Error())
	}
	id := hex.EncodeToString(b)

	os.MkdirAll(home, 0700)
	os.WriteFile(path, []byte(id+"\n"), 0600)
	return id
}

// ContainerPrefix returns the Docker container name prefix for this instance.
// Format: xnc-<instance_id>-
func ContainerPrefix(home string) string {
	return "xnc-" + InstanceID(home) + "-"
}

// ContainerName returns the Docker container name for an agent.
// Format: xnc-<instance_id>-<agentname>
func ContainerName(home, name string) string {
	return ContainerPrefix(home) + CanonicalName(name)
}

// Info holds summary information about an agent on disk.
type Info struct {
	Name    string `json:"name"`
	Dir     string `json:"dir"`
	Emoji   string `json:"emoji"`
	Created string `json:"created"`
}

// ListAll returns Info for every agent found under home/agents/.
func ListAll(home string) ([]Info, error) {
	agentsDir := AgentsDir(home)
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list agents: %w", err)
	}

	var agents []Info
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		name := e.Name()
		dir := filepath.Join(agentsDir, name)
		if _, err := os.Stat(filepath.Join(dir, "config.json")); err != nil {
			continue // not an agent directory
		}
		meta, _ := ReadMeta(dir)
		displayName := meta["NAME"]
		if displayName == "" {
			displayName = name // fallback to directory name
		}
		agents = append(agents, Info{
			Name:    displayName,
			Dir:     dir,
			Emoji:   meta["EMOJI"],
			Created: meta["CREATED"],
		})
	}

	sort.Slice(agents, func(i, j int) bool {
		return agents[i].Name < agents[j].Name
	})
	return agents, nil
}
