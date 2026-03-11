package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetup(t *testing.T) {
	home := t.TempDir()

	if err := Setup(home, "alice"); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	// Agent should exist.
	if !Exists(home, "alice") {
		t.Fatal("alice should exist after setup")
	}

	// Check directory structure.
	dir := Dir(home, "alice")
	for _, sub := range []string{
		"data/.nullclaw",
		"data/workspace",
	} {
		path := filepath.Join(dir, sub)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("expected dir %s to exist: %v", sub, err)
		} else if !info.IsDir() {
			t.Errorf("expected %s to be a directory", sub)
		}
	}

	// Check config.json exists and is valid JSON.
	cfgData, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("read config.json: %v", err)
	}
	if len(cfgData) < 10 {
		t.Error("config.json too small")
	}

	// Check .meta has CREATED and EMOJI.
	meta, err := ReadMeta(dir)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if meta["CREATED"] == "" {
		t.Error("CREATED not set in .meta")
	}
	if meta["EMOJI"] == "" {
		t.Error("EMOJI not set in .meta")
	}
}

func TestSetupDuplicate(t *testing.T) {
	home := t.TempDir()

	Setup(home, "alice")
	err := Setup(home, "alice")
	if err == nil {
		t.Fatal("expected error for duplicate setup")
	}
}

func TestSetupInvalidName(t *testing.T) {
	home := t.TempDir()

	err := Setup(home, "1bad")
	if err == nil {
		t.Fatal("expected error for invalid name")
	}
}

func TestSetupReservedName(t *testing.T) {
	home := t.TempDir()

	err := Setup(home, "mux")
	if err == nil {
		t.Fatal("expected error for reserved name")
	}
}
