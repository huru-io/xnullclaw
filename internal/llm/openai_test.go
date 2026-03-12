package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jotavich/xnullclaw/internal/config"
)

// newTestAdapter creates an adapter pointed at a test server.
func newTestAdapter(serverURL string) *OpenAIAdapter {
	cfg := config.DefaultConfig()
	cfg.OpenAI.APIKey = "test-key"
	cfg.OpenAI.BaseURL = serverURL
	a := NewOpenAIAdapter(cfg)
	// Speed up retries for tests.
	a.retryBaseDelay = 1 * time.Millisecond
	a.retryMaxDelay = 5 * time.Millisecond
	return a
}

// successResponse returns a valid OpenAI chat completions JSON response.
func successResponse(text string) []byte {
	resp := oaiResponse{
		Choices: []oaiChoice{{
			Message: oaiMessage{Role: "assistant", Content: text},
		}},
		Usage: oaiUsage{PromptTokens: 10, CompletionTokens: 5},
	}
	data, _ := json.Marshal(resp)
	return data
}

func TestDoRequestWithRetry_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write(successResponse("hello"))
	}))
	defer ts.Close()

	a := newTestAdapter(ts.URL)
	body := a.buildRequest("gpt-4o-mini", []oaiMessage{{Role: "user", Content: "hi"}}, nil, 0.5)

	resp, retryParam, err := a.doRequestWithRetry(context.Background(), body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if retryParam != "" {
		t.Errorf("expected empty retryParam, got %q", retryParam)
	}
	if resp.Text != "hello" {
		t.Errorf("got %q, want %q", resp.Text, "hello")
	}
}

func TestDoRequestWithRetry_500ThenSuccess(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n <= 2 {
			w.WriteHeader(500)
			w.Write([]byte(`{"error":"internal"}`))
			return
		}
		w.WriteHeader(200)
		w.Write(successResponse("recovered"))
	}))
	defer ts.Close()

	a := newTestAdapter(ts.URL)

	body := a.buildRequest("gpt-4o-mini", []oaiMessage{{Role: "user", Content: "hi"}}, nil, 0.5)
	resp, _, err := a.doRequestWithRetry(context.Background(), body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "recovered" {
		t.Errorf("got %q, want %q", resp.Text, "recovered")
	}
	if n := atomic.LoadInt32(&calls); n != 3 {
		t.Errorf("expected 3 calls, got %d", n)
	}
}

func TestDoRequestWithRetry_AllRetriesFail(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(503)
		w.Write([]byte(`{"error":"unavailable"}`))
	}))
	defer ts.Close()

	a := newTestAdapter(ts.URL)
	a.maxRetries = 2

	body := a.buildRequest("gpt-4o-mini", []oaiMessage{{Role: "user", Content: "hi"}}, nil, 0.5)
	_, _, err := a.doRequestWithRetry(context.Background(), body)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// 1 initial + 2 retries = 3 calls.
	if n := atomic.LoadInt32(&calls); n != 3 {
		t.Errorf("expected 3 calls (initial + 2 retries), got %d", n)
	}
}

func TestDoRequestWithRetry_NonRetryableError(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(401)
		w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer ts.Close()

	a := newTestAdapter(ts.URL)

	body := a.buildRequest("gpt-4o-mini", []oaiMessage{{Role: "user", Content: "hi"}}, nil, 0.5)
	_, _, err := a.doRequestWithRetry(context.Background(), body)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// Should NOT retry — only 1 call.
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Errorf("expected 1 call (no retry for 401), got %d", n)
	}
}

func TestDoRequestWithRetry_ContextCancelled(t *testing.T) {
	// Block in the HTTP handler to ensure the retry backoff sees the cancelled ctx.
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(500)
		w.Write([]byte(`{"error":"internal"}`))
	}))
	defer ts.Close()

	a := newTestAdapter(ts.URL)
	a.retryBaseDelay = 10 * time.Second // very long so cancel fires during backoff wait
	a.retryMaxDelay = 10 * time.Second

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel shortly after first failure — during the backoff sleep.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	body := a.buildRequest("gpt-4o-mini", []oaiMessage{{Role: "user", Content: "hi"}}, nil, 0.5)
	_, _, err := a.doRequestWithRetry(ctx, body)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	// Should have made only 1 call (cancelled during backoff before retry).
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Errorf("expected 1 call, got %d", n)
	}
}

func TestDoRequestWithRetry_429Retry(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.WriteHeader(429)
			w.Write([]byte(`{"error":"rate limited"}`))
			return
		}
		w.WriteHeader(200)
		w.Write(successResponse("ok"))
	}))
	defer ts.Close()

	a := newTestAdapter(ts.URL)

	body := a.buildRequest("gpt-4o-mini", []oaiMessage{{Role: "user", Content: "hi"}}, nil, 0.5)
	resp, _, err := a.doRequestWithRetry(context.Background(), body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "ok" {
		t.Errorf("got %q, want %q", resp.Text, "ok")
	}
}

func TestIsRetryableStatus(t *testing.T) {
	retryable := []int{429, 500, 502, 503, 504}
	for _, code := range retryable {
		if !isRetryableStatus(code) {
			t.Errorf("expected %d to be retryable", code)
		}
	}
	nonRetryable := []int{200, 400, 401, 403, 404, 422}
	for _, code := range nonRetryable {
		if isRetryableStatus(code) {
			t.Errorf("expected %d to NOT be retryable", code)
		}
	}
}

func TestBackoffDelay(t *testing.T) {
	a := &OpenAIAdapter{
		retryBaseDelay: 100 * time.Millisecond,
		retryMaxDelay:  1 * time.Second,
	}

	// Attempt 1: max = 100ms.
	for i := 0; i < 20; i++ {
		d := a.backoffDelay(1)
		if d > 100*time.Millisecond {
			t.Errorf("attempt 1: delay %v exceeds 100ms", d)
		}
	}

	// Attempt 3: max = 400ms.
	for i := 0; i < 20; i++ {
		d := a.backoffDelay(3)
		if d > 400*time.Millisecond {
			t.Errorf("attempt 3: delay %v exceeds 400ms", d)
		}
	}

	// Attempt 10: capped at retryMaxDelay (1s).
	for i := 0; i < 20; i++ {
		d := a.backoffDelay(10)
		if d > 1*time.Second {
			t.Errorf("attempt 10: delay %v exceeds max 1s", d)
		}
	}
}
