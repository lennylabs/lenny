// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	apisession "github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// gateClock pins the wall clock for the §15.1 line 797 drain-gate tests.
var gateClock = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func newDrainGateServer(t *testing.T, drainingPool string) (*Server, *memstore.Store, *poolstore.Memory) {
	t.Helper()
	store := memstore.New()
	pools := poolstore.NewMemory()
	s := New(store, Options{Pools: pools})
	s.clock = func() time.Time { return gateClock }
	// Resolve every (runtimeRef, profile) to the one pool under test so
	// the gate exercises without a Kubernetes client.
	s.poolNameResolver = func(context.Context, string, isolation.Profile, string) (string, bool) {
		return drainingPool, true
	}
	return s, store, pools
}

// TestRequirePoolNotDrainingRejects verifies the §15.1 line 797 gate
// rejects a create against a draining pool with 503 POOL_DRAINING, a
// Retry-After header, and details.pool / details.estimatedDrainSeconds.
func TestRequirePoolNotDrainingRejects_spec_15_1_797(t *testing.T) {
	s, store, pools := newDrainGateServer(t, "p")
	if err := pools.Create(context.Background(), poolstore.Pool{Name: "p", RuntimeRef: "echo", MaxSessionAgeSeconds: 3600}); err != nil {
		t.Fatalf("create pool: %v", err)
	}
	if _, err := pools.Update(context.Background(), "p", func(p *poolstore.Pool) error {
		p.DrainingSince = gateClock.Add(-time.Minute)
		return nil
	}); err != nil {
		t.Fatalf("drain pool: %v", err)
	}
	// A live session aged 120s sets the drain estimate (under the cap).
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "s1", TenantID: "acme", PoolRef: "p", State: apisession.StateRunning,
		CreatedAt: gateClock.Add(-120 * time.Second),
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/sessions", nil)
	ok := s.requirePoolNotDraining(rr, req, "echo", isolation.ProfileSandboxed, "")
	if ok {
		t.Fatal("gate admitted a create against a draining pool")
	}
	if rr.Code != 503 {
		t.Errorf("status = %d, want 503", rr.Code)
	}
	if rr.Header().Get("Retry-After") != "120" {
		t.Errorf("Retry-After = %q, want 120", rr.Header().Get("Retry-After"))
	}
	body := decodeErrorBody(t, rr.Body.Bytes())
	if body["code"] != "POOL_DRAINING" {
		t.Errorf("code = %v, want POOL_DRAINING", body["code"])
	}
	details, _ := body["details"].(map[string]any)
	if details["pool"] != "p" {
		t.Errorf("details.pool = %v, want p", details["pool"])
	}
	if details["estimatedDrainSeconds"].(float64) != 120 {
		t.Errorf("details.estimatedDrainSeconds = %v, want 120", details["estimatedDrainSeconds"])
	}
}

// TestRequirePoolNotDrainingAdmits verifies the gate is a pass-through
// when the resolved pool is active, when the resolver finds no pool, and
// when no pool store is wired. spec: §15.1 line 797.
func TestRequirePoolNotDrainingAdmits_spec_15_1_797(t *testing.T) {
	t.Run("active pool", func(t *testing.T) {
		s, _, pools := newDrainGateServer(t, "p")
		if err := pools.Create(context.Background(), poolstore.Pool{Name: "p", RuntimeRef: "echo"}); err != nil {
			t.Fatalf("create pool: %v", err)
		}
		rr := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/v1/sessions", nil)
		if !s.requirePoolNotDraining(rr, req, "echo", isolation.ProfileSandboxed, "") {
			t.Errorf("gate rejected against an active pool: %d %s", rr.Code, rr.Body.String())
		}
	})

	t.Run("resolver finds no pool", func(t *testing.T) {
		s, _, _ := newDrainGateServer(t, "p")
		s.poolNameResolver = func(context.Context, string, isolation.Profile, string) (string, bool) { return "", false }
		rr := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/v1/sessions", nil)
		if !s.requirePoolNotDraining(rr, req, "echo", isolation.ProfileSandboxed, "") {
			t.Errorf("gate rejected when no pool resolves: %d", rr.Code)
		}
	})

	t.Run("no pool store wired", func(t *testing.T) {
		s := New(memstore.New(), Options{})
		rr := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/v1/sessions", nil)
		if !s.requirePoolNotDraining(rr, req, "echo", isolation.ProfileSandboxed, "") {
			t.Errorf("gate rejected with no pool store: %d", rr.Code)
		}
	})
}
