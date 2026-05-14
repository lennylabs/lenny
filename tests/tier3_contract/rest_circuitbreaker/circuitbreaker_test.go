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

// spec: 11.6 (admission with no breakers configured)
// diagnosis: Empty registry returned non-201 — the middleware
//
//	short-circuit on the no-breakers path is broken. Inspect
//	registry.Active() in pkg/gateway/middleware/circuitbreaker.
func TestClosedRegistryPassesThrough(t *testing.T) {
	reg := cbmw.NewMemoryRegistry()
	ts := newTestServer(t, reg)
	resp, _ := post(t, ts, "/v1/sessions", map[string]any{"runtimeRef": "claude-code"})
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("empty registry should not block; got %d", resp.StatusCode)
	}
}

// spec: 11.6 (breaker state machine: Closed allows traffic)
// diagnosis: A breaker in StateClosed blocked the request. The
//
//	state-based admission check is incorrectly treating Closed
//	as Open.
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

// spec: 11.6 (open-tier breaker → 503 CIRCUIT_BREAKER_OPEN envelope with details)
// diagnosis: Open operation_type breaker did not reject with the
//
//	full envelope (code/details). Inspect Match() or
//	writeOpenEnvelope; the limitTier mapping may have drifted.
func TestOpenOperationTypeBreakerRejects(t *testing.T) {
	reg := cbmw.NewMemoryRegistry()
	reg.Set([]circuitbreaker.Breaker{
		{
			Name:      "session-creation-paused",
			State:     circuitbreaker.StateOpen,
			Reason:    "incident-2026-05-13",
			OpenedAt:  time.Date(2026, 5, 13, 0, 0, 0, 0, time.UTC),
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

// spec: 11.6 (scope-narrowing: a breaker matches only when its Scope contains the request)
// diagnosis: An open breaker with a different operation_type blocked
//
//	the request — Scope matching is too broad. Inspect
//	Scope.Contains() in pkg/circuitbreaker.
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

// spec: 11.6 (deterministic first-match selection across the active breaker list)
// diagnosis: Two breakers both matched and the response named the
//
//	wrong circuitName. The selection order is non-deterministic
//	or the registry returned breakers in the wrong order.
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
