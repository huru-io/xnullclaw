package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestCopyHostFileToContainer_Success(t *testing.T) {
	// Create a temp file to copy.
	dir := t.TempDir()
	src := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(src, []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}

	var captured bytes.Buffer
	mock := &MockOps{
		CopyToContainerFn: func(_ context.Context, name, dest string, content io.Reader) error {
			if _, err := io.Copy(&captured, content); err != nil {
				return err
			}
			return nil
		},
	}

	err := CopyHostFileToContainer(context.Background(), mock, src, "test-container", "/dest/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the tar archive contains our file.
	tr := tar.NewReader(&captured)
	hdr, err := tr.Next()
	if err != nil {
		t.Fatalf("tar read: %v", err)
	}
	if hdr.Name != "hello.txt" {
		t.Errorf("expected filename 'hello.txt', got %q", hdr.Name)
	}
	data, _ := io.ReadAll(tr)
	if string(data) != "hello world" {
		t.Errorf("expected content 'hello world', got %q", data)
	}
}

func TestCopyHostFileToContainer_FileNotFound(t *testing.T) {
	mock := &MockOps{}
	err := CopyHostFileToContainer(context.Background(), mock, "/nonexistent/file", "c", "/dest/")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestCopyHostFileToContainer_CopyError(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(src, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	mock := &MockOps{
		CopyToContainerFn: func(_ context.Context, _, _ string, _ io.Reader) error {
			return io.ErrUnexpectedEOF
		},
	}

	err := CopyHostFileToContainer(context.Background(), mock, src, "c", "/dest/")
	if err == nil {
		t.Fatal("expected error from CopyToContainer")
	}
}

func TestTarDir_Success(t *testing.T) {
	dir := t.TempDir()

	// Create a few files.
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("aaa"), 0644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "b.txt"), []byte("bbb"), 0644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := tarDir(dir, &buf); err != nil {
		t.Fatalf("tarDir: %v", err)
	}

	// Read back the tar and verify files.
	files := make(map[string]string)
	tr := tar.NewReader(&buf)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar read: %v", err)
		}
		if hdr.Typeflag == tar.TypeDir {
			continue
		}
		data, _ := io.ReadAll(tr)
		files[hdr.Name] = string(data)
	}

	if files["a.txt"] != "aaa" {
		t.Errorf("a.txt: got %q, want %q", files["a.txt"], "aaa")
	}
	if v, ok := files[filepath.Join("sub", "b.txt")]; !ok || v != "bbb" {
		t.Errorf("sub/b.txt: got %q (ok=%v), want %q", v, ok, "bbb")
	}
}

func TestTarDir_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	var buf bytes.Buffer
	if err := tarDir(dir, &buf); err != nil {
		t.Fatalf("tarDir on empty dir: %v", err)
	}

	// Should produce a valid but empty tar.
	tr := tar.NewReader(&buf)
	_, err := tr.Next()
	if err != io.EOF {
		t.Errorf("expected EOF for empty dir, got %v", err)
	}
}

func TestTarDir_NonExistentDir(t *testing.T) {
	var buf bytes.Buffer
	err := tarDir("/nonexistent/path", &buf)
	if err == nil {
		t.Fatal("expected error for non-existent directory")
	}
}
