// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/delegationpolicystore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// spec: §8.3 / §15.1 admin DelegationPolicy CRUD.

func newDelegationPolicyAdmin(t *testing.T) (*admin.Router, *delegationpolicystore.Memory) {
	t.Helper()
	store := delegationpolicystore.NewMemory()
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithDelegationPolicies(store)
	return router, store
}

func validDelegationPolicy(name string) admin.DelegationPolicyPayload {
	return admin.DelegationPolicyPayload{
		Name: name,
		Rules: []admin.DelegationRulePayload{
			{
				Target: admin.DelegationTargetPayload{
					MatchLabels: map[string]string{"team": "platform"},
					Types:       []string{"agent"},
				},
				Allow: true,
			},
		},
		ContentPolicy: admin.ContentPolicyPayload{MaxInputSize: 131072},
	}
}

func TestCreateDelegationPolicy(t *testing.T) {
	router, store := newDelegationPolicyAdmin(t)
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/delegation-policies",
		validDelegationPolicy("orchestrator-policy"), withAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rr.Code, rr.Body.String())
	}
	var resp admin.DelegationPolicyPayload
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Name != "orchestrator-policy" || len(resp.Rules) != 1 {
		t.Errorf("response = %+v, want orchestrator-policy with 1 rule", resp)
	}
	row, err := store.Get(context.Background(), "platform", "orchestrator-policy")
	if err != nil {
		t.Fatalf("store missing policy: %v", err)
	}
	if len(row.Rules) != 1 || !row.Rules[0].Allow ||
		row.Rules[0].Target.MatchLabels["team"] != "platform" {
		t.Errorf("stored rules = %+v", row.Rules)
	}
}

func TestCreateDelegationPolicyAppliesContentDefaults(t *testing.T) {
	router, store := newDelegationPolicyAdmin(t)
	// A payload that omits the contentPolicy sizes.
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/delegation-policies",
		admin.DelegationPolicyPayload{Name: "p1"}, withAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rr.Code, rr.Body.String())
	}
	row, err := store.Get(context.Background(), "platform", "p1")
	if err != nil {
		t.Fatalf("store missing policy: %v", err)
	}
	if row.ContentPolicy.MaxInputSize != delegationpolicystore.DefaultMaxInputSize {
		t.Errorf("stored MaxInputSize = %d, want the §8.3 default", row.ContentPolicy.MaxInputSize)
	}
	if row.ContentPolicy.MaxExportedFileSize != delegationpolicystore.DefaultMaxExportedFileSize {
		t.Errorf("stored MaxExportedFileSize = %d, want the §8.3 default", row.ContentPolicy.MaxExportedFileSize)
	}
}

func TestCreateDelegationPolicyRejectsScanWithoutInterceptor(t *testing.T) {
	router, _ := newDelegationPolicyAdmin(t)
	body := validDelegationPolicy("scan-policy")
	body.ContentPolicy.ScanExportedFiles = true // no interceptorRef
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/delegation-policies",
		body, withAdminPrincipal)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("scan without interceptor: status %d, want 400; body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "EXPORT_SCAN_REQUIRES_INTERCEPTOR") {
		t.Errorf("error should carry EXPORT_SCAN_REQUIRES_INTERCEPTOR: %s", rr.Body.String())
	}
}

func TestCreateDelegationPolicyAllowsScanWithInterceptor(t *testing.T) {
	router, _ := newDelegationPolicyAdmin(t)
	body := validDelegationPolicy("scan-policy")
	body.ContentPolicy.ScanExportedFiles = true
	body.ContentPolicy.InterceptorRef = "pii-scanner"
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/delegation-policies",
		body, withAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Errorf("scan with interceptor: status %d, want 201; body %s", rr.Code, rr.Body.String())
	}
}

func TestCreateDelegationPolicyRejectsDuplicate(t *testing.T) {
	router, _ := newDelegationPolicyAdmin(t)
	h := router.Handler()
	doAdminReq(t, h, http.MethodPost, "/v1/admin/delegation-policies",
		validDelegationPolicy("p1"), withAdminPrincipal)
	rr := doAdminReq(t, h, http.MethodPost, "/v1/admin/delegation-policies",
		validDelegationPolicy("p1"), withAdminPrincipal)
	if rr.Code != http.StatusConflict {
		t.Errorf("duplicate create: status %d, want 409", rr.Code)
	}
}

func TestCreateDelegationPolicyRejectsInvalidName(t *testing.T) {
	router, _ := newDelegationPolicyAdmin(t)
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/delegation-policies",
		validDelegationPolicy("Invalid Name"), withAdminPrincipal)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("invalid name: status %d, want 400", rr.Code)
	}
}

func TestGetDelegationPolicy(t *testing.T) {
	router, store := newDelegationPolicyAdmin(t)
	if err := store.Create(context.Background(), delegationpolicystore.DelegationPolicy{TenantID: "platform", Name: "p1"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rr := doAdminReq(t, router.Handler(), http.MethodGet, "/v1/admin/delegation-policies/p1",
		nil, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("get: status %d", rr.Code)
	}
	var resp admin.DelegationPolicyPayload
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Name != "p1" {
		t.Errorf("response name = %q, want p1", resp.Name)
	}
}

func TestGetDelegationPolicyMissing(t *testing.T) {
	router, _ := newDelegationPolicyAdmin(t)
	rr := doAdminReq(t, router.Handler(), http.MethodGet, "/v1/admin/delegation-policies/ghost",
		nil, withAdminPrincipal)
	if rr.Code != http.StatusNotFound {
		t.Errorf("get missing: status %d, want 404", rr.Code)
	}
}

func TestListDelegationPolicies(t *testing.T) {
	router, store := newDelegationPolicyAdmin(t)
	_ = store.Create(context.Background(), delegationpolicystore.DelegationPolicy{TenantID: "platform", Name: "p1"})
	_ = store.Create(context.Background(), delegationpolicystore.DelegationPolicy{TenantID: "platform", Name: "p2"})

	rr := doAdminReq(t, router.Handler(), http.MethodGet, "/v1/admin/delegation-policies",
		nil, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("list: status %d", rr.Code)
	}
	var resp struct {
		DelegationPolicies []admin.DelegationPolicyPayload `json:"delegationPolicies"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.DelegationPolicies) != 2 {
		t.Errorf("list: got %d policies, want 2", len(resp.DelegationPolicies))
	}
}

func TestUpdateDelegationPolicyReplacesFields(t *testing.T) {
	router, store := newDelegationPolicyAdmin(t)
	if err := store.Create(context.Background(), delegationpolicystore.DelegationPolicy{TenantID: "platform", Name: "p1"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	body := validDelegationPolicy("p1")
	body.AllowSelfRecursion = true
	rr := doAdminReq(t, router.Handler(), http.MethodPut, "/v1/admin/delegation-policies/p1",
		body, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("update: status %d, body %s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(context.Background(), "platform", "p1")
	if !row.AllowSelfRecursion || len(row.Rules) != 1 {
		t.Errorf("updated policy = %+v, want allowSelfRecursion=true with 1 rule", row)
	}
}

func TestUpdateDelegationPolicyRejectsScanInvariant(t *testing.T) {
	router, store := newDelegationPolicyAdmin(t)
	if err := store.Create(context.Background(), delegationpolicystore.DelegationPolicy{TenantID: "platform", Name: "p1"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	body := validDelegationPolicy("p1")
	body.ContentPolicy.ScanExportedFiles = true // no interceptorRef
	rr := doAdminReq(t, router.Handler(), http.MethodPut, "/v1/admin/delegation-policies/p1",
		body, withAdminPrincipal)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("update violating the scan invariant: status %d, want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "EXPORT_SCAN_REQUIRES_INTERCEPTOR") {
		t.Errorf("error should carry EXPORT_SCAN_REQUIRES_INTERCEPTOR: %s", rr.Body.String())
	}
}

func TestDeleteDelegationPolicy(t *testing.T) {
	router, store := newDelegationPolicyAdmin(t)
	if err := store.Create(context.Background(), delegationpolicystore.DelegationPolicy{TenantID: "platform", Name: "p1"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rr := doAdminReq(t, router.Handler(), http.MethodDelete, "/v1/admin/delegation-policies/p1",
		nil, withAdminPrincipal)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete: status %d", rr.Code)
	}
	row, err := store.Get(context.Background(), "platform", "p1")
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if row.IsActive() {
		t.Error("a deleted policy must report IsActive() == false")
	}
}

// newDelegationPolicyWithRuntimesAdmin wires both the delegation-policy
// and runtime stores so the §8.3 deletion guard can run.
func newDelegationPolicyWithRuntimesAdmin(t *testing.T) (*admin.Router, *delegationpolicystore.Memory, *runtimestore.Memory) {
	t.Helper()
	policies := delegationpolicystore.NewMemory()
	runtimes := runtimestore.NewMemory()
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithDelegationPolicies(policies).WithRuntimes(runtimes)
	return router, policies, runtimes
}

func TestDeleteDelegationPolicyBlockedByRuntimeDependent(t *testing.T) {
	router, policies, runtimes := newDelegationPolicyWithRuntimesAdmin(t)
	ctx := context.Background()
	if err := policies.Create(ctx, delegationpolicystore.DelegationPolicy{TenantID: "platform", Name: "orchestrator-policy"}); err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	if err := runtimes.Create(ctx, runtimestore.Runtime{
		Name: "claude-worker", DelegationPolicyRef: "orchestrator-policy",
	}); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}

	rr := doAdminReq(t, router.Handler(), http.MethodDelete,
		"/v1/admin/delegation-policies/orchestrator-policy", nil, withAdminPrincipal)
	if rr.Code != http.StatusConflict {
		t.Fatalf("delete a referenced policy: status %d, want 409; body %s", rr.Code, rr.Body.String())
	}
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if env.Error.Code != "RESOURCE_HAS_DEPENDENTS" {
		t.Errorf("error code = %q, want RESOURCE_HAS_DEPENDENTS", env.Error.Code)
	}
	deps, _ := env.Error.Details["dependents"].([]any)
	if len(deps) != 1 {
		t.Fatalf("details.dependents = %v, want one entry", env.Error.Details["dependents"])
	}
	entry, _ := deps[0].(map[string]any)
	if entry["type"] != "runtime" {
		t.Errorf("dependent type = %v, want runtime", entry["type"])
	}
	// §8.3: the blocked policy stays active.
	row, _ := policies.Get(ctx, "platform", "orchestrator-policy")
	if !row.IsActive() {
		t.Error("a policy blocked by the deletion guard must remain active")
	}
}

func TestDeleteDelegationPolicyAllowedWhenNoDependents(t *testing.T) {
	router, policies, runtimes := newDelegationPolicyWithRuntimesAdmin(t)
	ctx := context.Background()
	if err := policies.Create(ctx, delegationpolicystore.DelegationPolicy{TenantID: "platform", Name: "p1"}); err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	// A runtime referencing a different policy must not block the delete.
	if err := runtimes.Create(ctx, runtimestore.Runtime{
		Name: "rt1", DelegationPolicyRef: "other-policy",
	}); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}

	rr := doAdminReq(t, router.Handler(), http.MethodDelete,
		"/v1/admin/delegation-policies/p1", nil, withAdminPrincipal)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete an unreferenced policy: status %d, want 204; body %s", rr.Code, rr.Body.String())
	}
	row, _ := policies.Get(ctx, "platform", "p1")
	if row.IsActive() {
		t.Error("an unreferenced policy must be soft-deleted")
	}
}

// newAuditedDelegationPolicyAdmin wires a recording audit sink so the
// §8.3 scanExportedFiles transition events can be observed.
func newAuditedDelegationPolicyAdmin(t *testing.T) (*admin.Router, *delegationpolicystore.Memory, *recordingAudit) {
	t.Helper()
	store := delegationpolicystore.NewMemory()
	audit := &recordingAudit{}
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Audit: audit,
	}).WithDelegationPolicies(store)
	return router, store, audit
}

func TestUpdateDelegationPolicyEmitsScanWeakenedEvent(t *testing.T) {
	router, store, audit := newAuditedDelegationPolicyAdmin(t)
	if err := store.Create(context.Background(), delegationpolicystore.DelegationPolicy{
		TenantID: "platform",
		Name:     "p1",
		ContentPolicy: delegationpolicystore.ContentPolicy{
			ScanExportedFiles: true, InterceptorRef: "pii-scanner",
		},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Update turns scanExportedFiles off — a §8.3 weakening.
	body := validDelegationPolicy("p1")
	body.ContentPolicy.ScanExportedFiles = false
	rr := doAdminReq(t, router.Handler(), http.MethodPut, "/v1/admin/delegation-policies/p1",
		body, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("update: status %d, body %s", rr.Code, rr.Body.String())
	}
	ev, ok := findAuditEvent(audit.snapshot(), "delegation_policy.export_scan_weakened")
	if !ok {
		t.Fatal("a true->false scanExportedFiles transition must emit delegation_policy.export_scan_weakened")
	}
	if ev.Detail["old_scanExportedFiles"] != true || ev.Detail["new_scanExportedFiles"] != false {
		t.Errorf("event detail = %v, want old=true new=false", ev.Detail)
	}
	if ev.Detail["cooldown_seconds"] != 60 {
		t.Errorf("weakened event cooldown_seconds = %v, want 60", ev.Detail["cooldown_seconds"])
	}
}

func TestUpdateDelegationPolicyEmitsScanStrengthenedEvent(t *testing.T) {
	router, store, audit := newAuditedDelegationPolicyAdmin(t)
	if err := store.Create(context.Background(), delegationpolicystore.DelegationPolicy{TenantID: "platform", Name: "p1"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Update turns scanExportedFiles on — a §8.3 strengthening.
	body := validDelegationPolicy("p1")
	body.ContentPolicy.ScanExportedFiles = true
	body.ContentPolicy.InterceptorRef = "pii-scanner"
	rr := doAdminReq(t, router.Handler(), http.MethodPut, "/v1/admin/delegation-policies/p1",
		body, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("update: status %d, body %s", rr.Code, rr.Body.String())
	}
	ev, ok := findAuditEvent(audit.snapshot(), "delegation_policy.export_scan_strengthened")
	if !ok {
		t.Fatal("a false->true scanExportedFiles transition must emit delegation_policy.export_scan_strengthened")
	}
	if _, hasCooldown := ev.Detail["cooldown_seconds"]; hasCooldown {
		t.Error("a strengthening transition takes effect immediately — it must not carry cooldown_seconds")
	}
}

func TestUpdateDelegationPolicyNoScanEventWhenUnchanged(t *testing.T) {
	router, store, audit := newAuditedDelegationPolicyAdmin(t)
	if err := store.Create(context.Background(), delegationpolicystore.DelegationPolicy{TenantID: "platform", Name: "p1"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// validDelegationPolicy leaves scanExportedFiles false — unchanged.
	rr := doAdminReq(t, router.Handler(), http.MethodPut, "/v1/admin/delegation-policies/p1",
		validDelegationPolicy("p1"), withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("update: status %d", rr.Code)
	}
	if _, ok := findAuditEvent(audit.snapshot(), "delegation_policy.export_scan_weakened"); ok {
		t.Error("an unchanged scanExportedFiles must not emit a weakened event")
	}
	if _, ok := findAuditEvent(audit.snapshot(), "delegation_policy.export_scan_strengthened"); ok {
		t.Error("an unchanged scanExportedFiles must not emit a strengthened event")
	}
}

// TestDelegationPolicyAuthorization covers the §10.2 "Manage delegation
// policies" matrix row. The matrix grants the operation to
// platform-admin and tenant-admin and denies it to tenant-viewer,
// billing-viewer, and user. (Reconciliation note: the delegation-policy
// CRUD previously used the platform-admin-only requireAdmin gate, which
// over-restricted the tenant-admin entitlement the §10.2 matrix
// defines; the routes now use the manage_delegation_policies permission
// gate.)
func TestDelegationPolicyAuthorization(t *testing.T) {
	routes := []struct {
		method, path string
	}{
		{http.MethodPost, "/v1/admin/delegation-policies"},
		{http.MethodGet, "/v1/admin/delegation-policies"},
		{http.MethodGet, "/v1/admin/delegation-policies/p1"},
		{http.MethodPut, "/v1/admin/delegation-policies/p1"},
		{http.MethodDelete, "/v1/admin/delegation-policies/p1"},
	}
	// Roles the §10.2 matrix denies the operation.
	for _, as := range []struct {
		name string
		fn   func(*http.Request) *http.Request
	}{
		{"user", withUserPrincipal},
		{"billing-viewer", withBillingViewerPrincipal},
		{"tenant-viewer", withTenantViewerPrincipal},
	} {
		router, _ := newDelegationPolicyAdmin(t)
		h := router.Handler()
		for _, c := range routes {
			rr := doAdminReq(t, h, c.method, c.path, validDelegationPolicy("p1"), as.fn)
			if rr.Code != http.StatusForbidden {
				t.Errorf("%s %s as %s: status %d, want 403", c.method, c.path, as.name, rr.Code)
			}
		}
	}
	// Roles the §10.2 matrix grants the operation: every route is
	// reachable (the handler runs, so the status is never 403).
	for _, as := range []struct {
		name string
		fn   func(*http.Request) *http.Request
	}{
		{"platform-admin", withAdminPrincipal},
		{"tenant-admin", withTenantAdminPrincipal},
	} {
		router, store := newDelegationPolicyAdmin(t)
		if err := store.Create(context.Background(),
			delegationpolicystore.DelegationPolicy{TenantID: "platform", Name: "p1"}); err != nil {
			t.Fatalf("seed: %v", err)
		}
		h := router.Handler()
		for _, c := range routes {
			rr := doAdminReq(t, h, c.method, c.path, validDelegationPolicy("p1"), as.fn)
			if rr.Code == http.StatusForbidden {
				t.Errorf("%s %s as %s: got 403, want the handler to run", c.method, c.path, as.name)
			}
		}
	}
}
