package mux

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jotavich/xnullclaw/internal/docker"
	"github.com/jotavich/xnullclaw/internal/media"
)

// makeTar builds a tar archive containing a single file and returns it as an
// io.ReadCloser suitable for use as a CopyFromContainer return value.
func makeTar(name string, content []byte) io.ReadCloser {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(content))})
	_, _ = tw.Write(content)
	_ = tw.Close()
	return io.NopCloser(&buf)
}

// makeTarLarge builds a tar archive whose header advertises a file of the given
// size but writes no actual data (the reader will hit EOF, but the header size
// is what the code under test checks).
func makeTarLarge(name string, size int64) io.ReadCloser {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: size})
	_ = tw.Close()
	return io.NopCloser(&buf)
}

// makeTarEmpty builds a valid tar archive with zero entries.
func makeTarEmpty() io.ReadCloser {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.Close()
	return io.NopCloser(&buf)
}

// --- retrieveContainerFile tests ---

func TestRetrieveContainerFile_HappyPath(t *testing.T) {
	content := []byte("hello world")
	dops := &docker.MockOps{
		CopyFromContainerFn: func(ctx context.Context, name, path string) (io.ReadCloser, error) {
			return makeTar("workspace/report.txt", content), nil
		},
	}

	tmpDir := t.TempDir()
	ctx := context.Background()

	got, err := retrieveContainerFile(ctx, dops, "/tmp/home", "alice", "/nullclaw-data/workspace/report.txt", tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Returned path must be inside mediaTmpDir.
	if !strings.HasPrefix(got, tmpDir) {
		t.Errorf("returned path %q not under mediaTmpDir %q", got, tmpDir)
	}

	// File must exist and have correct content.
	data, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("read returned file: %v", err)
	}
	if !bytes.Equal(data, content) {
		t.Errorf("content mismatch: got %q, want %q", data, content)
	}

	// Filename should contain the original base name.
	base := filepath.Base(got)
	if !strings.HasSuffix(base, "_report.txt") {
		t.Errorf("filename %q should end with _report.txt", base)
	}

	// Filename should start with a numeric timestamp prefix.
	parts := strings.SplitN(base, "_", 2)
	if len(parts) < 2 {
		t.Fatalf("expected timestamp_name format, got %q", base)
	}
	// The timestamp part should be all digits.
	for _, c := range parts[0] {
		if c < '0' || c > '9' {
			t.Errorf("timestamp prefix %q contains non-digit %c", parts[0], c)
			break
		}
	}
}

func TestRetrieveContainerFile_PathOutsideNullclawData(t *testing.T) {
	dops := &docker.MockOps{}
	ctx := context.Background()

	_, err := retrieveContainerFile(ctx, dops, "/tmp/home", "alice", "/etc/passwd", t.TempDir())
	if err == nil {
		t.Fatal("expected error for path outside /nullclaw-data")
	}
	if !strings.Contains(err.Error(), "path must be under /nullclaw-data") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestRetrieveContainerFile_PathTraversal(t *testing.T) {
	dops := &docker.MockOps{}
	ctx := context.Background()

	_, err := retrieveContainerFile(ctx, dops, "/tmp/home", "alice", "/nullclaw-data/../../etc/passwd", t.TempDir())
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
	if !strings.Contains(err.Error(), "path must be under /nullclaw-data") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestRetrieveContainerFile_TooLarge(t *testing.T) {
	dops := &docker.MockOps{
		CopyFromContainerFn: func(ctx context.Context, name, path string) (io.ReadCloser, error) {
			return makeTarLarge("big.bin", maxRetrieveSize+1), nil
		},
	}
	ctx := context.Background()

	_, err := retrieveContainerFile(ctx, dops, "/tmp/home", "alice", "/nullclaw-data/big.bin", t.TempDir())
	if err == nil {
		t.Fatal("expected error for oversized file")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestRetrieveContainerFile_CopyError(t *testing.T) {
	copyErr := errors.New("container not running")
	dops := &docker.MockOps{
		CopyFromContainerFn: func(ctx context.Context, name, path string) (io.ReadCloser, error) {
			return nil, copyErr
		},
	}
	ctx := context.Background()

	_, err := retrieveContainerFile(ctx, dops, "/tmp/home", "alice", "/nullclaw-data/file.txt", t.TempDir())
	if err == nil {
		t.Fatal("expected error from CopyFromContainer")
	}
	if !errors.Is(err, copyErr) {
		t.Errorf("expected wrapped copyErr, got: %v", err)
	}
}

func TestRetrieveContainerFile_EmptyTar(t *testing.T) {
	dops := &docker.MockOps{
		CopyFromContainerFn: func(ctx context.Context, name, path string) (io.ReadCloser, error) {
			return makeTarEmpty(), nil
		},
	}
	ctx := context.Background()

	_, err := retrieveContainerFile(ctx, dops, "/tmp/home", "alice", "/nullclaw-data/file.txt", t.TempDir())
	if err == nil {
		t.Fatal("expected error for empty tar archive")
	}
	if !strings.Contains(err.Error(), "tar header") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// --- resolveContainerAttachments tests ---

func TestResolveContainerAttachments_MixedPaths(t *testing.T) {
	content := []byte("image data")
	dops := &docker.MockOps{
		CopyFromContainerFn: func(ctx context.Context, name, path string) (io.ReadCloser, error) {
			return makeTar("img.jpg", content), nil
		},
	}
	tmpDir := t.TempDir()
	ctx := context.Background()

	attachments := []media.Attachment{
		{Type: media.TypeImage, Path: "/nullclaw-data/workspace/img.jpg"},
		{Type: media.TypeFile, Path: "/tmp/local.pdf"},
	}

	var logCalls []string
	logErr := func(msg string, args ...any) {
		logCalls = append(logCalls, msg)
	}

	result := resolveContainerAttachments(ctx, dops, "/tmp/home", "alice", tmpDir, attachments, logErr)

	if len(result) != 2 {
		t.Fatalf("expected 2 attachments, got %d", len(result))
	}

	// First attachment should be resolved to a host path in tmpDir.
	if !strings.HasPrefix(result[0].Path, tmpDir) {
		t.Errorf("container path not resolved: %q", result[0].Path)
	}
	if result[0].Type != media.TypeImage {
		t.Errorf("type mismatch: got %q, want %q", result[0].Type, media.TypeImage)
	}

	// Second attachment should pass through unchanged.
	if result[1].Path != "/tmp/local.pdf" {
		t.Errorf("local path changed: got %q, want /tmp/local.pdf", result[1].Path)
	}
	if result[1].Type != media.TypeFile {
		t.Errorf("type mismatch: got %q, want %q", result[1].Type, media.TypeFile)
	}

	// No errors expected.
	if len(logCalls) != 0 {
		t.Errorf("unexpected log calls: %v", logCalls)
	}
}

func TestResolveContainerAttachments_RetrievalFailure(t *testing.T) {
	callCount := 0
	dops := &docker.MockOps{
		CopyFromContainerFn: func(ctx context.Context, name, path string) (io.ReadCloser, error) {
			callCount++
			if callCount == 2 {
				return nil, errors.New("disk full")
			}
			return makeTar("ok.txt", []byte("ok")), nil
		},
	}
	tmpDir := t.TempDir()
	ctx := context.Background()

	attachments := []media.Attachment{
		{Type: media.TypeDocument, Path: "/nullclaw-data/doc1.pdf"},
		{Type: media.TypeDocument, Path: "/nullclaw-data/doc2.pdf"},
	}

	var logCalls []string
	logErr := func(msg string, args ...any) {
		logCalls = append(logCalls, msg)
	}

	result := resolveContainerAttachments(ctx, dops, "/tmp/home", "alice", tmpDir, attachments, logErr)

	// Only the first attachment should survive; second was dropped.
	if len(result) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(result))
	}
	if !strings.HasPrefix(result[0].Path, tmpDir) {
		t.Errorf("first attachment not resolved: %q", result[0].Path)
	}

	// logErr should have been called for the failed retrieval.
	if len(logCalls) == 0 {
		t.Error("expected logErr to be called for failed retrieval")
	}
	if len(logCalls) > 0 && !strings.Contains(logCalls[0], "retrieve attachment failed") {
		t.Errorf("unexpected log message: %q", logCalls[0])
	}
}

func TestResolveContainerAttachments_AllMediaTypes(t *testing.T) {
	dops := &docker.MockOps{
		CopyFromContainerFn: func(ctx context.Context, name, path string) (io.ReadCloser, error) {
			return makeTar("file", []byte("data")), nil
		},
	}
	tmpDir := t.TempDir()
	ctx := context.Background()

	types := []media.MarkerType{
		media.TypeImage,
		media.TypeFile,
		media.TypeDocument,
		media.TypeVideo,
		media.TypeAudio,
		media.TypeVoice,
	}

	attachments := make([]media.Attachment, len(types))
	for i, mt := range types {
		attachments[i] = media.Attachment{
			Type: mt,
			Path: "/nullclaw-data/workspace/" + strings.ToLower(string(mt)) + ".dat",
		}
	}

	logErr := func(msg string, args ...any) {
		t.Errorf("unexpected logErr call: %s", msg)
	}

	result := resolveContainerAttachments(ctx, dops, "/tmp/home", "alice", tmpDir, attachments, logErr)

	if len(result) != len(types) {
		t.Fatalf("expected %d attachments, got %d", len(types), len(result))
	}

	for i, att := range result {
		if att.Type != types[i] {
			t.Errorf("attachment %d: type %q, want %q", i, att.Type, types[i])
		}
		if !strings.HasPrefix(att.Path, tmpDir) {
			t.Errorf("attachment %d: path not resolved: %q", i, att.Path)
		}
	}
}

func TestResolveContainerAttachments_EmptySlice(t *testing.T) {
	dops := &docker.MockOps{}
	ctx := context.Background()

	logErr := func(msg string, args ...any) {
		t.Errorf("unexpected logErr call: %s", msg)
	}

	result := resolveContainerAttachments(ctx, dops, "/tmp/home", "alice", t.TempDir(), nil, logErr)

	if len(result) != 0 {
		t.Fatalf("expected 0 attachments, got %d", len(result))
	}

	result = resolveContainerAttachments(ctx, dops, "/tmp/home", "alice", t.TempDir(), []media.Attachment{}, logErr)

	if len(result) != 0 {
		t.Fatalf("expected 0 attachments for empty slice, got %d", len(result))
	}
}
