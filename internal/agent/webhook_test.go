package agent

import (
	"encoding/json"
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
	resp, err := SendWebhook(port, "zc_test123", "hello agent")
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
	_, err := SendWebhook(port, "", "test")
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
	_, err := SendWebhook(port, "bad-token", "test")
	if err == nil {
		t.Fatal("expected error for 401")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention 401: %v", err)
	}
}

func TestSendWebhook_ConnectionRefused(t *testing.T) {
	_, err := SendWebhook(1, "token", "test")
	if err == nil {
		t.Fatal("expected error for unreachable port")
	}
}
