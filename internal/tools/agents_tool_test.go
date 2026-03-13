package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/jotavich/xnullclaw/internal/agent"
	"github.com/jotavich/xnullclaw/internal/config"
	"github.com/jotavich/xnullclaw/internal/docker"
)

// --- Pure function tests ---

func TestIsReservedName_Match(t *testing.T) {
	cfg := config.DefaultConfig()
	// Default persona name is "Mux"
	if !isReservedName(cfg, "Mux") {
		t.Error("expected 'Mux' to be reserved")
	}
	// Case-insensitive
	if !isReservedName(cfg, "mux") {
		t.Error("expected 'mux' (lowercase) to be reserved")
	}
	if !isReservedName(cfg, "MUX") {
		t.Error("expected 'MUX' (uppercase) to be reserved")
	}
}

func TestIsReservedName_NoMatch(t *testing.T) {
	cfg := config.DefaultConfig()
	if isReservedName(cfg, "Alice") {
		t.Error("expected 'Alice' to NOT be reserved")
	}
	if isReservedName(cfg, "bob") {
		t.Error("expected 'bob' to NOT be reserved")
	}
}

func TestPickAgentName_FromPool(t *testing.T) {
	d, _ := newTestDeps(t)
	// No agents exist, so we should get a name from the pool.
	name := pickAgentName(d)
	if name == "" {
		t.Fatal("expected non-empty name")
	}

	// Verify it's one of the pool names.
	found := false
	for _, poolName := range namePool {
		if name == poolName {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("name %q not found in namePool", name)
	}
}

func TestPickAgentName_Fallback(t *testing.T) {
	d, _ := newTestDeps(t)

	// Create agents for all pool names so they're all taken.
	for _, name := range namePool {
		agent.Setup(d.Home, name, agent.SetupOpts{})
	}

	name := pickAgentName(d)
	if name == "" {
		t.Fatal("expected non-empty name")
	}
	// Should be a fallback name like "Agent-1"
	if !strings.HasPrefix(name, "Agent-") {
		t.Errorf("expected fallback name starting with 'Agent-', got %q", name)
	}
}

func TestBuildAgentSystemPrompt(t *testing.T) {
	v := personaVariant{
		Trait:          "friendly and creative",
		Warmth:         0.7,
		Humor:          0.5,
		Verbosity:      0.3,
		Proactiveness:  0.6,
		Formality:      0.4,
		Empathy:        0.5,
		Sarcasm:        0.1,
		Autonomy:       0.5,
		Interpretation: 0.2,
		Creativity:     0.8,
	}
	prompt := buildAgentSystemPrompt("Alice", v)

	if !strings.Contains(prompt, "Alice") {
		t.Error("prompt should contain the agent name")
	}
	if !strings.Contains(prompt, "friendly and creative") {
		t.Error("prompt should contain the trait")
	}
	if !strings.Contains(prompt, "Communication style:") {
		t.Error("prompt should contain 'Communication style:' section")
	}
}

func TestValidatedAgentArg_Valid(t *testing.T) {
	args := map[string]any{"agent": "Alice"}
	name, err := validatedAgentArg(args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "Alice" {
		t.Errorf("got %q, want %q", name, "Alice")
	}
}

func TestValidatedAgentArg_Missing(t *testing.T) {
	args := map[string]any{}
	_, err := validatedAgentArg(args)
	if err == nil {
		t.Fatal("expected error for missing 'agent' key")
	}
}

func TestValidatedAgentArg_Invalid(t *testing.T) {
	// Agent name with invalid chars.
	args := map[string]any{"agent": "!!!"}
	_, err := validatedAgentArg(args)
	if err == nil {
		t.Fatal("expected error for invalid agent name")
	}
}

// --- Tool execution tests via Registry.Execute ---

func TestListAgents_Empty(t *testing.T) {
	d, _ := newTestDeps(t)
	r := NewRegistry()
	registerAgentTools(r, d)

	result, err := r.Execute(context.Background(), "list_agents", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should be valid JSON (empty array or null).
	var parsed []any
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		// Could be "null" which is also valid JSON.
		if result != "null" {
			t.Fatalf("expected valid JSON, got %q: %v", result, err)
		}
	}
}

func TestListAgents_WithAgents(t *testing.T) {
	d, _ := newTestDeps(t)
	// Create some agents.
	agent.Setup(d.Home, "Alice", agent.SetupOpts{})
	agent.Setup(d.Home, "Bob", agent.SetupOpts{})

	r := NewRegistry()
	registerAgentTools(r, d)

	result, err := r.Execute(context.Background(), "list_agents", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed []agent.Info
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v\nresult: %s", err, result)
	}
	if len(parsed) != 2 {
		t.Errorf("expected 2 agents, got %d", len(parsed))
	}
}

func TestAgentStatus(t *testing.T) {
	d, mock := newTestDeps(t)
	agent.Setup(d.Home, "Alice", agent.SetupOpts{})

	mock.InspectContainerFn = func(ctx context.Context, name string) (*docker.ContainerInfo, error) {
		return &docker.ContainerInfo{
			Name:  name,
			State: "running",
		}, nil
	}

	r := NewRegistry()
	registerAgentTools(r, d)

	result, err := r.Execute(context.Background(), "agent_status", map[string]any{"agent": "Alice"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "running") {
		t.Errorf("expected 'running' in result, got %s", result)
	}
}

func TestStopAgent(t *testing.T) {
	d, mock := newTestDeps(t)
	agent.Setup(d.Home, "Alice", agent.SetupOpts{})

	stopCalled := false
	mock.StopContainerFn = func(ctx context.Context, name string) error {
		stopCalled = true
		return nil
	}

	r := NewRegistry()
	registerAgentTools(r, d)

	result, err := r.Execute(context.Background(), "stop_agent", map[string]any{"agent": "Alice"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !stopCalled {
		t.Error("expected StopContainer to be called")
	}
	if !strings.Contains(result, "stopped") {
		t.Errorf("expected 'stopped' in result, got %q", result)
	}
}

func TestDestroyAgent_NotConfirmed(t *testing.T) {
	d, _ := newTestDeps(t)
	agent.Setup(d.Home, "Alice", agent.SetupOpts{})

	r := NewRegistry()
	registerAgentTools(r, d)

	// Without confirm=true, should return a warning message (not an error).
	result, err := r.Execute(context.Background(), "destroy_agent", map[string]any{"agent": "Alice"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "PERMANENTLY DESTROY") {
		t.Errorf("expected warning message, got %q", result)
	}
}

func TestDestroyAgent_Confirmed(t *testing.T) {
	d, _ := newTestDeps(t)
	agent.Setup(d.Home, "Alice", agent.SetupOpts{})

	r := NewRegistry()
	registerAgentTools(r, d)

	result, err := r.Execute(context.Background(), "destroy_agent", map[string]any{
		"agent":   "Alice",
		"confirm": true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "destroyed") {
		t.Errorf("expected 'destroyed' in result, got %q", result)
	}
	// Agent directory should be removed.
	if agent.Exists(d.Home, "Alice") {
		t.Error("expected agent directory to be removed")
	}
}

func TestGetAgentConfig(t *testing.T) {
	d, _ := newTestDeps(t)
	agent.Setup(d.Home, "Alice", agent.SetupOpts{})

	r := NewRegistry()
	registerAgentTools(r, d)

	result, err := r.Execute(context.Background(), "get_agent_config", map[string]any{"agent": "Alice"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should be valid JSON.
	var parsed map[string]any
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("expected valid JSON, got error: %v\nresult: %s", err, result)
	}
}

func TestUpdateAgentConfig_UnknownKey(t *testing.T) {
	d, _ := newTestDeps(t)
	agent.Setup(d.Home, "Alice", agent.SetupOpts{})

	r := NewRegistry()
	registerAgentTools(r, d)

	_, err := r.Execute(context.Background(), "update_agent_config", map[string]any{
		"agent": "Alice",
		"key":   "nonexistent_key_xyz",
		"value": "something",
	})
	if err == nil {
		t.Fatal("expected error for unknown config key")
	}
	if !strings.Contains(err.Error(), "unknown config key") {
		t.Errorf("expected 'unknown config key' in error, got: %v", err)
	}
}

func TestRenameAgent_Running(t *testing.T) {
	d, mock := newTestDeps(t)
	agent.Setup(d.Home, "Alice", agent.SetupOpts{})

	mock.IsRunningFn = func(ctx context.Context, name string) (bool, error) {
		return true, nil
	}

	r := NewRegistry()
	registerAgentTools(r, d)

	_, err := r.Execute(context.Background(), "rename_agent", map[string]any{
		"agent":    "Alice",
		"new_name": "Beth",
	})
	if err == nil {
		t.Fatal("expected error when agent is running")
	}
	if !strings.Contains(err.Error(), "stop it first") {
		t.Errorf("expected 'stop it first' in error, got: %v", err)
	}
}

func TestRenameAgent_NewNameExists(t *testing.T) {
	d, mock := newTestDeps(t)
	agent.Setup(d.Home, "Alice", agent.SetupOpts{})
	agent.Setup(d.Home, "Beth", agent.SetupOpts{})

	mock.IsRunningFn = func(ctx context.Context, name string) (bool, error) {
		return false, nil
	}

	r := NewRegistry()
	registerAgentTools(r, d)

	_, err := r.Execute(context.Background(), "rename_agent", map[string]any{
		"agent":    "Alice",
		"new_name": "Beth",
	})
	if err == nil {
		t.Fatal("expected error when new name already exists")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("expected 'already exists' in error, got: %v", err)
	}
}

func TestRenameAgent_ReservedName(t *testing.T) {
	d, mock := newTestDeps(t)
	agent.Setup(d.Home, "Alice", agent.SetupOpts{})

	mock.IsRunningFn = func(ctx context.Context, name string) (bool, error) {
		return false, nil
	}

	r := NewRegistry()
	registerAgentTools(r, d)

	// "Mux" is in agent.reservedNames, so ValidateName rejects it.
	_, err := r.Execute(context.Background(), "rename_agent", map[string]any{
		"agent":    "Alice",
		"new_name": "Mux",
	})
	if err == nil {
		t.Fatal("expected error for reserved name")
	}
	if !strings.Contains(err.Error(), "reserved") {
		t.Errorf("expected 'reserved' in error, got: %v", err)
	}
}

func TestCloneAgent_ReservedName(t *testing.T) {
	d, _ := newTestDeps(t)
	agent.Setup(d.Home, "Alice", agent.SetupOpts{})

	r := NewRegistry()
	registerAgentTools(r, d)

	// "Mux" is in agent.reservedNames, so ValidateName rejects it.
	_, err := r.Execute(context.Background(), "clone_agent", map[string]any{
		"agent":  "Mux",
		"source": "Alice",
	})
	if err == nil {
		t.Fatal("expected error for reserved name clone")
	}
	if !strings.Contains(err.Error(), "reserved") {
		t.Errorf("expected 'reserved' in error, got: %v", err)
	}
}

func TestGetAgentPersona_NoPersona(t *testing.T) {
	d, _ := newTestDeps(t)
	agent.Setup(d.Home, "Alice", agent.SetupOpts{})

	r := NewRegistry()
	registerAgentTools(r, d)

	result, err := r.Execute(context.Background(), "get_agent_persona", map[string]any{"agent": "Alice"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "No persona stored") {
		t.Errorf("expected 'No persona stored' message, got %q", result)
	}
}

func TestUpdateAgentPersona_Warmth(t *testing.T) {
	d, _ := newTestDeps(t)
	agent.Setup(d.Home, "Alice", agent.SetupOpts{})

	r := NewRegistry()
	registerAgentTools(r, d)

	result, err := r.Execute(context.Background(), "update_agent_persona", map[string]any{
		"agent":  "Alice",
		"warmth": 0.9,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "warmth=0.9") {
		t.Errorf("expected 'warmth=0.9' in result, got %q", result)
	}
}

func TestUpdateAgentPersona_NoChanges(t *testing.T) {
	d, _ := newTestDeps(t)
	agent.Setup(d.Home, "Alice", agent.SetupOpts{})

	r := NewRegistry()
	registerAgentTools(r, d)

	// No dimension args provided.
	result, err := r.Execute(context.Background(), "update_agent_persona", map[string]any{
		"agent": "Alice",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "No dimensions changed") {
		t.Errorf("expected 'No dimensions changed', got %q", result)
	}
}

func TestSendToSome_EmptyList(t *testing.T) {
	d, _ := newTestDeps(t)

	r := NewRegistry()
	registerAgentTools(r, d)

	_, err := r.Execute(context.Background(), "send_to_some", map[string]any{
		"agents":  []any{},
		"message": "hello",
	})
	if err == nil {
		t.Fatal("expected error for empty agents list")
	}
	if !strings.Contains(err.Error(), "must not be empty") {
		t.Errorf("expected 'must not be empty' in error, got: %v", err)
	}
}

func TestSendToAll_NoRunning(t *testing.T) {
	d, mock := newTestDeps(t)

	mock.ListContainersFn = func(ctx context.Context, prefix string) ([]docker.ContainerInfo, error) {
		return nil, nil
	}

	r := NewRegistry()
	registerAgentTools(r, d)

	result, err := r.Execute(context.Background(), "send_to_all", map[string]any{
		"message": "hello",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "No running agents") {
		t.Errorf("expected 'No running agents' message, got %q", result)
	}
}

func TestSendToSome_InvalidAgentName(t *testing.T) {
	d, _ := newTestDeps(t)

	r := NewRegistry()
	registerAgentTools(r, d)

	_, err := r.Execute(context.Background(), "send_to_some", map[string]any{
		"agents":  []any{"!!!"},
		"message": "hello",
	})
	if err == nil {
		t.Fatal("expected error for invalid agent name in list")
	}
	if !strings.Contains(err.Error(), "invalid") {
		t.Errorf("expected 'invalid' in error, got: %v", err)
	}
}

func TestProvisionAgent_ReservedName(t *testing.T) {
	d, _ := newTestDeps(t)

	r := NewRegistry()
	registerAgentTools(r, d)

	// "Mux" is the default persona name.
	_, err := r.Execute(context.Background(), "provision_agent", map[string]any{
		"agent": "Mux",
	})
	if err == nil {
		t.Fatal("expected error for reserved name")
	}
	if !strings.Contains(err.Error(), "mux bot") {
		t.Errorf("expected 'mux bot' in error, got: %v", err)
	}
}

// TestStopAgent_InvalidAgent tests that an invalid agent name returns an error.
func TestStopAgent_InvalidAgent(t *testing.T) {
	d, _ := newTestDeps(t)
	r := NewRegistry()
	registerAgentTools(r, d)

	_, err := r.Execute(context.Background(), "stop_agent", map[string]any{"agent": "!!!"})
	if err == nil {
		t.Fatal("expected error for invalid agent name")
	}
}

// TestStartAgent_NoAPIKey tests that starting an agent without a provider key fails.
func TestStartAgent_NoAPIKey(t *testing.T) {
	d, _ := newTestDeps(t)
	agent.Setup(d.Home, "Alice", agent.SetupOpts{})

	r := NewRegistry()
	registerAgentTools(r, d)

	_, err := r.Execute(context.Background(), "start_agent", map[string]any{"agent": "Alice"})
	if err == nil {
		t.Fatal("expected error when agent has no API key")
	}
	if !strings.Contains(err.Error(), "no API key") {
		t.Errorf("expected 'no API key' in error, got: %v", err)
	}
}

// TestStopAgent_DockerError tests that a Docker error propagates.
func TestStopAgent_DockerError(t *testing.T) {
	d, mock := newTestDeps(t)
	agent.Setup(d.Home, "Alice", agent.SetupOpts{})

	mock.StopContainerFn = func(ctx context.Context, name string) error {
		return fmt.Errorf("container not found")
	}

	r := NewRegistry()
	registerAgentTools(r, d)

	_, err := r.Execute(context.Background(), "stop_agent", map[string]any{"agent": "Alice"})
	if err == nil {
		t.Fatal("expected error from Docker")
	}
	if !strings.Contains(err.Error(), "container not found") {
		t.Errorf("expected 'container not found' in error, got: %v", err)
	}
}
