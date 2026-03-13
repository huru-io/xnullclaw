package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCheckHealth_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer srv.Close()

	if !checkHealth(srv.URL) {
		t.Error("expected healthy")
	}
}

func TestCheckHealth_Degraded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"status": "degraded"})
	}))
	defer srv.Close()

	if !checkHealth(srv.URL) {
		t.Error("degraded should be accepted as healthy")
	}
}

func TestCheckHealth_Down(t *testing.T) {
	if checkHealth("http://127.0.0.1:1") {
		t.Error("expected unhealthy for unreachable server")
	}
}

func TestCheckHealth_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer srv.Close()

	if checkHealth(srv.URL) {
		t.Error("expected unhealthy for bad JSON")
	}
}

func TestCheckHealth_500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	if checkHealth(srv.URL) {
		t.Error("expected unhealthy for 500")
	}
}

func TestWaitForHealthy_SkipsZeroPort(t *testing.T) {
	if err := WaitForHealthy(0, time.Second); err != nil {
		t.Errorf("port 0 should be a no-op, got: %v", err)
	}
}

func TestWaitForHealthy_TimesOut(t *testing.T) {
	err := WaitForHealthy(1, 1*time.Second)
	if err == nil {
		t.Error("expected timeout error for unreachable port")
	}
}

func TestWaitForHealthy_EventualSuccess(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(503)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer srv.Close()

	// Extract port from test server URL.
	// WaitForHealthy uses 127.0.0.1:{port}, so we need to use checkHealth directly.
	// Instead, test the retry logic via checkHealth calls.
	for i := 0; i < 2; i++ {
		if checkHealth(srv.URL) {
			t.Error("should not be healthy yet")
		}
	}
	if !checkHealth(srv.URL) {
		t.Error("should be healthy on 3rd call")
	}
}
