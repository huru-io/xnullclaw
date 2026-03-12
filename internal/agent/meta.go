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
	existing, err := readMetaOrEmpty(agentDir)
	if err != nil {
		return err
	}
	existing[key] = value
	return writeMetaMap(agentDir, existing)
}

// WriteMetaBatch writes multiple key=value pairs atomically.
func WriteMetaBatch(agentDir string, pairs map[string]string) error {
	existing, err := readMetaOrEmpty(agentDir)
	if err != nil {
		return err
	}
	for k, v := range pairs {
		existing[k] = v
	}
	return writeMetaMap(agentDir, existing)
}

// DeleteMetaKey removes a key from the .meta file.
func DeleteMetaKey(agentDir, key string) error {
	existing, err := readMetaOrEmpty(agentDir)
	if err != nil {
		return err
	}
	if _, ok := existing[key]; !ok {
		return nil // nothing to delete
	}
	delete(existing, key)
	return writeMetaMap(agentDir, existing)
}

// readMetaOrEmpty returns existing meta, treating not-exist as empty.
// Other read errors are propagated.
func readMetaOrEmpty(agentDir string) (map[string]string, error) {
	m, err := ReadMeta(agentDir)
	if err != nil {
		return nil, fmt.Errorf("meta: %w", err)
	}
	return m, nil
}

// writeMetaMap atomically writes a map of key=value pairs to the .meta file.
func writeMetaMap(agentDir string, data map[string]string) error {
	path := filepath.Join(agentDir, metaFile)
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("write meta: %w", err)
	}

	for k, v := range data {
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
