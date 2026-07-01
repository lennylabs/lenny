// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationpolicystore"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor/interceptorstore"
)

// spec: §4.8 lines 1034-1040 / §8.3 lines 205-224 (SEC-013) — the admin
// external-interceptor registry CRUD and the fail-policy weakening
// cooldown control. F-4.8.17.

func newInterceptorAdmin(t *testing.T) (*admin.Router, *interceptorstore.Memory, *delegationpolicystore.Memory, *recordingAudit) {
	t.Helper()
	ics := interceptorstore.NewMemory()
	pols := delegationpolicystore.NewMemory()
	audit := &recordingAudit{}
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Audit: audit,
	}).WithInterceptors(ics, 60).WithDelegationPolicies(pols)
	return router, ics, pols, audit
}

func validInterceptorPayload(name string) admin.InterceptorPayload {
	return admin.InterceptorPayload{
		Name:       name,
		Endpoint:   "scanner.acme.svc:9000",
		Priority:   500,
		FailPolicy: "fail-closed",
		TimeoutMs:  500,
		Phases:     []string{"PreDelegation", "PreMessageDelivery"},
	}
}

func TestCreateInterceptor(t *testing.T) {
	router, store, _, _ := newInterceptorAdmin(t)
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/interceptors",
		validInterceptorPayload("pii-scanner"), withAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rr.Code, rr.Body.String())
	}
	row, err := store.Get(context.Background(), "pii-scanner")
	if err != nil {
		t.Fatalf("store missing interceptor: %v", err)
	}
	if row.Priority != 500 || row.Version != 1 || !row.FailOpenTransitionAt.IsZero() {
		t.Errorf("stored = %+v, want priority 500 version 1 no transition", row)
	}
}

// spec: §4.8 line 1020 — external interceptors must register above the
// reserved ceiling (INVALID_INTERCEPTOR_PRIORITY).
func TestCreateInterceptorRejectsReservedPriority_spec_4_8_1020(t *testing.T) {
	router, _, _, _ := newInterceptorAdmin(t)
	body := validInterceptorPayload("low")
	body.Priority = 50
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/interceptors", body, withAdminPrincipal)
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "INVALID_INTERCEPTOR_PRIORITY") {
		t.Fatalf("reserved priority: status %d, body %s", rr.Code, rr.Body.String())
	}
}

// spec: §4.8 line 1023 — the PreAuth phase is built-in only
// (INVALID_INTERCEPTOR_PHASE).
func TestCreateInterceptorRejectsPreAuthPhase_spec_4_8_1023(t *testing.T) {
	router, _, _, _ := newInterceptorAdmin(t)
	body := validInterceptorPayload("preauth")
	body.Phases = []string{"PreAuth"}
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/interceptors", body, withAdminPrincipal)
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "INVALID_INTERCEPTOR_PHASE") {
		t.Fatalf("preauth phase: status %d, body %s", rr.Code, rr.Body.String())
	}
}

func TestCreateInterceptorRejectsDuplicate(t *testing.T) {
	router, _, _, _ := newInterceptorAdmin(t)
	h := router.Handler()
	doAdminReq(t, h, http.MethodPost, "/v1/admin/interceptors", validInterceptorPayload("scan"), withAdminPrincipal)
	rr := doAdminReq(t, h, http.MethodPost, "/v1/admin/interceptors", validInterceptorPayload("scan"), withAdminPrincipal)
	if rr.Code != http.StatusConflict {
		t.Fatalf("duplicate: status %d, want 409; body %s", rr.Code, rr.Body.String())
	}
}

// spec: §8.3 SEC-013 — a request body carrying the server-minted
// transition timestamp or the cooldown duration is rejected as a whole
// with INTERCEPTOR_COOLDOWN_IMMUTABLE.
func TestCreateInterceptorRejectsImmutableCooldownFields_spec_8_3_SEC013(t *testing.T) {
	router, _, _, _ := newInterceptorAdmin(t)
	for _, field := range []string{"transition_ts", "cooldownSeconds", "failOpenTransitionAt", "cooldownSecondsAtTransition"} {
		body := map[string]any{
			"name":       "scan",
			"endpoint":   "scanner.acme.svc:9000",
			"priority":   500,
			"failPolicy": "fail-closed",
			"phases":     []string{"PreDelegation"},
			field:        "2026-01-01T00:00:00Z",
		}
		rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/interceptors", body, withAdminPrincipal)
		if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "INTERCEPTOR_COOLDOWN_IMMUTABLE") {
			t.Fatalf("field %q: status %d, body %s", field, rr.Code, rr.Body.String())
		}
	}
}

// spec: §4.8 line 1034 / §8.3 line 218 — a fail-closed → fail-open
// transition server-mints the cooldown, emits interceptor.fail_policy_weakened
// plus interceptor.weakening_cooldown_active with the affected policy
// count, and arms the cooldown.
func TestUpdateInterceptorWeakeningArmsCooldown_spec_4_8_1034(t *testing.T) {
	router, store, pols, audit := newInterceptorAdmin(t)
	h := router.Handler()
	doAdminReq(t, h, http.MethodPost, "/v1/admin/interceptors", validInterceptorPayload("scan"), withAdminPrincipal)
	// A DelegationPolicy referencing the interceptor — the affected set.
	if err := pols.Create(context.Background(), delegationpolicystore.DelegationPolicy{
		TenantID:      "acme",
		Name:          "p1",
		ContentPolicy: delegationpolicystore.ContentPolicy{InterceptorRef: "scan"},
	}); err != nil {
		t.Fatalf("seed policy: %v", err)
	}

	body := validInterceptorPayload("scan")
	body.FailPolicy = "fail-open"
	rr := doAdminReq(t, h, http.MethodPut, "/v1/admin/interceptors/scan", body, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("weaken: status %d, body %s", rr.Code, rr.Body.String())
	}

	row, _ := store.Get(context.Background(), "scan")
	if row.FailOpenTransitionAt.IsZero() || row.CooldownSecondsAtTransition != 60 {
		t.Fatalf("transition not stamped: %+v", row)
	}

	weakened := findAudit(t, audit, "interceptor.fail_policy_weakened")
	if weakened.Detail["new_fail_policy"] != "fail-open" || weakened.Detail["affected_policy_count"].(int) != 1 {
		t.Errorf("weakened detail = %+v", weakened.Detail)
	}
	names, _ := weakened.Detail["affected_policy_names"].([]string)
	if len(names) != 1 || names[0] != "p1" {
		t.Errorf("affected_policy_names = %v, want [p1]", weakened.Detail["affected_policy_names"])
	}
	// The window-entry event fires once.
	findAudit(t, audit, "interceptor.weakening_cooldown_active")
}

// spec: §4.8 line 1034 — the reverse fail-open → fail-closed transition
// emits interceptor.fail_policy_strengthened and clears the cooldown
// immediately.
func TestUpdateInterceptorStrengtheningClearsCooldown_spec_4_8_1034(t *testing.T) {
	router, store, _, audit := newInterceptorAdmin(t)
	h := router.Handler()
	doAdminReq(t, h, http.MethodPost, "/v1/admin/interceptors", validInterceptorPayload("scan"), withAdminPrincipal)
	weaken := validInterceptorPayload("scan")
	weaken.FailPolicy = "fail-open"
	doAdminReq(t, h, http.MethodPut, "/v1/admin/interceptors/scan", weaken, withAdminPrincipal)

	strengthen := validInterceptorPayload("scan")
	strengthen.FailPolicy = "fail-closed"
	rr := doAdminReq(t, h, http.MethodPut, "/v1/admin/interceptors/scan", strengthen, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("strengthen: status %d, body %s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(context.Background(), "scan")
	if !row.FailOpenTransitionAt.IsZero() || row.CooldownSecondsAtTransition != 0 {
		t.Fatalf("cooldown not cleared: %+v", row)
	}
	findAudit(t, audit, "interceptor.fail_policy_strengthened")
}

// spec: §8.3 rule 6 — an interceptor referenced by an active
// DelegationPolicy cannot be deleted (RESOURCE_HAS_DEPENDENTS).
func TestDeleteInterceptorGuard_spec_8_3_rule6(t *testing.T) {
	router, store, pols, _ := newInterceptorAdmin(t)
	h := router.Handler()
	doAdminReq(t, h, http.MethodPost, "/v1/admin/interceptors", validInterceptorPayload("scan"), withAdminPrincipal)
	if err := pols.Create(context.Background(), delegationpolicystore.DelegationPolicy{
		TenantID:      "acme",
		Name:          "p1",
		ContentPolicy: delegationpolicystore.ContentPolicy{InterceptorRef: "scan"},
	}); err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	rr := doAdminReq(t, h, http.MethodDelete, "/v1/admin/interceptors/scan", nil, withAdminPrincipal)
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "RESOURCE_HAS_DEPENDENTS") {
		t.Fatalf("guarded delete: status %d, body %s", rr.Code, rr.Body.String())
	}
	// Soft-delete the policy → no active dependent → delete now succeeds.
	if err := pols.SoftDelete(context.Background(), "acme", "p1", time.Now()); err != nil {
		t.Fatalf("soft-delete policy: %v", err)
	}
	rr = doAdminReq(t, h, http.MethodDelete, "/v1/admin/interceptors/scan", nil, withAdminPrincipal)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("unreferenced delete: status %d, body %s", rr.Code, rr.Body.String())
	}
	if _, err := store.Get(context.Background(), "scan"); err == nil {
		t.Fatal("interceptor still present after delete")
	}
}

func TestInterceptorRoutesRequirePlatformAdmin(t *testing.T) {
	router, _, _, _ := newInterceptorAdmin(t)
	h := router.Handler()
	cases := []struct {
		method, path string
	}{
		{http.MethodPost, "/v1/admin/interceptors"},
		{http.MethodGet, "/v1/admin/interceptors"},
		{http.MethodGet, "/v1/admin/interceptors/scan"},
		{http.MethodPut, "/v1/admin/interceptors/scan"},
		{http.MethodDelete, "/v1/admin/interceptors/scan"},
	}
	for _, tc := range cases {
		rr := doAdminReq(t, h, tc.method, tc.path, validInterceptorPayload("scan"), withUserPrincipal)
		if rr.Code != http.StatusForbidden {
			t.Errorf("%s %s as user: status %d, want 403", tc.method, tc.path, rr.Code)
		}
	}
}

func TestListAndGetInterceptor(t *testing.T) {
	router, _, _, _ := newInterceptorAdmin(t)
	h := router.Handler()
	doAdminReq(t, h, http.MethodPost, "/v1/admin/interceptors", validInterceptorPayload("scan"), withAdminPrincipal)
	getRR := doAdminReq(t, h, http.MethodGet, "/v1/admin/interceptors/scan", nil, withAdminPrincipal)
	if getRR.Code != http.StatusOK || getRR.Header().Get("ETag") == "" {
		t.Fatalf("get: status %d etag %q", getRR.Code, getRR.Header().Get("ETag"))
	}
	var got admin.InterceptorPayload
	if err := json.Unmarshal(getRR.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Name != "scan" || got.FailPolicy != "fail-closed" {
		t.Errorf("get payload = %+v", got)
	}
	listRR := doAdminReq(t, h, http.MethodGet, "/v1/admin/interceptors", nil, withAdminPrincipal)
	if listRR.Code != http.StatusOK || !strings.Contains(listRR.Body.String(), "scan") {
		t.Fatalf("list: status %d, body %s", listRR.Code, listRR.Body.String())
	}
}
