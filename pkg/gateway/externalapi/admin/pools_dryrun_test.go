// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
)

// spec: §15.1 line 1140 — ?dryRun=true validates without persisting or
// auditing, returns the computed resource, and sets X-Dry-Run: true.

func TestCreatePoolDryRun_spec_15_1(t *testing.T) {
	router, store, runtimes, audit := newPoolAdmin(t)
	if err := runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"}); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools?dryRun=true", admin.PoolPayload{
		Name:             "default-pool",
		RuntimeRef:       "echo",
		IsolationProfile: "sandboxed",
		ExecutionMode:    "session",
		ResourceClass:    "small",
		WarmCount:        3,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("X-Dry-Run"); got != "true" {
		t.Errorf("X-Dry-Run = %q, want true", got)
	}
	var resp admin.PoolPayload
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Name != "default-pool" || resp.WarmCount != 3 {
		t.Errorf("computed resource: %+v", resp)
	}
	// No persistence: the pool was not created.
	if _, err := store.Get(context.Background(), "default-pool"); err == nil {
		t.Error("dry-run create must not persist the pool")
	}
	if len(audit.snapshot()) != 0 {
		t.Errorf("dry-run must not emit audit events: %+v", audit.snapshot())
	}
}

func TestUpdatePoolDryRun_spec_15_1(t *testing.T) {
	router, store, runtimes, audit := newPoolAdmin(t)
	if err := runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"}); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	if err := store.Create(context.Background(), poolstore.Pool{
		Name: "p", RuntimeRef: "echo", WarmCount: 1,
	}); err != nil {
		t.Fatalf("seed pool: %v", err)
	}
	newWarm := 9
	// poolReq fetches and sets the current ETag as If-Match so the
	// precondition passes ahead of the dry-run branch.
	rr := poolReq(t, router.Handler(), http.MethodPut, "/v1/admin/pools/p?dryRun=true",
		admin.UpdatePoolRequest{WarmCount: &newWarm})
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("X-Dry-Run"); got != "true" {
		t.Errorf("X-Dry-Run = %q, want true", got)
	}
	var resp admin.PoolPayload
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.WarmCount != 9 {
		t.Errorf("preview warmCount = %d, want the merged value 9", resp.WarmCount)
	}
	// No persistence: the stored pool is unchanged.
	row, _ := store.Get(context.Background(), "p")
	if row.WarmCount != 1 {
		t.Errorf("dry-run update must not persist: stored warmCount = %d", row.WarmCount)
	}
	if len(audit.snapshot()) != 0 {
		t.Errorf("dry-run must not emit audit events: %+v", audit.snapshot())
	}
}

// spec: §15.1 — dryRun=true combined with a stale If-Match still returns
// 412 ETAG_MISMATCH: the precondition runs before the dry-run branch.
func TestUpdatePoolDryRunStaleIfMatch_spec_15_1(t *testing.T) {
	router, store, runtimes, audit := newPoolAdmin(t)
	if err := runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"}); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	if err := store.Create(context.Background(), poolstore.Pool{
		Name: "p", RuntimeRef: "echo", WarmCount: 1,
	}); err != nil {
		t.Fatalf("seed pool: %v", err)
	}
	newWarm := 9
	b, _ := json.Marshal(admin.UpdatePoolRequest{WarmCount: &newWarm})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPut,
		"/v1/admin/pools/p?dryRun=true", bytes.NewReader(b)))
	req.Header.Set("If-Match", `"999"`) // stale generation
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusPreconditionFailed {
		t.Fatalf("dryRun=true with stale If-Match: status %d, want 412; body=%s", rr.Code, rr.Body.String())
	}
	if len(audit.snapshot()) != 0 {
		t.Errorf("a 412 must not emit audit events: %+v", audit.snapshot())
	}
}

// Validation still runs under dryRun: an invalid isolation profile
// returns 400.
func TestCreatePoolDryRunValidates_spec_15_1(t *testing.T) {
	router, _, _, _ := newPoolAdmin(t)
	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools?dryRun=true", admin.PoolPayload{
		Name:             "p",
		IsolationProfile: "ferrous", // not a recognised profile
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("dry-run with an invalid body: status %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}
