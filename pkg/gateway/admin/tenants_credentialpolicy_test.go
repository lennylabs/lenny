// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
)

// spec: §4.9 lines 1303-1336 — the tenant credentialPolicy round-trips
// through POST /v1/admin/tenants: the providerPools, preferredSource,
// fallback, and userCredentialsEnabled all persist and surface on the
// response payload.
func TestCreateTenantWithCredentialPolicy(t *testing.T) {
	router, store := newAdminServer(t)
	policy := &credential.CredentialPolicy{
		PreferredSource: credential.PreferPoolThenUser,
		ProviderPools: map[string]credential.ProviderPool{
			"anthropic_direct": {
				DefaultPool: "claude-prod",
				Fallback:    credential.ProviderFallback{Order: []string{"claude-prod", "claude-backup"}},
			},
		},
		Fallback:               credential.PolicyFallback{CooldownOnRateLimitSeconds: 60, MaxRotationsPerSession: 3},
		UserCredentialsEnabled: true,
	}
	body, _ := json.Marshal(admin.TenantPayload{ID: "acme", CredentialPolicy: policy})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var resp admin.TenantPayload
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.CredentialPolicy == nil || resp.CredentialPolicy.PreferredSource != credential.PreferPoolThenUser {
		t.Fatalf("response credentialPolicy = %+v, want prefer-pool-then-user", resp.CredentialPolicy)
	}
	row, err := store.Get(req.Context(), "acme")
	if err != nil {
		t.Fatalf("store missing tenant: %v", err)
	}
	got := row.CredentialPolicy
	if !got.UserCredentialsEnabled {
		t.Error("stored userCredentialsEnabled = false, want true")
	}
	order := got.PoolOrderFor("anthropic_direct")
	if len(order) != 2 || order[0] != "claude-prod" || order[1] != "claude-backup" {
		t.Errorf("stored fallback order = %v, want [claude-prod claude-backup]", order)
	}
	if got.Fallback.MaxRotationsPerSession != 3 {
		t.Errorf("stored maxRotationsPerSession = %d, want 3", got.Fallback.MaxRotationsPerSession)
	}
}

// spec: §4.9 lines 1310-1319 — an invalid credentialPolicy (unknown
// preferredSource) is rejected at admission with 400.
func TestCreateTenantRejectsInvalidCredentialPolicy(t *testing.T) {
	router, _ := newAdminServer(t)
	body, _ := json.Marshal(admin.TenantPayload{
		ID:               "acme",
		CredentialPolicy: &credential.CredentialPolicy{PreferredSource: "bogus"},
	})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400 for an invalid credentialPolicy; body=%s", rr.Code, rr.Body.String())
	}
}

// A providerPools entry with neither defaultPool nor a fallback order is
// rejected: the §4.9 chain would be empty.
func TestCreateTenantRejectsEmptyProviderPool(t *testing.T) {
	router, _ := newAdminServer(t)
	body, _ := json.Marshal(admin.TenantPayload{
		ID: "acme",
		CredentialPolicy: &credential.CredentialPolicy{
			ProviderPools: map[string]credential.ProviderPool{"anthropic_direct": {}},
		},
	})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400 for an empty providerPool; body=%s", rr.Code, rr.Body.String())
	}
}

// spec: §4.9 lines 1303-1336 — PUT /v1/admin/tenants/{id} sets the
// credentialPolicy on an existing tenant.
func TestUpdateTenantSetsCredentialPolicy(t *testing.T) {
	router, store := newAdminServer(t)
	if err := store.Create(context.Background(), tenantstore.Tenant{ID: "acme"}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	body, _ := json.Marshal(admin.UpdateTenantRequest{
		CredentialPolicy: &credential.CredentialPolicy{
			PreferredSource: credential.PreferredSourcePool,
			ProviderPools:   map[string]credential.ProviderPool{"aws_bedrock": {DefaultPool: "bedrock-prod"}},
		},
	})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPut, "/v1/admin/tenants/acme", bytes.NewReader(body)))
	injectAdminIfMatch(t, router.Handler(), req)
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(req.Context(), "acme")
	if row.CredentialPolicy.PoolOrderFor("aws_bedrock")[0] != "bedrock-prod" {
		t.Errorf("stored credentialPolicy = %+v, want aws_bedrock→bedrock-prod", row.CredentialPolicy)
	}
}
