// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/experimentstore"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// spec: §10.7 admission-time variant-pool isolation-monotonicity check.

// newExperimentIsolationAdmin builds an experiment admin router with
// the runtime and pool stores wired and seeded: the base runtime
// `claude-worker` runs at the `sandboxed` profile, pool `cw-weak` is
// `standard` (weaker), and pool `cw-strong` is `microvm` (stronger).
func newExperimentIsolationAdmin(t *testing.T) *admin.Router {
	t.Helper()
	runtimes := runtimestore.NewMemory()
	if err := runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "claude-worker", IsolationProfile: isolation.ProfileSandboxed,
	}); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	pools := poolstore.NewMemory()
	if err := pools.Create(context.Background(), poolstore.Pool{
		Name: "cw-weak", RuntimeRef: "claude-worker",
		IsolationProfile: isolation.ProfileStandard, AllowStandardIsolation: true,
	}); err != nil {
		t.Fatalf("seed weak pool: %v", err)
	}
	if err := pools.Create(context.Background(), poolstore.Pool{
		Name: "cw-strong", RuntimeRef: "claude-worker",
		IsolationProfile: isolation.ProfileMicrovm,
	}); err != nil {
		t.Fatalf("seed strong pool: %v", err)
	}
	return admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC) },
	}).WithRuntimes(runtimes).WithPools(pools).WithExperiments(experimentstore.NewMemory())
}

func TestCreateExperimentRejectsWeakVariantPool(t *testing.T) {
	router := newExperimentIsolationAdmin(t)
	body := validExperimentPayload("exp_weak")
	body.Variants[0].Pool = "cw-weak" // standard — weaker than the sandboxed base
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/experiments", body, withAdminPrincipal)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("weak variant pool: status %d, want 422; body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "CONFIGURATION_CONFLICT") {
		t.Errorf("rejection should carry CONFIGURATION_CONFLICT: %s", rr.Body.String())
	}
}

func TestCreateExperimentAcceptsStrongerVariantPool(t *testing.T) {
	router := newExperimentIsolationAdmin(t)
	body := validExperimentPayload("exp_strong")
	body.Variants[0].Pool = "cw-strong" // microvm — stronger than the sandboxed base
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/experiments", body, withAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Errorf("stronger variant pool: status %d, want 201; body %s", rr.Code, rr.Body.String())
	}
}

func TestCreateExperimentSkipsUnresolvableVariantPool(t *testing.T) {
	router := newExperimentIsolationAdmin(t)
	body := validExperimentPayload("exp_unres")
	body.Variants[0].Pool = "no-such-pool" // unresolvable — the check skips it
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/experiments", body, withAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Errorf("unresolvable variant pool: status %d, want 201 (check is best-effort)", rr.Code)
	}
}

func TestUpdateExperimentRejectsWeakVariantPool(t *testing.T) {
	router := newExperimentIsolationAdmin(t)
	seed := validExperimentPayload("exp_u")
	seed.Variants[0].Pool = "cw-strong"
	if rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/experiments",
		seed, withAdminPrincipal); rr.Code != http.StatusCreated {
		t.Fatalf("seed experiment: status %d", rr.Code)
	}
	bad := validExperimentPayload("exp_u")
	bad.Variants[0].Pool = "cw-weak"
	rr := doAdminReq(t, router.Handler(), http.MethodPut, "/v1/admin/experiments/exp_u",
		bad, withAdminPrincipal)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("PUT with a weak variant pool: status %d, want 422", rr.Code)
	}
}
