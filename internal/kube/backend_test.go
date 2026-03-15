package kube

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jotavich/xnullclaw/internal/agent"
)

// TestKubeBackend_CollectKeys verifies CollectKeys aggregates provider keys
// and returns the correct values keyed by provider name.
func TestKubeBackend_CollectKeys(t *testing.T) {
	store := newMockK8sStore()
	b := newTestBackend(t, store)

	b.Setup("alice", agent.SetupOpts{OpenAIKey: "sk-alice"})
	b.Setup("bob", agent.SetupOpts{AnthropicKey: "ant-bob"})

	keys := b.CollectKeys()

	// The mock's POST handler merges StringData into Data, simulating K8s behavior.
	// CollectKeys reads from Secret.Data and maps config key names to provider names.
	if len(keys) < 2 {
		t.Fatalf("expected at least 2 keys, got %d: %v", len(keys), keys)
	}
	if v, ok := keys["openai"]; !ok || v != "sk-alice" {
		t.Errorf("keys[openai] = %q, want %q (present=%v)", v, "sk-alice", ok)
	}
	if v, ok := keys["anthropic"]; !ok || v != "ant-bob" {
		t.Errorf("keys[anthropic] = %q, want %q (present=%v)", v, "ant-bob", ok)
	}
}

// mockK8sStore is a simple in-memory K8s resource store for tests.
type mockK8sStore struct {
	mu         sync.Mutex
	configmaps map[string]ConfigMap
	secrets    map[string]Secret
}

func newMockK8sStore() *mockK8sStore {
	return &mockK8sStore{
		configmaps: make(map[string]ConfigMap),
		secrets:    make(map[string]Secret),
	}
}

func (s *mockK8sStore) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()

		path := r.URL.Path

		switch r.Method {
		case http.MethodGet:
			if strings.Contains(path, "/configmaps/") {
				name := path[strings.LastIndex(path, "/")+1:]
				cm, ok := s.configmaps[name]
				if !ok {
					w.WriteHeader(http.StatusNotFound)
					json.NewEncoder(w).Encode(map[string]any{"code": 404, "reason": "NotFound", "message": "not found"})
					return
				}
				json.NewEncoder(w).Encode(cm)
			} else if strings.Contains(path, "/secrets/") {
				name := path[strings.LastIndex(path, "/")+1:]
				sec, ok := s.secrets[name]
				if !ok {
					w.WriteHeader(http.StatusNotFound)
					json.NewEncoder(w).Encode(map[string]any{"code": 404, "reason": "NotFound", "message": "not found"})
					return
				}
				json.NewEncoder(w).Encode(sec)
			} else if strings.HasSuffix(path, "/configmaps") {
				// List
				var items []ConfigMap
				for _, cm := range s.configmaps {
					items = append(items, cm)
				}
				json.NewEncoder(w).Encode(ConfigMapList{Items: items})
			} else if strings.HasSuffix(path, "/secrets") {
				var items []Secret
				for _, sec := range s.secrets {
					items = append(items, sec)
				}
				json.NewEncoder(w).Encode(SecretList{Items: items})
			}

		case http.MethodPost:
			if strings.Contains(path, "/configmaps") {
				var cm ConfigMap
				json.NewDecoder(r.Body).Decode(&cm)
				if _, exists := s.configmaps[cm.Metadata.Name]; exists {
					w.WriteHeader(http.StatusConflict)
					json.NewEncoder(w).Encode(map[string]any{"code": 409, "reason": "AlreadyExists", "message": "exists"})
					return
				}
				s.configmaps[cm.Metadata.Name] = cm
				w.WriteHeader(http.StatusCreated)
				json.NewEncoder(w).Encode(cm)
			} else if strings.Contains(path, "/secrets") {
				var sec Secret
				json.NewDecoder(r.Body).Decode(&sec)
				if _, exists := s.secrets[sec.Metadata.Name]; exists {
					w.WriteHeader(http.StatusConflict)
					json.NewEncoder(w).Encode(map[string]any{"code": 409, "reason": "AlreadyExists", "message": "exists"})
					return
				}
				// Simulate K8s: merge StringData into Data (K8s stores base64-encoded).
				if sec.Data == nil {
					sec.Data = map[string]string{}
				}
				for k, v := range sec.StringData {
					sec.Data[k] = v
				}
				s.secrets[sec.Metadata.Name] = sec
				w.WriteHeader(http.StatusCreated)
				json.NewEncoder(w).Encode(sec)
			}

		case http.MethodPut:
			if strings.Contains(path, "/configmaps/") {
				name := path[strings.LastIndex(path, "/")+1:]
				var cm ConfigMap
				json.NewDecoder(r.Body).Decode(&cm)
				s.configmaps[name] = cm
				json.NewEncoder(w).Encode(cm)
			} else if strings.Contains(path, "/secrets/") {
				name := path[strings.LastIndex(path, "/")+1:]
				var sec Secret
				json.NewDecoder(r.Body).Decode(&sec)
				// Merge stringData into Data (simulating K8s behavior).
				if sec.Data == nil {
					sec.Data = map[string]string{}
				}
				for k, v := range sec.StringData {
					sec.Data[k] = v
				}
				s.secrets[name] = sec
				json.NewEncoder(w).Encode(sec)
			}

		case http.MethodDelete:
			if strings.Contains(path, "/configmaps/") {
				name := path[strings.LastIndex(path, "/")+1:]
				delete(s.configmaps, name)
				w.WriteHeader(http.StatusOK)
			} else if strings.Contains(path, "/secrets/") {
				name := path[strings.LastIndex(path, "/")+1:]
				delete(s.secrets, name)
				w.WriteHeader(http.StatusOK)
			}
		}
	})
}

func newTestBackend(t *testing.T, store *mockK8sStore) *KubeBackend {
	t.Helper()
	srv := httptest.NewServer(store.handler())
	t.Cleanup(srv.Close)
	client := NewFromConfig(srv.URL, "test-token", "default", srv.Client())
	return NewBackend(client, "abc123")
}

func TestKubeBackend_Setup(t *testing.T) {
	store := newMockK8sStore()
	b := newTestBackend(t, store)

	err := b.Setup("Alice", agent.SetupOpts{
		OpenAIKey:    "sk-test",
		SystemPrompt: "You are Alice",
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	// Verify ConfigMap was created.
	cm, ok := store.configmaps["xnc-abc123-alice"]
	if !ok {
		t.Fatal("expected configmap to be created")
	}
	if cm.Metadata.Labels["xnc.io/agent"] != "alice" {
		t.Errorf("label xnc.io/agent = %q", cm.Metadata.Labels["xnc.io/agent"])
	}

	// Verify Secret was created with key.
	sec, ok := store.secrets["xnc-abc123-alice"]
	if !ok {
		t.Fatal("expected secret to be created")
	}
	if sec.StringData["openai_key"] != "sk-test" {
		t.Errorf("secret openai_key = %q", sec.StringData["openai_key"])
	}
}

func TestKubeBackend_Exists(t *testing.T) {
	store := newMockK8sStore()
	b := newTestBackend(t, store)

	if b.Exists("alice") {
		t.Error("expected Exists=false before Setup")
	}

	b.Setup("alice", agent.SetupOpts{})

	if !b.Exists("alice") {
		t.Error("expected Exists=true after Setup")
	}
}

func TestKubeBackend_Destroy(t *testing.T) {
	store := newMockK8sStore()
	b := newTestBackend(t, store)

	b.Setup("alice", agent.SetupOpts{})
	if !b.Exists("alice") {
		t.Fatal("setup failed")
	}

	if err := b.Destroy("alice"); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if b.Exists("alice") {
		t.Error("expected Exists=false after Destroy")
	}
}

func TestKubeBackend_ListAll(t *testing.T) {
	store := newMockK8sStore()
	b := newTestBackend(t, store)

	b.Setup("alice", agent.SetupOpts{})
	b.Setup("bob", agent.SetupOpts{})

	agents, err := b.ListAll()
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(agents))
	}
}

func TestKubeBackend_Clone(t *testing.T) {
	store := newMockK8sStore()
	b := newTestBackend(t, store)

	b.Setup("alice", agent.SetupOpts{SystemPrompt: "I am Alice"})

	if err := b.Clone("alice", "bob", agent.CloneOpts{}); err != nil {
		t.Fatalf("Clone: %v", err)
	}

	if !b.Exists("bob") {
		t.Error("bob should exist after clone")
	}

	// Verify config was copied.
	cm := store.configmaps["xnc-abc123-bob"]
	if cm.Metadata.Annotations["xnc.io/cloned-from"] != "alice" {
		t.Errorf("expected cloned-from annotation")
	}
}

func TestKubeBackend_Rename(t *testing.T) {
	store := newMockK8sStore()
	b := newTestBackend(t, store)

	b.Setup("alice", agent.SetupOpts{})

	if err := b.Rename("alice", "carol"); err != nil {
		t.Fatalf("Rename: %v", err)
	}

	if b.Exists("alice") {
		t.Error("alice should not exist after rename")
	}
	if !b.Exists("carol") {
		t.Error("carol should exist after rename")
	}
}

func TestKubeBackend_ConfigSetGet(t *testing.T) {
	store := newMockK8sStore()
	b := newTestBackend(t, store)

	b.Setup("alice", agent.SetupOpts{})

	if err := b.ConfigSet("alice", "model", "gpt-4"); err != nil {
		t.Fatalf("ConfigSet: %v", err)
	}

	val, err := b.ConfigGet("alice", "model")
	if err != nil {
		t.Fatalf("ConfigGet: %v", err)
	}
	if val != "gpt-4" {
		t.Errorf("model = %v, want %q", val, "gpt-4")
	}
}

func TestKubeBackend_ReadWriteMeta(t *testing.T) {
	store := newMockK8sStore()
	b := newTestBackend(t, store)

	b.Setup("alice", agent.SetupOpts{})

	if err := b.WriteMeta("alice", "emoji", "🎯"); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	meta, err := b.ReadMeta("alice")
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if meta["emoji"] != "🎯" {
		t.Errorf("emoji = %q, want %q", meta["emoji"], "🎯")
	}
}

func TestKubeBackend_WriteMetaBatch(t *testing.T) {
	store := newMockK8sStore()
	b := newTestBackend(t, store)

	b.Setup("alice", agent.SetupOpts{})

	err := b.WriteMetaBatch("alice", map[string]string{
		"emoji":  "🤖",
		"status": "active",
	})
	if err != nil {
		t.Fatalf("WriteMetaBatch: %v", err)
	}

	meta, err := b.ReadMeta("alice")
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if meta["emoji"] != "🤖" || meta["status"] != "active" {
		t.Errorf("meta = %v", meta)
	}
}

func TestKubeBackend_SetupWebhookAuth(t *testing.T) {
	store := newMockK8sStore()
	b := newTestBackend(t, store)

	b.Setup("alice", agent.SetupOpts{})

	token, err := b.SetupWebhookAuth("alice")
	if err != nil {
		t.Fatalf("SetupWebhookAuth: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}
	if !strings.HasPrefix(token, "zc_") {
		t.Errorf("token should have zc_ prefix, got %q", token[:10])
	}

	// Read it back.
	got, err := b.ReadToken("alice")
	if err != nil {
		t.Fatalf("ReadToken: %v", err)
	}
	if got != token {
		t.Errorf("ReadToken = %q, want %q", got, token)
	}
}

func TestKubeBackend_HasProviderKey(t *testing.T) {
	store := newMockK8sStore()
	b := newTestBackend(t, store)

	b.Setup("alice", agent.SetupOpts{})

	if b.HasProviderKey("alice") {
		t.Error("expected no provider key initially")
	}

	b.Setup("bob", agent.SetupOpts{OpenAIKey: "sk-test"})

	if !b.HasProviderKey("bob") {
		t.Error("expected bob to have provider key")
	}
}

func TestKubeBackend_Dir(t *testing.T) {
	b := NewBackend(nil, "abc123")
	if dir := b.Dir("alice"); dir != "" {
		t.Errorf("Dir should return empty string in K8s mode, got %q", dir)
	}
}

func TestKubeBackend_ContainerEnv(t *testing.T) {
	store := newMockK8sStore()
	b := newTestBackend(t, store)

	// brave_key is the only provider key with EnvVar set ("BRAVE_API_KEY").
	b.Setup("alice", agent.SetupOpts{BraveKey: "brave-test-key"})

	env, err := b.ContainerEnv("alice")
	if err != nil {
		t.Fatalf("ContainerEnv: %v", err)
	}

	// Should include BRAVE_API_KEY from Secret.
	found := false
	for _, e := range env {
		if strings.HasPrefix(e, "BRAVE_API_KEY=") && strings.Contains(e, "brave-test-key") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected env to contain BRAVE_API_KEY=brave-test-key, got: %v", env)
	}
}

func TestKubeBackend_ContainerEnv_Empty(t *testing.T) {
	store := newMockK8sStore()
	b := newTestBackend(t, store)

	b.Setup("alice", agent.SetupOpts{})

	env, err := b.ContainerEnv("alice")
	if err != nil {
		t.Fatalf("ContainerEnv: %v", err)
	}
	// Gateway vars always present (NULLCLAW_GATEWAY_HOST, NULLCLAW_ALLOW_PUBLIC_BIND, NULLCLAW_WEB_TOKEN).
	if len(env) != 3 {
		t.Errorf("expected 3 gateway env vars, got: %v", env)
	}
}

func TestKubeBackend_ListAll_CrossInstance(t *testing.T) {
	store := newMockK8sStore()

	// Create a backend with instance "abc123".
	srv := httptest.NewServer(store.handler())
	t.Cleanup(srv.Close)
	client := NewFromConfig(srv.URL, "test-token", "default", srv.Client())

	b1 := NewBackend(client, "abc123")
	b2 := NewBackend(client, "xyz789")

	// Both backends create agents that land in the same mock store.
	b1.Setup("alice", agent.SetupOpts{})
	b2.Setup("bob", agent.SetupOpts{})

	// b1 should only see alice.
	agents1, err := b1.ListAll()
	if err != nil {
		t.Fatalf("ListAll (b1): %v", err)
	}
	if len(agents1) != 1 {
		t.Fatalf("b1: expected 1 agent, got %d", len(agents1))
	}
	if agents1[0].Name != "alice" {
		t.Errorf("b1: agent = %q, want %q", agents1[0].Name, "alice")
	}

	// b2 should only see bob.
	agents2, err := b2.ListAll()
	if err != nil {
		t.Fatalf("ListAll (b2): %v", err)
	}
	if len(agents2) != 1 {
		t.Fatalf("b2: expected 1 agent, got %d", len(agents2))
	}
	if agents2[0].Name != "bob" {
		t.Errorf("b2: agent = %q, want %q", agents2[0].Name, "bob")
	}
}

func TestKubeBackend_ConfigGetAllRedacted(t *testing.T) {
	store := newMockK8sStore()
	b := newTestBackend(t, store)

	b.Setup("alice", agent.SetupOpts{SystemPrompt: "test"})
	b.ConfigSet("alice", "model", "gpt-4")

	all, err := b.ConfigGetAllRedacted("alice")
	if err != nil {
		t.Fatalf("ConfigGetAllRedacted: %v", err)
	}
	if all["model"] != "gpt-4" {
		t.Errorf("model = %v", all["model"])
	}
}

func TestKubeBackend_ConfigGet_TelegramToken_UsesConfigMap(t *testing.T) {
	store := newMockK8sStore()
	b := newTestBackend(t, store)

	// Setup with a telegram token — it should go to ConfigMap (config.json), not Secret.
	b.Setup("alice", agent.SetupOpts{})
	// Set telegram_token via ConfigSet — should go to ConfigMap.
	if err := b.ConfigSet("alice", "telegram_token", "123456:ABC"); err != nil {
		t.Fatalf("ConfigSet telegram_token: %v", err)
	}

	// ConfigGet should find it in ConfigMap, not Secret.
	val, err := b.ConfigGet("alice", "telegram_token")
	if err != nil {
		t.Fatalf("ConfigGet telegram_token: %v", err)
	}
	if val != "123456:ABC" {
		t.Errorf("telegram_token = %v, want %q", val, "123456:ABC")
	}
}

func TestKubeBackend_ConfigGet_BraveKey_UsesSecret(t *testing.T) {
	store := newMockK8sStore()
	b := newTestBackend(t, store)

	b.Setup("alice", agent.SetupOpts{BraveKey: "brv-test-123"})

	// brave_key has Redacted=true and EnvVar set — should be in Secret.
	val, err := b.ConfigGet("alice", "brave_key")
	if err != nil {
		t.Fatalf("ConfigGet brave_key: %v", err)
	}
	if val != "brv-test-123" {
		t.Errorf("brave_key = %v, want %q", val, "brv-test-123")
	}
}

func TestKubeBackend_WriteMetaBatch_InvalidKey(t *testing.T) {
	store := newMockK8sStore()
	b := newTestBackend(t, store)

	b.Setup("alice", agent.SetupOpts{})

	// Valid key should work.
	if err := b.WriteMetaBatch("alice", map[string]string{"emoji": "🎯"}); err != nil {
		t.Fatalf("WriteMetaBatch valid key: %v", err)
	}

	// Invalid key (path traversal attempt) should be rejected.
	err := b.WriteMetaBatch("alice", map[string]string{"../evil": "payload"})
	if err == nil {
		t.Fatal("expected error for invalid metadata key '../evil'")
	}
	if !strings.Contains(err.Error(), "invalid metadata key") {
		t.Errorf("error should mention invalid key: %v", err)
	}
}

func TestIsSecretStoredKey(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"openai_key", true},     // Redacted + Provider
		{"anthropic_key", true},  // Redacted + Provider
		{"brave_key", true},      // Redacted + EnvVar
		{"telegram_token", false}, // Redacted but no Provider/EnvVar
		{"gateway_paired_tokens", false}, // Redacted but no Provider/EnvVar
		{"model", false},         // Not redacted
	}
	for _, tt := range tests {
		ck, ok := agent.LookupConfigKey(tt.name)
		if !ok {
			t.Fatalf("unknown config key: %s", tt.name)
		}
		got := isSecretStoredKey(ck)
		if got != tt.want {
			t.Errorf("isSecretStoredKey(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestDecodeSecretValue(t *testing.T) {
	tests := []struct {
		name    string
		encoded string
		want    string
	}{
		{"empty", "", ""},
		{"valid base64", base64.StdEncoding.EncodeToString([]byte("sk-live-1234")), "sk-live-1234"},
		{"raw string fallback", "not-base64-!!!", "not-base64-!!!"},
		{"base64 round-trip", base64.StdEncoding.EncodeToString([]byte("BRAVE_KEY_abc")), "BRAVE_KEY_abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decodeSecretValue(tt.encoded)
			if got != tt.want {
				t.Errorf("decodeSecretValue(%q) = %q, want %q", tt.encoded, got, tt.want)
			}
		})
	}
}

// mockK8sStoreFailSecrets wraps mockK8sStore but rejects POST to secrets.
type mockK8sStoreFailSecrets struct {
	*mockK8sStore
}

func (s *mockK8sStoreFailSecrets) handler() http.Handler {
	inner := s.mockK8sStore.handler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/secrets") {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]any{
				"code": 500, "reason": "InternalError", "message": "simulated secret creation failure",
			})
			return
		}
		inner.ServeHTTP(w, r)
	})
}

func TestKubeBackend_Setup_RollbackOnSecretFailure(t *testing.T) {
	store := &mockK8sStoreFailSecrets{newMockK8sStore()}
	srv := httptest.NewServer(store.handler())
	t.Cleanup(srv.Close)
	client := NewFromConfig(srv.URL, "test-token", "default", srv.Client())
	b := NewBackend(client, "abc123")

	err := b.Setup("alice", agent.SetupOpts{OpenAIKey: "sk-test"})
	if err == nil {
		t.Fatal("expected error from secret creation failure")
	}

	// ConfigMap should have been rolled back (deleted).
	if _, ok := store.configmaps["xnc-abc123-alice"]; ok {
		t.Error("expected ConfigMap to be rolled back (deleted) after secret creation failure")
	}
}

func TestKubeBackend_Clone_RollbackOnSecretFailure(t *testing.T) {
	innerStore := newMockK8sStore()

	// Create source agent with normal store first.
	srv1 := httptest.NewServer(innerStore.handler())
	client1 := NewFromConfig(srv1.URL, "test-token", "default", srv1.Client())
	b1 := NewBackend(client1, "abc123")
	if err := b1.Setup("alice", agent.SetupOpts{}); err != nil {
		t.Fatalf("setup source: %v", err)
	}
	srv1.Close()

	// Now use failing store for clone.
	store := &mockK8sStoreFailSecrets{innerStore}
	srv2 := httptest.NewServer(store.handler())
	t.Cleanup(srv2.Close)
	client2 := NewFromConfig(srv2.URL, "test-token", "default", srv2.Client())
	b2 := NewBackend(client2, "abc123")

	err := b2.Clone("alice", "bob", agent.CloneOpts{})
	if err == nil {
		t.Fatal("expected error from secret creation failure during clone")
	}

	// Dest ConfigMap should have been rolled back.
	if _, ok := innerStore.configmaps["xnc-abc123-bob"]; ok {
		t.Error("expected dest ConfigMap to be rolled back after clone secret failure")
	}
}

func TestKubeBackend_ConfigGetAll_IncludesSecretKeys(t *testing.T) {
	store := newMockK8sStore()
	b := newTestBackend(t, store)

	b.Setup("alice", agent.SetupOpts{OpenAIKey: "sk-test-all", BraveKey: "brv-test-all"})
	// Also set a non-secret key.
	b.ConfigSet("alice", "model", "gpt-4")

	all, err := b.ConfigGetAll("alice")
	if err != nil {
		t.Fatalf("ConfigGetAll: %v", err)
	}

	if all["model"] != "gpt-4" {
		t.Errorf("model = %v, want %q", all["model"], "gpt-4")
	}
	if all["openai_key"] != "sk-test-all" {
		t.Errorf("openai_key = %v, want %q", all["openai_key"], "sk-test-all")
	}
	if all["brave_key"] != "brv-test-all" {
		t.Errorf("brave_key = %v, want %q", all["brave_key"], "brv-test-all")
	}
}

func TestKubeBackend_ConfigGetAllRedacted_IncludesSecretKeys(t *testing.T) {
	store := newMockK8sStore()
	b := newTestBackend(t, store)

	b.Setup("alice", agent.SetupOpts{OpenAIKey: "sk-redact-me"})
	b.ConfigSet("alice", "model", "gpt-4")

	all, err := b.ConfigGetAllRedacted("alice")
	if err != nil {
		t.Fatalf("ConfigGetAllRedacted: %v", err)
	}

	// Model should be visible.
	if all["model"] != "gpt-4" {
		t.Errorf("model = %v", all["model"])
	}
	// Provider key should be redacted.
	if all["openai_key"] != "***" {
		t.Errorf("openai_key should be redacted, got %v", all["openai_key"])
	}
}

func TestKubeBackend_ConfigSetGet_RedactedRoundTrip(t *testing.T) {
	store := newMockK8sStore()
	b := newTestBackend(t, store)

	b.Setup("alice", agent.SetupOpts{})

	// Set a redacted provider key (goes to Secret).
	if err := b.ConfigSet("alice", "openai_key", "sk-round-trip"); err != nil {
		t.Fatalf("ConfigSet openai_key: %v", err)
	}

	// Read it back — should come from Secret.
	val, err := b.ConfigGet("alice", "openai_key")
	if err != nil {
		t.Fatalf("ConfigGet openai_key: %v", err)
	}
	if val != "sk-round-trip" {
		t.Errorf("openai_key = %v, want %q", val, "sk-round-trip")
	}

	// Set a non-provider redacted key (goes to ConfigMap).
	if err := b.ConfigSet("alice", "telegram_token", "12345:ABCDEF"); err != nil {
		t.Fatalf("ConfigSet telegram_token: %v", err)
	}

	val, err = b.ConfigGet("alice", "telegram_token")
	if err != nil {
		t.Fatalf("ConfigGet telegram_token: %v", err)
	}
	if val != "12345:ABCDEF" {
		t.Errorf("telegram_token = %v, want %q", val, "12345:ABCDEF")
	}
}

// mockK8sStoreFailSecretGET wraps mockK8sStore but fails GET on secrets with 500.
type mockK8sStoreFailSecretGET struct {
	*mockK8sStore
}

func (s *mockK8sStoreFailSecretGET) handler() http.Handler {
	inner := s.mockK8sStore.handler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/secrets/") {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]any{
				"code": 500, "reason": "InternalError", "message": "simulated secret read failure",
			})
			return
		}
		inner.ServeHTTP(w, r)
	})
}

func TestKubeBackend_ContainerEnv_SecretReadError(t *testing.T) {
	innerStore := newMockK8sStore()

	// Create agent with normal store first.
	srv1 := httptest.NewServer(innerStore.handler())
	client1 := NewFromConfig(srv1.URL, "test-token", "default", srv1.Client())
	b1 := NewBackend(client1, "abc123")
	if err := b1.Setup("alice", agent.SetupOpts{OpenAIKey: "sk-test"}); err != nil {
		t.Fatalf("setup: %v", err)
	}
	srv1.Close()

	// Now use failing store for ContainerEnv.
	store := &mockK8sStoreFailSecretGET{innerStore}
	srv2 := httptest.NewServer(store.handler())
	t.Cleanup(srv2.Close)
	client2 := NewFromConfig(srv2.URL, "test-token", "default", srv2.Client())
	b2 := NewBackend(client2, "abc123")

	_, err := b2.ContainerEnv("alice")
	if err == nil {
		t.Fatal("expected error when Secret read fails")
	}
	if !strings.Contains(err.Error(), "read secret") {
		t.Errorf("error should mention secret read failure: %v", err)
	}
}

func TestKubeBackend_ConfigGetAll_SecretReadError(t *testing.T) {
	innerStore := newMockK8sStore()

	// Create agent with normal store first.
	srv1 := httptest.NewServer(innerStore.handler())
	client1 := NewFromConfig(srv1.URL, "test-token", "default", srv1.Client())
	b1 := NewBackend(client1, "abc123")
	if err := b1.Setup("alice", agent.SetupOpts{OpenAIKey: "sk-test"}); err != nil {
		t.Fatalf("setup: %v", err)
	}
	srv1.Close()

	// Now use failing store.
	store := &mockK8sStoreFailSecretGET{innerStore}
	srv2 := httptest.NewServer(store.handler())
	t.Cleanup(srv2.Close)
	client2 := NewFromConfig(srv2.URL, "test-token", "default", srv2.Client())
	b2 := NewBackend(client2, "abc123")

	_, err := b2.ConfigGetAll("alice")
	if err == nil {
		t.Fatal("expected error when Secret read fails")
	}
	if !strings.Contains(err.Error(), "read secret") {
		t.Errorf("error should mention secret read failure: %v", err)
	}
}

func TestKubeBackend_Clone_PreservesMetaAnnotations(t *testing.T) {
	store := newMockK8sStore()
	b := newTestBackend(t, store)

	b.Setup("alice", agent.SetupOpts{})

	// Write custom metadata to source.
	if err := b.WriteMetaBatch("alice", map[string]string{
		"emoji":  "🎯",
		"status": "active",
	}); err != nil {
		t.Fatalf("WriteMetaBatch: %v", err)
	}

	// Clone.
	if err := b.Clone("alice", "bob", agent.CloneOpts{}); err != nil {
		t.Fatalf("Clone: %v", err)
	}

	// Verify bob has the same metadata annotations.
	meta, err := b.ReadMeta("bob")
	if err != nil {
		t.Fatalf("ReadMeta bob: %v", err)
	}
	if meta["emoji"] != "🎯" {
		t.Errorf("bob emoji = %q, want %q", meta["emoji"], "🎯")
	}
	if meta["status"] != "active" {
		t.Errorf("bob status = %q, want %q", meta["status"], "active")
	}

	// Verify system annotations are correct (not copied from source).
	cm := store.configmaps["xnc-abc123-bob"]
	if cm.Metadata.Annotations["xnc.io/agent-name"] != "bob" {
		t.Errorf("agent-name = %q, want %q", cm.Metadata.Annotations["xnc.io/agent-name"], "bob")
	}
	if cm.Metadata.Annotations["xnc.io/cloned-from"] != "alice" {
		t.Errorf("cloned-from = %q, want %q", cm.Metadata.Annotations["xnc.io/cloned-from"], "alice")
	}
}

func TestKubeBackend_WriteMetaBatch_ValueTooLarge(t *testing.T) {
	store := newMockK8sStore()
	b := newTestBackend(t, store)

	b.Setup("alice", agent.SetupOpts{})

	// Value within limit should work.
	if err := b.WriteMetaBatch("alice", map[string]string{"note": "short"}); err != nil {
		t.Fatalf("WriteMetaBatch small value: %v", err)
	}

	// Value exceeding 128KB should be rejected.
	bigValue := strings.Repeat("x", maxMetaValueSize+1)
	err := b.WriteMetaBatch("alice", map[string]string{"big": bigValue})
	if err == nil {
		t.Fatal("expected error for oversized metadata value")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Errorf("error should mention size: %v", err)
	}
}
