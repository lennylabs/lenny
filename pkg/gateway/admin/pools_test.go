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
