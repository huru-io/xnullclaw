package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRename_Success(t *testing.T) {
	home := t.TempDir()

	// Create agent "alice".
	if err := Setup(home, "alice", SetupOpts{}); err != nil {
		t.Fatalf("setup alice: %v", err)
	}

	// Rename alice → bob.
	if err := Rename(home, "alice", "bob"); err != nil {
		t.Fatalf("rename: %v", err)
	}

	// alice should no longer exist, bob should.
	if Exists(home, "alice") {
		t.Error("alice should not exist after rename")
	}
	if !Exists(home, "bob") {
		t.Error("bob should exist after rename")
	}

	// .meta NAME should be updated.
	name := ReadMetaKey(Dir(home, "bob"), "NAME", "")
	if name != "bob" {
		t.Errorf("NAME meta: got %q, want %q", name, "bob")
	}
}

func TestRename_WritesIdentityFile(t *testing.T) {
	home := t.TempDir()

	if err := Setup(home, "alice", SetupOpts{}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// Create workspace dir (setup may not create it).
	ws := filepath.Join(Dir(home, "alice"), "data", "workspace")
	if err := os.MkdirAll(ws, 0755); err != nil {
		t.Fatal(err)
	}

	if err := Rename(home, "alice", "bob"); err != nil {
		t.Fatalf("rename: %v", err)
	}

	// Check IDENTITY.md was written.
	idFile := filepath.Join(Dir(home, "bob"), "data", "workspace", "IDENTITY.md")
	data, err := os.ReadFile(idFile)
	if err != nil {
		t.Fatalf("read IDENTITY.md: %v", err)
	}
	if !strings.Contains(string(data), "bob") {
		t.Error("IDENTITY.md should contain new name 'bob'")
	}
	if !strings.Contains(string(data), "alice") {
		t.Error("IDENTITY.md should contain old name 'alice'")
	}
}

func TestRename_InvalidNewName(t *testing.T) {
	home := t.TempDir()
	if err := Setup(home, "alice", SetupOpts{}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	err := Rename(home, "alice", "../escape")
	if err == nil {
		t.Fatal("expected error for invalid name")
	}
}

func TestRename_NonExistentSource(t *testing.T) {
	home := t.TempDir()
	err := Rename(home, "ghost", "bob")
	if err == nil {
		t.Fatal("expected error for non-existent agent")
	}
}

func TestRename_TargetExists(t *testing.T) {
	home := t.TempDir()
	if err := Setup(home, "alice", SetupOpts{}); err != nil {
		t.Fatalf("setup alice: %v", err)
	}
	if err := Setup(home, "bob", SetupOpts{}); err != nil {
		t.Fatalf("setup bob: %v", err)
	}

	err := Rename(home, "alice", "bob")
	if err == nil {
		t.Fatal("expected error when target exists")
	}
}

func TestRename_SameCanonicalName(t *testing.T) {
	home := t.TempDir()
	if err := Setup(home, "alice", SetupOpts{}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// Rename to different case — same canonical name, should just update display name.
	if err := Rename(home, "alice", "Alice"); err != nil {
		t.Fatalf("rename same canonical: %v", err)
	}

	name := ReadMetaKey(Dir(home, "alice"), "NAME", "")
	if name != "Alice" {
		t.Errorf("NAME meta: got %q, want %q", name, "Alice")
	}
}

func TestIdentityChangeMessage(t *testing.T) {
	msg := IdentityChangeMessage("alice", "bob")
	if !strings.Contains(msg, "alice") || !strings.Contains(msg, "bob") {
		t.Errorf("message should contain both names: %s", msg)
	}
}
