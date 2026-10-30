// SPDX-License-Identifier: MIT

package lenny

import (
	"context"
	"encoding/json"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestRetryPolicyBackoffGrows confirms the delay doubles per attempt
// and is capped at MaxDelay. Jitter is disabled so the delay is
// deterministic.
func TestRetryPolicyBackoffGrows(t *testing.T) {
	p := RetryPolicy{
		MaxAttempts: 6,
		BaseDelay:   100 * time.Millisecond,
		MaxDelay:    1 * time.Second,
		Jitter:      false,
	}
	want := []time.Duration{
		100 * time.Millisecond,
		200 * time.Millisecond,
		400 * time.Millisecond,
		800 * time.Millisecond,
		1 * time.Second, // capped
		1 * time.Second, // capped
	}
	for i, w := range want {
		got := p.delayForAttempt(i+1, nil)
		if got != w {
			t.Errorf("attempt %d delay: want %v, got %v", i+1, w, got)
		}
	}
}

// TestRetryPolicyJitterWithinBounds confirms a jittered delay stays
// within [0.5x, 1.0x) of the base computed delay.
func TestRetryPolicyJitterWithinBounds(t *testing.T) {
	p := RetryPolicy{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Second,
		MaxDelay:    10 * time.Second,
		Jitter:      true,
	}
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 100; i++ {
		got := p.delayForAttempt(1, rng)
		if got < 500*time.Millisecond || got >= 1*time.Second {
			t.Fatalf("jittered delay %v out of [500ms, 1s)", got)
		}
	}
}

// TestClientRetriesRetryableError confirms the Client retries a
// RATE_LIMITED response and succeeds on a later attempt.
func TestClientRetriesRetryableError(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
				"code": "RATE_LIMITED", "category": "POLICY",
				"message": "slow down", "retryable": true,
			}})
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "sess_1", "state": "created"})
	}))
	defer ts.Close()

	c, err := New(ts.URL, WithRetryPolicy(RetryPolicy{
		MaxAttempts: 5,
		BaseDelay:   time.Millisecond,
		MaxDelay:    time.Millisecond,
	}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	res, err := c.CreateSession(context.Background(), CreateSessionRequest{RuntimeRef: "echo"})
	if err != nil {
		t.Fatalf("CreateSession: want success after retries, got %v", err)
	}
	if res.ID != "sess_1" {
		t.Errorf("session id: want sess_1, got %q", res.ID)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("attempt count: want 3, got %d", got)
	}
}

// TestClientDoesNotRetryPermanentError confirms a non-retryable
// error is returned after a single attempt.
func TestClientDoesNotRetryPermanentError(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
			"code": "VALIDATION_ERROR", "category": "PERMANENT",
			"message": "bad request", "retryable": false,
		}})
	}))
	defer ts.Close()

	c, err := New(ts.URL, WithRetryPolicy(RetryPolicy{MaxAttempts: 5, BaseDelay: time.Millisecond}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = c.CreateSession(context.Background(), CreateSessionRequest{})
	apiErr, ok := AsAPIError(err)
	if !ok {
		t.Fatalf("want APIError, got %v", err)
	}
	if apiErr.Code != "VALIDATION_ERROR" {
		t.Errorf("code: want VALIDATION_ERROR, got %q", apiErr.Code)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("attempt count: want 1 (no retry), got %d", got)
	}
}

// TestClientIdempotencyKeyHeader confirms WithIdempotencyKey sets the
// §11.5 header on the request.
func TestClientIdempotencyKeyHeader(t *testing.T) {
	var seen string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Idempotency-Key")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "sess_1", "state": "created"})
	}))
	defer ts.Close()

	c, err := New(ts.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.CreateSession(context.Background(), CreateSessionRequest{RuntimeRef: "echo"},
		WithIdempotencyKey("key-123")); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if seen != "key-123" {
		t.Errorf("Idempotency-Key header: want key-123, got %q", seen)
	}
}

// TestClientContextCancellation confirms an already-canceled context
// aborts the request before it reaches the server.
func TestClientContextCancellation(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	c, err := New(ts.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.GetSession(ctx, "sess_1"); err == nil {
		t.Error("GetSession: want error for a canceled context")
	}
}
