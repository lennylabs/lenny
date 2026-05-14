// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract tests for the §11.6 operator-managed circuit-
// breaker admission middleware. Drives the middleware wrapped around
// the §15.1 sessionserver handler via httptest.

package rest_circuitbreaker_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/circuitbreaker"
	cbmw "github.com/lennylabs/lenny/pkg/gateway/middleware/circuitbreaker"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

func newTestServer(t *testing.T, reg cbmw.Registry) *httptest.Server {
	t.Helper()
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{})
	wrapped := cbmw.Wrap(srv.Handler(), reg, cbmw.Options{})
	ts := httptest.NewServer(wrapped)
	t.Cleanup(ts.Close)
	return ts
}

func post(t *testing.T, ts *httptest.Server, path string, body any) (*http.Response, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return resp, out
}

func TestClosedRegistryPassesThrough(t *testing.T) {
	reg := cbmw.NewMemoryRegistry()
	ts := newTestServer(t, reg)
	resp, _ := post(t, ts, "/v1/sessions", map[string]any{"runtimeRef": "claude-code"})
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("empty registry should not block; got %d", resp.StatusCode)
	}
}

func TestClosedBreakerPassesThrough(t *testing.T) {
	reg := cbmw.NewMemoryRegistry()
	reg.Set([]circuitbreaker.Breaker{
		{
			Name:      "session-creation-paused",
			State:     circuitbreaker.StateClosed,
			LimitTier: circuitbreaker.TierOperationType,
			Scope:     circuitbreaker.Scope{OperationType: circuitbreaker.OpSessionCreation},
		},
	})
	ts := newTestServer(t, reg)
	resp, _ := post(t, ts, "/v1/sessions", map[string]any{"runtimeRef": "claude-code"})
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("closed breaker must not block; got %d", resp.StatusCode)
	}
}

func TestOpenOperationTypeBreakerRejects(t *testing.T) {
	reg := cbmw.NewMemoryRegistry()
	reg.Set([]circuitbreaker.Breaker{
		{
			Name:      "session-creation-paused",
			State:     circuitbreaker.StateOpen,
			Reason:    "incident-2026-05-13",
			OpenedAt:  time.Now().UTC(),
			LimitTier: circuitbreaker.TierOperationType,
			Scope:     circuitbreaker.Scope{OperationType: circuitbreaker.OpSessionCreation},
		},
	})
	ts := newTestServer(t, reg)
	resp, body := post(t, ts, "/v1/sessions", map[string]any{"runtimeRef": "claude-code"})
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("open op-type breaker: want 503, got %d (body %v)", resp.StatusCode, body)
	}
	envelope, _ := body["error"].(map[string]any)
	if envelope["code"] != "CIRCUIT_BREAKER_OPEN" {
		t.Errorf("error code: want CIRCUIT_BREAKER_OPEN, got %v", envelope["code"])
	}
	details, _ := envelope["details"].(map[string]any)
	if details["circuitName"] != "session-creation-paused" {
		t.Errorf("details.circuitName: want session-creation-paused, got %v", details["circuitName"])
	}
	if details["limitTier"] != "operation_type" {
		t.Errorf("details.limitTier: want operation_type, got %v", details["limitTier"])
	}
}

// Non-matching open breaker (different operation_type) must NOT block.
func TestOpenBreakerWithDifferentScopeDoesNotMatch(t *testing.T) {
	reg := cbmw.NewMemoryRegistry()
	reg.Set([]circuitbreaker.Breaker{
		{
			Name:      "uploads-paused",
			State:     circuitbreaker.StateOpen,
			LimitTier: circuitbreaker.TierOperationType,
			Scope:     circuitbreaker.Scope{OperationType: circuitbreaker.OpUploads},
		},
	})
	ts := newTestServer(t, reg)
	resp, _ := post(t, ts, "/v1/sessions", map[string]any{"runtimeRef": "claude-code"})
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("uploads-only breaker must not block session-creation; got %d", resp.StatusCode)
	}
}

// Multiple breakers: middleware returns the first match per §11.6.
func TestFirstOpenMatchWins(t *testing.T) {
	reg := cbmw.NewMemoryRegistry()
	reg.Set([]circuitbreaker.Breaker{
		{
			Name:      "uploads-paused",
			State:     circuitbreaker.StateOpen,
			LimitTier: circuitbreaker.TierOperationType,
			Scope:     circuitbreaker.Scope{OperationType: circuitbreaker.OpUploads},
		},
		{
			Name:      "session-creation-paused",
			State:     circuitbreaker.StateOpen,
			LimitTier: circuitbreaker.TierOperationType,
			Scope:     circuitbreaker.Scope{OperationType: circuitbreaker.OpSessionCreation},
		},
	})
	ts := newTestServer(t, reg)
	resp, body := post(t, ts, "/v1/sessions", map[string]any{"runtimeRef": "claude-code"})
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", resp.StatusCode)
	}
	envelope, _ := body["error"].(map[string]any)
	details, _ := envelope["details"].(map[string]any)
	if details["circuitName"] != "session-creation-paused" {
		t.Errorf("FirstMatch should pick the session-creation breaker, got %v", details["circuitName"])
	}
}
