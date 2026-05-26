// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

func newPoolAdmin(t *testing.T) (*admin.Router, *poolstore.Memory, *runtimestore.Memory, *recordingAudit) {
	t.Helper()
	tenants := tenantstore.NewMemory()
	runtimes := runtimestore.NewMemory()
	pools := poolstore.NewMemory()
	audit := &recordingAudit{}
	router := admin.NewRouter(tenants, admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Audit: audit,
	}).WithRuntimes(runtimes).WithPools(pools)
	return router, pools, runtimes, audit
}

func poolReq(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		buf = bytes.NewReader(b)
	} else {
		buf = bytes.NewReader(nil)
	}
	req := withAdminPrincipal(httptest.NewRequest(method, path, buf))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestCreatePoolHappyPath(t *testing.T) {
	router, store, runtimes, audit := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})

	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:             "default-pool",
		RuntimeRef:       "echo",
		IsolationProfile: "sandboxed",
		ExecutionMode:    "session",
		ResourceClass:    "small",
		WarmCount:        3,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(context.Background(), "default-pool")
	if row.RuntimeRef != "echo" || row.WarmCount != 3 {
		t.Errorf("stored row: %+v", row)
	}
	if len(audit.snapshot()) != 1 {
		t.Errorf("audit: %+v", audit.snapshot())
	}
}

func TestCreatePoolRejectsUnknownRuntime(t *testing.T) {
	router, _, _, _ := newPoolAdmin(t)
	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:       "p",
		RuntimeRef: "missing-runtime",
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("unknown runtime: got %d, want 400", rr.Code)
	}
}

func TestCreatePoolRejectsBadIsolation(t *testing.T) {
	router, _, _, _ := newPoolAdmin(t)
	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:             "p",
		IsolationProfile: "ferrous",
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("bad isolation: got %d", rr.Code)
	}
}

// TestCreatePoolDefaultsSandboxedWithoutDevMode_spec_5_3 verifies a pool
// that omits isolationProfile defaults to the production `sandboxed`
// profile when dev mode is off.
//
// spec: §5.3 line 677.
func TestCreatePoolDefaultsSandboxedWithoutDevMode_spec_5_3(t *testing.T) {
	router, store, runtimes, _ := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})

	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:       "p",
		RuntimeRef: "echo",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(context.Background(), "p")
	if row.IsolationProfile != isolation.ProfileSandboxed {
		t.Errorf("isolationProfile = %q, want sandboxed", row.IsolationProfile)
	}
	if row.AllowStandardIsolation {
		t.Error("allowStandardIsolation set true without dev mode; want false")
	}
}

// TestCreatePoolDevModeDefaultsStandard_spec_5_3 verifies the §5.3 line
// 677 dev-mode fallback: a pool that omits isolationProfile under dev
// mode defaults to `standard` (runc) and receives the allowStandardIsolation
// opt-in dev mode supplies on the operator's behalf, so the pool is
// accepted on a gVisor-less cluster.
//
// spec: §5.3 line 677.
func TestCreatePoolDevModeDefaultsStandard_spec_5_3(t *testing.T) {
	tenants := tenantstore.NewMemory()
	runtimes := runtimestore.NewMemory()
	pools := poolstore.NewMemory()
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})
	router := admin.NewRouter(tenants, admin.Options{
		Clock:   func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		DevMode: true,
	}).WithRuntimes(runtimes).WithPools(pools)

	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:       "dev-pool",
		RuntimeRef: "echo",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("dev-mode pool create: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	row, _ := pools.Get(context.Background(), "dev-pool")
	if row.IsolationProfile != isolation.ProfileStandard {
		t.Errorf("isolationProfile = %q, want standard (runc) under dev mode", row.IsolationProfile)
	}
	if !row.AllowStandardIsolation {
		t.Error("allowStandardIsolation = false under dev-mode default; want true")
	}
}

// TestCreatePoolDevModeExplicitProfileWins_spec_5_3 verifies dev mode
// only governs the *default*: an explicitly-set standard profile without
// the opt-in is still rejected, so dev mode does not silently weaken an
// explicit configuration.
//
// spec: §5.3 line 677.
func TestCreatePoolDevModeExplicitProfileWins_spec_5_3(t *testing.T) {
	tenants := tenantstore.NewMemory()
	runtimes := runtimestore.NewMemory()
	pools := poolstore.NewMemory()
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})
	router := admin.NewRouter(tenants, admin.Options{DevMode: true}).
		WithRuntimes(runtimes).WithPools(pools)

	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:             "explicit-runc",
		RuntimeRef:       "echo",
		IsolationProfile: "standard",
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("explicit standard without opt-in under dev mode: got %d, want 400", rr.Code)
	}
}

func TestCreatePoolStandardIsolationRequiresOptIn(t *testing.T) {
	router, _, _, _ := newPoolAdmin(t)
	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:             "runc-pool",
		IsolationProfile: "standard",
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("standard without opt-in: got %d, want 400", rr.Code)
	}
}

func TestCreatePoolStandardIsolationWithOptInAllowed(t *testing.T) {
	router, _, _, _ := newPoolAdmin(t)
	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:                   "runc-pool",
		IsolationProfile:       "standard",
		AllowStandardIsolation: true,
	})
	if rr.Code != http.StatusCreated {
		t.Errorf("standard with opt-in: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
}

func TestListPoolsFilterByRuntime(t *testing.T) {
	router, store, _, _ := newPoolAdmin(t)
	_ = store.Create(context.Background(), poolstore.Pool{Name: "a", RuntimeRef: "echo", IsolationProfile: isolation.ProfileSandboxed})
	_ = store.Create(context.Background(), poolstore.Pool{Name: "b", RuntimeRef: "claude", IsolationProfile: isolation.ProfileSandboxed})
	rr := poolReq(t, router.Handler(), http.MethodGet, "/v1/admin/pools?runtimeRef=echo", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var resp struct {
		Pools []admin.PoolPayload `json:"pools"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.Pools) != 1 || resp.Pools[0].Name != "a" {
		t.Errorf("filter: %+v", resp.Pools)
	}
}

func TestUpdatePool(t *testing.T) {
	router, store, _, _ := newPoolAdmin(t)
	_ = store.Create(context.Background(), poolstore.Pool{
		Name: "p", IsolationProfile: isolation.ProfileSandboxed, WarmCount: 1,
	})
	wc := 5
	body := admin.UpdatePoolRequest{WarmCount: &wc}
	rr := poolReq(t, router.Handler(), http.MethodPut, "/v1/admin/pools/p", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(context.Background(), "p")
	if row.WarmCount != 5 {
		t.Errorf("WarmCount: %d", row.WarmCount)
	}
}

func TestUpdatePoolRejectsBadIsolation(t *testing.T) {
	router, store, _, _ := newPoolAdmin(t)
	_ = store.Create(context.Background(), poolstore.Pool{Name: "p", IsolationProfile: isolation.ProfileSandboxed})
	iso := "ferrous"
	rr := poolReq(t, router.Handler(), http.MethodPut, "/v1/admin/pools/p", admin.UpdatePoolRequest{IsolationProfile: &iso})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("bad isolation update: got %d", rr.Code)
	}
}

func TestDeletePoolSoftDeletes(t *testing.T) {
	router, store, _, audit := newPoolAdmin(t)
	_ = store.Create(context.Background(), poolstore.Pool{Name: "p", IsolationProfile: isolation.ProfileSandboxed})

	rr := poolReq(t, router.Handler(), http.MethodDelete, "/v1/admin/pools/p", nil)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status: %d", rr.Code)
	}
	row, _ := store.Get(context.Background(), "p")
	if row.DeletedAt.IsZero() {
		t.Errorf("DeletedAt should be set")
	}
	if len(audit.snapshot()) != 1 || audit.snapshot()[0].Type != "admin.pool.soft_deleted" {
		t.Errorf("audit: %+v", audit.snapshot())
	}
}

// TestPoolMutationAuthorization covers the pool mutation endpoints
// against the §10.2 "Manage pools / scaling policies" matrix row. Pool
// create and delete are platform-admin-only per §15.1 (they define or
// remove a platform-global record): a tenant-admin receives 403. Pool
// update is granted to tenant-admin for "own tenant", which §15.1
// scopes to access-table entries — a tenant-admin with no
// pool_tenant_access grant for the target pool receives 404 (the gate
// reports the out-of-scope resource as absent, matching the read-path
// scoping in handleGetPool). (Reconciliation note: the PUT previously
// used requireAdmin, which denied the tenant-admin entitlement the
// §10.2 matrix grants.)
func TestPoolMutationAuthorization(t *testing.T) {
	router, store, _, _ := newPoolAdmin(t)
	_ = store.Create(context.Background(), poolstore.Pool{Name: "p", IsolationProfile: isolation.ProfileSandboxed})

	for _, c := range []struct {
		method, path string
		body         []byte
		want         int
	}{
		// Create / delete: platform-admin only; tenant-admin is forbidden.
		{http.MethodPost, "/v1/admin/pools", []byte(`{"name":"x"}`), http.StatusForbidden},
		{http.MethodDelete, "/v1/admin/pools/p", nil, http.StatusForbidden},
		// Update: granted to tenant-admin, scoped to the access table —
		// an ungranted pool reads as absent (404), not forbidden.
		{http.MethodPut, "/v1/admin/pools/p", []byte("{}"), http.StatusNotFound},
	} {
		req := withTenantAdminPrincipal(httptest.NewRequest(c.method, c.path, bytes.NewReader(c.body)))
		rr := httptest.NewRecorder()
		router.Handler().ServeHTTP(rr, req)
		if rr.Code != c.want {
			t.Errorf("%s %s as tenant-admin: got %d, want %d", c.method, c.path, rr.Code, c.want)
		}
	}
}

// fakeResumer records the pool names passed to
// ResumePoolReconciliation and returns a canned cleared count.
type fakeResumer struct {
	cleared map[string]int
	calls   []string
}

func (f *fakeResumer) ResumePoolReconciliation(_ context.Context, poolName string) (int, error) {
	f.calls = append(f.calls, poolName)
	return f.cleared[poolName], nil
}

// spec: §4.6.2 item 3 condition (c)
// (POST /v1/admin/pools/{name}/resume-reconciliation clears denial backoff)
// An operator resets a stuck pool's in-memory admission-denial counter
// and the handler reports how many CRD tuples were cleared.
func TestResumeReconciliationClearsStuckPool(t *testing.T) {
	router, pools, runtimes, audit := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})
	_ = pools.Create(context.Background(), poolstore.Pool{
		Name: "stuck-pool", RuntimeRef: "echo", IsolationProfile: isolation.Default(),
	})
	resumer := &fakeResumer{cleared: map[string]int{"stuck-pool": 2}}
	router.WithReconciliationResumer(resumer)

	rr := poolReq(t, router.Handler(), http.MethodPost,
		"/v1/admin/pools/stuck-pool/resume-reconciliation", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		Pool          string `json:"pool"`
		ClearedTuples int    `json:"clearedTuples"`
		WasStuck      bool   `json:"wasStuck"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Pool != "stuck-pool" || body.ClearedTuples != 2 || !body.WasStuck {
		t.Errorf("body = %+v, want stuck-pool/2/true", body)
	}
	if len(resumer.calls) != 1 || resumer.calls[0] != "stuck-pool" {
		t.Errorf("resumer calls = %v", resumer.calls)
	}
	if len(audit.snapshot()) != 1 {
		t.Errorf("audit: %+v", audit.snapshot())
	}
}

// spec: §4.6.2 item 3 condition (c) (unknown pool is 404, resumer untouched)
func TestResumeReconciliationUnknownPool(t *testing.T) {
	router, _, _, _ := newPoolAdmin(t)
	resumer := &fakeResumer{cleared: map[string]int{}}
	router.WithReconciliationResumer(resumer)

	rr := poolReq(t, router.Handler(), http.MethodPost,
		"/v1/admin/pools/ghost/resume-reconciliation", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if len(resumer.calls) != 0 {
		t.Errorf("resumer called for an unknown pool: %v", resumer.calls)
	}
}

// spec: §4.6.2 item 3 (route absent without a wired PoolScalingController)
// The endpoint is registered only when a resumer is wired; otherwise
// the gateway has no PSC to address and the route 404s as unrouted.
func TestResumeReconciliationRouteAbsentWithoutResumer(t *testing.T) {
	router, pools, runtimes, _ := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})
	_ = pools.Create(context.Background(), poolstore.Pool{
		Name: "p", RuntimeRef: "echo", IsolationProfile: isolation.Default(),
	})
	rr := poolReq(t, router.Handler(), http.MethodPost,
		"/v1/admin/pools/p/resume-reconciliation", nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d, want 404 (route unregistered)", rr.Code)
	}
}

// fakePoolStatus is a stub admin.PoolStatusReader for the §5.2 line 629
// admin-GET live-status assertions.
type fakePoolStatus struct {
	condition string
	idle      int
	found     bool
	err       error
}

func (f fakePoolStatus) PoolStatus(_ context.Context, _ string) (string, int, bool, error) {
	return f.condition, f.idle, f.found, f.err
}

func seedGetPool(t *testing.T, pools *poolstore.Memory, runtimes *runtimestore.Memory) {
	t.Helper()
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})
	_ = pools.Create(context.Background(), poolstore.Pool{
		Name: "p1", RuntimeRef: "echo", IsolationProfile: isolation.Default(),
		ExecutionMode: runtimestore.ExecutionModeSession,
	})
}

// spec: §5.2 line 629 — the admin pool GET surfaces poolCondition and
// idlePodCount during the bootstrap window when a PoolStatusReader is
// wired. idlePodCount of 0 must be emitted (the pointer field is
// non-nil), and poolCondition reports the warming state.
func TestGetPoolSurfacesWarmingStatus(t *testing.T) {
	router, pools, runtimes, _ := newPoolAdmin(t)
	seedGetPool(t, pools, runtimes)
	router = router.WithPoolStatusReader(fakePoolStatus{condition: "PoolWarmingUp", idle: 0, found: true})

	rr := poolReq(t, router.Handler(), http.MethodGet, "/v1/admin/pools/p1", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte(`"idlePodCount":0`)) {
		t.Errorf("body omits idlePodCount:0 during bootstrap: %s", rr.Body.String())
	}
	var got admin.PoolPayload
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.PoolCondition == nil || *got.PoolCondition != "PoolWarmingUp" {
		t.Errorf("poolCondition = %v, want PoolWarmingUp", got.PoolCondition)
	}
	if got.IdlePodCount == nil || *got.IdlePodCount != 0 {
		t.Errorf("idlePodCount = %v, want 0", got.IdlePodCount)
	}
}

// spec: §5.2 line 629 — a ready pool reports idlePodCount with no
// warming condition.
func TestGetPoolReadyHasIdleCountNoCondition(t *testing.T) {
	router, pools, runtimes, _ := newPoolAdmin(t)
	seedGetPool(t, pools, runtimes)
	router = router.WithPoolStatusReader(fakePoolStatus{condition: "", idle: 5, found: true})

	rr := poolReq(t, router.Handler(), http.MethodGet, "/v1/admin/pools/p1", nil)
	var got admin.PoolPayload
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.PoolCondition != nil {
		t.Errorf("poolCondition = %v, want nil for a ready pool", *got.PoolCondition)
	}
	if got.IdlePodCount == nil || *got.IdlePodCount != 5 {
		t.Errorf("idlePodCount = %v, want 5", got.IdlePodCount)
	}
}

// spec: §5.2 line 629 — the live-status fields are omitted when no
// reader is wired (the Postgres-only posture) or when the pool has no
// reconciled SandboxWarmPool yet (found=false), so a misleading zero is
// never reported.
func TestGetPoolOmitsLiveStatusWhenUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reader admin.PoolStatusReader
	}{
		{"reader unwired", nil},
		{"pool unreconciled", fakePoolStatus{found: false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router, pools, runtimes, _ := newPoolAdmin(t)
			seedGetPool(t, pools, runtimes)
			if tc.reader != nil {
				router = router.WithPoolStatusReader(tc.reader)
			}
			rr := poolReq(t, router.Handler(), http.MethodGet, "/v1/admin/pools/p1", nil)
			if rr.Code != http.StatusOK {
				t.Fatalf("status: %d", rr.Code)
			}
			if bytes.Contains(rr.Body.Bytes(), []byte("idlePodCount")) ||
				bytes.Contains(rr.Body.Bytes(), []byte("poolCondition")) {
				t.Errorf("live-status fields should be omitted: %s", rr.Body.String())
			}
		})
	}
}

// TestCreatePoolEgressProfileRoundTrip exercises the §13.2 egressProfile
// admin round-trip: an explicit profile persists, and an omitted one
// resolves to the §13.2 default (restricted).
func TestCreatePoolEgressProfileRoundTrip(t *testing.T) {
	router, store, runtimes, _ := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})

	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name: "p-internet", RuntimeRef: "echo", IsolationProfile: "sandboxed",
		EgressProfile: "internet",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	if row, _ := store.Get(context.Background(), "p-internet"); string(row.EgressProfile) != "internet" {
		t.Errorf("stored egressProfile = %q, want internet", row.EgressProfile)
	}

	rr = poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name: "p-default", RuntimeRef: "echo", IsolationProfile: "sandboxed",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	if row, _ := store.Get(context.Background(), "p-default"); string(row.EgressProfile) != "restricted" {
		t.Errorf("omitted egressProfile defaulted to %q, want restricted (§13.2)", row.EgressProfile)
	}
}

// TestCreatePoolRejectsInternetEgressOnStandard surfaces the §13.2
// cross-control through the admin API as a 400 VALIDATION_ERROR.
func TestCreatePoolRejectsInternetEgressOnStandard(t *testing.T) {
	router, _, runtimes, _ := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})
	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name: "runc-internet", RuntimeRef: "echo", IsolationProfile: "standard",
		AllowStandardIsolation: true, EgressProfile: "internet",
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreatePoolRejectsUnknownEgressProfile(t *testing.T) {
	router, _, runtimes, _ := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})
	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name: "bad", RuntimeRef: "echo", IsolationProfile: "sandboxed", EgressProfile: "open",
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
}

// TestCreatePoolEmitsWeakIsolationEvent covers F-5.3.12: a runc pool
// admitted via allowStandardIsolation emits the DirectModeWeakIsolation
// warning audit event in addition to admin.pool.created.
func TestCreatePoolEmitsWeakIsolationEvent(t *testing.T) {
	router, _, runtimes, audit := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})
	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name: "runc-pool", RuntimeRef: "echo", IsolationProfile: "standard",
		AllowStandardIsolation: true,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	var weak bool
	for _, e := range audit.snapshot() {
		if e.Type == "pool.direct_mode_weak_isolation" {
			weak = true
		}
	}
	if !weak {
		t.Errorf("expected pool.direct_mode_weak_isolation event, got %+v", audit.snapshot())
	}
}

// TestCreateSandboxedPoolEmitsNoWeakIsolationEvent confirms a sandboxed
// pool (the default posture) emits no weak-isolation warning.
func TestCreateSandboxedPoolEmitsNoWeakIsolationEvent(t *testing.T) {
	router, _, runtimes, audit := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})
	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name: "gvisor-pool", RuntimeRef: "echo", IsolationProfile: "sandboxed",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	for _, e := range audit.snapshot() {
		if e.Type == "pool.direct_mode_weak_isolation" {
			t.Errorf("sandboxed pool should not emit weak-isolation event")
		}
	}
}
