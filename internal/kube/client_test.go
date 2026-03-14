package kube

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTestClient creates a Client pointing at the given test server.
func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	return NewFromConfig(srv.URL, "test-token", "default", srv.Client())
}

func TestGet(t *testing.T) {
	want := ConfigMap{
		APIVersion: "v1",
		Kind:       "ConfigMap",
		Metadata:   ObjectMeta{Name: "my-cm", Namespace: "default"},
		Data:       map[string]string{"key": "value"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/namespaces/default/configmaps/my-cm" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("missing auth header")
		}
		json.NewEncoder(w).Encode(want)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	var got ConfigMap
	if err := c.Get(context.Background(), "configmaps", "my-cm", &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Metadata.Name != want.Metadata.Name {
		t.Errorf("name = %q, want %q", got.Metadata.Name, want.Metadata.Name)
	}
	if got.Data["key"] != "value" {
		t.Errorf("data[key] = %q, want %q", got.Data["key"], "value")
	}
}

func TestCreate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/namespaces/default/configmaps" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("missing content-type")
		}
		var cm ConfigMap
		json.NewDecoder(r.Body).Decode(&cm)
		cm.Metadata.Namespace = "default"
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(cm)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	cm := ConfigMap{
		APIVersion: "v1",
		Kind:       "ConfigMap",
		Metadata:   ObjectMeta{Name: "new-cm"},
		Data:       map[string]string{"hello": "world"},
	}
	var result ConfigMap
	if err := c.Create(context.Background(), "configmaps", cm, &result); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if result.Metadata.Name != "new-cm" {
		t.Errorf("name = %q, want %q", result.Metadata.Name, "new-cm")
	}
}

func TestDelete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/namespaces/default/pods/my-pod" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	if err := c.Delete(context.Background(), "pods", "my-pod"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestList_WithLabels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		sel := r.URL.Query().Get("labelSelector")
		if sel == "" {
			t.Error("expected labelSelector query param")
		}
		list := PodList{Items: []Pod{{
			APIVersion: "v1",
			Kind:       "Pod",
			Metadata:   ObjectMeta{Name: "test-pod"},
		}}}
		json.NewEncoder(w).Encode(list)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	var list PodList
	err := c.List(context.Background(), "pods", map[string]string{"app": "xnc"}, &list)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("expected 1 pod, got %d", len(list.Items))
	}
	if list.Items[0].Metadata.Name != "test-pod" {
		t.Errorf("pod name = %q, want %q", list.Items[0].Metadata.Name, "test-pod")
	}
}

func TestStatusError_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]any{
			"kind":    "Status",
			"code":    404,
			"reason":  "NotFound",
			"message": "pods \"missing\" not found",
		})
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	var pod Pod
	err := c.Get(context.Background(), "pods", "missing", &pod)
	if err == nil {
		t.Fatal("expected error for 404")
	}
	if !IsNotFound(err) {
		t.Errorf("expected IsNotFound, got: %v", err)
	}
}

func TestStatusError_Conflict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]any{
			"kind":    "Status",
			"code":    409,
			"reason":  "AlreadyExists",
			"message": "already exists",
		})
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	err := c.Create(context.Background(), "configmaps", ConfigMap{}, nil)
	if err == nil {
		t.Fatal("expected error for 409")
	}
	if !IsConflict(err) {
		t.Errorf("expected IsConflict, got: %v", err)
	}
}

func TestUpdate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/namespaces/default/configmaps/my-cm" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		var cm ConfigMap
		json.NewDecoder(r.Body).Decode(&cm)
		json.NewEncoder(w).Encode(cm)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	cm := ConfigMap{
		APIVersion: "v1",
		Kind:       "ConfigMap",
		Metadata:   ObjectMeta{Name: "my-cm"},
		Data:       map[string]string{"updated": "true"},
	}
	var result ConfigMap
	if err := c.Update(context.Background(), "configmaps", "my-cm", cm, &result); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if result.Data["updated"] != "true" {
		t.Errorf("data not updated")
	}
}

func TestPodLogs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/namespaces/default/pods/my-pod/log" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("tailLines") != "100" {
			t.Errorf("unexpected tailLines: %s", r.URL.Query().Get("tailLines"))
		}
		w.Write([]byte("log line 1\nlog line 2\n"))
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	rc, err := c.PodLogs(context.Background(), "my-pod", 100)
	if err != nil {
		t.Fatalf("PodLogs: %v", err)
	}
	defer rc.Close()

	buf := make([]byte, 1024)
	n, _ := rc.Read(buf)
	got := string(buf[:n])
	if got != "log line 1\nlog line 2\n" {
		t.Errorf("logs = %q, want %q", got, "log line 1\nlog line 2\n")
	}
}

func TestNamespace(t *testing.T) {
	c := NewFromConfig("https://localhost", "tok", "kube-system", nil)
	if c.Namespace() != "kube-system" {
		t.Errorf("Namespace() = %q, want %q", c.Namespace(), "kube-system")
	}
}

func TestBearerToken_ReadsFromFile(t *testing.T) {
	// Write a token file that the client will read.
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("file-token-123\n"), 0600); err != nil {
		t.Fatalf("write token file: %v", err)
	}

	c := &Client{
		host:      "https://localhost",
		token:     "initial-token",
		tokenPath: tokenFile,
		namespace: "default",
	}

	// First call should read from file.
	got := c.bearerToken()
	if got != "file-token-123" {
		t.Errorf("bearerToken() = %q, want %q", got, "file-token-123")
	}

	// Cached: second call within TTL should return cached value without re-reading.
	if err := os.WriteFile(tokenFile, []byte("rotated-token\n"), 0600); err != nil {
		t.Fatalf("write rotated token: %v", err)
	}
	got = c.bearerToken()
	if got != "file-token-123" {
		t.Errorf("bearerToken() should return cached value, got %q", got)
	}
}

func TestBearerToken_FallsBackOnFileError(t *testing.T) {
	// Point to a non-existent file.
	c := &Client{
		host:      "https://localhost",
		token:     "fallback-token",
		tokenPath: "/nonexistent/path/to/token",
		namespace: "default",
	}

	got := c.bearerToken()
	if got != "fallback-token" {
		t.Errorf("bearerToken() = %q, want fallback %q", got, "fallback-token")
	}
}

func TestBearerToken_FallsBackToCachedOnFileError(t *testing.T) {
	// Write initial token.
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("good-token\n"), 0600); err != nil {
		t.Fatalf("write token file: %v", err)
	}

	c := &Client{
		host:      "https://localhost",
		token:     "initial-token",
		tokenPath: tokenFile,
		namespace: "default",
	}

	// Read and cache the token.
	got := c.bearerToken()
	if got != "good-token" {
		t.Fatalf("initial bearerToken() = %q", got)
	}

	// Delete the file and expire the cache.
	os.Remove(tokenFile)
	c.cachedTokenAt = time.Time{} // force cache expiry

	// Should fall back to cached token (not the initial constructor token).
	got = c.bearerToken()
	if got != "good-token" {
		t.Errorf("bearerToken() after file deletion = %q, want cached %q", got, "good-token")
	}
}

func TestBearerToken_NoTokenPath(t *testing.T) {
	// When tokenPath is empty (e.g. NewFromConfig), should return static token.
	c := &Client{
		host:      "https://localhost",
		token:     "static-token",
		namespace: "default",
	}

	got := c.bearerToken()
	if got != "static-token" {
		t.Errorf("bearerToken() = %q, want %q", got, "static-token")
	}
}

func TestBearerToken_Concurrent(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("concurrent-token\n"), 0600); err != nil {
		t.Fatalf("write token file: %v", err)
	}

	c := &Client{
		host:      "https://localhost",
		token:     "initial",
		tokenPath: tokenFile,
		namespace: "default",
	}

	// Hammer bearerToken from multiple goroutines to catch data races.
	// Run with -race to detect issues.
	const goroutines = 50
	done := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 100; j++ {
				tok := c.bearerToken()
				if tok != "concurrent-token" && tok != "initial" {
					t.Errorf("unexpected token: %q", tok)
				}
			}
		}()
	}
	for i := 0; i < goroutines; i++ {
		<-done
	}
}

func TestCheckAccess_Allowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/apis/authorization.k8s.io/v1/selfsubjectaccessreviews" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		spec := body["spec"].(map[string]any)
		ra := spec["resourceAttributes"].(map[string]any)
		if ra["resource"] != "pods" || ra["verb"] != "create" {
			t.Errorf("unexpected resource/verb: %v/%v", ra["resource"], ra["verb"])
		}
		json.NewEncoder(w).Encode(map[string]any{
			"status": map[string]any{"allowed": true},
		})
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	allowed, err := c.CheckAccess(context.Background(), "pods", "create")
	if err != nil {
		t.Fatalf("CheckAccess: %v", err)
	}
	if !allowed {
		t.Error("expected allowed=true")
	}
}

func TestCheckAccess_Denied(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"status": map[string]any{"allowed": false},
		})
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	allowed, err := c.CheckAccess(context.Background(), "secrets", "delete")
	if err != nil {
		t.Fatalf("CheckAccess: %v", err)
	}
	if allowed {
		t.Error("expected allowed=false")
	}
}

func TestValidateRBAC_AllAllowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"status": map[string]any{"allowed": true},
		})
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	if err := c.ValidateRBAC(context.Background()); err != nil {
		t.Fatalf("ValidateRBAC: %v", err)
	}
}

func TestValidateRBAC_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate a server error on the RBAC check endpoint.
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{
			"code":    500,
			"reason":  "InternalError",
			"message": "kube-apiserver overloaded",
		})
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	err := c.ValidateRBAC(context.Background())
	if err == nil {
		t.Fatal("expected error when API returns 500")
	}
	if !strings.Contains(err.Error(), "RBAC check failed") {
		t.Errorf("error should mention RBAC check failed: %v", err)
	}
}

func TestValidateRBAC_SomeDenied(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		spec := body["spec"].(map[string]any)
		ra := spec["resourceAttributes"].(map[string]any)
		// Deny pods/exec and secrets/delete.
		allowed := true
		if ra["resource"] == "pods/exec" || (ra["resource"] == "secrets" && ra["verb"] == "delete") {
			allowed = false
		}
		json.NewEncoder(w).Encode(map[string]any{
			"status": map[string]any{"allowed": allowed},
		})
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	err := c.ValidateRBAC(context.Background())
	if err == nil {
		t.Fatal("expected error for denied permissions")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "pods/exec") {
		t.Errorf("error should mention pods/exec: %v", err)
	}
	if !strings.Contains(errStr, "secrets") {
		t.Errorf("error should mention secrets: %v", err)
	}
}
