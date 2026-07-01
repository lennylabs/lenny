// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationpolicystore"
)

// spec: §15.1 line 1140 — ?dryRun=true validates without persisting or
// auditing, returns the computed resource, and sets X-Dry-Run: true.

func TestCreateDelegationPolicyDryRun_spec_15_1(t *testing.T) {
	router, store, audit := newAuditedDelegationPolicyAdmin(t)
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/delegation-policies?dryRun=true",
		validDelegationPolicy("orchestrator-policy"), withAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("X-Dry-Run"); got != "true" {
		t.Errorf("X-Dry-Run = %q, want true", got)
	}
	var resp admin.DelegationPolicyPayload
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Name != "orchestrator-policy" || len(resp.Rules) != 1 {
		t.Errorf("computed resource: %+v", resp)
	}
	// No persistence: the policy was not created.
	if _, err := store.Get(context.Background(), "platform", "orchestrator-policy"); err == nil {
		t.Error("dry-run create must not persist the policy")
	}
	if len(audit.snapshot()) != 0 {
		t.Errorf("dry-run must not emit audit events: %+v", audit.snapshot())
	}
}

func TestUpdateDelegationPolicyDryRun_spec_15_1(t *testing.T) {
	router, store, audit := newAuditedDelegationPolicyAdmin(t)
	if err := store.Create(context.Background(), delegationpolicystore.DelegationPolicy{
		TenantID: "platform", Name: "p1",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	body := validDelegationPolicy("p1")
	body.AllowSelfRecursion = true
	rr := doAdminReq(t, router.Handler(), http.MethodPut, "/v1/admin/delegation-policies/p1?dryRun=true",
		body, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("X-Dry-Run"); got != "true" {
		t.Errorf("X-Dry-Run = %q, want true", got)
	}
	var resp admin.DelegationPolicyPayload
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.AllowSelfRecursion || len(resp.Rules) != 1 {
		t.Errorf("preview = %+v, want allowSelfRecursion=true with 1 rule", resp)
	}
	// No persistence: the stored policy is unchanged.
	row, _ := store.Get(context.Background(), "platform", "p1")
	if row.AllowSelfRecursion || len(row.Rules) != 0 {
		t.Errorf("dry-run update must not persist: stored = %+v", row)
	}
	if len(audit.snapshot()) != 0 {
		t.Errorf("dry-run must not emit audit events: %+v", audit.snapshot())
	}
}

// A missing policy 404s ahead of the dry-run branch.
func TestUpdateDelegationPolicyDryRunMissing_spec_15_1(t *testing.T) {
	router, _ := newDelegationPolicyAdmin(t)
	rr := doAdminReq(t, router.Handler(), http.MethodPut, "/v1/admin/delegation-policies/ghost?dryRun=true",
		validDelegationPolicy("ghost"), withAdminPrincipal)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("dry-run update of a missing policy: status %d, want 404", rr.Code)
	}
}

// Validation still runs under dryRun: scanExportedFiles without an
// interceptorRef returns 400.
func TestCreateDelegationPolicyDryRunValidates_spec_15_1(t *testing.T) {
	router, _ := newDelegationPolicyAdmin(t)
	body := validDelegationPolicy("scan-policy")
	body.ContentPolicy.ScanExportedFiles = true // no interceptorRef
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/delegation-policies?dryRun=true",
		body, withAdminPrincipal)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("dry-run with an invalid body: status %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}
