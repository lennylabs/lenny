// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
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
	if method == http.MethodPut {
		// spec: §15.1 lines 1207-1211 — the canonical pool PUT requires an
		// If-Match precondition. Fetch the resource's current ETag so the
		// many tests exercising other pool-update behaviour pass the
		// concurrency gate; the dedicated ETag tests set headers directly.
		if etag := currentPoolETag(h, path); etag != "" {
			req.Header.Set("If-Match", etag)
		}
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// currentPoolETag reads the ETag a GET on the pool reports, so a PUT can
// satisfy the §15.1 optimistic-concurrency precondition. It strips the
// §25.17 /warm-count sub-route suffix to address the base resource.
func currentPoolETag(h http.Handler, putPath string) string {
	base := strings.TrimSuffix(putPath, "/warm-count")
	g := withAdminPrincipal(httptest.NewRequest(http.MethodGet, base, nil))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, g)
	return rr.Header().Get("ETag")
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
		Pools []admin.PoolPayload `json:"items"`
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

// TestUpdatePoolWarmCount covers §25.17 lines 5232-5239 — the warm-count
// sub-route maps the operability `minWarm` field onto the §15.1 warm-count
// update and applies it when confirm:true.
func TestUpdatePoolWarmCount_spec_25_17(t *testing.T) {
	router, store, _, _ := newPoolAdmin(t)
	_ = store.Create(context.Background(), poolstore.Pool{
		Name: "default-gvisor", IsolationProfile: isolation.ProfileSandboxed, WarmCount: 5,
	})
	rr := poolReq(t, router.Handler(), http.MethodPut, "/v1/admin/pools/default-gvisor/warm-count",
		map[string]any{"minWarm": 15, "confirm": true})
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(context.Background(), "default-gvisor")
	if row.WarmCount != 15 {
		t.Errorf("WarmCount: %d, want 15", row.WarmCount)
	}
}

// TestUpdatePoolWarmCountDryRun covers §25.2 dry-run/confirm — without
// confirm the warm-count route returns a preview and does not scale.
func TestUpdatePoolWarmCountDryRun_spec_25_17(t *testing.T) {
	router, store, _, _ := newPoolAdmin(t)
	_ = store.Create(context.Background(), poolstore.Pool{
		Name: "p", IsolationProfile: isolation.ProfileSandboxed, WarmCount: 5,
	})
	rr := poolReq(t, router.Handler(), http.MethodPut, "/v1/admin/pools/p/warm-count",
		map[string]any{"minWarm": 15})
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	var out map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out["dryRun"] != true {
		t.Errorf("dryRun = %v, want true", out["dryRun"])
	}
	row, _ := store.Get(context.Background(), "p")
	if row.WarmCount != 5 {
		t.Errorf("WarmCount: %d, want unchanged 5 on a dry run", row.WarmCount)
	}
}

// TestUpdatePoolWarmCountRejectsMissingAndNegative covers the §25.17
// warm-count validation: minWarm is required and must not be negative.
func TestUpdatePoolWarmCountRejectsMissingAndNegative_spec_25_17(t *testing.T) {
	router, store, _, _ := newPoolAdmin(t)
	_ = store.Create(context.Background(), poolstore.Pool{Name: "p", IsolationProfile: isolation.ProfileSandboxed})
	missing := poolReq(t, router.Handler(), http.MethodPut, "/v1/admin/pools/p/warm-count",
		map[string]any{"confirm": true})
	if missing.Code != http.StatusBadRequest {
		t.Errorf("missing minWarm: got %d, want 400", missing.Code)
	}
	negative := poolReq(t, router.Handler(), http.MethodPut, "/v1/admin/pools/p/warm-count",
		map[string]any{"minWarm": -1, "confirm": true})
	if negative.Code != http.StatusBadRequest {
		t.Errorf("negative minWarm: got %d, want 400", negative.Code)
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

// spec: §4.6.2 item 3 condition (c) (durable cross-process resume)
// Without an in-process resumer (the production split deployment) the
// route is still registered; the handler bumps the pool's durable
// reconciliation_resume_epoch and reports it on the async response so the
// PoolScalingController honors the resume on its next reconcile tick.
func TestResumeReconciliationBumpsEpochWithoutResumer_spec_4_6_2(t *testing.T) {
	router, pools, runtimes, audit := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})
	_ = pools.Create(context.Background(), poolstore.Pool{
		Name: "p", RuntimeRef: "echo", IsolationProfile: isolation.Default(),
	})
	rr := poolReq(t, router.Handler(), http.MethodPost,
		"/v1/admin/pools/p/resume-reconciliation", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		Pool          string `json:"pool"`
		ClearedTuples int    `json:"clearedTuples"`
		WasStuck      bool   `json:"wasStuck"`
		Async         bool   `json:"async"`
		ResumeEpoch   int64  `json:"resumeEpoch"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Async || body.ResumeEpoch != 1 {
		t.Errorf("body = %+v, want async=true resumeEpoch=1", body)
	}
	// The bump is durable: re-reading the pool reflects the new epoch.
	got, err := pools.Get(context.Background(), "p")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ReconciliationResumeEpoch != 1 {
		t.Errorf("stored resume epoch = %d, want 1", got.ReconciliationResumeEpoch)
	}
	if len(audit.snapshot()) != 1 {
		t.Errorf("audit: %+v", audit.snapshot())
	}
}

// spec: §4.6.2 item 3 condition (c) — the durable path 404s for an
// unknown pool without recording a phantom resume.
func TestResumeReconciliationDurableUnknownPool_spec_4_6_2(t *testing.T) {
	router, _, _, _ := newPoolAdmin(t)
	rr := poolReq(t, router.Handler(), http.MethodPost,
		"/v1/admin/pools/ghost/resume-reconciliation", nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d, want 404 for unknown pool", rr.Code)
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

// TestCreatePoolDNSPolicyRoundTrip exercises the §13.2 dnsPolicy admin
// round-trip: a standard pool that sets cluster-default persists the
// opt-out and surfaces it on GET, while an omitted dnsPolicy stays empty
// (the pool keeps the dedicated CoreDNS instance).
// spec: 13.2 (per-pool DNS opt-out)
func TestCreatePoolDNSPolicyRoundTrip(t *testing.T) {
	router, store, runtimes, _ := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})

	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name: "p-optout", RuntimeRef: "echo", IsolationProfile: "standard",
		AllowStandardIsolation: true, DNSPolicy: "cluster-default",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	if row, _ := store.Get(context.Background(), "p-optout"); row.DNSPolicy != "cluster-default" {
		t.Errorf("stored dnsPolicy = %q, want cluster-default", row.DNSPolicy)
	}
	rr = poolReq(t, router.Handler(), http.MethodGet, "/v1/admin/pools/p-optout", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET status: %d, body=%s", rr.Code, rr.Body.String())
	}
	var got admin.PoolPayload
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode GET: %v", err)
	}
	if got.DNSPolicy != "cluster-default" {
		t.Errorf("GET dnsPolicy = %q, want cluster-default", got.DNSPolicy)
	}

	rr = poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name: "p-dedicated", RuntimeRef: "echo", IsolationProfile: "sandboxed",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	if row, _ := store.Get(context.Background(), "p-dedicated"); row.DNSPolicy != "" {
		t.Errorf("omitted dnsPolicy = %q, want empty (dedicated instance)", row.DNSPolicy)
	}
}

// TestCreatePoolRejectsDNSOptOutOnNonStandard surfaces the §13.2
// cross-control through the admin API as a 400 VALIDATION_ERROR: only a
// standard (runc) pool may opt out of the dedicated CoreDNS instance.
// spec: 13.2 (per-pool DNS opt-out)
func TestCreatePoolRejectsDNSOptOutOnNonStandard(t *testing.T) {
	router, _, runtimes, _ := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})
	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name: "sandboxed-optout", RuntimeRef: "echo", IsolationProfile: "sandboxed",
		DNSPolicy: "cluster-default",
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
}

// TestCreatePoolRejectsUnknownDNSPolicy fails closed on a mistyped
// dnsPolicy through the admin API.
// spec: 13.2 (per-pool DNS opt-out)
func TestCreatePoolRejectsUnknownDNSPolicy(t *testing.T) {
	router, _, runtimes, _ := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})
	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name: "bad-dns", RuntimeRef: "echo", IsolationProfile: "standard",
		AllowStandardIsolation: true, DNSPolicy: "kube-system",
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

// TestCreatePoolRejectsCrossTenantReuseOnT4Runtime_spec_5_2_396 verifies
// the admin pool create handler rejects allowCrossTenantReuse: true when
// the referenced runtime is workspaceTier T4, before the pool is stored.
//
// spec: §5.2 line 396.
func TestCreatePoolRejectsCrossTenantReuseOnT4Runtime_spec_5_2_396(t *testing.T) {
	router, store, runtimes, _ := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "phi-agent", WorkspaceTier: runtimestore.WorkspaceTierT4,
	})

	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:             "t4-pool",
		RuntimeRef:       "phi-agent",
		ExecutionMode:    "session",
		IsolationProfile: "microvm",
		SessionPolicy: &runtimestore.SessionPolicy{
			Recycle: &runtimestore.RecyclePolicy{
				Enabled:                    true,
				AcknowledgeBestEffortScrub: true,
				MaxSessionsPerPod:          10,
				AllowCrossTenantReuse:      true,
				// Well-formed for every gate except the T4-tier rule, so the
				// rejection is attributable to the T4 prohibition alone.
				ScrubProfile: runtimestore.MicrovmScrubVMRestart,
			},
		},
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte("T4-tier pools")) {
		t.Errorf("body does not name the §5.2 T4 rule: %s", rr.Body.String())
	}
	if _, err := store.Get(context.Background(), "t4-pool"); err == nil {
		t.Error("rejected pool was stored; the tier check must run before Create")
	}
}

// TestCreatePoolAllowsCrossTenantReuseOnT3Runtime_spec_5_2_396 verifies a
// cross-tenant-reuse pool backed by a T3 (default) runtime is admitted —
// the prohibition is T4-specific.
//
// spec: §5.2 line 396.
func TestCreatePoolAllowsCrossTenantReuseOnT3Runtime_spec_5_2_396(t *testing.T) {
	router, store, runtimes, _ := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "general-agent", WorkspaceTier: runtimestore.WorkspaceTierT3,
	})

	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:             "reuse-pool",
		RuntimeRef:       "general-agent",
		IsolationProfile: "microvm",
		ExecutionMode:    "session",
		SessionPolicy: &runtimestore.SessionPolicy{
			Recycle: &runtimestore.RecyclePolicy{
				Enabled:                    true,
				AcknowledgeBestEffortScrub: true,
				MaxSessionsPerPod:          10,
				AllowCrossTenantReuse:      true,
				// §5.2: a cross-tenant-reuse microvm pool must carry vm-restart
				// or in-place; the standard in-guest scrub is rejected.
				ScrubProfile: runtimestore.MicrovmScrubVMRestart,
			},
		},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	if _, err := store.Get(context.Background(), "reuse-pool"); err != nil {
		t.Errorf("valid pool not stored: %v", err)
	}
}

// TestUpdatePoolRejectsEnablingCrossTenantReuseOnT4Runtime_spec_5_2_396
// verifies a PUT that newly enables cross-tenant reuse on a T4-runtime
// pool is rejected even though the pool was created without it.
//
// spec: §5.2 line 396.
func TestUpdatePoolRejectsEnablingCrossTenantReuseOnT4Runtime_spec_5_2_396(t *testing.T) {
	router, _, runtimes, _ := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "phi-agent", WorkspaceTier: runtimestore.WorkspaceTierT4,
	})
	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name: "t4-pool", RuntimeRef: "phi-agent", ExecutionMode: "session",
		IsolationProfile: "microvm",
		SessionPolicy: &runtimestore.SessionPolicy{
			Recycle: &runtimestore.RecyclePolicy{
				Enabled:                    true,
				AcknowledgeBestEffortScrub: true,
				MaxSessionsPerPod:          5,
			},
		},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: got %d, body=%s", rr.Code, rr.Body.String())
	}

	// PUT newly enables cross-tenant reuse on the recycle block, which the
	// T4 prohibition rejects on the effective post-update policy.
	rr = poolReq(t, router.Handler(), http.MethodPut, "/v1/admin/pools/t4-pool",
		admin.UpdatePoolRequest{SessionPolicy: &runtimestore.SessionPolicy{
			Recycle: &runtimestore.RecyclePolicy{
				Enabled:                    true,
				AcknowledgeBestEffortScrub: true,
				MaxSessionsPerPod:          5,
				AllowCrossTenantReuse:      true,
				ScrubProfile:               runtimestore.MicrovmScrubVMRestart,
			},
		}})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("update status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte("T4-tier pools")) {
		t.Errorf("body does not name the §5.2 T4 rule: %s", rr.Body.String())
	}
}

// fakeCRDGeneration stubs admin.CRDGenerationReader so the sync-status
// tests exercise the §4.6.2 comparison without a Kubernetes client.
type fakeCRDGeneration struct {
	generation int64
	lastAt     time.Time
	ok         bool
	err        error
}

func (f fakeCRDGeneration) CRDGeneration(_ context.Context, _ string) (int64, time.Time, bool, error) {
	return f.generation, f.lastAt, f.ok, f.err
}

// TestSyncStatusEndpointReportsSyncedWhenGenerationsMatch_Spec4_6_2_560
// covers the §4.6.2 line 560 sync-status GET in the synced case.
func TestSyncStatusEndpointReportsSyncedWhenGenerationsMatch_Spec4_6_2_560(t *testing.T) {
	router, pools, runtimes, _ := newPoolAdmin(t)
	seedGetPool(t, pools, runtimes)
	// Bump Postgres generation to a known value via an Update.
	_, _ = pools.Update(context.Background(), "p1", func(p *poolstore.Pool) error {
		p.WarmCount = 3
		return nil
	})
	row, _ := pools.Get(context.Background(), "p1")
	last := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(-5 * time.Second)
	router = router.WithCRDGenerationReader(fakeCRDGeneration{
		generation: row.Generation,
		lastAt:     last,
		ok:         true,
	})

	rr := poolReq(t, router.Handler(), http.MethodGet, "/v1/admin/pools/p1/sync-status", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	var got admin.PoolSyncStatus
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Pool != "p1" || got.PostgresGeneration != row.Generation {
		t.Errorf("payload = %+v", got)
	}
	if got.CRDGeneration != row.Generation || !got.InSync {
		t.Errorf("expected inSync, got = %+v", got)
	}
	if got.LagSeconds < 4 || got.LagSeconds > 6 {
		t.Errorf("lagSeconds = %d, want ~5", got.LagSeconds)
	}
}

// TestSyncStatusEndpointPendingWhenGenerationsDiverge_Spec4_6_2_560
// covers the §4.6.2 mismatch case: Postgres has advanced, the CRD has
// not, inSync is false.
func TestSyncStatusEndpointPendingWhenGenerationsDiverge_Spec4_6_2_560(t *testing.T) {
	router, pools, runtimes, _ := newPoolAdmin(t)
	seedGetPool(t, pools, runtimes)
	// Two updates bump Postgres generation to 3 (Create=1 + Update*2).
	_, _ = pools.Update(context.Background(), "p1", func(p *poolstore.Pool) error { p.WarmCount = 2; return nil })
	_, _ = pools.Update(context.Background(), "p1", func(p *poolstore.Pool) error { p.WarmCount = 3; return nil })
	router = router.WithCRDGenerationReader(fakeCRDGeneration{
		generation: 1, // CRD lagging at the create-time generation
		lastAt:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(-30 * time.Second),
		ok:         true,
	})
	rr := poolReq(t, router.Handler(), http.MethodGet, "/v1/admin/pools/p1/sync-status", nil)
	var got admin.PoolSyncStatus
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.InSync {
		t.Errorf("inSync = true, want false; payload = %+v", got)
	}
	if got.PostgresGeneration <= got.CRDGeneration {
		t.Errorf("expected postgresGeneration > crdGeneration; got %+v", got)
	}
}

// TestSyncStatusOnGetReportsUnknownWithoutReader_Spec4_6_2_559 confirms
// the GET /v1/admin/pools/{name} response carries syncStatus=unknown
// when no CRD reader is wired (the Postgres-only dev posture).
func TestSyncStatusOnGetReportsUnknownWithoutReader_Spec4_6_2_559(t *testing.T) {
	router, pools, runtimes, _ := newPoolAdmin(t)
	seedGetPool(t, pools, runtimes)
	rr := poolReq(t, router.Handler(), http.MethodGet, "/v1/admin/pools/p1", nil)
	var got admin.PoolPayload
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.SyncStatus != "unknown" {
		t.Errorf("syncStatus = %q, want unknown", got.SyncStatus)
	}
}

// TestSyncStatusOnPUTReportsPendingAfterWrite_Spec4_6_2_559 confirms an
// admin PUT that bumps Postgres generation past the CRD's reports
// syncStatus=pending. spec: spec/04_system-components.md line 559.
func TestSyncStatusOnPUTReportsPendingAfterWrite_Spec4_6_2_559(t *testing.T) {
	router, pools, runtimes, _ := newPoolAdmin(t)
	seedGetPool(t, pools, runtimes)
	// Reader pins the CRD generation at the create-time value (1), so a
	// PUT that bumps Postgres to 2 makes the payload "pending".
	router = router.WithCRDGenerationReader(fakeCRDGeneration{generation: 1, ok: true})
	warm := 4
	rr := poolReq(t, router.Handler(), http.MethodPut, "/v1/admin/pools/p1",
		admin.UpdatePoolRequest{WarmCount: &warm})
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	_ = pools // ensure store-state assertions still possible
	var got admin.PoolPayload
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.SyncStatus != "pending" {
		t.Errorf("syncStatus after PUT = %q, want pending", got.SyncStatus)
	}
}

// TestCreatePoolRoundTripsSessionPolicy_spec_5_2 verifies the §5.2
// session-mode sessionPolicy block flows from PoolPayload → store → GET
// response with every field preserved.
func TestCreatePoolRoundTripsSessionPolicy_spec_5_2(t *testing.T) {
	router, store, runtimes, _ := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "claude-code"})
	mt := 2
	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:             "recycle-pool",
		RuntimeRef:       "claude-code",
		IsolationProfile: "microvm",
		ExecutionMode:    "session",
		SessionPolicy: &runtimestore.SessionPolicy{
			MaxSessionRetries: &mt,
			Recycle: &runtimestore.RecyclePolicy{
				Enabled:                         true,
				AcknowledgeBestEffortScrub:      true,
				AllowCrossTenantReuse:           true,
				ScrubProfile:                    "in-place",
				AcknowledgeMicrovmResidualState: true,
				OnScrubFailure:                  "warn",
				MaxScrubFailures:                3,
				MaxSessionsPerPod:               50,
				MaxPodUptimeSeconds:             86400,
			},
		},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}
	row, err := store.Get(context.Background(), "recycle-pool")
	if err != nil {
		t.Fatalf("store get: %v", err)
	}
	if row.SessionPolicy == nil || row.SessionPolicy.Recycle == nil {
		t.Fatal("session policy not persisted")
	}
	if row.SessionPolicy.Recycle.MaxSessionsPerPod != 50 || !row.SessionPolicy.Recycle.AcknowledgeBestEffortScrub {
		t.Errorf("session policy fields not round-tripped: %+v", row.SessionPolicy.Recycle)
	}

	// GET surfaces the session policy back to the client.
	rr = poolReq(t, router.Handler(), http.MethodGet, "/v1/admin/pools/recycle-pool", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get: %d %s", rr.Code, rr.Body.String())
	}
	var got admin.PoolPayload
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.SessionPolicy == nil || got.SessionPolicy.Recycle == nil {
		t.Fatal("GET response missing sessionPolicy")
	}
	if got.SessionPolicy.Recycle.ScrubProfile != "in-place" {
		t.Errorf("ScrubProfile = %q", got.SessionPolicy.Recycle.ScrubProfile)
	}
	if got.SessionPolicy.MaxSessionRetries == nil || *got.SessionPolicy.MaxSessionRetries != 2 {
		t.Errorf("MaxSessionRetries = %#v", got.SessionPolicy.MaxSessionRetries)
	}
	if !got.SessionPolicy.Recycle.AllowCrossTenantReuse {
		t.Error("recycle.allowCrossTenantReuse not preserved")
	}
}

// TestCreatePoolRejectsRecycleWithoutSessionLimit_spec_5_2 confirms a
// recycle-enabled pool POST without maxSessionsPerPod is rejected with the
// §5.2 message.
func TestCreatePoolRejectsRecycleWithoutSessionLimit_spec_5_2(t *testing.T) {
	router, _, runtimes, _ := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "claude-code"})
	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name: "tp", RuntimeRef: "claude-code", ExecutionMode: "session",
		SessionPolicy: &runtimestore.SessionPolicy{
			Recycle: &runtimestore.RecyclePolicy{Enabled: true, AcknowledgeBestEffortScrub: true},
		},
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte("recycle.maxSessionsPerPod")) {
		t.Errorf("body: %s", rr.Body.String())
	}
}

// TestCreatePoolRejectsSessionPolicyOnServicePool_spec_5_2 confirms a
// service-mode pool that carries a sessionPolicy is rejected — sessionPolicy
// is session-mode-only.
func TestCreatePoolRejectsSessionPolicyOnServicePool_spec_5_2(t *testing.T) {
	router, _, runtimes, _ := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "claude-code"})
	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name: "wrong", RuntimeRef: "claude-code", ExecutionMode: "service", MaxConcurrent: 4,
		SessionPolicy: &runtimestore.SessionPolicy{MaxConcurrentSessions: 1},
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte("sessionPolicy is valid only when executionMode is session")) {
		t.Errorf("body: %s", rr.Body.String())
	}
}

// TestUpdatePoolClearsSessionPolicy_spec_5_2 confirms ClearSessionPolicy on
// a PUT removes the persisted policy block while leaving everything else
// intact.
func TestUpdatePoolClearsSessionPolicy_spec_5_2(t *testing.T) {
	router, store, runtimes, _ := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "claude-code"})
	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name: "p1", RuntimeRef: "claude-code", ExecutionMode: "session",
		SessionPolicy: &runtimestore.SessionPolicy{
			Recycle: &runtimestore.RecyclePolicy{Enabled: true, AcknowledgeBestEffortScrub: true, MaxSessionsPerPod: 5},
		},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}

	rr = poolReq(t, router.Handler(), http.MethodPut, "/v1/admin/pools/p1",
		admin.UpdatePoolRequest{ClearSessionPolicy: true})
	if rr.Code != http.StatusOK {
		t.Fatalf("put: %d %s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(context.Background(), "p1")
	if row.SessionPolicy != nil {
		t.Errorf("SessionPolicy not cleared: %+v", row.SessionPolicy)
	}
}

// TestUpdatePoolMutexClearAndSetSessionPolicy_spec_5_2 confirms a PUT that
// sets both ClearSessionPolicy and SessionPolicy is rejected.
func TestUpdatePoolMutexClearAndSetSessionPolicy_spec_5_2(t *testing.T) {
	router, _, runtimes, _ := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "claude-code"})
	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name: "p1", RuntimeRef: "claude-code", ExecutionMode: "session",
		SessionPolicy: &runtimestore.SessionPolicy{
			Recycle: &runtimestore.RecyclePolicy{Enabled: true, AcknowledgeBestEffortScrub: true, MaxSessionsPerPod: 5},
		},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}
	rr = poolReq(t, router.Handler(), http.MethodPut, "/v1/admin/pools/p1",
		admin.UpdatePoolRequest{
			ClearSessionPolicy: true,
			SessionPolicy: &runtimestore.SessionPolicy{
				Recycle: &runtimestore.RecyclePolicy{Enabled: true, AcknowledgeBestEffortScrub: true, MaxSessionsPerPod: 6},
			},
		})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte("mutually exclusive")) {
		t.Errorf("body: %s", rr.Body.String())
	}
}

// TestPoolUpdateBumpsGeneration_Spec4_6_2_558 confirms every admin
// write bumps the pool_config_generation on the Postgres row.
func TestPoolUpdateBumpsGeneration_Spec4_6_2_558(t *testing.T) {
	_, pools, runtimes, _ := newPoolAdmin(t)
	seedGetPool(t, pools, runtimes)
	row, _ := pools.Get(context.Background(), "p1")
	startGen := row.Generation
	if startGen == 0 {
		t.Errorf("Create did not set Generation; got 0")
	}
	updated, _ := pools.Update(context.Background(), "p1", func(p *poolstore.Pool) error { p.WarmCount = 5; return nil })
	if updated.Generation <= startGen {
		t.Errorf("Update did not advance Generation: %d -> %d", startGen, updated.Generation)
	}
}

// spec: §5.2 — the recycle pod-uptime retirement cap is accepted on the
// admin POST and round-trips into the pool store so the PoolScalingController
// renders it and the claim path drains an over-uptime pod. A PUT updates it.
func TestCreateSessionPoolPersistsRecycleMaxPodUptime_spec_5_2(t *testing.T) {
	router, store, runtimes, _ := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})

	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:             "recycle-pool",
		RuntimeRef:       "echo",
		IsolationProfile: "sandboxed",
		ExecutionMode:    "session",
		SessionPolicy: &runtimestore.SessionPolicy{
			Recycle: &runtimestore.RecyclePolicy{
				Enabled:                    true,
				AcknowledgeBestEffortScrub: true,
				MaxSessionsPerPod:          50,
				MaxPodUptimeSeconds:        3600,
			},
		},
		ResourceClass: "small",
		WarmCount:     1,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status: %d, body=%s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(context.Background(), "recycle-pool")
	if row.SessionPolicy == nil || row.SessionPolicy.Recycle == nil ||
		row.SessionPolicy.Recycle.MaxPodUptimeSeconds != 3600 {
		t.Fatalf("stored recycle.maxPodUptimeSeconds not 3600: %+v", row.SessionPolicy)
	}

	// A PUT updates the cap (If-Match the created pool's version 1).
	put := putPoolRaw(t, router.Handler(), "recycle-pool", `"1"`, admin.UpdatePoolRequest{
		SessionPolicy: &runtimestore.SessionPolicy{
			Recycle: &runtimestore.RecyclePolicy{
				Enabled:                    true,
				AcknowledgeBestEffortScrub: true,
				MaxSessionsPerPod:          50,
				MaxPodUptimeSeconds:        7200,
			},
		},
	})
	if put.Code != http.StatusOK {
		t.Fatalf("update status: %d, body=%s", put.Code, put.Body.String())
	}
	row, _ = store.Get(context.Background(), "recycle-pool")
	if row.SessionPolicy.Recycle.MaxPodUptimeSeconds != 7200 {
		t.Errorf("after PUT recycle.maxPodUptimeSeconds = %d, want 7200", row.SessionPolicy.Recycle.MaxPodUptimeSeconds)
	}
}

// spec: §5.2 — maxConcurrent is service-mode-only; a session-mode pool that
// sets it is rejected by ValidateServiceConfig at admission.
func TestCreateSessionPoolRejectsMaxConcurrent_spec_5_2(t *testing.T) {
	router, _, runtimes, _ := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})

	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:             "bad-pool",
		RuntimeRef:       "echo",
		IsolationProfile: "sandboxed",
		ExecutionMode:    "session",
		MaxConcurrent:    4,
		ResourceClass:    "small",
		WarmCount:        1,
	})
	if rr.Code == http.StatusCreated {
		t.Fatalf("a session-mode pool with maxConcurrent must be rejected; got %d", rr.Code)
	}
}
