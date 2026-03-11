package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClone(t *testing.T) {
	home := t.TempDir()

	// Setup source.
	Setup(home, "alice", SetupOpts{})

	// Write some data to source.
	srcData := filepath.Join(Dir(home, "alice"), "data", "workspace", "hello.txt")
	os.WriteFile(srcData, []byte("hello"), 0644)

	// Clone without data.
	if err := Clone(home, "alice", "bob", CloneOpts{}); err != nil {
		t.Fatalf("Clone: %v", err)
	}

	if !Exists(home, "bob") {
		t.Fatal("bob should exist after clone")
	}

	// Config should be copied.
	_, err := os.ReadFile(filepath.Join(Dir(home, "bob"), "config.json"))
	if err != nil {
		t.Fatal("config.json should exist in clone")
	}

	// Data file should NOT be copied (WithData=false).
	_, err = os.Stat(filepath.Join(Dir(home, "bob"), "data", "workspace", "hello.txt"))
	if err == nil {
		t.Error("data file should not be copied without WithData")
	}

	// Meta should have CLONED_FROM.
	meta, _ := ReadMeta(Dir(home, "bob"))
	if meta["CLONED_FROM"] != "alice" {
		t.Errorf("expected CLONED_FROM=alice, got %q", meta["CLONED_FROM"])
	}

	// Emoji should be different from source.
	srcMeta, _ := ReadMeta(Dir(home, "alice"))
	if meta["EMOJI"] == srcMeta["EMOJI"] {
		t.Error("cloned agent should get a different emoji")
	}
}

func TestCloneWithData(t *testing.T) {
	home := t.TempDir()
	Setup(home, "alice", SetupOpts{})

	// Write data.
	srcData := filepath.Join(Dir(home, "alice"), "data", "workspace", "hello.txt")
	os.WriteFile(srcData, []byte("hello world"), 0644)

	// Clone with data.
	if err := Clone(home, "alice", "charlie", CloneOpts{WithData: true}); err != nil {
		t.Fatalf("Clone with data: %v", err)
	}

	// Data file should be copied.
	got, err := os.ReadFile(filepath.Join(Dir(home, "charlie"), "data", "workspace", "hello.txt"))
	if err != nil {
		t.Fatal("data file should exist in clone with data")
	}
	if string(got) != "hello world" {
		t.Errorf("expected 'hello world', got %q", got)
	}
}

func TestCloneNonexistent(t *testing.T) {
	home := t.TempDir()

	err := Clone(home, "nonexistent", "bob", CloneOpts{})
	if err == nil {
		t.Fatal("expected error for cloning nonexistent agent")
	}
}
