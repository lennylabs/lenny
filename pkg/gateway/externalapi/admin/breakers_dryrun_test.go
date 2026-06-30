// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/circuitbreaker"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
)

// spec: §15.1 line 1140 — the circuit-breaker open/close endpoints
// support ?dryRun=true, returning a reduced simulation object that reads
// Redis state but never writes it and emits no audit event.

type breakerSimResponse struct {
	Name       string `json:"name"`
	State      string `json:"state"`
	Reason     string `json:"reason"`
	LimitTier  string `json:"limit_tier"`
	Simulation struct {
		CurrentState     string `json:"currentState"`
		PredictedState   string `json:"predictedState"`
		WouldChangeState bool   `json:"wouldChangeState"`
	} `json:"simulation"`
}

func decodeBreakerSim(t *testing.T, body []byte) breakerSimResponse {
	t.Helper()
	var r breakerSimResponse
	if err := json.Unmarshal(body, &r); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, body)
	}
	return r
}

func TestOpenBreakerDryRunNotRegistered_spec_15_1(t *testing.T) {
	router, store, audit := newBreakerAdmin(t)
	rr := breakerReq(t, router.Handler(), http.MethodPost,
		"/v1/admin/circuit-breakers/rt-new/open?dryRun=true",
		admin.OpenBreakerRequest{Reason: "drill", LimitTier: "runtime", Scope: admin.ScopePayload{Runtime: "echo"}})
	if rr.Code != http.StatusOK {
		t.Fatalf("dryRun open: status %d, body %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-Dry-Run") != "true" {
		t.Errorf("missing X-Dry-Run header")
	}
	sim := decodeBreakerSim(t, rr.Body.Bytes())
	if sim.State != "open" || sim.Simulation.PredictedState != "open" {
		t.Errorf("predicted state: %+v", sim)
	}
	if sim.Simulation.CurrentState != "not_registered" || !sim.Simulation.WouldChangeState {
		t.Errorf("simulation = %+v, want currentState=not_registered wouldChangeState=true", sim.Simulation)
	}
	// No write, no audit.
	if _, err := store.Get(context.Background(), "rt-new"); err == nil {
		t.Errorf("dryRun open registered a breaker")
	}
	if snap := audit.snapshot(); len(snap) != 0 {
		t.Errorf("dryRun open emitted audit: %+v", snap)
	}
}

func TestOpenBreakerDryRunIdempotentNoOp_spec_15_1(t *testing.T) {
	router, store, _ := newBreakerAdmin(t)
	_, _ = store.Open(context.Background(), circuitbreaker.Breaker{
		Name: "rt-x", State: circuitbreaker.StateOpen, LimitTier: "runtime",
		Scope: circuitbreaker.Scope{Runtime: "echo"},
	})
	rr := breakerReq(t, router.Handler(), http.MethodPost,
		"/v1/admin/circuit-breakers/rt-x/open?dryRun=true",
		admin.OpenBreakerRequest{Reason: "again", LimitTier: "runtime", Scope: admin.ScopePayload{Runtime: "echo"}})
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
	}
	sim := decodeBreakerSim(t, rr.Body.Bytes())
	if sim.Simulation.CurrentState != "open" || sim.Simulation.WouldChangeState {
		t.Errorf("idempotent open simulation = %+v, want currentState=open wouldChangeState=false", sim.Simulation)
	}
}

func TestOpenBreakerDryRunRejectsScopeChange_spec_15_1(t *testing.T) {
	router, store, _ := newBreakerAdmin(t)
	_, _ = store.Open(context.Background(), circuitbreaker.Breaker{
		Name: "rt-pinned", State: circuitbreaker.StateOpen, LimitTier: "runtime",
		Scope: circuitbreaker.Scope{Runtime: "echo"},
	})
	rr := breakerReq(t, router.Handler(), http.MethodPost,
		"/v1/admin/circuit-breakers/rt-pinned/open?dryRun=true",
		admin.OpenBreakerRequest{LimitTier: "runtime", Scope: admin.ScopePayload{Runtime: "claude"}})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("scope change under dryRun: status %d, want 422; body %s", rr.Code, rr.Body.String())
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &env)
	if env.Error.Code != "INVALID_BREAKER_SCOPE" {
		t.Errorf("code = %q, want INVALID_BREAKER_SCOPE", env.Error.Code)
	}
}

func TestOpenBreakerDryRunRejectsInvalidScope_spec_15_1(t *testing.T) {
	router, _, _ := newBreakerAdmin(t)
	rr := breakerReq(t, router.Handler(), http.MethodPost,
		"/v1/admin/circuit-breakers/bad/open?dryRun=true",
		admin.OpenBreakerRequest{LimitTier: "runtime", Scope: admin.ScopePayload{Pool: "wrong-field"}})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("invalid scope under dryRun: status %d, want 422", rr.Code)
	}
}

func TestCloseBreakerDryRun_spec_15_1(t *testing.T) {
	router, store, audit := newBreakerAdmin(t)
	_, _ = store.Open(context.Background(), circuitbreaker.Breaker{
		Name: "rt-close", State: circuitbreaker.StateOpen, LimitTier: "runtime",
		Scope: circuitbreaker.Scope{Runtime: "echo"},
	})
	rr := breakerReq(t, router.Handler(), http.MethodPost,
		"/v1/admin/circuit-breakers/rt-close/close?dryRun=true", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("dryRun close: status %d, body %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-Dry-Run") != "true" {
		t.Errorf("missing X-Dry-Run header")
	}
	sim := decodeBreakerSim(t, rr.Body.Bytes())
	if sim.State != "closed" || sim.Simulation.PredictedState != "closed" {
		t.Errorf("predicted state: %+v", sim)
	}
	if sim.Simulation.CurrentState != "open" || !sim.Simulation.WouldChangeState {
		t.Errorf("simulation = %+v, want currentState=open wouldChangeState=true", sim.Simulation)
	}
	if sim.LimitTier != "runtime" {
		t.Errorf("limit_tier = %q, want runtime (echoed from persisted breaker)", sim.LimitTier)
	}
	// The persisted breaker must remain open, and no audit row written.
	got, _ := store.Get(context.Background(), "rt-close")
	if got.State != circuitbreaker.StateOpen {
		t.Errorf("dryRun close mutated the breaker: %s", got.State)
	}
	if snap := audit.snapshot(); len(snap) != 0 {
		t.Errorf("dryRun close emitted audit: %+v", snap)
	}
}

func TestCloseBreakerDryRunNotFound_spec_15_1(t *testing.T) {
	router, _, _ := newBreakerAdmin(t)
	rr := breakerReq(t, router.Handler(), http.MethodPost,
		"/v1/admin/circuit-breakers/ghost/close?dryRun=true", nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("dryRun close missing breaker: status %d, want 404", rr.Code)
	}
}
