package agent

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const metaFile = ".meta"

// ReadMeta reads all key=value pairs from the agent's .meta file.
func ReadMeta(agentDir string) (map[string]string, error) {
	path := filepath.Join(agentDir, metaFile)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]string), nil
		}
		return nil, fmt.Errorf("read meta: %w", err)
	}
	defer f.Close()

	m := make(map[string]string)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if k, v, ok := strings.Cut(line, "="); ok && k != "" {
			m[k] = v
		}
	}
	return m, sc.Err()
}

// ReadMetaKey reads a single key from the .meta file.
// Returns the default value if the key is not found.
func ReadMetaKey(agentDir, key, defaultVal string) string {
	m, err := ReadMeta(agentDir)
	if err != nil {
		return defaultVal
	}
	if v, ok := m[key]; ok {
		return v
	}
	return defaultVal
}

// WriteMeta writes or updates a key=value pair in the .meta file.
// Uses atomic write (temp file + rename) for safety.
func WriteMeta(agentDir, key, value string) error {
	path := filepath.Join(agentDir, metaFile)

	// Read existing contents.
	existing, _ := ReadMeta(agentDir)
	existing[key] = value

	// Write to temp file, then rename.
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("write meta: %w", err)
	}

	for k, v := range existing {
		if _, err := fmt.Fprintf(f, "%s=%s\n", k, v); err != nil {
			f.Close()
			os.Remove(tmp)
			return fmt.Errorf("write meta: %w", err)
		}
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("write meta: %w", err)
	}

	return os.Rename(tmp, path)
}

// WriteMetaBatch writes multiple key=value pairs atomically.
func WriteMetaBatch(agentDir string, pairs map[string]string) error {
	path := filepath.Join(agentDir, metaFile)

	existing, _ := ReadMeta(agentDir)
	for k, v := range pairs {
		existing[k] = v
	}

	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("write meta: %w", err)
	}

	for k, v := range existing {
		if _, err := fmt.Fprintf(f, "%s=%s\n", k, v); err != nil {
			f.Close()
			os.Remove(tmp)
			return fmt.Errorf("write meta: %w", err)
		}
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("write meta: %w", err)
	}

	return os.Rename(tmp, path)
}

// DeleteMetaKey removes a key from the .meta file.
func DeleteMetaKey(agentDir, key string) error {
	path := filepath.Join(agentDir, metaFile)

	existing, _ := ReadMeta(agentDir)
	if _, ok := existing[key]; !ok {
		return nil // nothing to delete
	}
	delete(existing, key)

	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("delete meta key: %w", err)
	}

	for k, v := range existing {
		if _, err := fmt.Fprintf(f, "%s=%s\n", k, v); err != nil {
			f.Close()
			os.Remove(tmp)
			return fmt.Errorf("delete meta key: %w", err)
		}
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("delete meta key: %w", err)
	}

	return os.Rename(tmp, path)
}
