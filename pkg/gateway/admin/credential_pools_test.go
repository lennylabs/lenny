// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/customrolestore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// spec: §4.9 / §15.1 admin CredentialPool CRUD.

func newCredentialPoolAdmin(t *testing.T) (*admin.Router, *credentialpoolstore.Memory) {
	t.Helper()
	store := credentialpoolstore.NewMemory()
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithCredentialPools(store)
	return router, store
}

func validCredentialPool(tenant, name string) admin.CredentialPoolPayload {
	return admin.CredentialPoolPayload{
		TenantID: tenant,
		Name:     name,
		Provider: "anthropic_direct",
		Credentials: []admin.CredentialEntryPayload{
			{ID: "key-1", SecretRef: "lenny-system/anthropic-key-1"},
		},
		AssignmentStrategy:    "least-loaded",
		MaxConcurrentSessions: 10,
	}
}

// newCredentialPoolAdminWithTenants builds a pool admin router over a
// tenant store seeded with the given (tenantID → complianceProfile)
// map, so the §4.9 cacheScope compliance check has a tenant to read.
func newCredentialPoolAdminWithTenants(t *testing.T, profiles map[string]string) (*admin.Router, *credentialpoolstore.Memory) {
	t.Helper()
	tenants := tenantstore.NewMemory()
	for id, profile := range profiles {
		if err := tenants.Create(context.Background(), tenantstore.Tenant{ID: id, ComplianceProfile: profile}); err != nil {
			t.Fatalf("seed tenant %q: %v", id, err)
		}
	}
	store := credentialpoolstore.NewMemory()
	router := admin.NewRouter(tenants, admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithCredentialPools(store)
	return router, store
}

// TestCreateCredentialPoolRejectsTenantCacheScopeForRegulatedTenant is
// the §4.9 cross-user-cache prohibition: cacheScope tenant on a hipaa /
// fedramp tenant is rejected with COMPLIANCE_CROSS_USER_CACHE_PROHIBITED.
func TestCreateCredentialPoolRejectsTenantCacheScopeForRegulatedTenant(t *testing.T) {
	for _, profile := range []string{"hipaa", "fedramp"} {
		router, store := newCredentialPoolAdminWithTenants(t, map[string]string{"acme": profile})
		body := validCredentialPool("acme", "p-regulated")
		body.CacheScope = "tenant"
		rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/credential-pools", body, withAdminPrincipal)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400; body %s", profile, rr.Code, rr.Body.String())
		}
		var env struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(rr.Body.Bytes(), &env)
		if env.Error.Code != "COMPLIANCE_CROSS_USER_CACHE_PROHIBITED" {
			t.Errorf("%s: error code = %q, want COMPLIANCE_CROSS_USER_CACHE_PROHIBITED", profile, env.Error.Code)
		}
		if _, err := store.Get(context.Background(), "acme", "p-regulated"); err == nil {
			t.Errorf("%s: rejected pool must not be persisted", profile)
		}
	}
}

// TestCreateCredentialPoolAllowsTenantCacheScopeForUnregulatedTenant
// confirms cacheScope tenant is accepted for a non-regulated tenant
// (soc2 is not on the §4.9 cross-user-cache prohibition list).
func TestCreateCredentialPoolAllowsTenantCacheScopeForUnregulatedTenant(t *testing.T) {
	router, store := newCredentialPoolAdminWithTenants(t, map[string]string{"acme": "soc2"})
	body := validCredentialPool("acme", "p-ok")
	body.CacheScope = "tenant"
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/credential-pools", body, withAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status %d, want 201; body %s", rr.Code, rr.Body.String())
	}
	row, err := store.Get(context.Background(), "acme", "p-ok")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if row.CacheScope != "tenant" {
		t.Errorf("stored cacheScope = %q, want tenant", row.CacheScope)
	}
}

// TestCreateCredentialPoolRejectsInvalidCacheScope covers the §4.9
// cacheScope enum at the admin layer.
func TestCreateCredentialPoolRejectsInvalidCacheScope(t *testing.T) {
	router, _ := newCredentialPoolAdminWithTenants(t, map[string]string{"acme": "none"})
	body := validCredentialPool("acme", "p-bad")
	body.CacheScope = "global"
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/credential-pools", body, withAdminPrincipal)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400; body %s", rr.Code, rr.Body.String())
	}
}

// TestCreateCredentialPoolRejectsHTTPProxyEndpoint covers the §4.9 line
// 1513 rule: an http:// proxyEndpoint is rejected with 422
// INVALID_POOL_PROXY_ENDPOINT so a lease token is never sent in
// plaintext on the cluster network.
func TestCreateCredentialPoolRejectsHTTPProxyEndpoint(t *testing.T) {
	router, _ := newCredentialPoolAdmin(t)
	body := validCredentialPool("acme", "p-proxy")
	body.DeliveryMode = "proxy"
	body.ProxyDialect = "anthropic"
	body.ProxyEndpoint = "http://gateway-internal:8080/llm-proxy"
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/credential-pools", body, withAdminPrincipal)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422; body %s", rr.Code, rr.Body.String())
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error.Code != "INVALID_POOL_PROXY_ENDPOINT" {
		t.Errorf("error code = %q, want INVALID_POOL_PROXY_ENDPOINT", env.Error.Code)
	}
}

// TestCreateCredentialPoolAcceptsHTTPSProxyEndpoint confirms an https://
// proxy endpoint round-trips through the admin payload.
func TestCreateCredentialPoolAcceptsHTTPSProxyEndpoint(t *testing.T) {
	router, store := newCredentialPoolAdmin(t)
	body := validCredentialPool("acme", "p-proxy-ok")
	body.DeliveryMode = "proxy"
	body.ProxyDialect = "openai"
	body.ProxyEndpoint = "https://gateway-internal:8443/llm-proxy"
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/credential-pools", body, withAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status %d, want 201; body %s", rr.Code, rr.Body.String())
	}
	row, err := store.Get(context.Background(), "acme", "p-proxy-ok")
	if err != nil {
		t.Fatalf("store missing pool: %v", err)
	}
	if row.DeliveryMode != "proxy" || row.ProxyDialect != "openai" ||
		row.ProxyEndpoint != "https://gateway-internal:8443/llm-proxy" {
		t.Errorf("proxy fields not persisted: %+v", row)
	}
}

func TestCreateCredentialPool(t *testing.T) {
	router, store := newCredentialPoolAdmin(t)
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/credential-pools",
		validCredentialPool("acme", "claude-direct-prod"), withAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rr.Code, rr.Body.String())
	}
	var resp admin.CredentialPoolPayload
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Name != "claude-direct-prod" || resp.Provider != "anthropic_direct" {
		t.Errorf("response = %+v", resp)
	}
	row, err := store.Get(context.Background(), "acme", "claude-direct-prod")
	if err != nil {
		t.Fatalf("store missing pool: %v", err)
	}
	if len(row.Credentials) != 1 || row.Credentials[0].ID != "key-1" {
		t.Errorf("stored credentials = %+v", row.Credentials)
	}
}

func TestCreateCredentialPoolAsTenantAdmin(t *testing.T) {
	router, store := newCredentialPoolAdmin(t)
	// A tenant-admin omits tenantId — the call targets their own tenant.
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/credential-pools",
		validCredentialPool("", "p1"), withTenantAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Fatalf("tenant-admin create: status %d, body %s", rr.Code, rr.Body.String())
	}
	if _, err := store.Get(context.Background(), "acme", "p1"); err != nil {
		t.Errorf("pool not stored under the tenant-admin's tenant: %v", err)
	}
}

func TestCreateCredentialPoolTenantAdminScopedToOwnTenant(t *testing.T) {
	router, store := newCredentialPoolAdmin(t)
	// A tenant-admin of "acme" supplies tenantId "globex"; §10.2 scoping
	// pins the pool to their own tenant regardless of the body.
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/credential-pools",
		validCredentialPool("globex", "p1"), withTenantAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rr.Code, rr.Body.String())
	}
	if _, err := store.Get(context.Background(), "acme", "p1"); err != nil {
		t.Errorf("pool must be pinned to the tenant-admin's own tenant (acme): %v", err)
	}
	if _, err := store.Get(context.Background(), "globex", "p1"); err == nil {
		t.Error("a tenant-admin must not create a pool in another tenant")
	}
}

func TestCreateCredentialPoolPlatformAdminMustSpecifyTenant(t *testing.T) {
	router, _ := newCredentialPoolAdmin(t)
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/credential-pools",
		validCredentialPool("", "p1"), withAdminPrincipal)
	if rr.Code != http.StatusForbidden {
		t.Errorf("platform-admin create without tenantId: status %d, want 403", rr.Code)
	}
}

func TestCreateCredentialPoolRejectsBadStrategy(t *testing.T) {
	router, _ := newCredentialPoolAdmin(t)
	body := validCredentialPool("acme", "p1")
	body.AssignmentStrategy = "random"
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/credential-pools",
		body, withAdminPrincipal)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("invalid assignmentStrategy: status %d, want 400", rr.Code)
	}
}

func TestCreateCredentialPoolRejectsMissingProvider(t *testing.T) {
	router, _ := newCredentialPoolAdmin(t)
	body := validCredentialPool("acme", "p1")
	body.Provider = ""
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/credential-pools",
		body, withAdminPrincipal)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("missing provider: status %d, want 400", rr.Code)
	}
}

func TestCreateCredentialPoolRejectsDuplicate(t *testing.T) {
	router, _ := newCredentialPoolAdmin(t)
	h := router.Handler()
	doAdminReq(t, h, http.MethodPost, "/v1/admin/credential-pools",
		validCredentialPool("acme", "p1"), withAdminPrincipal)
	rr := doAdminReq(t, h, http.MethodPost, "/v1/admin/credential-pools",
		validCredentialPool("acme", "p1"), withAdminPrincipal)
	if rr.Code != http.StatusConflict {
		t.Errorf("duplicate create: status %d, want 409", rr.Code)
	}
}

func TestGetCredentialPool(t *testing.T) {
	router, store := newCredentialPoolAdmin(t)
	if err := store.Create(context.Background(), credentialpoolstore.CredentialPool{
		TenantID: "acme", Name: "p1", Provider: "anthropic_direct",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// A tenant-admin of acme reads their own pool.
	rr := doAdminReq(t, router.Handler(), http.MethodGet, "/v1/admin/credential-pools/p1",
		nil, withTenantAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("get: status %d, body %s", rr.Code, rr.Body.String())
	}
	var resp admin.CredentialPoolPayload
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Name != "p1" {
		t.Errorf("response name = %q, want p1", resp.Name)
	}
}

func TestGetCredentialPoolMissing(t *testing.T) {
	router, _ := newCredentialPoolAdmin(t)
	rr := doAdminReq(t, router.Handler(), http.MethodGet, "/v1/admin/credential-pools/ghost",
		nil, withTenantAdminPrincipal)
	if rr.Code != http.StatusNotFound {
		t.Errorf("get missing: status %d, want 404", rr.Code)
	}
}

func TestListCredentialPools(t *testing.T) {
	router, store := newCredentialPoolAdmin(t)
	ctx := context.Background()
	_ = store.Create(ctx, credentialpoolstore.CredentialPool{TenantID: "acme", Name: "p1", Provider: "anthropic_direct"})
	_ = store.Create(ctx, credentialpoolstore.CredentialPool{TenantID: "acme", Name: "p2", Provider: "aws_bedrock"})
	// A pool in another tenant must not leak into acme's list.
	_ = store.Create(ctx, credentialpoolstore.CredentialPool{TenantID: "globex", Name: "p3", Provider: "github"})

	rr := doAdminReq(t, router.Handler(), http.MethodGet, "/v1/admin/credential-pools",
		nil, withTenantAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("list: status %d", rr.Code)
	}
	var resp struct {
		CredentialPools []admin.CredentialPoolPayload `json:"credentialPools"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.CredentialPools) != 2 {
		t.Errorf("list: got %d pools, want 2 (acme-scoped)", len(resp.CredentialPools))
	}
}

func TestUpdateCredentialPoolReplacesFields(t *testing.T) {
	router, store := newCredentialPoolAdmin(t)
	if err := store.Create(context.Background(), credentialpoolstore.CredentialPool{
		TenantID: "acme", Name: "p1", Provider: "anthropic_direct", MaxConcurrentSessions: 5,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	body := validCredentialPool("", "p1")
	body.MaxConcurrentSessions = 25
	rr := doAdminReq(t, router.Handler(), http.MethodPut, "/v1/admin/credential-pools/p1",
		body, withTenantAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("update: status %d, body %s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(context.Background(), "acme", "p1")
	if row.MaxConcurrentSessions != 25 {
		t.Errorf("stored maxConcurrentSessions = %d, want 25", row.MaxConcurrentSessions)
	}
}

func TestDeleteCredentialPool(t *testing.T) {
	router, store := newCredentialPoolAdmin(t)
	if err := store.Create(context.Background(), credentialpoolstore.CredentialPool{
		TenantID: "acme", Name: "p1", Provider: "anthropic_direct",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rr := doAdminReq(t, router.Handler(), http.MethodDelete, "/v1/admin/credential-pools/p1",
		nil, withTenantAdminPrincipal)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete: status %d", rr.Code)
	}
	row, err := store.Get(context.Background(), "acme", "p1")
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if row.IsActive() {
		t.Error("a deleted pool must report IsActive() == false")
	}
}

func TestCredentialPoolRejectsNonAdmin(t *testing.T) {
	router, _ := newCredentialPoolAdmin(t)
	// An anonymous request (no principal) is rejected by the gate.
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/credential-pools", nil)
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("anonymous list: status %d, want 403", rr.Code)
	}
}

// §10.2: credential-pool endpoints are gated on manage_credential_pools.
// A tenant custom role that holds the permission is admitted; one that
// does not is rejected.
func TestCredentialPoolCustomRoleEnforcement(t *testing.T) {
	store := credentialpoolstore.NewMemory()
	roles := customrolestore.NewMemory()
	_ = roles.Create(context.Background(), customrolestore.CustomRole{
		TenantID: "acme", Name: "cred-admin",
		Permissions: []pkgauth.Permission{pkgauth.PermManageCredentialPools},
	})
	_ = roles.Create(context.Background(), customrolestore.CustomRole{
		TenantID: "acme", Name: "usage-viewer",
		Permissions: []pkgauth.Permission{pkgauth.PermViewUsage},
	})
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithCredentialPools(store).WithCustomRoles(roles)

	asCredAdmin := func(req *http.Request) *http.Request { return withRolesFor(req, "acme", "cred-admin") }
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/credential-pools",
		validCredentialPool("acme", "p1"), asCredAdmin)
	if rr.Code != http.StatusCreated {
		t.Fatalf("custom role with manage_credential_pools: got %d, want 201 (body=%s)", rr.Code, rr.Body.String())
	}

	asUsageViewer := func(req *http.Request) *http.Request { return withRolesFor(req, "acme", "usage-viewer") }
	rr = doAdminReq(t, router.Handler(), http.MethodGet, "/v1/admin/credential-pools", nil, asUsageViewer)
	if rr.Code != http.StatusForbidden {
		t.Errorf("custom role without manage_credential_pools: got %d, want 403 (body=%s)", rr.Code, rr.Body.String())
	}
}
