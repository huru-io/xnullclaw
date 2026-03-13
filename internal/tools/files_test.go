package tools

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jotavich/xnullclaw/internal/agent"
)

func TestSendFileToAgent_PathOutsideMediaTmp(t *testing.T) {
	d, _ := newTestDeps(t)
	agent.Setup(d.Home, "Alice", agent.SetupOpts{})

	r := NewRegistry()
	registerFileTools(r, d)

	// file_path is outside the mux media_tmp directory.
	_, err := r.Execute(context.Background(), "send_file_to_agent", map[string]any{
		"agent":     "Alice",
		"file_path": "/etc/passwd",
		"message":   "here's a file",
	})
	if err == nil {
		t.Fatal("expected error for path outside media_tmp")
	}
	expectedDir := filepath.Join(d.Home, "mux", "media_tmp")
	if !strings.Contains(err.Error(), expectedDir) {
		t.Errorf("expected error to mention %q, got: %v", expectedDir, err)
	}
}

func TestGetAgentFile_PathOutsideNullclaw(t *testing.T) {
	d, _ := newTestDeps(t)
	agent.Setup(d.Home, "Alice", agent.SetupOpts{})

	r := NewRegistry()
	registerFileTools(r, d)

	// Container path is outside /nullclaw-data.
	_, err := r.Execute(context.Background(), "get_agent_file", map[string]any{
		"agent":     "Alice",
		"path":      "/etc/shadow",
		"dest_path": filepath.Join(d.Home, "mux", "media_tmp", "output.txt"),
	})
	if err == nil {
		t.Fatal("expected error for container path outside /nullclaw-data")
	}
	if !strings.Contains(err.Error(), "must be under /nullclaw-data") {
		t.Errorf("expected 'must be under /nullclaw-data' in error, got: %v", err)
	}
}

func TestGetAgentFile_DestOutsideMediaTmp(t *testing.T) {
	d, _ := newTestDeps(t)
	agent.Setup(d.Home, "Alice", agent.SetupOpts{})

	r := NewRegistry()
	registerFileTools(r, d)

	// dest_path is outside media_tmp.
	_, err := r.Execute(context.Background(), "get_agent_file", map[string]any{
		"agent":     "Alice",
		"path":      "/nullclaw-data/output.txt",
		"dest_path": "/tmp/evil.txt",
	})
	if err == nil {
		t.Fatal("expected error for dest_path outside media_tmp")
	}
	expectedDir := filepath.Join(d.Home, "mux", "media_tmp")
	if !strings.Contains(err.Error(), expectedDir) {
		t.Errorf("expected error to mention %q, got: %v", expectedDir, err)
	}
}

func TestListAgentFiles_RestrictedPath(t *testing.T) {
	d, _ := newTestDeps(t)
	agent.Setup(d.Home, "Alice", agent.SetupOpts{})

	r := NewRegistry()
	registerFileTools(r, d)

	// Trying to list /etc should be rejected.
	_, err := r.Execute(context.Background(), "list_agent_files", map[string]any{
		"agent": "Alice",
		"path":  "/etc",
	})
	if err == nil {
		t.Fatal("expected error for restricted path")
	}
	if !strings.Contains(err.Error(), "must be under /nullclaw-data") {
		t.Errorf("expected 'must be under /nullclaw-data' in error, got: %v", err)
	}
}

func TestListAgentFiles_DefaultPath(t *testing.T) {
	d, mock := newTestDeps(t)
	agent.Setup(d.Home, "Alice", agent.SetupOpts{})

	var capturedCmd []string
	mock.ExecSyncFn = func(ctx context.Context, name string, cmd []string, stdin io.Reader) (string, error) {
		capturedCmd = cmd
		return "total 0", nil
	}

	r := NewRegistry()
	registerFileTools(r, d)

	// No path specified — should default to /nullclaw-data.
	result, err := r.Execute(context.Background(), "list_agent_files", map[string]any{
		"agent": "Alice",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "total 0") {
		t.Errorf("unexpected result: %q", result)
	}
	// Verify the command used /nullclaw-data.
	if len(capturedCmd) >= 3 && capturedCmd[2] != "/nullclaw-data" {
		t.Errorf("expected default path /nullclaw-data, got %q", capturedCmd[2])
	}
}

func TestListAgentFiles_PathTraversal(t *testing.T) {
	d, _ := newTestDeps(t)
	agent.Setup(d.Home, "Alice", agent.SetupOpts{})

	r := NewRegistry()
	registerFileTools(r, d)

	// Path traversal attempt.
	_, err := r.Execute(context.Background(), "list_agent_files", map[string]any{
		"agent": "Alice",
		"path":  "/nullclaw-data/../../etc",
	})
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
}

func TestSendFileToAgent_PathTraversal(t *testing.T) {
	d, _ := newTestDeps(t)
	agent.Setup(d.Home, "Alice", agent.SetupOpts{})

	r := NewRegistry()
	registerFileTools(r, d)

	// Try path traversal in file_path.
	traversalPath := filepath.Join(d.Home, "mux", "media_tmp", "..", "..", "etc", "passwd")
	_, err := r.Execute(context.Background(), "send_file_to_agent", map[string]any{
		"agent":     "Alice",
		"file_path": traversalPath,
		"message":   "sneaky",
	})
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
}

func TestListAgentFiles_InvalidAgent(t *testing.T) {
	d, _ := newTestDeps(t)

	r := NewRegistry()
	registerFileTools(r, d)

	_, err := r.Execute(context.Background(), "list_agent_files", map[string]any{
		"agent": "!!!",
	})
	if err == nil {
		t.Fatal("expected error for invalid agent name")
	}
}

// Verify that ExecSync stub signature matches what mock expects (io.Reader).
func TestListAgentFiles_WithExecSyncMock(t *testing.T) {
	d, mock := newTestDeps(t)
	agent.Setup(d.Home, "Alice", agent.SetupOpts{})

	mock.ExecSyncFn = func(ctx context.Context, name string, cmd []string, stdin io.Reader) (string, error) {
		return "drwxr-xr-x 2 root root 4096 Jan 1 00:00 inbox\n-rw-r--r-- 1 root root 42 Jan 1 00:00 config.json", nil
	}

	r := NewRegistry()
	registerFileTools(r, d)

	result, err := r.Execute(context.Background(), "list_agent_files", map[string]any{
		"agent": "Alice",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "inbox") {
		t.Errorf("expected 'inbox' in result, got %q", result)
	}
}
