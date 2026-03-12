package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// SnapshotDir returns the base directory for snapshots under home.
func SnapshotDir(home string) string {
	return filepath.Join(home, "backups")
}

// SnapshotInfo describes a stored snapshot.
type SnapshotInfo struct {
	Name      string `json:"name"`       // snapshot directory name (agent-timestamp)
	Agent     string `json:"agent"`      // original agent name
	Emoji     string `json:"emoji"`      // original emoji
	Created   string `json:"created"`    // when snapshot was taken
	Dir       string `json:"dir"`        // full path
	SizeBytes int64  `json:"size_bytes"` // total size
}

// Snapshot copies an agent's full state (config, data, meta) into
// ~/.xnc/backups/<agentname>-<timestamp>/
// The agent must be stopped before snapshotting.
func Snapshot(home, name string) (SnapshotInfo, error) {
	if !Exists(home, name) {
		return SnapshotInfo{}, fmt.Errorf("agent %q does not exist", name)
	}

	srcDir := Dir(home, name)
	now := time.Now().UTC()
	stamp := now.Format("20060102-150405")
	snapName := name + "-" + stamp

	snapDir := filepath.Join(SnapshotDir(home), snapName)
	if err := os.MkdirAll(snapDir, 0700); err != nil {
		return SnapshotInfo{}, fmt.Errorf("snapshot: create dir: %w", err)
	}

	// Copy everything: config.json, .meta, data/
	if err := copyDir(srcDir, snapDir); err != nil {
		os.RemoveAll(snapDir)
		return SnapshotInfo{}, fmt.Errorf("snapshot: copy: %w", err)
	}

	// Write snapshot metadata.
	meta, _ := ReadMeta(srcDir)
	if err := WriteMetaBatch(snapDir, map[string]string{
		"SNAPSHOT_OF":   name,
		"SNAPSHOT_TIME": now.Format(time.RFC3339),
	}); err != nil {
		os.RemoveAll(snapDir)
		return SnapshotInfo{}, fmt.Errorf("snapshot: write meta: %w", err)
	}

	size := dirSize(snapDir)

	return SnapshotInfo{
		Name:      snapName,
		Agent:     name,
		Emoji:     meta["EMOJI"],
		Created:   now.Format(time.RFC3339),
		Dir:       snapDir,
		SizeBytes: size,
	}, nil
}

// Restore creates a new agent from a snapshot.
// The target agent must NOT exist — destroy it first.
func Restore(home, snapName, targetName string) error {
	if targetName == "" {
		// Default: use the original agent name from the snapshot.
		snapDir := filepath.Join(SnapshotDir(home), snapName)
		targetName = ReadMetaKey(snapDir, "SNAPSHOT_OF", snapName)
	}

	if err := ValidateName(targetName); err != nil {
		return err
	}
	if Exists(home, targetName) {
		return fmt.Errorf("agent %q already exists; destroy it first", targetName)
	}

	snapDir := filepath.Join(SnapshotDir(home), snapName)
	if _, err := os.Stat(snapDir); err != nil {
		return fmt.Errorf("snapshot %q not found", snapName)
	}

	targetDir := Dir(home, targetName)

	// Copy snapshot contents to agent directory.
	if err := copyDir(snapDir, targetDir); err != nil {
		os.RemoveAll(targetDir)
		return fmt.Errorf("restore: copy: %w", err)
	}

	// Update meta: new CREATED, keep original emoji, record restore source.
	now := time.Now().UTC().Format(time.RFC3339)
	if err := WriteMetaBatch(targetDir, map[string]string{
		"CREATED":       now,
		"RESTORED_FROM": snapName,
	}); err != nil {
		return fmt.Errorf("restore: write meta: %w", err)
	}

	// Remove snapshot-specific meta keys from the restored agent.
	DeleteMetaKey(targetDir, "SNAPSHOT_OF")
	DeleteMetaKey(targetDir, "SNAPSHOT_TIME")

	return nil
}

// ListSnapshots returns all snapshots under home, sorted by creation time (newest first).
func ListSnapshots(home string) ([]SnapshotInfo, error) {
	snapBase := SnapshotDir(home)
	entries, err := os.ReadDir(snapBase)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list snapshots: %w", err)
	}

	var snaps []SnapshotInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(snapBase, e.Name())
		meta, _ := ReadMeta(dir)

		snaps = append(snaps, SnapshotInfo{
			Name:      e.Name(),
			Agent:     meta["SNAPSHOT_OF"],
			Emoji:     meta["EMOJI"],
			Created:   meta["SNAPSHOT_TIME"],
			Dir:       dir,
			SizeBytes: dirSize(dir),
		})
	}

	// Newest first.
	sort.Slice(snaps, func(i, j int) bool {
		return snaps[i].Created > snaps[j].Created
	})
	return snaps, nil
}

// DeleteSnapshot removes a snapshot from disk.
func DeleteSnapshot(home, snapName string) error {
	dir := filepath.Join(SnapshotDir(home), snapName)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("snapshot %q not found", snapName)
	}
	return os.RemoveAll(dir)
}

// dirSize returns the total size of all files in a directory tree.
func dirSize(path string) int64 {
	var total int64
	filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}
