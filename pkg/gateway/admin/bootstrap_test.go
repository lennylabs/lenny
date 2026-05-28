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

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/userstore"
)

// spec: §24.1 lenny-ctl bootstrap; §15.1 POST /v1/admin/bootstrap.

func newBootstrapRouter(t *testing.T) (*admin.Router, *tenantstore.Memory, *runtimestore.Memory, *userstore.Memory, *recordingAudit) {
	t.Helper()
	tenants := tenantstore.NewMemory()
	runtimes := runtimestore.NewMemory()
	users := userstore.NewMemory()
	audit := &recordingAudit{}
	router := admin.NewRouter(tenants, admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Audit: audit,
	}).WithRuntimes(runtimes).WithUsers(users)
	return router, tenants, runtimes, users, audit
}

// TestBootstrapCredentialPools covers the §4.9 bootstrap.credentialPools
// seed surface: a pool is created from the seed list, and a
// cacheScope-tenant entry for a regulated tenant is rejected per-entry
// without blocking the rest of the batch.
func TestBootstrapCredentialPools(t *testing.T) {
	tenants := tenantstore.NewMemory()
	_ = tenants.Create(context.Background(), tenantstore.Tenant{ID: "acme", ComplianceProfile: "none"})
	_ = tenants.Create(context.Background(), tenantstore.Tenant{ID: "globex", ComplianceProfile: "hipaa"})
	pools := credentialpoolstore.NewMemory()
	router := admin.NewRouter(tenants, admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithCredentialPools(pools)

	body := admin.BootstrapRequest{
		CredentialPools: []admin.CredentialPoolPayload{
			{TenantID: "acme", Name: "anthropic-shared", Provider: "anthropic_direct",
				AssignmentStrategy: "least-loaded", CacheScope: "per-user"},
			// Rejected: cacheScope tenant on a hipaa tenant.
			{TenantID: "globex", Name: "bad-cache", Provider: "anthropic_direct", CacheScope: "tenant"},
		},
	}
	buf, _ := json.Marshal(body)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/bootstrap", bytes.NewReader(buf)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusMultiStatus {
		t.Fatalf("status: got %d, want 207 (partial); body=%s", rr.Code, rr.Body.String())
	}
	var resp admin.BootstrapResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.CredentialPools.CreatedCount != 1 {
		t.Errorf("createdCount = %d, want 1", resp.CredentialPools.CreatedCount)
	}
	if len(resp.CredentialPools.Errors) != 1 {
		t.Errorf("errors = %+v, want one (the regulated cacheScope rejection)", resp.CredentialPools.Errors)
	}
	if _, err := pools.Get(context.Background(), "acme", "anthropic-shared"); err != nil {
		t.Errorf("seeded pool not stored: %v", err)
	}
	if _, err := pools.Get(context.Background(), "globex", "bad-cache"); err == nil {
		t.Error("rejected pool must not be persisted")
	}
}

func TestBootstrapHappyPath(t *testing.T) {
	router, tenants, runtimes, users, audit := newBootstrapRouter(t)
	body := admin.BootstrapRequest{
		Tenants: []admin.TenantPayload{
			{ID: "acme", DisplayName: "Acme Corp"},
		},
		Runtimes: []admin.RuntimePayload{
			{Name: "echo", Image: "lenny/echo@sha256:abc", Type: "agent"},
		},
		Users: []admin.UserPayload{
			{Subject: "alice@acme.com", TenantID: "acme", Roles: []pkgauth.Role{pkgauth.RoleUser}},
		},
	}
	buf, _ := json.Marshal(body)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/bootstrap", bytes.NewReader(buf)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var resp admin.BootstrapResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Tenants.CreatedCount != 1 || resp.Runtimes.CreatedCount != 1 || resp.Users.CreatedCount != 1 {
		t.Fatalf("counts: %+v", resp)
	}

	if _, err := tenants.Get(context.Background(), "acme"); err != nil {
		t.Errorf("tenant not stored: %v", err)
	}
	if _, err := runtimes.Get(context.Background(), "echo"); err != nil {
		t.Errorf("runtime not stored: %v", err)
	}
	if _, err := users.Get(context.Background(), "acme", "alice@acme.com"); err != nil {
		t.Errorf("user not stored: %v", err)
	}

	if got := audit.snapshot(); len(got) != 1 || got[0].Type != "platform.bootstrap_applied" {
		t.Errorf("audit: %+v", got)
	}
}

// TestBootstrapSkipsDifferingFieldsWithoutForceUpdate covers the §17.6
// line 450 upsert table: an existing resource whose seed fields differ is
// left unchanged (skip) unless --force-update is supplied. The skip is
// not a failure (HTTP 200), carries the §15.1 line 1007 SEED_CONFLICT
// code, and lists the conflicting fields.
//
// spec: §17.6 lines 450-451; §15.1 line 1007.
func TestBootstrapSkipsDifferingFieldsWithoutForceUpdate_spec_17_6_450(t *testing.T) {
	router, tenants, runtimes, _, _ := newBootstrapRouter(t)
	_ = tenants.Create(context.Background(), tenantstore.Tenant{ID: "acme", DisplayName: "Old name"})
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo", Image: "old@sha256:a"})

	body := admin.BootstrapRequest{
		Tenants:  []admin.TenantPayload{{ID: "acme", DisplayName: "New name"}},
		Runtimes: []admin.RuntimePayload{{Name: "echo", Image: "new@sha256:b"}},
	}
	buf, _ := json.Marshal(body)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/bootstrap", bytes.NewReader(buf)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (skip is not a failure); body=%s", rr.Code, rr.Body.String())
	}

	var resp admin.BootstrapResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Tenants.SkippedCount != 1 || resp.Runtimes.SkippedCount != 1 {
		t.Fatalf("expected both resources skipped: %+v", resp)
	}
	if resp.Tenants.UpdatedCount != 0 || resp.Runtimes.UpdatedCount != 0 {
		t.Errorf("nothing should have been updated without --force-update: %+v", resp)
	}
	if len(resp.Runtimes.Skipped) != 1 || resp.Runtimes.Skipped[0].Code != "SEED_CONFLICT" {
		t.Fatalf("runtime skip should carry SEED_CONFLICT: %+v", resp.Runtimes.Skipped)
	}
	if got := resp.Runtimes.Skipped[0].ConflictingFields; len(got) != 1 || got[0] != "image" {
		t.Errorf("conflictingFields = %v, want [image]", got)
	}

	// Stored values must be unchanged.
	t1, _ := tenants.Get(context.Background(), "acme")
	if t1.DisplayName != "Old name" {
		t.Errorf("tenant displayName changed on skip: got %q", t1.DisplayName)
	}
	r1, _ := runtimes.Get(context.Background(), "echo")
	if r1.Image != "old@sha256:a" {
		t.Errorf("runtime image changed on skip: got %q", r1.Image)
	}
}

// TestBootstrapForceUpdateOverwritesDifferingFields covers the §17.6 line
// 450 with-force-update column: ?forceUpdate=true replaces differing
// fields on an existing resource.
//
// spec: §17.6 line 450.
func TestBootstrapForceUpdateOverwritesDifferingFields_spec_17_6_450(t *testing.T) {
	router, tenants, runtimes, _, _ := newBootstrapRouter(t)
	_ = tenants.Create(context.Background(), tenantstore.Tenant{ID: "acme", DisplayName: "Old name"})
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo", Image: "old@sha256:a"})

	body := admin.BootstrapRequest{
		Tenants:  []admin.TenantPayload{{ID: "acme", DisplayName: "New name"}},
		Runtimes: []admin.RuntimePayload{{Name: "echo", Image: "new@sha256:b"}},
	}
	buf, _ := json.Marshal(body)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/bootstrap?forceUpdate=true", bytes.NewReader(buf)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}

	var resp admin.BootstrapResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Tenants.UpdatedCount != 1 || resp.Runtimes.UpdatedCount != 1 {
		t.Errorf("counts: %+v", resp)
	}

	t1, _ := tenants.Get(context.Background(), "acme")
	if t1.DisplayName != "New name" {
		t.Errorf("tenant displayName: got %q", t1.DisplayName)
	}
	r1, _ := runtimes.Get(context.Background(), "echo")
	if r1.Image != "new@sha256:b" {
		t.Errorf("runtime image: got %q", r1.Image)
	}
}

func TestBootstrapReportsPerEntryErrors(t *testing.T) {
	router, tenants, _, _, _ := newBootstrapRouter(t)
	body := admin.BootstrapRequest{
		Tenants: []admin.TenantPayload{
			{ID: "acme"},
			{ID: ""},           // missing id
			{ID: "with space"}, // invalid format
		},
	}
	buf, _ := json.Marshal(body)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/bootstrap", bytes.NewReader(buf)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusMultiStatus {
		t.Fatalf("status: got %d, want 207", rr.Code)
	}
	var resp admin.BootstrapResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Tenants.CreatedCount != 1 || len(resp.Tenants.Errors) != 2 {
		t.Errorf("partial result: %+v", resp.Tenants)
	}
	// Successful entry should have been stored.
	if _, err := tenants.Get(context.Background(), "acme"); err != nil {
		t.Errorf("acme tenant not stored: %v", err)
	}
}

// TestBootstrapAuditCarriesPerEntryErrors asserts the §24.1 R6 audit
// contract: the platform.bootstrap_applied event's per-resource summary
// carries `{name, action: "error", message}` rows for each failed
// entry, not just an error count. A forensic reader must be able to
// answer "which seed entry failed" from the audit chain alone.
// F-24.1.9.
func TestBootstrapAuditCarriesPerEntryErrors_spec_24_1_R6(t *testing.T) {
	router, _, _, _, audit := newBootstrapRouter(t)
	body := admin.BootstrapRequest{
		Tenants: []admin.TenantPayload{
			{ID: "acme", DisplayName: "Acme"},
			{ID: "with space"}, // invalid id format
			{ID: ""},           // missing id
		},
	}
	buf, _ := json.Marshal(body)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/bootstrap", bytes.NewReader(buf)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusMultiStatus {
		t.Fatalf("status: got %d, want 207", rr.Code)
	}
	got := audit.snapshot()
	if len(got) != 1 || got[0].Type != "platform.bootstrap_applied" {
		t.Fatalf("audit: %+v", got)
	}
	tenantsSection, ok := got[0].Detail["tenants"].(map[string]any)
	if !ok {
		t.Fatalf("tenants section missing from audit detail: %+v", got[0].Detail)
	}
	if c, _ := tenantsSection["created"].(int); c != 1 {
		t.Errorf("tenants.created = %v, want 1", tenantsSection["created"])
	}
	errs, ok := tenantsSection["errors"].([]map[string]any)
	if !ok {
		t.Fatalf("tenants.errors is %T, want []map[string]any; section=%+v", tenantsSection["errors"], tenantsSection)
	}
	if len(errs) != 2 {
		t.Fatalf("tenants.errors length = %d, want 2; got %+v", len(errs), errs)
	}
	for _, e := range errs {
		if e["action"] != "error" {
			t.Errorf("audit entry action = %v, want %q", e["action"], "error")
		}
		if _, present := e["message"]; !present {
			t.Errorf("audit entry missing message: %+v", e)
		}
	}
	// One entry carries the rejected id "with space"; the other has
	// an empty id because the input row had no id.
	gotNames := map[string]bool{}
	for _, e := range errs {
		if n, ok := e["name"].(string); ok {
			gotNames[n] = true
		}
	}
	if !gotNames["with space"] {
		t.Errorf("audit errors missing 'with space' entry: %+v", errs)
	}
}

// TestBootstrapDryRunValidatesWithoutPersisting covers §15.1 line 1140:
// ?dryRun=true runs full validation, persists nothing, sets the
// X-Dry-Run response header, and (per the bootstrap exception) still
// emits the platform.bootstrap_applied audit event with dryRun: true.
//
// spec: §15.1 lines 863, 1140; §17.6 line 419.
func TestBootstrapDryRunValidatesWithoutPersisting_spec_15_1_1140(t *testing.T) {
	router, tenants, runtimes, users, audit := newBootstrapRouter(t)
	body := admin.BootstrapRequest{
		Tenants:  []admin.TenantPayload{{ID: "acme", DisplayName: "Acme Corp"}},
		Runtimes: []admin.RuntimePayload{{Name: "echo", Image: "lenny/echo@sha256:abc", Type: "agent"}},
		Users:    []admin.UserPayload{{Subject: "alice@acme.com", TenantID: "acme", Roles: []pkgauth.Role{pkgauth.RoleUser}}},
	}
	buf, _ := json.Marshal(body)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/bootstrap?dryRun=true", bytes.NewReader(buf)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("X-Dry-Run"); got != "true" {
		t.Errorf("X-Dry-Run header = %q, want %q", got, "true")
	}

	var resp admin.BootstrapResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	// The response previews the would-be actions.
	if resp.Tenants.CreatedCount != 1 || resp.Runtimes.CreatedCount != 1 || resp.Users.CreatedCount != 1 {
		t.Errorf("dry-run preview counts: %+v", resp)
	}

	// Nothing persisted.
	if _, err := tenants.Get(context.Background(), "acme"); err == nil {
		t.Error("dry-run must not persist the tenant")
	}
	if _, err := runtimes.Get(context.Background(), "echo"); err == nil {
		t.Error("dry-run must not persist the runtime")
	}
	if _, err := users.Get(context.Background(), "acme", "alice@acme.com"); err == nil {
		t.Error("dry-run must not persist the user")
	}

	// Audit event emitted even for dry-run, with dryRun: true.
	got := audit.snapshot()
	if len(got) != 1 || got[0].Type != "platform.bootstrap_applied" {
		t.Fatalf("audit: %+v", got)
	}
	if dr, _ := got[0].Detail["dryRun"].(bool); !dr {
		t.Errorf("audit detail dryRun = %v, want true", got[0].Detail["dryRun"])
	}
}

// TestBootstrapAuditCarriesSeedHashAndResourceSummary covers the §15.1
// line 863 audit-detail contract: a seed-file SHA-256 hash and a
// per-resource {type, name, action} summary.
//
// spec: §15.1 line 863.
func TestBootstrapAuditCarriesSeedHashAndResourceSummary_spec_15_1_863(t *testing.T) {
	router, _, _, _, audit := newBootstrapRouter(t)
	body := admin.BootstrapRequest{
		Tenants:  []admin.TenantPayload{{ID: "acme", DisplayName: "Acme Corp"}},
		Runtimes: []admin.RuntimePayload{{Name: "echo", Image: "lenny/echo@sha256:abc", Type: "agent"}},
	}
	buf, _ := json.Marshal(body)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/bootstrap", bytes.NewReader(buf)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}

	got := audit.snapshot()
	if len(got) != 1 {
		t.Fatalf("audit events: %+v", got)
	}
	detail := got[0].Detail
	hash, _ := detail["seedSha256"].(string)
	if len(hash) != 64 {
		t.Errorf("seedSha256 = %q, want a 64-char hex digest", hash)
	}
	if dr, ok := detail["dryRun"].(bool); !ok || dr {
		t.Errorf("dryRun = %v, want false", detail["dryRun"])
	}
	resources, ok := detail["resources"].([]map[string]any)
	if !ok {
		t.Fatalf("resources is %T, want []map[string]any", detail["resources"])
	}
	if len(resources) != 2 {
		t.Fatalf("resources length = %d, want 2; got %+v", len(resources), resources)
	}
	byName := map[string]map[string]any{}
	for _, r := range resources {
		name, _ := r["name"].(string)
		byName[name] = r
	}
	if byName["acme"]["type"] != "tenant" || byName["acme"]["action"] != "created" {
		t.Errorf("tenant summary: %+v", byName["acme"])
	}
	if byName["echo"]["type"] != "runtime" || byName["echo"]["action"] != "created" {
		t.Errorf("runtime summary: %+v", byName["echo"])
	}
}

// TestBootstrapBlocksSecurityCriticalFieldOverwrite covers §17.6 line
// 451: a seed that would overwrite a runtime's isolationProfile is an
// error regardless of --force-update, and the resource is not changed.
//
// spec: §17.6 line 451.
func TestBootstrapBlocksSecurityCriticalFieldOverwrite_spec_17_6_451(t *testing.T) {
	router, _, runtimes, _, _ := newBootstrapRouter(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name:             "echo",
		Image:            "lenny/echo@sha256:abc",
		IsolationProfile: "standard",
	})

	body := admin.BootstrapRequest{
		Runtimes: []admin.RuntimePayload{{
			Name: "echo", Image: "lenny/echo@sha256:abc", Type: "agent",
			IsolationProfile: "sandboxed", // security-critical change
		}},
	}
	buf, _ := json.Marshal(body)
	// Even with force-update the overwrite is blocked.
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/bootstrap?forceUpdate=true", bytes.NewReader(buf)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusMultiStatus {
		t.Fatalf("status: got %d, want 207 (per-entry error)", rr.Code)
	}

	var resp admin.BootstrapResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.Runtimes.Errors) != 1 || resp.Runtimes.Errors[0].Code != "SEED_SECURITY_CRITICAL_FIELD" {
		t.Fatalf("expected a SEED_SECURITY_CRITICAL_FIELD error: %+v", resp.Runtimes.Errors)
	}
	if resp.Runtimes.UpdatedCount != 0 {
		t.Errorf("security-critical overwrite must not update: %+v", resp.Runtimes)
	}

	// The stored isolationProfile is unchanged.
	r1, _ := runtimes.Get(context.Background(), "echo")
	if string(r1.IsolationProfile) != "standard" {
		t.Errorf("isolationProfile changed despite block: got %q", r1.IsolationProfile)
	}
}

func TestBootstrapRequiresPlatformAdmin(t *testing.T) {
	router, _, _, _, _ := newBootstrapRouter(t)
	body, _ := json.Marshal(admin.BootstrapRequest{})
	req := withTenantAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/bootstrap", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("tenant-admin: got %d, want 403", rr.Code)
	}
}

func TestBootstrapRejectsMalformedJSON(t *testing.T) {
	router, _, _, _, _ := newBootstrapRouter(t)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/bootstrap", bytes.NewReader([]byte("not-json"))))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rr.Code)
	}
}
