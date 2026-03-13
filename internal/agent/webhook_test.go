package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSendWebhook_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected application/json, got %s", ct)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer zc_test123" {
			t.Errorf("expected Bearer auth, got %s", auth)
		}

		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["message"] != "hello agent" {
			t.Errorf("unexpected message: %v", body)
		}

		json.NewEncoder(w).Encode(map[string]string{
			"status":   "ok",
			"response": "I received your message",
		})
	}))
	defer srv.Close()

	resp, err := SendWebhook(context.Background(), srv.URL, "zc_test123", "hello agent")
	if err != nil {
		t.Fatalf("SendWebhook: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("status = %q, want ok", resp.Status)
	}
	if resp.Response != "I received your message" {
		t.Errorf("response = %q", resp.Response)
	}
}

func TestSendWebhook_NoToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); auth != "" {
			t.Errorf("expected no auth header, got %s", auth)
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer srv.Close()

	_, err := SendWebhook(context.Background(), srv.URL, "", "test")
	if err != nil {
		t.Fatalf("SendWebhook: %v", err)
	}
}

func TestSendWebhook_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte("unauthorized"))
	}))
	defer srv.Close()

	_, err := SendWebhook(context.Background(), srv.URL, "bad-token", "test")
	if err == nil {
		t.Fatal("expected error for 401")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention 401: %v", err)
	}
}

func TestSendWebhook_ConnectionRefused(t *testing.T) {
	_, err := SendWebhook(context.Background(), "http://127.0.0.1:1", "token", "test")
	if err == nil {
		t.Fatal("expected error for unreachable port")
	}
}

func TestSendWebhook_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		w.Write([]byte("service unavailable"))
	}))
	defer srv.Close()

	_, err := SendWebhook(context.Background(), srv.URL, "token", "test")
	if err == nil {
		t.Fatal("expected error for 503")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("error should mention 503: %v", err)
	}
}

func TestSendWebhook_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html>not json</html>"))
	}))
	defer srv.Close()

	_, err := SendWebhook(context.Background(), srv.URL, "", "test")
	if err == nil {
		t.Fatal("expected error for non-JSON response")
	}
	if !strings.Contains(err.Error(), "parse webhook response") {
		t.Errorf("error should mention parse failure: %v", err)
	}
}

func TestSendWebhook_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := SendWebhook(ctx, "http://127.0.0.1:9999", "token", "test")
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestSendWebhook_MessageTooLarge(t *testing.T) {
	largeMsg := strings.Repeat("x", maxWebhookMessageSize+1)
	_, err := SendWebhook(context.Background(), "http://127.0.0.1:9999", "token", largeMsg)
	if err == nil {
		t.Fatal("expected error for oversized message")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Errorf("error should mention size: %v", err)
	}
}

func TestSendWebhook_LargeResponseCapped(t *testing.T) {
	// Response slightly over 2MB — should be capped by io.LimitReader.
	// The truncated JSON will fail to parse, producing a parse error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Write a JSON response where "response" field exceeds 2MB.
		w.Write([]byte(`{"status":"ok","response":"`))
		w.Write([]byte(strings.Repeat("a", 3*1024*1024))) // 3MB of data
		w.Write([]byte(`"}`))
	}))
	defer srv.Close()

	_, err := SendWebhook(context.Background(), srv.URL, "", "test")
	if err == nil {
		t.Fatal("expected error for truncated large response")
	}
	if !strings.Contains(err.Error(), "parse webhook response") {
		t.Errorf("error should mention parse failure from truncation: %v", err)
	}
}

func TestFriendlyWebhookError(t *testing.T) {
	tests := []struct {
		err  string
		want string
	}{
		{"webhook request failed: dial tcp 127.0.0.1:49823: connection refused", "agent gateway is not reachable"},
		{"webhook returned HTTP 401: unauthorized", "authentication failed"},
		{"webhook request failed: context deadline exceeded", "agent did not respond"},
		{"message too large (300000 bytes, max 262144)", "too large"},
		{"read token for alice: permission denied", "could not read auth token"},
		{"some other error", "some other error"},
	}
	for _, tt := range tests {
		got := FriendlyWebhookError(fmt.Errorf("%s", tt.err))
		if !strings.Contains(got, tt.want) {
			t.Errorf("FriendlyWebhookError(%q) = %q, want containing %q", tt.err, got, tt.want)
		}
	}

	if FriendlyWebhookError(nil) != "" {
		t.Error("nil error should return empty string")
	}
}

func TestAgentBaseURL(t *testing.T) {
	tests := []struct {
		name          string
		mode          string
		port          int
		containerName string
		want          string
	}{
		{"local mode", "local", 49823, "xnc-abc-alice", "http://127.0.0.1:49823"},
		{"docker mode", "docker", 0, "xnc-abc-alice", "http://xnc-abc-alice:3000"},
		{"docker mode empty container", "docker", 8080, "", "http://127.0.0.1:8080"},
		{"unknown mode", "bogus", 9999, "xnc-abc-alice", "http://127.0.0.1:9999"},
		{"empty mode", "", 3000, "xnc-abc-alice", "http://127.0.0.1:3000"},
		{"docker unsafe name", "docker", 5000, "alice@evil.com:3000/", "http://127.0.0.1:5000"},
		{"docker name with dots", "docker", 5000, "xnc-abc.alice", "http://127.0.0.1:5000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AgentBaseURL(tt.mode, tt.port, tt.containerName)
			if got != tt.want {
				t.Errorf("AgentBaseURL(%q, %d, %q) = %q, want %q",
					tt.mode, tt.port, tt.containerName, got, tt.want)
			}
		})
	}
}

func TestTrySendWebhook_NoPort(t *testing.T) {
	resp, err := TrySendWebhook(context.Background(), "local", 0, "", "/tmp", "test", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != nil {
		t.Errorf("expected nil response for port 0, got %v", resp)
	}
}

func TestTrySendWebhook_DockerMode(t *testing.T) {
	// Start a test server that simulates an agent gateway.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"status": "ok", "response": "docker pong"})
	}))
	defer srv.Close()

	// Set up a temp agent dir with a token file.
	home := t.TempDir()
	Setup(home, "dtest", SetupOpts{})

	// In docker mode with empty containerName and port 0 → should return (nil, nil).
	resp, err := TrySendWebhook(context.Background(), "docker", 0, "", home, "dtest", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != nil {
		t.Error("expected nil response for docker mode with empty containerName and port 0")
	}
}

func TestTrySendWebhook_DockerModeNoPortFallback(t *testing.T) {
	// Docker mode with non-empty containerName: skips port check, uses DNS.
	// This will fail to connect since the container doesn't exist, but it
	// should NOT return (nil, nil) — it should attempt the request.
	home := t.TempDir()
	Setup(home, "dtest2", SetupOpts{})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_, err := TrySendWebhook(ctx, "docker", 0, "nonexistent-container", home, "dtest2", "hello")
	// Should get a connection error, NOT nil (which would mean "skip").
	if err == nil {
		t.Error("expected connection error for unreachable docker container, got nil")
	}
}
