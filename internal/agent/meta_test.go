package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMetaReadWrite(t *testing.T) {
	dir := t.TempDir()

	// Read from non-existent file returns empty map.
	m, err := ReadMeta(dir)
	if err != nil {
		t.Fatalf("ReadMeta empty: %v", err)
	}
	if len(m) != 0 {
		t.Fatalf("expected empty map, got %v", m)
	}

	// Write first key.
	if err := WriteMeta(dir, "EMOJI", "🍎"); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Read it back.
	if got := ReadMetaKey(dir, "EMOJI", ""); got != "🍎" {
		t.Errorf("expected 🍎, got %q", got)
	}

	// Write second key.
	if err := WriteMeta(dir, "CREATED", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Both keys present.
	m, err = ReadMeta(dir)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if len(m) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(m))
	}
	if m["EMOJI"] != "🍎" || m["CREATED"] != "2026-01-01T00:00:00Z" {
		t.Errorf("unexpected values: %v", m)
	}

	// Update existing key.
	if err := WriteMeta(dir, "EMOJI", "🍊"); err != nil {
		t.Fatalf("WriteMeta update: %v", err)
	}
	if got := ReadMetaKey(dir, "EMOJI", ""); got != "🍊" {
		t.Errorf("expected 🍊 after update, got %q", got)
	}

	// Default value for missing key.
	if got := ReadMetaKey(dir, "NONEXISTENT", "default"); got != "default" {
		t.Errorf("expected 'default', got %q", got)
	}
}

func TestWriteMetaBatch(t *testing.T) {
	dir := t.TempDir()

	err := WriteMetaBatch(dir, map[string]string{
		"A": "1",
		"B": "2",
		"C": "3",
	})
	if err != nil {
		t.Fatalf("WriteMetaBatch: %v", err)
	}

	m, err := ReadMeta(dir)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if len(m) != 3 {
		t.Fatalf("expected 3 keys, got %d", len(m))
	}
}

func TestDeleteMetaKey(t *testing.T) {
	dir := t.TempDir()

	WriteMetaBatch(dir, map[string]string{"A": "1", "B": "2"})

	if err := DeleteMetaKey(dir, "A"); err != nil {
		t.Fatalf("DeleteMetaKey: %v", err)
	}

	m, _ := ReadMeta(dir)
	if _, ok := m["A"]; ok {
		t.Error("key A should be deleted")
	}
	if m["B"] != "2" {
		t.Errorf("key B should still be '2', got %q", m["B"])
	}
}

func TestMetaAtomicWrite(t *testing.T) {
	dir := t.TempDir()

	WriteMeta(dir, "KEY", "value")

	// Verify no .tmp file left behind.
	tmpPath := filepath.Join(dir, ".meta.tmp")
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Error("temp file should not exist after successful write")
	}
}
