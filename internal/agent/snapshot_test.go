package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotAndRestore(t *testing.T) {
	home := t.TempDir()

	// Setup agent with some data.
	Setup(home, "alice", SetupOpts{})
	dir := Dir(home, "alice")
	os.WriteFile(filepath.Join(dir, "data", "workspace", "notes.txt"), []byte("important"), 0644)

	// Snapshot.
	snap, err := Snapshot(home, "alice")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Agent != "alice" {
		t.Errorf("expected agent 'alice', got %q", snap.Agent)
	}
	if snap.SizeBytes == 0 {
		t.Error("expected non-zero snapshot size")
	}

	// Verify snapshot directory exists.
	if _, err := os.Stat(snap.Dir); err != nil {
		t.Fatalf("snapshot dir missing: %v", err)
	}

	// Verify snapshot has the data file.
	snapData := filepath.Join(snap.Dir, "data", "workspace", "notes.txt")
	got, err := os.ReadFile(snapData)
	if err != nil {
		t.Fatalf("snapshot data missing: %v", err)
	}
	if string(got) != "important" {
		t.Errorf("expected 'important', got %q", got)
	}

	// Destroy original agent.
	Destroy(home, "alice")
	if Exists(home, "alice") {
		t.Fatal("alice should be destroyed")
	}

	// Restore from snapshot.
	if err := Restore(home, snap.Name, ""); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	// Agent should exist again.
	if !Exists(home, "alice") {
		t.Fatal("alice should exist after restore")
	}

	// Data should be restored.
	restored, err := os.ReadFile(filepath.Join(Dir(home, "alice"), "data", "workspace", "notes.txt"))
	if err != nil {
		t.Fatal("restored data missing")
	}
	if string(restored) != "important" {
		t.Errorf("expected 'important', got %q", restored)
	}

	// Meta should have RESTORED_FROM.
	meta, _ := ReadMeta(Dir(home, "alice"))
	if meta["RESTORED_FROM"] != snap.Name {
		t.Errorf("expected RESTORED_FROM=%s, got %q", snap.Name, meta["RESTORED_FROM"])
	}
	// Snapshot-specific keys should be gone.
	if meta["SNAPSHOT_OF"] != "" {
		t.Error("SNAPSHOT_OF should be removed after restore")
	}
}

func TestRestoreToNewName(t *testing.T) {
	home := t.TempDir()

	Setup(home, "alice", SetupOpts{})
	snap, _ := Snapshot(home, "alice")

	// Restore as different name while alice still exists.
	if err := Restore(home, snap.Name, "alice-v2"); err != nil {
		t.Fatalf("Restore as new name: %v", err)
	}

	if !Exists(home, "alice-v2") {
		t.Fatal("alice-v2 should exist")
	}

	meta, _ := ReadMeta(Dir(home, "alice-v2"))
	if meta["RESTORED_FROM"] != snap.Name {
		t.Errorf("RESTORED_FROM should be %s", snap.Name)
	}
}

func TestRestoreFailsIfExists(t *testing.T) {
	home := t.TempDir()

	Setup(home, "alice", SetupOpts{})
	snap, _ := Snapshot(home, "alice")

	err := Restore(home, snap.Name, "alice")
	if err == nil {
		t.Fatal("expected error when restoring over existing agent")
	}
}

func TestListSnapshots(t *testing.T) {
	home := t.TempDir()

	Setup(home, "alice", SetupOpts{})
	Setup(home, "bob", SetupOpts{})
	Snapshot(home, "alice")
	Snapshot(home, "bob")

	snaps, err := ListSnapshots(home)
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(snaps) != 2 {
		t.Fatalf("expected 2 snapshots, got %d", len(snaps))
	}
	// Newest first.
	if snaps[0].Created < snaps[1].Created {
		t.Error("expected newest snapshot first")
	}
}

func TestListSnapshotsEmpty(t *testing.T) {
	home := t.TempDir()
	snaps, err := ListSnapshots(home)
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(snaps) != 0 {
		t.Fatalf("expected 0 snapshots, got %d", len(snaps))
	}
}

func TestDeleteSnapshot(t *testing.T) {
	home := t.TempDir()

	Setup(home, "alice", SetupOpts{})
	snap, _ := Snapshot(home, "alice")

	if err := DeleteSnapshot(home, snap.Name); err != nil {
		t.Fatalf("DeleteSnapshot: %v", err)
	}

	snaps, _ := ListSnapshots(home)
	if len(snaps) != 0 {
		t.Error("snapshot should be deleted")
	}
}

func TestSnapshotNonexistent(t *testing.T) {
	home := t.TempDir()

	_, err := Snapshot(home, "ghost")
	if err == nil {
		t.Fatal("expected error for nonexistent agent")
	}
}
