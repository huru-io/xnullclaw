package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/jotavich/xnullclaw/internal/agent"
	"github.com/jotavich/xnullclaw/internal/docker"
)

func TestSendToAgent_WebhookPath(t *testing.T) {
	// Set up a test HTTP server that acts as the gateway.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			t.Error("expected Authorization header")
		}
		json.NewEncoder(w).Encode(map[string]string{
			"status":   "ok",
			"response": "hello back",
		})
	}))
	defer srv.Close()
	port, _ := strconv.Atoi(strings.Split(srv.URL, ":")[2])

	home := t.TempDir()
	agent.Setup(home, "alice", agent.SetupOpts{})

	mock := &docker.MockOps{
		MappedPortFn: func(ctx context.Context, name string) (int, error) {
			return port, nil
		},
	}
	d := Deps{Docker: mock, Home: home, Image: "test:latest"}

	resp, err := sendToAgent(context.Background(), d, "alice", "hello")
	if err != nil {
		t.Fatalf("sendToAgent: %v", err)
	}
	if resp != "hello back" {
		t.Errorf("response = %q, want %q", resp, "hello back")
	}
}

func TestSendToAgent_WebhookEmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"status": "ok", "response": ""})
	}))
	defer srv.Close()
	port, _ := strconv.Atoi(strings.Split(srv.URL, ":")[2])

	home := t.TempDir()
	agent.Setup(home, "alice", agent.SetupOpts{})

	mock := &docker.MockOps{
		MappedPortFn: func(ctx context.Context, name string) (int, error) {
			return port, nil
		},
	}
	d := Deps{Docker: mock, Home: home, Image: "test:latest"}

	resp, err := sendToAgent(context.Background(), d, "alice", "hello")
	if err != nil {
		t.Fatalf("sendToAgent: %v", err)
	}
	if !strings.Contains(resp, "webhook") {
		t.Errorf("expected webhook delivery message, got %q", resp)
	}
}

func TestSendToAgent_FallbackToExec(t *testing.T) {
	home := t.TempDir()
	agent.Setup(home, "bob", agent.SetupOpts{})

	execCalled := false
	mock := &docker.MockOps{
		MappedPortFn: func(ctx context.Context, name string) (int, error) {
			return 0, nil // no port mapped → fallback
		},
		ExecFireFn: func(ctx context.Context, name string, cmd []string, stdin io.Reader) error {
			execCalled = true
			return nil
		},
	}
	d := Deps{Docker: mock, Home: home, Image: "test:latest"}

	resp, err := sendToAgent(context.Background(), d, "bob", "hi")
	if err != nil {
		t.Fatalf("sendToAgent: %v", err)
	}
	if !execCalled {
		t.Error("expected exec fallback to be called")
	}
	if !strings.Contains(resp, "shortly") {
		t.Errorf("expected async delivery message, got %q", resp)
	}
}

func TestSendToAgent_WebhookError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		w.Write([]byte("service unavailable"))
	}))
	defer srv.Close()
	port, _ := strconv.Atoi(strings.Split(srv.URL, ":")[2])

	home := t.TempDir()
	agent.Setup(home, "alice", agent.SetupOpts{})

	mock := &docker.MockOps{
		MappedPortFn: func(ctx context.Context, name string) (int, error) {
			return port, nil
		},
	}
	d := Deps{Docker: mock, Home: home, Image: "test:latest"}

	_, err := sendToAgent(context.Background(), d, "alice", "hello")
	if err == nil {
		t.Fatal("expected error for 503 webhook response")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("error should mention 503: %v", err)
	}
}

func TestSendToMultiple_Parallel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"status":   "ok",
			"response": "ack",
		})
	}))
	defer srv.Close()
	port, _ := strconv.Atoi(strings.Split(srv.URL, ":")[2])

	home := t.TempDir()
	agent.Setup(home, "alice", agent.SetupOpts{})
	agent.Setup(home, "bob", agent.SetupOpts{})

	mock := &docker.MockOps{
		MappedPortFn: func(ctx context.Context, name string) (int, error) {
			return port, nil
		},
	}
	d := Deps{Docker: mock, Home: home, Image: "test:latest"}

	resp, err := sendToMultiple(context.Background(), d, []string{"alice", "bob"}, "broadcast")
	if err != nil {
		t.Fatalf("sendToMultiple: %v", err)
	}

	// Parse JSON results.
	var results []struct {
		Agent  string `json:"agent"`
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal([]byte(resp), &results); err != nil {
		t.Fatalf("parse results: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for _, r := range results {
		if r.Status != "delivered" {
			t.Errorf("agent %s: status = %q, want delivered", r.Agent, r.Status)
		}
	}
}
