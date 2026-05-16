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
	row, err := store.Get(context.Background(), "orchestrator-policy")
	if err != nil {
		t.Fatalf("store missing policy: %v", err)
	}
	if len(row.Rules) != 1 || !row.Rules[0].Allow ||
		row.Rules[0].Target.MatchLabels["team"] != "platform" {
		t.Errorf("stored rules = %+v", row.Rules)
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
	if err := store.Create(context.Background(), delegationpolicystore.DelegationPolicy{Name: "p1"}); err != nil {
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
	_ = store.Create(context.Background(), delegationpolicystore.DelegationPolicy{Name: "p1"})
	_ = store.Create(context.Background(), delegationpolicystore.DelegationPolicy{Name: "p2"})

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
	if err := store.Create(context.Background(), delegationpolicystore.DelegationPolicy{Name: "p1"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	body := validDelegationPolicy("p1")
	body.AllowSelfRecursion = true
	rr := doAdminReq(t, router.Handler(), http.MethodPut, "/v1/admin/delegation-policies/p1",
		body, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("update: status %d, body %s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(context.Background(), "p1")
	if !row.AllowSelfRecursion || len(row.Rules) != 1 {
		t.Errorf("updated policy = %+v, want allowSelfRecursion=true with 1 rule", row)
	}
}

func TestUpdateDelegationPolicyRejectsScanInvariant(t *testing.T) {
	router, store := newDelegationPolicyAdmin(t)
	if err := store.Create(context.Background(), delegationpolicystore.DelegationPolicy{Name: "p1"}); err != nil {
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
	if err := store.Create(context.Background(), delegationpolicystore.DelegationPolicy{Name: "p1"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rr := doAdminReq(t, router.Handler(), http.MethodDelete, "/v1/admin/delegation-policies/p1",
		nil, withAdminPrincipal)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete: status %d", rr.Code)
	}
	row, err := store.Get(context.Background(), "p1")
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if row.IsActive() {
		t.Error("a deleted policy must report IsActive() == false")
	}
}

func TestDelegationPolicyRequiresPlatformAdmin(t *testing.T) {
	router, _ := newDelegationPolicyAdmin(t)
	h := router.Handler()
	for _, c := range []struct {
		method, path string
	}{
		{http.MethodPost, "/v1/admin/delegation-policies"},
		{http.MethodGet, "/v1/admin/delegation-policies"},
		{http.MethodGet, "/v1/admin/delegation-policies/p1"},
		{http.MethodPut, "/v1/admin/delegation-policies/p1"},
		{http.MethodDelete, "/v1/admin/delegation-policies/p1"},
	} {
		rr := doAdminReq(t, h, c.method, c.path, validDelegationPolicy("p1"), withTenantAdminPrincipal)
		if rr.Code != http.StatusForbidden {
			t.Errorf("%s %s as tenant-admin: status %d, want 403", c.method, c.path, rr.Code)
		}
	}
}
