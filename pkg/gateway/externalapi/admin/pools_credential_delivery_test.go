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

	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
)

// newPoolAdminTenancy builds a pool admin Router in the given tenancy mode
// (with devMode false) so the §4.9 layer-1 credential-delivery gate can be
// exercised under multi-tenant enforcement. It mirrors newPoolAdmin but sets
// Options.TenancyMode.
func newPoolAdminTenancy(t *testing.T, tenancyMode string) (*admin.Router, *poolstore.Memory, *runtimestore.Memory, *recordingAudit) {
	t.Helper()
	tenants := tenantstore.NewMemory()
	runtimes := runtimestore.NewMemory()
	pools := poolstore.NewMemory()
	audit := &recordingAudit{}
	router := admin.NewRouter(tenants, admin.Options{
		Clock:       func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Audit:       audit,
		TenancyMode: tenancyMode,
	}).WithRuntimes(runtimes).WithPools(pools)
	return router, pools, runtimes, audit
}

// TestCreatePoolCredentialDeliveryRoundTrip_spec_4_9 verifies the §4.9
// pool-definition credential-delivery fields (deliveryMode, spiffeBinding,
// and the two deployer opt-ins) are copied from the create payload onto the
// stored pool and surfaced back on GET, so one warm-pool admin resource
// holds the whole combination the enforcement layers evaluate.
func TestCreatePoolCredentialDeliveryRoundTrip_spec_4_9(t *testing.T) {
	router, store, runtimes, _ := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})

	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:                                "cred-pool",
		RuntimeRef:                          "echo",
		IsolationProfile:                    "sandboxed",
		ExecutionMode:                       "session",
		DeliveryMode:                        "proxy",
		SpiffeBinding:                       "disabled",
		AllowProxyModeSpiffeBindingDisabled: true,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status: %d body=%s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(context.Background(), "cred-pool")
	if row.DeliveryMode != "proxy" || row.SpiffeBinding != "disabled" ||
		!row.AllowProxyModeSpiffeBindingDisabled || row.AllowDirectModeStandardIsolation {
		t.Fatalf("stored row missing credential-delivery fields: %+v", row)
	}

	g := poolReq(t, router.Handler(), http.MethodGet, "/v1/admin/pools/cred-pool", nil)
	if g.Code != http.StatusOK {
		t.Fatalf("get status: %d body=%s", g.Code, g.Body.String())
	}
	var got admin.PoolPayload
	if err := json.Unmarshal(g.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode GET body: %v", err)
	}
	if got.DeliveryMode != "proxy" || got.SpiffeBinding != "disabled" ||
		!got.AllowProxyModeSpiffeBindingDisabled {
		t.Fatalf("GET payload missing credential-delivery fields: %+v", got)
	}
}

// TestCreatePoolRejectsUnknownDeliveryMode_spec_4_9 verifies a non-empty
// deliveryMode outside {proxy, direct} is rejected with 400 VALIDATION_ERROR
// at registration, before any store write, so an out-of-enum value never
// reconciles onto the enum-constrained SandboxTemplate CRD.
func TestCreatePoolRejectsUnknownDeliveryMode_spec_4_9(t *testing.T) {
	router, store, runtimes, _ := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})

	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:         "bad-delivery",
		RuntimeRef:   "echo",
		DeliveryMode: "PROXY",
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad deliveryMode: got %d body=%s", rr.Code, rr.Body.String())
	}
	if _, err := store.Get(context.Background(), "bad-delivery"); err == nil {
		t.Fatalf("rejected pool must not be stored")
	}
}

// TestCreatePoolRejectsUnknownSpiffeBinding_spec_4_9 verifies a non-empty
// spiffeBinding outside {enabled, disabled} is rejected with 400
// VALIDATION_ERROR at registration.
func TestCreatePoolRejectsUnknownSpiffeBinding_spec_4_9(t *testing.T) {
	router, store, runtimes, _ := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})

	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:          "bad-spiffe",
		RuntimeRef:    "echo",
		SpiffeBinding: "off",
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad spiffeBinding: got %d body=%s", rr.Code, rr.Body.String())
	}
	if _, err := store.Get(context.Background(), "bad-spiffe"); err == nil {
		t.Fatalf("rejected pool must not be stored")
	}
}

// TestUpdatePoolCredentialDeliveryMerge_spec_4_9 verifies a partial PUT
// toggles the §4.9 credential-delivery fields on the stored pool while an
// omitted field is left unchanged.
func TestUpdatePoolCredentialDeliveryMerge_spec_4_9(t *testing.T) {
	router, store, runtimes, _ := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})

	create := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:             "up-pool",
		RuntimeRef:       "echo",
		IsolationProfile: "sandboxed",
		ExecutionMode:    "session",
		DeliveryMode:     "proxy",
		SpiffeBinding:    "enabled",
	})
	if create.Code != http.StatusCreated {
		t.Fatalf("create status: %d body=%s", create.Code, create.Body.String())
	}

	dm := "direct"
	rr := poolReq(t, router.Handler(), http.MethodPut, "/v1/admin/pools/up-pool", admin.UpdatePoolRequest{
		DeliveryMode: &dm,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("update status: %d body=%s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(context.Background(), "up-pool")
	if row.DeliveryMode != "direct" {
		t.Fatalf("deliveryMode not merged: %+v", row)
	}
	if row.SpiffeBinding != "enabled" {
		t.Fatalf("omitted spiffeBinding must be unchanged: %+v", row)
	}
}

// TestUpdatePoolRejectsUnknownDeliveryMode_spec_4_9 verifies the update path
// rejects a non-empty deliveryMode outside {proxy, direct} with 400
// VALIDATION_ERROR, matching the create-side enum clause.
func TestUpdatePoolRejectsUnknownDeliveryMode_spec_4_9(t *testing.T) {
	router, _, runtimes, _ := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})

	create := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:             "u2",
		RuntimeRef:       "echo",
		IsolationProfile: "sandboxed",
		ExecutionMode:    "session",
	})
	if create.Code != http.StatusCreated {
		t.Fatalf("create status: %d body=%s", create.Code, create.Body.String())
	}

	bad := "sideways"
	rr := poolReq(t, router.Handler(), http.MethodPut, "/v1/admin/pools/u2", admin.UpdatePoolRequest{
		DeliveryMode: &bad,
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad deliveryMode update: got %d body=%s", rr.Code, rr.Body.String())
	}
}

// TestUpdatePoolRejectsUnknownSpiffeBinding_spec_4_9 verifies the update path
// rejects a non-empty spiffeBinding outside {enabled, disabled} with 400
// VALIDATION_ERROR.
func TestUpdatePoolRejectsUnknownSpiffeBinding_spec_4_9(t *testing.T) {
	router, _, runtimes, _ := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})

	create := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:             "u3",
		RuntimeRef:       "echo",
		IsolationProfile: "sandboxed",
		ExecutionMode:    "session",
	})
	if create.Code != http.StatusCreated {
		t.Fatalf("create status: %d body=%s", create.Code, create.Body.String())
	}

	bad := "off"
	rr := poolReq(t, router.Handler(), http.MethodPut, "/v1/admin/pools/u3", admin.UpdatePoolRequest{
		SpiffeBinding: &bad,
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad spiffeBinding update: got %d body=%s", rr.Code, rr.Body.String())
	}
}

// TestCreatePoolRejectsDirectStandardMultiTenant_spec_4_9 pins the §4.9
// layer-1 pool-registration gate: in multi-tenant mode a pool that sets
// deliveryMode: direct with isolationProfile: standard is rejected with 422
// carrying the DirectModeStandardIsolationMultiTenantRejected code, before any
// store write, even when both deployer opt-ins are set (the opt-in cannot
// rescue the combination in multi-tenant mode). Before layer 1 existed the
// combination was admitted and stored, reconciling into a SandboxTemplate the
// layer-2 webhook would then reject; this asserts the corrected rejection.
func TestCreatePoolRejectsDirectStandardMultiTenant_spec_4_9(t *testing.T) {
	router, store, runtimes, _ := newPoolAdminTenancy(t, "multi")
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})

	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:                             "ds-pool",
		RuntimeRef:                       "echo",
		IsolationProfile:                 "standard",
		ExecutionMode:                    "session",
		DeliveryMode:                     "direct",
		AllowStandardIsolation:           true,
		AllowDirectModeStandardIsolation: true,
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("direct+standard multi-tenant: got %d body=%s", rr.Code, rr.Body.String())
	}
	if body := rr.Body.String(); !bytesContains(body, "DirectModeStandardIsolationMultiTenantRejected") {
		t.Fatalf("rejection reason missing guard code: %s", body)
	}
	if _, err := store.Get(context.Background(), "ds-pool"); err == nil {
		t.Fatalf("rejected pool must not be stored")
	}
}

// TestCreatePoolRejectsProxySpiffeDisabledMultiTenant_spec_4_9 pins the §4.9
// layer-1 gate for the second combination: deliveryMode: proxy with
// spiffeBinding: disabled is rejected with 422 in multi-tenant mode before
// any store write, even with the opt-in set.
func TestCreatePoolRejectsProxySpiffeDisabledMultiTenant_spec_4_9(t *testing.T) {
	router, store, runtimes, _ := newPoolAdminTenancy(t, "multi")
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})

	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:                                "ps-pool",
		RuntimeRef:                          "echo",
		IsolationProfile:                    "sandboxed",
		ExecutionMode:                       "session",
		DeliveryMode:                        "proxy",
		SpiffeBinding:                       "disabled",
		AllowProxyModeSpiffeBindingDisabled: true,
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("proxy+spiffe-disabled multi-tenant: got %d body=%s", rr.Code, rr.Body.String())
	}
	if body := rr.Body.String(); !bytesContains(body, "ProxyModeSpiffeBindingDisabledMultiTenantRejected") {
		t.Fatalf("rejection reason missing guard code: %s", body)
	}
	if _, err := store.Get(context.Background(), "ps-pool"); err == nil {
		t.Fatalf("rejected pool must not be stored")
	}
}

// TestCreatePoolRejectsProxyProviderDirectAnyTenancy_spec_13_2 pins the §13.2
// NET-006 mutual exclusivity carried through the layer-1 gate: deliveryMode:
// proxy with egressProfile: provider-direct is rejected in every tenancy mode,
// including single-tenant, keeping layer 1 a superset of the layer-2 gate so a
// stored pool never reconciles into a SandboxTemplate the failurePolicy: Fail
// webhook would reject.
func TestCreatePoolRejectsProxyProviderDirectAnyTenancy_spec_13_2(t *testing.T) {
	router, store, runtimes, _ := newPoolAdminTenancy(t, "single")
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})

	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:             "pd-pool",
		RuntimeRef:       "echo",
		IsolationProfile: "sandboxed",
		ExecutionMode:    "session",
		DeliveryMode:     "proxy",
		EgressProfile:    "provider-direct",
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("proxy+provider-direct single-tenant: got %d body=%s", rr.Code, rr.Body.String())
	}
	if body := rr.Body.String(); !bytesContains(body, "InvalidPoolEgressDeliveryCombo") {
		t.Fatalf("rejection reason missing NET-006 guard code: %s", body)
	}
	if _, err := store.Get(context.Background(), "pd-pool"); err == nil {
		t.Fatalf("rejected pool must not be stored")
	}
}

// TestUpdatePoolRejectsPartialPutFormingForbiddenCombo_spec_4_9 pins that the
// layer-1 gate evaluates the MERGED effective pool on the update path, so a
// partial PUT that sets only one half of a forbidden combination cannot bypass
// the gate. A stored proxy + spiffeBinding: enabled pool, PUT with only
// spiffeBinding: disabled, is rejected with 422 and the stored row is
// unchanged.
func TestUpdatePoolRejectsPartialPutFormingForbiddenCombo_spec_4_9(t *testing.T) {
	router, store, runtimes, _ := newPoolAdminTenancy(t, "multi")
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})

	create := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:             "merge-pool",
		RuntimeRef:       "echo",
		IsolationProfile: "sandboxed",
		ExecutionMode:    "session",
		DeliveryMode:     "proxy",
		SpiffeBinding:    "enabled",
	})
	if create.Code != http.StatusCreated {
		t.Fatalf("create status: %d body=%s", create.Code, create.Body.String())
	}

	disabled := "disabled"
	rr := poolReq(t, router.Handler(), http.MethodPut, "/v1/admin/pools/merge-pool", admin.UpdatePoolRequest{
		SpiffeBinding: &disabled,
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("partial PUT forming forbidden combo: got %d body=%s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(context.Background(), "merge-pool")
	if row.SpiffeBinding != "enabled" {
		t.Fatalf("rejected update must not persist: spiffeBinding=%q", row.SpiffeBinding)
	}
}

// TestCreatePoolDryRunRejectsForbiddenComboMultiTenant_spec_4_9 pins that the
// dry-run create preview runs the same layer-1 gate, so a forbidden
// combination is rejected before it can be stored on a follow-up real create.
func TestCreatePoolDryRunRejectsForbiddenComboMultiTenant_spec_4_9(t *testing.T) {
	router, _, runtimes, _ := newPoolAdminTenancy(t, "multi")
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})

	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools?dryRun=true", admin.PoolPayload{
		Name:             "dry-pool",
		RuntimeRef:       "echo",
		IsolationProfile: "sandboxed",
		ExecutionMode:    "session",
		DeliveryMode:     "proxy",
		SpiffeBinding:    "disabled",
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("dry-run forbidden combo: got %d body=%s", rr.Code, rr.Body.String())
	}
}

// TestCreatePoolEmitsProxySpiffeDisabledWarning_spec_4_9_2 pins the §4.9.2
// credential.proxy_mode_spiffe_binding_disabled audit event: a single-tenant
// pool admitted with deliveryMode: proxy + spiffeBinding: disabled (permitted
// outside multi-tenant mode) emits the credential-stream warning so auditors
// see the disablement.
func TestCreatePoolEmitsProxySpiffeDisabledWarning_spec_4_9_2(t *testing.T) {
	router, _, runtimes, audit := newPoolAdminTenancy(t, "single")
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})

	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:                                "warn-pool",
		RuntimeRef:                          "echo",
		IsolationProfile:                    "sandboxed",
		ExecutionMode:                       "session",
		DeliveryMode:                        "proxy",
		SpiffeBinding:                       "disabled",
		AllowProxyModeSpiffeBindingDisabled: true,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("single-tenant proxy+spiffe-disabled: got %d body=%s", rr.Code, rr.Body.String())
	}
	var warned bool
	for _, e := range audit.snapshot() {
		if e.Type == "credential.proxy_mode_spiffe_binding_disabled" {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("expected credential.proxy_mode_spiffe_binding_disabled event, got %+v", audit.snapshot())
	}
}

// TestCreatePoolAllowsProxySpiffeDisabledSingleTenant_spec_4_9 pins that the
// gate is tenancy-gated: the same proxy + spiffeBinding: disabled pool the
// multi-tenant gate rejects is admitted in single-tenant mode, so the control
// does not over-block permitted single-tenant deployments.
func TestCreatePoolAllowsProxySpiffeDisabledSingleTenant_spec_4_9(t *testing.T) {
	router, store, runtimes, _ := newPoolAdminTenancy(t, "single")
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})

	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:                                "ok-pool",
		RuntimeRef:                          "echo",
		IsolationProfile:                    "sandboxed",
		ExecutionMode:                       "session",
		DeliveryMode:                        "proxy",
		SpiffeBinding:                       "disabled",
		AllowProxyModeSpiffeBindingDisabled: true,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("single-tenant admit: got %d body=%s", rr.Code, rr.Body.String())
	}
	if _, err := store.Get(context.Background(), "ok-pool"); err != nil {
		t.Fatalf("admitted pool must be stored: %v", err)
	}
}

// TestBootstrapSeedRejectsForbiddenComboMultiTenant_spec_4_9 pins the §17.6
// seed path layer-1 gate: a bootstrap.pools entry carrying a forbidden
// combination in multi-tenant mode is recorded as a per-entry SEED_VALIDATION
// error and is not stored, so the seed path never reconciles a SandboxTemplate
// the layer-2 webhook would reject.
func TestBootstrapSeedRejectsForbiddenComboMultiTenant_spec_4_9(t *testing.T) {
	tenants := tenantstore.NewMemory()
	runtimes := runtimestore.NewMemory()
	pools := poolstore.NewMemory()
	router := admin.NewRouter(tenants, admin.Options{
		Clock:       func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		TenancyMode: "multi",
	}).WithRuntimes(runtimes).WithPools(pools)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})

	req := admin.BootstrapRequest{
		Pools: []admin.PoolPayload{{
			Name:             "seed-pool",
			RuntimeRef:       "echo",
			IsolationProfile: "standard",
			DeliveryMode:     "direct",
			WarmCount:        1,
		}},
	}
	buf, _ := json.Marshal(req)
	httpReq := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/bootstrap", bytes.NewReader(buf)))
	rec := httptest.NewRecorder()
	router.Handler().ServeHTTP(rec, httpReq)
	var resp admin.BootstrapResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)

	if len(resp.Pools.Errors) != 1 || resp.Pools.Errors[0].Code != "SEED_VALIDATION" {
		t.Fatalf("expected one SEED_VALIDATION error, got %+v", resp.Pools.Errors)
	}
	if _, err := pools.Get(context.Background(), "seed-pool"); err == nil {
		t.Fatalf("forbidden seed pool must not be stored")
	}
}

func bytesContains(haystack, needle string) bool {
	return bytes.Contains([]byte(haystack), []byte(needle))
}
