// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// drainClock is the fixed wall clock the drain admin tests run against so
// session-age arithmetic is deterministic.
var drainClock = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

// fakeDrainMetrics records the last §15.1 line 797
// lenny_pool_draining_sessions_total gauge value per pool.
type fakeDrainMetrics struct {
	mu   sync.Mutex
	last map[string]int
}

func (f *fakeDrainMetrics) SetPoolDrainingSessions(pool string, count int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.last == nil {
		f.last = map[string]int{}
	}
	f.last[pool] = count
}

func (f *fakeDrainMetrics) get(pool string) (int, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.last[pool]
	return v, ok
}

func newDrainAdmin(t *testing.T) (*admin.Router, *poolstore.Memory, *memstore.Store, *fakeDrainMetrics, *recordingAudit) {
	t.Helper()
	pools := poolstore.NewMemory()
	sessions := memstore.New()
	metricsFake := &fakeDrainMetrics{}
	audit := &recordingAudit{}
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return drainClock },
		Audit: audit,
	}).WithPools(pools).WithSessions(sessions).WithPoolDrainMetrics(metricsFake)
	return router, pools, sessions, metricsFake, audit
}

func seedPool(t *testing.T, pools *poolstore.Memory, name string, maxAgeSeconds int) {
	t.Helper()
	if err := pools.Create(context.Background(), poolstore.Pool{
		Name:                 name,
		RuntimeRef:           "echo",
		IsolationProfile:     "sandboxed",
		WarmCount:            2,
		MaxSessionAgeSeconds: maxAgeSeconds,
	}); err != nil {
		t.Fatalf("seed pool %s: %v", name, err)
	}
}

func seedDrainSession(t *testing.T, s *memstore.Store, id, pool string, state session.State, ageSeconds int) {
	t.Helper()
	if err := s.Create(context.Background(), sessionstore.Session{
		ID:        id,
		TenantID:  "acme",
		PoolRef:   pool,
		State:     state,
		CreatedAt: drainClock.Add(-time.Duration(ageSeconds) * time.Second),
	}); err != nil {
		t.Fatalf("seed session %s: %v", id, err)
	}
}

// TestDrainPoolTransitionsAndReports verifies the §15.1 line 797 drain
// endpoint transitions the pool to `draining`, returns the documented
// body, sets the gauge, and emits an audit event.
func TestDrainPoolTransitionsAndReports_spec_15_1_797(t *testing.T) {
	router, pools, sessions, metricsFake, audit := newDrainAdmin(t)
	seedPool(t, pools, "p", 3600)
	seedDrainSession(t, sessions, "s1", "p", session.StateRunning, 100)
	seedDrainSession(t, sessions, "s2", "p", session.StateRunning, 40)

	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools/p/drain", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("drain status: %d body=%s", rr.Code, rr.Body.String())
	}
	var resp admin.PoolDrainResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != "draining" {
		t.Errorf("status = %q, want draining", resp.Status)
	}
	if resp.ActiveSessions != 2 {
		t.Errorf("activeSessions = %d, want 2", resp.ActiveSessions)
	}
	// longest active age = 100s, under the 3600s cap.
	if resp.EstimatedDrainSeconds != 100 {
		t.Errorf("estimatedDrainSeconds = %d, want 100", resp.EstimatedDrainSeconds)
	}
	row, _ := pools.Get(context.Background(), "p")
	if !row.IsDraining() {
		t.Errorf("pool not marked draining: %+v", row)
	}
	if v, ok := metricsFake.get("p"); !ok || v != 2 {
		t.Errorf("gauge = (%d,%v), want (2,true)", v, ok)
	}
	var found bool
	for _, ev := range audit.snapshot() {
		if ev.Type == "admin.pool.drained" {
			found = true
		}
	}
	if !found {
		t.Errorf("no admin.pool.drained audit event: %+v", audit.snapshot())
	}
}

// TestDrainPoolEstimateCappedAtMaxAge verifies the Retry-After estimate is
// capped at maxSessionAgeSeconds when the longest session age exceeds it.
// spec: §15.1 line 797.
func TestDrainPoolEstimateCappedAtMaxAge_spec_15_1_797(t *testing.T) {
	router, pools, sessions, _, _ := newDrainAdmin(t)
	seedPool(t, pools, "p", 30)
	seedDrainSession(t, sessions, "s1", "p", session.StateRunning, 100)

	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools/p/drain", nil)
	var resp admin.PoolDrainResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.EstimatedDrainSeconds != 30 {
		t.Errorf("estimatedDrainSeconds = %d, want 30 (capped)", resp.EstimatedDrainSeconds)
	}
}

// TestDrainPoolCountsOnlyLivePoolSessions verifies activeSessions excludes
// terminal sessions and sessions bound to other pools. spec: §15.1 line 797.
func TestDrainPoolCountsOnlyLivePoolSessions_spec_15_1_797(t *testing.T) {
	router, pools, sessions, _, _ := newDrainAdmin(t)
	seedPool(t, pools, "p", 3600)
	seedPool(t, pools, "other", 3600)
	seedDrainSession(t, sessions, "live", "p", session.StateRunning, 50)
	seedDrainSession(t, sessions, "done", "p", session.StateCompleted, 80)
	seedDrainSession(t, sessions, "elsewhere", "other", session.StateRunning, 90)

	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools/p/drain", nil)
	var resp admin.PoolDrainResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.ActiveSessions != 1 {
		t.Errorf("activeSessions = %d, want 1 (only the live session in pool p)", resp.ActiveSessions)
	}
}

// TestDrainPoolNotFound verifies a drain on a missing pool returns 404.
// spec: §15.1 line 797.
func TestDrainPoolNotFound_spec_15_1_797(t *testing.T) {
	router, _, _, _, _ := newDrainAdmin(t)
	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools/ghost/drain", nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

// TestDrainPoolDeletedReads404 verifies a drain on a soft-deleted pool
// reads as not found. spec: §15.1 line 797.
func TestDrainPoolDeletedReads404_spec_15_1_797(t *testing.T) {
	router, pools, _, _, _ := newDrainAdmin(t)
	seedPool(t, pools, "p", 3600)
	if err := pools.SoftDelete(context.Background(), "p", drainClock); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools/p/drain", nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for deleted pool", rr.Code)
	}
}

// TestDrainPoolIdempotent verifies a second drain does not churn the
// pool_config_generation (the drain clock is not reset). spec: §15.1 line 797.
func TestDrainPoolIdempotent_spec_15_1_797(t *testing.T) {
	router, pools, sessions, _, _ := newDrainAdmin(t)
	seedPool(t, pools, "p", 3600)
	seedDrainSession(t, sessions, "s1", "p", session.StateRunning, 60)

	if rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools/p/drain", nil); rr.Code != http.StatusOK {
		t.Fatalf("first drain: %d", rr.Code)
	}
	first, _ := pools.Get(context.Background(), "p")
	if rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools/p/drain", nil); rr.Code != http.StatusOK {
		t.Fatalf("second drain: %d", rr.Code)
	}
	second, _ := pools.Get(context.Background(), "p")
	if first.Generation != second.Generation {
		t.Errorf("generation churned on idempotent drain: %d -> %d", first.Generation, second.Generation)
	}
	if !first.DrainingSince.Equal(second.DrainingSince) {
		t.Errorf("DrainingSince reset on idempotent drain: %v -> %v", first.DrainingSince, second.DrainingSince)
	}
}

// TestGetPoolSurfacesPhase verifies the §15.1 line 797 GET surfaces
// `phase` and, while draining, `activeSessions`. An active pool reports
// phase=active and omits activeSessions.
func TestGetPoolSurfacesPhase_spec_15_1_797(t *testing.T) {
	router, pools, sessions, _, _ := newDrainAdmin(t)
	seedPool(t, pools, "p", 3600)
	seedDrainSession(t, sessions, "s1", "p", session.StateRunning, 70)

	// Active pool: phase=active, no activeSessions.
	g := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/pools/p", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, g)
	var before admin.PoolPayload
	_ = json.Unmarshal(rr.Body.Bytes(), &before)
	if before.Phase != "active" {
		t.Errorf("phase = %q, want active", before.Phase)
	}
	if before.ActiveSessions != nil {
		t.Errorf("activeSessions = %v, want nil for active pool", *before.ActiveSessions)
	}

	// Drain then GET: phase=draining, activeSessions=1.
	if rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools/p/drain", nil); rr.Code != http.StatusOK {
		t.Fatalf("drain: %d", rr.Code)
	}
	g2 := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/pools/p", nil))
	rr2 := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr2, g2)
	var after admin.PoolPayload
	_ = json.Unmarshal(rr2.Body.Bytes(), &after)
	if after.Phase != "draining" {
		t.Errorf("phase = %q, want draining", after.Phase)
	}
	if after.ActiveSessions == nil || *after.ActiveSessions != 1 {
		t.Errorf("activeSessions = %v, want 1", after.ActiveSessions)
	}
}
