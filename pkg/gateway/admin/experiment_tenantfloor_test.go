// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/experimentstore"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// spec: §10.7 admission-time tenant-floor advisory check.

// newExperimentTenantFloorAdmin builds an experiment admin router with
// tenant `acme` carrying the given minIsolationProfile floor, a
// `sandboxed` base runtime, and pools at `sandboxed` and `microvm`.
func newExperimentTenantFloorAdmin(t *testing.T, tenantFloor string) (*admin.Router, *recordingAudit) {
	t.Helper()
	tenants := tenantstore.NewMemory()
	if err := tenants.Create(context.Background(), tenantstore.Tenant{
		ID: "acme", MinIsolationProfile: tenantFloor,
	}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	runtimes := runtimestore.NewMemory()
	if err := runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "claude-worker", IsolationProfile: isolation.ProfileSandboxed,
	}); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	pools := poolstore.NewMemory()
	if err := pools.Create(context.Background(), poolstore.Pool{
		Name: "cw-sandboxed", RuntimeRef: "claude-worker", IsolationProfile: isolation.ProfileSandboxed,
	}); err != nil {
		t.Fatalf("seed sandboxed pool: %v", err)
	}
	if err := pools.Create(context.Background(), poolstore.Pool{
		Name: "cw-microvm", RuntimeRef: "claude-worker", IsolationProfile: isolation.ProfileMicrovm,
	}); err != nil {
		t.Fatalf("seed microvm pool: %v", err)
	}
	audit := &recordingAudit{}
	router := admin.NewRouter(tenants, admin.Options{
		Clock: func() time.Time { return time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC) },
		Audit: audit,
	}).WithRuntimes(runtimes).WithPools(pools).WithExperiments(experimentstore.NewMemory())
	return router, audit
}

func emittedTenantFloorAdvisory(audit *recordingAudit) bool {
	for _, ev := range audit.snapshot() {
		if ev.Type == "experiment.variant_weaker_than_tenant_floor" {
			return true
		}
	}
	return false
}

func TestCreateExperimentEmitsTenantFloorAdvisory(t *testing.T) {
	router, audit := newExperimentTenantFloorAdmin(t, "microvm")
	body := validExperimentPayload("exp_tf")
	// cw-sandboxed meets the sandboxed base runtime (the hard check
	// passes) but is below the microvm tenant floor.
	body.Variants[0].Pool = "cw-sandboxed"
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/experiments", body, withAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, want 201 (the tenant-floor check is advisory); body %s",
			rr.Code, rr.Body.String())
	}
	if !emittedTenantFloorAdvisory(audit) {
		t.Error("a variant pool below the tenant floor must emit experiment.variant_weaker_than_tenant_floor")
	}
}

func TestCreateExperimentNoAdvisoryWhenAtTenantFloor(t *testing.T) {
	router, audit := newExperimentTenantFloorAdmin(t, "microvm")
	body := validExperimentPayload("exp_ok")
	body.Variants[0].Pool = "cw-microvm" // meets the microvm tenant floor
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/experiments", body, withAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d", rr.Code)
	}
	if emittedTenantFloorAdvisory(audit) {
		t.Error("a variant pool meeting the tenant floor must not emit the advisory event")
	}
}

func TestCreateExperimentNoAdvisoryWithoutTenantFloor(t *testing.T) {
	// A tenant with no minIsolationProfile floor configured.
	router, audit := newExperimentTenantFloorAdmin(t, "")
	body := validExperimentPayload("exp_nofloor")
	body.Variants[0].Pool = "cw-sandboxed"
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/experiments", body, withAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d", rr.Code)
	}
	if emittedTenantFloorAdvisory(audit) {
		t.Error("no tenant floor configured — the advisory event must not fire")
	}
}
