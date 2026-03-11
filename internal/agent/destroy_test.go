package agent

import (
	"testing"
)

func TestDestroy(t *testing.T) {
	home := t.TempDir()

	Setup(home, "alice")

	if !Exists(home, "alice") {
		t.Fatal("alice should exist before destroy")
	}

	if err := Destroy(home, "alice"); err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	if Exists(home, "alice") {
		t.Fatal("alice should not exist after destroy")
	}
}

func TestDestroyNonexistent(t *testing.T) {
	home := t.TempDir()

	err := Destroy(home, "nonexistent")
	if err == nil {
		t.Fatal("expected error for destroying nonexistent agent")
	}
}
