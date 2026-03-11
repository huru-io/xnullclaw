package agent

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// CloneOpts controls clone behavior.
type CloneOpts struct {
	WithData bool // also copy the data/ directory
}

// Clone duplicates an agent. Config is always copied. Data is optional.
func Clone(home, src, dst string, opts CloneOpts) error {
	if err := ValidateName(dst); err != nil {
		return err
	}
	if !Exists(home, src) {
		return fmt.Errorf("source agent %q does not exist", src)
	}
	if Exists(home, dst) {
		return fmt.Errorf("destination agent %q already exists", dst)
	}
	if conflict, found := ConflictsWith(home, dst); found {
		return fmt.Errorf("agent %q conflicts with existing agent %q (names sound the same)", dst, conflict)
	}

	srcDir := Dir(home, src)
	dstDir := Dir(home, dst)

	// Create destination directory structure.
	for _, sub := range []string{
		"data/.nullclaw",
		"data/workspace",
	} {
		if err := os.MkdirAll(filepath.Join(dstDir, sub), 0755); err != nil {
			return fmt.Errorf("clone: create %s: %w", sub, err)
		}
	}

	// Copy config.json.
	if err := copyFile(
		filepath.Join(srcDir, "config.json"),
		filepath.Join(dstDir, "config.json"),
	); err != nil {
		return fmt.Errorf("clone: copy config: %w", err)
	}

	// Optionally copy data.
	if opts.WithData {
		if err := copyDir(
			filepath.Join(srcDir, "data"),
			filepath.Join(dstDir, "data"),
		); err != nil {
			return fmt.Errorf("clone: copy data: %w", err)
		}
	}

	// Assign new identity.
	emoji := NextEmoji(home, dst)
	now := time.Now().UTC().Format(time.RFC3339)

	if err := WriteMetaBatch(dstDir, map[string]string{
		"NAME":        dst,
		"CREATED":     now,
		"EMOJI":       emoji,
		"CLONED_FROM": src,
	}); err != nil {
		return fmt.Errorf("clone: write meta: %w", err)
	}

	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}

		return copyFile(path, target)
	})
}
