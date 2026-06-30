// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
)

func bip(v int) *int { return &v }

// fakeBootstrapStatus is a §17.8.2 step-3 PoolBootstrapStatusReader that
// returns fixed cold-start signals.
type fakeBootstrapStatus struct {
	hours float64
	mode  string
}

func (f fakeBootstrapStatus) PoolBootstrapStatus(context.Context, string) (float64, string, bool, error) {
	return f.hours, f.mode, true, nil
}

func createBootstrapPool(t *testing.T, router *admin.Router, runtimes *runtimestore.Memory) {
	t.Helper()
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})
	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:             "p",
		RuntimeRef:       "echo",
		IsolationProfile: "sandboxed",
		ExecutionMode:    "session",
		ResourceClass:    "small",
		WarmCount:        3,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create pool: %d %s", rr.Code, rr.Body.String())
	}
}

func getPoolPayload(t *testing.T, router *admin.Router) admin.PoolPayload {
	t.Helper()
	rr := poolReq(t, router.Handler(), http.MethodGet, "/v1/admin/pools/p", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get pool: %d %s", rr.Code, rr.Body.String())
	}
	var p admin.PoolPayload
	if err := json.Unmarshal(rr.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode pool: %v", err)
	}
	return p
}

// spec: §17.8.2 step 3 — PUT {bootstrapMinWarm: N} sets the override and
// GET surfaces the bootstrapStatus object.
func TestPutBootstrapMinWarmAndGetStatus_spec_17_8_2(t *testing.T) {
	router, store, runtimes, _ := newPoolAdmin(t)
	createBootstrapPool(t, router, runtimes)

	rr := poolReq(t, router.Handler(), http.MethodPut, "/v1/admin/pools/p",
		admin.UpdatePoolRequest{BootstrapMinWarm: bip(2096)})
	if rr.Code != http.StatusOK {
		t.Fatalf("put bootstrapMinWarm: %d %s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(context.Background(), "p")
	if row.BootstrapMinWarm == nil || *row.BootstrapMinWarm != 2096 {
		t.Fatalf("stored override = %v, want 2096", row.BootstrapMinWarm)
	}

	p := getPoolPayload(t, router)
	if p.BootstrapStatus == nil {
		t.Fatalf("GET omitted bootstrapStatus for an override pool")
	}
	if !p.BootstrapStatus.Active || p.BootstrapStatus.BootstrapMinWarm != 2096 {
		t.Errorf("bootstrapStatus = %+v, want active with override 2096", *p.BootstrapStatus)
	}
}

// spec: §17.8.2 step 3 — a wired PoolBootstrapStatusReader populates
// hoursOfData and estimatedConvergenceAt; a converged pool reports
// active:false.
func TestGetBootstrapStatusWithReader_spec_17_8_2(t *testing.T) {
	router, _, runtimes, _ := newPoolAdmin(t)
	createBootstrapPool(t, router, runtimes)
	router.WithPoolBootstrapStatusReader(fakeBootstrapStatus{hours: 12, mode: "bootstrap"})
	rr := poolReq(t, router.Handler(), http.MethodPut, "/v1/admin/pools/p",
		admin.UpdatePoolRequest{BootstrapMinWarm: bip(50)})
	if rr.Code != http.StatusOK {
		t.Fatalf("put: %d %s", rr.Code, rr.Body.String())
	}
	p := getPoolPayload(t, router)
	if p.BootstrapStatus == nil || p.BootstrapStatus.HoursOfData != 12 {
		t.Fatalf("bootstrapStatus = %+v, want hoursOfData 12", p.BootstrapStatus)
	}
	if !p.BootstrapStatus.Active || p.BootstrapStatus.EstimatedConvergenceAt == "" {
		t.Errorf("active bootstrap pool must project estimatedConvergenceAt: %+v", *p.BootstrapStatus)
	}

	// A converged pool (scalingMode: formula) reports active:false.
	router.WithPoolBootstrapStatusReader(fakeBootstrapStatus{hours: 72, mode: "formula"})
	p = getPoolPayload(t, router)
	if p.BootstrapStatus.Active {
		t.Errorf("converged pool must report active:false: %+v", *p.BootstrapStatus)
	}
}

// spec: §17.8.2 step 3 — DELETE /bootstrap-override clears the override
// and GET no longer carries a bootstrapStatus object.
func TestDeleteBootstrapOverride_spec_17_8_2(t *testing.T) {
	router, store, runtimes, audit := newPoolAdmin(t)
	createBootstrapPool(t, router, runtimes)
	if rr := poolReq(t, router.Handler(), http.MethodPut, "/v1/admin/pools/p",
		admin.UpdatePoolRequest{BootstrapMinWarm: bip(50)}); rr.Code != http.StatusOK {
		t.Fatalf("put: %d %s", rr.Code, rr.Body.String())
	}
	before := len(audit.snapshot())

	rr := poolReq(t, router.Handler(), http.MethodDelete, "/v1/admin/pools/p/bootstrap-override", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("delete override: %d %s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(context.Background(), "p")
	if row.BootstrapMinWarm != nil {
		t.Fatalf("override not cleared: %v", *row.BootstrapMinWarm)
	}
	if got := getPoolPayload(t, router); got.BootstrapStatus != nil {
		t.Errorf("GET still carries bootstrapStatus after clear: %+v", *got.BootstrapStatus)
	}
	if len(audit.snapshot()) <= before {
		t.Errorf("DELETE bootstrap-override must emit an audit event")
	}
}

// The DELETE is idempotent: clearing an absent override is a 200 no-op.
func TestDeleteBootstrapOverrideIdempotent_spec_17_8_2(t *testing.T) {
	router, _, runtimes, _ := newPoolAdmin(t)
	createBootstrapPool(t, router, runtimes)
	rr := poolReq(t, router.Handler(), http.MethodDelete, "/v1/admin/pools/p/bootstrap-override", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("idempotent delete: %d %s", rr.Code, rr.Body.String())
	}
}

// A DELETE on a missing pool reads as 404.
func TestDeleteBootstrapOverrideMissingPool_spec_17_8_2(t *testing.T) {
	router, _, _, _ := newPoolAdmin(t)
	rr := poolReq(t, router.Handler(), http.MethodDelete, "/v1/admin/pools/nope/bootstrap-override", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("missing pool delete: %d, want 404", rr.Code)
	}
}
