package voice

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// NOTE: These tests mutate the package-level voiceClient var via withTestServer.
// Do NOT use t.Parallel() — concurrent tests would race on the shared client.

// rewriteTransport intercepts outgoing requests and redirects them
// to a local httptest server, allowing us to test against the
// package-level baseURL const without modifying it.
type rewriteTransport struct {
	base   http.RoundTripper
	target string // test server URL, e.g. "http://127.0.0.1:12345"
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	req.URL.Host = strings.TrimPrefix(t.target, "http://")
	return t.base.RoundTrip(req)
}

// withTestServer replaces the package-level voiceClient with one that
// routes all requests to ts. It returns a cleanup function that
// restores the original client.
func withTestServer(ts *httptest.Server) func() {
	orig := voiceClient
	voiceClient = &http.Client{
		Transport: &rewriteTransport{
			base:   http.DefaultTransport,
			target: ts.URL,
		},
	}
	return func() { voiceClient = orig }
}

// tmpAudioFile creates a small temporary file to act as a fake audio
// input and returns its path.
func tmpAudioFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "test.ogg")
	if err := os.WriteFile(p, []byte("fake-audio-bytes"), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

// --- Transcribe tests ---

func TestTranscribe_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/transcriptions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("unexpected auth header: %s", got)
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"text": "hello"})
	}))
	defer ts.Close()
	defer withTestServer(ts)()

	text, err := Transcribe(context.Background(), tmpAudioFile(t), "test-key", "whisper-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "hello" {
		t.Fatalf("got %q, want %q", text, "hello")
	}
}

func TestTranscribe_DefaultModel(t *testing.T) {
	var receivedModel string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Parse multipart to inspect the "model" field.
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse multipart: %v", err)
		}
		receivedModel = r.FormValue("model")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"text": "ok"})
	}))
	defer ts.Close()
	defer withTestServer(ts)()

	// Pass empty model; package should default to "whisper-1".
	_, err := Transcribe(context.Background(), tmpAudioFile(t), "k", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receivedModel != "whisper-1" {
		t.Fatalf("default model: got %q, want %q", receivedModel, "whisper-1")
	}
}

func TestTranscribe_APIError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"boom"}`))
	}))
	defer ts.Close()
	defer withTestServer(ts)()

	_, err := Transcribe(context.Background(), tmpAudioFile(t), "k", "")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("error should mention status 500: %v", err)
	}
}

func TestTranscribe_BadFile(t *testing.T) {
	// No server needed; the function should fail before making a request.
	_, err := Transcribe(context.Background(), "/nonexistent/audio.ogg", "k", "")
	if err == nil {
		t.Fatal("expected error for non-existent file, got nil")
	}
	if !strings.Contains(err.Error(), "open audio") {
		t.Fatalf("error should mention opening audio: %v", err)
	}
}

// --- Synthesize tests ---

func TestSynthesize_Success(t *testing.T) {
	audioPayload := []byte("synthesized-audio-data")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/speech" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("unexpected auth header: %s", got)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("unexpected content-type: %s", ct)
		}
		w.WriteHeader(http.StatusOK)
		w.Write(audioPayload)
	}))
	defer ts.Close()
	defer withTestServer(ts)()

	dest := filepath.Join(t.TempDir(), "out.mp3")
	err := Synthesize(context.Background(), "say this", "test-key", "tts-1", "alloy", dest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	if string(got) != string(audioPayload) {
		t.Fatalf("file content: got %q, want %q", got, audioPayload)
	}
}

func TestSynthesize_DefaultModelAndVoice(t *testing.T) {
	var receivedBody map[string]any

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedBody)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("audio"))
	}))
	defer ts.Close()
	defer withTestServer(ts)()

	dest := filepath.Join(t.TempDir(), "out.mp3")
	// Pass empty model and voice; package should default to "tts-1" and "nova".
	err := Synthesize(context.Background(), "hi", "k", "", "", dest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if m, _ := receivedBody["model"].(string); m != "tts-1" {
		t.Fatalf("default model: got %q, want %q", m, "tts-1")
	}
	if v, _ := receivedBody["voice"].(string); v != "nova" {
		t.Fatalf("default voice: got %q, want %q", v, "nova")
	}
}

func TestSynthesize_APIError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"bad request"}`))
	}))
	defer ts.Close()
	defer withTestServer(ts)()

	dest := filepath.Join(t.TempDir(), "out.mp3")
	err := Synthesize(context.Background(), "hi", "k", "", "", dest)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Fatalf("error should mention status 400: %v", err)
	}
}
