package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
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

	port, _ := strconv.Atoi(strings.Split(srv.URL, ":")[2])
	resp, err := SendWebhook(context.Background(), port, "zc_test123", "hello agent")
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

	port, _ := strconv.Atoi(strings.Split(srv.URL, ":")[2])
	_, err := SendWebhook(context.Background(), port, "", "test")
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

	port, _ := strconv.Atoi(strings.Split(srv.URL, ":")[2])
	_, err := SendWebhook(context.Background(), port, "bad-token", "test")
	if err == nil {
		t.Fatal("expected error for 401")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention 401: %v", err)
	}
}

func TestSendWebhook_ConnectionRefused(t *testing.T) {
	_, err := SendWebhook(context.Background(), 1, "token", "test")
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

	port, _ := strconv.Atoi(strings.Split(srv.URL, ":")[2])
	_, err := SendWebhook(context.Background(), port, "token", "test")
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

	port, _ := strconv.Atoi(strings.Split(srv.URL, ":")[2])
	_, err := SendWebhook(context.Background(), port, "", "test")
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

	_, err := SendWebhook(ctx, 9999, "token", "test")
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestSendWebhook_MessageTooLarge(t *testing.T) {
	largeMsg := strings.Repeat("x", maxWebhookMessageSize+1)
	_, err := SendWebhook(context.Background(), 9999, "token", largeMsg)
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

	port, _ := strconv.Atoi(strings.Split(srv.URL, ":")[2])
	_, err := SendWebhook(context.Background(), port, "", "test")
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

func TestTrySendWebhook_NoPort(t *testing.T) {
	resp, err := TrySendWebhook(context.Background(), 0, "/tmp", "test", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != nil {
		t.Errorf("expected nil response for port 0, got %v", resp)
	}
}
