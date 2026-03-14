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
	d := Deps{Docker: mock, Backend: &agent.LocalBackend{Home: home}, Home: home, Image: "test:latest"}

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
	d := Deps{Docker: mock, Backend: &agent.LocalBackend{Home: home}, Home: home, Image: "test:latest"}

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
	d := Deps{Docker: mock, Backend: &agent.LocalBackend{Home: home}, Home: home, Image: "test:latest"}

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
	d := Deps{Docker: mock, Backend: &agent.LocalBackend{Home: home}, Home: home, Image: "test:latest"}

	_, err := sendToAgent(context.Background(), d, "alice", "hello")
	if err == nil {
		t.Fatal("expected error for 503 webhook response")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("error should mention 503: %v", err)
	}
}

func TestSendToAgent_KubernetesGuard(t *testing.T) {
	// In K8s mode, when webhook fails, exec fallback should NOT be attempted.
	// Instead, the error should mention "kubernetes".
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Webhook returns a non-parseable response → TrySendWebhook returns error.
		w.WriteHeader(http.StatusServiceUnavailable)
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
	d := Deps{
		Docker:      mock,
		Backend:     &agent.LocalBackend{Home: home},
		Home:        home,
		Image:       "test:latest",
		RuntimeMode: "kubernetes",
	}

	// Webhook returns 503 → sendToAgent returns error (not fallback to exec).
	_, err := sendToAgent(context.Background(), d, "alice", "hi")
	if err == nil {
		t.Fatal("expected error in kubernetes mode with failing webhook")
	}
	// The error comes from the webhook failure, not the K8s guard.
	// The K8s guard prevents exec fallback — so no exec should be called.
	if !strings.Contains(err.Error(), "503") && !strings.Contains(err.Error(), "webhook") {
		t.Errorf("error should mention webhook failure: %v", err)
	}
}

func TestSendToAgent_KubernetesGuard_NoExecFallback(t *testing.T) {
	// In K8s mode, when the webhook fails (DNS unreachable, connection refused,
	// etc.), the exec fallback should NOT be attempted. The error should be
	// returned directly from the webhook attempt.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Webhook returns connection refused / 503.
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("service unavailable"))
	}))
	defer srv.Close()
	port, _ := strconv.Atoi(strings.Split(srv.URL, ":")[2])

	home := t.TempDir()
	agent.Setup(home, "alice", agent.SetupOpts{})

	execCalled := false
	mock := &docker.MockOps{
		MappedPortFn: func(ctx context.Context, name string) (int, error) {
			return port, nil
		},
		ExecFireFn: func(ctx context.Context, name string, cmd []string, stdin io.Reader) error {
			execCalled = true
			return nil
		},
	}
	d := Deps{
		Docker:      mock,
		Backend:     &agent.LocalBackend{Home: home},
		Home:        home,
		Image:       "test:latest",
		RuntimeMode: "kubernetes",
	}

	_, err := sendToAgent(context.Background(), d, "alice", "hi")
	if err == nil {
		t.Fatal("expected error in kubernetes mode with failing webhook")
	}
	if execCalled {
		t.Error("exec fallback should NOT be called in kubernetes mode")
	}
}

func TestStartOpts_KubernetesUsesBackendEnv(t *testing.T) {
	home := t.TempDir()
	agent.Setup(home, "alice", agent.SetupOpts{})

	envCalled := false
	mock := &agent.MockBackend{
		ContainerEnvFn: func(name string) ([]string, error) {
			envCalled = true
			return []string{"BRAVE_API_KEY=test-key", "CUSTOM=val"}, nil
		},
	}

	d := Deps{
		Backend:     mock,
		Home:        home,
		Image:       "test:latest",
		RuntimeMode: "kubernetes",
	}

	opts := startOpts(d, "alice")
	if !envCalled {
		t.Fatal("expected Backend.ContainerEnv to be called in kubernetes mode")
	}
	if len(opts.Env) != 2 {
		t.Fatalf("expected 2 env vars, got %d: %v", len(opts.Env), opts.Env)
	}
}

func TestStartOpts_LocalUsesFilesystem(t *testing.T) {
	home := t.TempDir()
	agent.Setup(home, "alice", agent.SetupOpts{})

	envCalled := false
	mock := &agent.MockBackend{
		ContainerEnvFn: func(name string) ([]string, error) {
			envCalled = true
			return nil, nil
		},
	}

	d := Deps{
		Backend: mock,
		Home:    home,
		Image:   "test:latest",
		// RuntimeMode is empty → local mode
	}

	_ = startOpts(d, "alice")
	if envCalled {
		t.Error("Backend.ContainerEnv should NOT be called in local mode")
	}
}

func TestEnvOrDefault(t *testing.T) {
	// With env var set.
	t.Setenv("XNC_TEST_ENV_OR_DEFAULT", "from-env")
	if got := envOrDefault("XNC_TEST_ENV_OR_DEFAULT", "fallback"); got != "from-env" {
		t.Errorf("envOrDefault with set env = %q, want %q", got, "from-env")
	}

	// With env var empty.
	t.Setenv("XNC_TEST_ENV_OR_DEFAULT", "")
	if got := envOrDefault("XNC_TEST_ENV_OR_DEFAULT", "fallback"); got != "fallback" {
		t.Errorf("envOrDefault with empty env = %q, want %q", got, "fallback")
	}

	// With env var unset (use a key that doesn't exist).
	if got := envOrDefault("XNC_TEST_NONEXISTENT_VAR_12345", "fb"); got != "fb" {
		t.Errorf("envOrDefault with unset env = %q, want %q", got, "fb")
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
	d := Deps{Docker: mock, Backend: &agent.LocalBackend{Home: home}, Home: home, Image: "test:latest"}

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
