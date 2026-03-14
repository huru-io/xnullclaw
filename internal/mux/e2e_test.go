//go:build e2e

// e2e_test.go — end-to-end tests against real Docker containers and LLM APIs.
// Run with: go test -tags e2e -count=1 -timeout 300s -v ./internal/mux/...
//
// Prerequisites:
//   - Docker daemon running with nullclaw image available (see `agent.DefaultImage()`)
//   - E2E_OPENAI_KEY or OPENAI_API_KEY set to a valid OpenAI API key
package mux

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jotavich/xnullclaw/internal/agent"
	"github.com/jotavich/xnullclaw/internal/config"
	"github.com/jotavich/xnullclaw/internal/docker"
	"github.com/jotavich/xnullclaw/internal/memory"
)

// e2eEnv holds a live test environment: real Docker container, real LLM,
// mock Telegram sender, and a fully wired Bridge.
type e2eEnv struct {
	home      string
	agentName string
	cn        string // container name
	dk        *docker.Client
	backend   agent.Backend
	bridge    *Bridge
	sender    *mockSender
	store     *memory.Store
	cfg       *config.Config
	mediaTmp  string
}

// e2eSetup creates a real agent container and wires a Bridge for testing.
// Skips if E2E_OPENAI_KEY/OPENAI_API_KEY is unset or Docker is unavailable.
func e2eSetup(t *testing.T) *e2eEnv {
	t.Helper()

	openaiKey := os.Getenv("E2E_OPENAI_KEY")
	if openaiKey == "" {
		openaiKey = os.Getenv("OPENAI_API_KEY")
	}
	if openaiKey == "" {
		t.Skip("set E2E_OPENAI_KEY or OPENAI_API_KEY to run e2e tests")
	}

	ctx := context.Background()

	dk, err := docker.NewClient()
	if err != nil {
		t.Fatalf("docker: %v", err)
	}
	t.Cleanup(func() { dk.Close() })

	if err := dk.Ping(ctx); err != nil {
		t.Skipf("Docker not available: %v", err)
	}

	image := agent.DefaultImage()
	if _, err := dk.ImageInspect(ctx, image); err != nil {
		t.Skipf("image %s not available: %v", image, err)
	}

	home := t.TempDir()
	agentName := "e2etest"
	backend := &agent.LocalBackend{Home: home}

	if err := backend.Setup(agentName, agent.SetupOpts{
		OpenAIKey: openaiKey,
		SystemPrompt: "You are a test agent. Follow instructions precisely. " +
			"Keep responses extremely short (under 50 words). " +
			"When asked to say something specific, say exactly that and nothing else.",
		Model: "gpt-4o-mini",
	}); err != nil {
		t.Fatalf("setup agent: %v", err)
	}

	cn := agent.ContainerName(home, agentName)
	opts := agent.StartOpts(image, home, agentName, true, "")

	if err := dk.StartContainer(ctx, cn, opts); err != nil {
		t.Fatalf("start container: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		dk.StopContainer(bg, cn)
		dk.RemoveContainer(bg, cn, true)
	})

	port, err := dk.MappedPort(ctx, cn)
	if err != nil {
		t.Fatalf("mapped port: %v", err)
	}

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	if err := agent.WaitForHealthy(ctx, baseURL, 90*time.Second); err != nil {
		t.Fatalf("agent not healthy: %v", err)
	}
	t.Logf("agent %s healthy at %s", cn, baseURL)

	store, err := memory.New(":memory:")
	if err != nil {
		t.Fatalf("memory: %v", err)
	}

	cfg := config.DefaultConfig()
	logger := testLogger(t)
	sender := &mockSender{}

	mediaTmp := filepath.Join(home, "media_tmp")
	os.MkdirAll(mediaTmp, 0700)

	var chatID int64 = 12345
	var turnMu sync.Mutex

	bridge := &Bridge{
		home:        home,
		mediaTmpDir: mediaTmp,
		backend:     backend,
		store:       store,
		bot:         sender,
		cfg:         cfg,
		logger:      logger,
		mode:        "local",
		docker:      dk,
		chatID:      &chatID,
		turnMu:      &turnMu,
		done:        make(chan struct{}),
		conns:       make(map[string]*agentConn),
	}
	t.Cleanup(func() { bridge.CloseAll() })

	if err := bridge.Connect(ctx, agentName); err != nil {
		t.Fatalf("bridge connect: %v", err)
	}
	t.Logf("bridge connected to %s", agentName)

	return &e2eEnv{
		home:      home,
		agentName: agentName,
		cn:        cn,
		dk:        dk,
		backend:   backend,
		bridge:    bridge,
		sender:    sender,
		store:     store,
		cfg:       cfg,
		mediaTmp:  mediaTmp,
	}
}

// TestE2E runs all e2e tests against a single live agent container.
// Subtests share the container to avoid repeated startup overhead (~30s each).
// The BridgeReconnect subtest runs last because it restarts the container.
func TestE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e tests skipped in short mode")
	}

	env := e2eSetup(t)

	t.Run("BridgeRoundtrip", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()

		resp, err := env.bridge.Send(ctx, env.agentName, "Say exactly: PONG_E2E_42")
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
		if !strings.Contains(resp, "PONG_E2E_42") {
			t.Errorf("expected PONG_E2E_42 in response, got: %s", resp)
		}
		t.Logf("response: %s", resp)
	})

	t.Run("FileRetrieve", func(t *testing.T) {
		ctx := context.Background()

		// Write a file inside the container via exec.
		content := "e2e_retrieve_" + time.Now().Format(time.RFC3339Nano)
		_, err := env.dk.ExecSync(ctx, env.cn, []string{
			"sh", "-c", fmt.Sprintf("echo -n '%s' > /nullclaw-data/workspace/retrieve_test.txt", content),
		}, nil)
		if err != nil {
			t.Fatalf("exec write: %v", err)
		}

		// Retrieve via retrieveContainerFile (CopyFromContainer + tar extraction).
		hostPath, err := retrieveContainerFile(ctx, env.dk, env.home, env.agentName,
			"/nullclaw-data/workspace/retrieve_test.txt", env.mediaTmp)
		if err != nil {
			t.Fatalf("retrieve: %v", err)
		}

		got, err := os.ReadFile(hostPath)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if string(got) != content {
			t.Errorf("got %q, want %q", string(got), content)
		}
	})

	t.Run("CopyToContainer", func(t *testing.T) {
		ctx := context.Background()

		// Create a tar archive with a test file.
		payload := []byte("copy_to_e2e_content")
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		if err := tw.WriteHeader(&tar.Header{
			Name: "injected.txt",
			Mode: 0644,
			Size: int64(len(payload)),
		}); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := tw.Write(payload); err != nil {
			t.Fatalf("tar write: %v", err)
		}
		tw.Close()

		// Copy tar into container.
		if err := env.dk.CopyToContainer(ctx, env.cn, "/nullclaw-data/workspace", &buf); err != nil {
			t.Fatalf("CopyToContainer: %v", err)
		}

		// Verify via exec.
		out, err := env.dk.ExecSync(ctx, env.cn, []string{"cat", "/nullclaw-data/workspace/injected.txt"}, nil)
		if err != nil {
			t.Fatalf("exec cat: %v", err)
		}
		if out != "copy_to_e2e_content" {
			t.Errorf("got %q, want 'copy_to_e2e_content'", out)
		}
	})

	t.Run("CopyFromContainer", func(t *testing.T) {
		ctx := context.Background()

		// Write a file inside container.
		_, err := env.dk.ExecSync(ctx, env.cn, []string{
			"sh", "-c", "echo -n 'copy_from_e2e' > /nullclaw-data/workspace/from_test.txt",
		}, nil)
		if err != nil {
			t.Fatalf("exec write: %v", err)
		}

		// CopyFrom returns a tar stream.
		rc, err := env.dk.CopyFromContainer(ctx, env.cn, "/nullclaw-data/workspace/from_test.txt")
		if err != nil {
			t.Fatalf("CopyFromContainer: %v", err)
		}
		defer rc.Close()

		// Extract single file from tar.
		tr := tar.NewReader(rc)
		hdr, err := tr.Next()
		if err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if hdr.Size == 0 {
			t.Fatal("tar file has zero size")
		}
		var got bytes.Buffer
		if _, err := got.ReadFrom(tr); err != nil {
			t.Fatalf("tar read: %v", err)
		}
		if got.String() != "copy_from_e2e" {
			t.Errorf("got %q, want 'copy_from_e2e'", got.String())
		}
	})

	t.Run("DeliverUnsolicited", func(t *testing.T) {
		before := len(env.sender.sent())

		env.bridge.deliverUnsolicited(env.agentName, "autonomous agent output")

		msgs := env.sender.sent()
		if len(msgs) <= before {
			t.Fatal("expected new message sent to Telegram")
		}

		found := false
		for _, m := range msgs[before:] {
			if strings.Contains(m.text, "autonomous agent output") {
				found = true
				break
			}
		}
		if !found {
			t.Error("unsolicited content not found in Telegram sends")
		}

		// Verify memory store.
		stored, err := env.store.RecentMessages("bridge", 100)
		if err != nil {
			t.Fatalf("RecentMessages: %v", err)
		}
		if len(stored) == 0 {
			t.Fatal("expected stored bridge message")
		}
	})

	// BridgeReconnect runs last — it restarts the container.
	t.Run("BridgeReconnect", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
		defer cancel()

		// Verify bridge works before restart.
		resp, err := env.bridge.Send(ctx, env.agentName, "Say exactly: PRE_RECONNECT")
		if err != nil {
			t.Fatalf("pre-reconnect Send: %v", err)
		}
		t.Logf("pre-reconnect response: %s", resp)

		// Stop container — bridge detects connection loss.
		if err := env.dk.StopContainer(ctx, env.cn); err != nil {
			t.Fatalf("stop: %v", err)
		}

		// Wait for bridge to detect disconnection.
		deadline := time.After(30 * time.Second)
		for env.bridge.IsConnected(env.agentName) {
			select {
			case <-deadline:
				t.Fatal("bridge did not detect disconnection")
			case <-time.After(200 * time.Millisecond):
			}
		}

		// Remove and recreate container (StartContainer creates new).
		if err := env.dk.RemoveContainer(ctx, env.cn, false); err != nil {
			t.Fatalf("remove: %v", err)
		}

		opts := agent.StartOpts(agent.DefaultImage(), env.home, env.agentName, true, "")
		if err := env.dk.StartContainer(ctx, env.cn, opts); err != nil {
			t.Fatalf("restart: %v", err)
		}

		port, err := env.dk.MappedPort(ctx, env.cn)
		if err != nil {
			t.Fatalf("mapped port: %v", err)
		}
		baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
		if err := agent.WaitForHealthy(ctx, baseURL, 90*time.Second); err != nil {
			t.Fatalf("not healthy after restart: %v", err)
		}

		// Send message — getOrConnect lazily reconnects.
		resp, err = env.bridge.Send(ctx, env.agentName, "Say exactly: POST_RECONNECT")
		if err != nil {
			t.Fatalf("post-reconnect Send: %v", err)
		}
		if !strings.Contains(resp, "POST_RECONNECT") {
			t.Errorf("expected POST_RECONNECT, got: %s", resp)
		}
		t.Logf("post-reconnect response: %s", resp)
	})
}
