// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security test for §25.4 Operations Inventory authorization
// scoping. pkg/ops/opsserver/operations_test.go asserts the same
// visibility rule by injecting a Principal directly onto the request
// context (authmw.WithPrincipal), bypassing the OIDC bearer-token
// verification path entirely. This file drives the identical rule
// through the real *opsserver.Server wired with the §25.4 auth stack
// (an HMAC JWT verifier standing in for OIDC, per the ops_rbac_test.go
// precedent in this package) and genuine minted bearer tokens, so the
// authorization decision is reached the same way a Bearer-JWT caller
// against the deployed lenny-ops service reaches it.
//
// spec: §25.4 line 1840 (Operations Inventory Authorization).

package tier9_security_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/ops/operations"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
)

// opsInventoryFixtureSource is a fixed operations.Source seeding one
// operation per §25.4 authorization case: the caller's own tenant-scoped
// operation, a same-tenant peer's tenant-scoped operation, a different
// tenant's tenant-scoped operation, another principal's platform-scoped
// operation, and the caller's own platform-scoped operation.
type opsInventoryFixtureSource struct{ ops []operations.Operation }

func (s opsInventoryFixtureSource) Kinds() []operations.Kind {
	return []operations.Kind{operations.KindRestore, operations.KindPlatformUpgrade}
}

func (s opsInventoryFixtureSource) List(context.Context, operations.Filter) ([]operations.Operation, error) {
	return s.ops, nil
}

// opsInventoryFixture returns the seeded operations described above.
// Every row is in_progress so it passes the §25.4 default status filter
// with no ?status= supplied.
func opsInventoryFixture() []operations.Operation {
	started := time.Now().UTC()
	return []operations.Operation{
		{OperationID: "restore-alice-own", Kind: operations.KindRestore,
			Status: operations.StatusInProgress, StartedBy: "alice@acme.com", StartedAt: started, TenantID: "acme"},
		{OperationID: "restore-bob-peer", Kind: operations.KindRestore,
			Status: operations.StatusInProgress, StartedBy: "bob@acme.com", StartedAt: started, TenantID: "acme"},
		{OperationID: "restore-carol-other-tenant", Kind: operations.KindRestore,
			Status: operations.StatusInProgress, StartedBy: "carol@globex.com", StartedAt: started, TenantID: "globex"},
		{OperationID: "upgrade-dave-platform", Kind: operations.KindPlatformUpgrade,
			Status: operations.StatusInProgress, StartedBy: "dave-platform", StartedAt: started},
		{OperationID: "upgrade-alice-platform-self", Kind: operations.KindPlatformUpgrade,
			Status: operations.StatusInProgress, StartedBy: "alice@acme.com", StartedAt: started},
	}
}

// authedOpsInventoryServer returns a real *opsserver.Server wired with
// the §25.4 auth stack (HMAC verifier standing in for OIDC, matching the
// authedOpsServer precedent in ops_rbac_test.go) and an Inventory backed
// by the given fixed operations. Multi-tenant auth is enabled with the
// permissive isoTenantRegistry (mcp_management_isolation_test.go) so the
// caller's tenant claim is honored rather than collapsed to the
// single-tenant default — the per-tenant authorization rule under test
// only exercises anything when the caller's resolved tenant reflects the
// minted claim.
func authedOpsInventoryServer(ops []operations.Operation) (*opsserver.Server, *jwt.HMACSigner) {
	signer := jwt.NewHMACSigner("ops-inventory-isolation-test", []byte("ops-inventory-isolation-test-secret"))
	srv := opsserver.New(opsserver.Options{
		Inventory: operations.New(opsInventoryFixtureSource{ops: ops}),
		Auth: &opsserver.AuthConfig{
			Options: authmw.Options{
				Verifier:    signer,
				MultiTenant: true,
				Registry:    isoTenantRegistry{},
			},
			RateLimiter: opsserver.NewRateLimiter(1000, 1000),
		},
	})
	return srv, signer
}

// opsInventoryOperationIDs decodes the §25.4 operations page body and
// returns the set of operationId values it carries.
func opsInventoryOperationIDs(t *testing.T, body []byte) []string {
	t.Helper()
	var page struct {
		Operations []struct {
			OperationID string `json:"operationId"`
		} `json:"operations"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("decode operations page %s: %v", body, err)
	}
	ids := make([]string, len(page.Operations))
	for i, op := range page.Operations {
		ids[i] = op.OperationID
	}
	return ids
}

// spec: §25.4 line 1840 — "tenant-admin sees only operations where (a)
// started_by is themselves, OR (b) the operation carries a tenantId
// field AND its value matches the caller's tenant. Platform-scoped
// operations (no tenantId field ...) are visible only when started_by
// matches the caller; a tenant-admin never sees platform-scoped
// operations started by other principals."
//
// diagnosis: a tenant-admin querying the live §25.4 Operations Inventory
// endpoint (through the genuine JWT auth stack, not an injected
// Principal) received another tenant's operation or another principal's
// platform-scoped operation. Either the per-row tenant match is missing
// on the JWT-authenticated path or the platform-scoped carve-out folds
// every principal's operations together, letting one tenant-admin
// enumerate another tenant's or another principal's operations activity
// through the deployed lenny-ops surface.
func TestOpsTenantAdminInventoryScopedToOwnAndTenant_spec_25_4_1840(t *testing.T) {
	srv, signer := authedOpsInventoryServer(opsInventoryFixture())
	tenantAdmin := mintOpsRBACToken(t, signer, "alice@acme.com", "acme", "", auth.RoleTenantAdmin)

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/operations", nil)
	req.Header.Set("Authorization", "Bearer "+tenantAdmin)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("tenant-admin operations list: status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	ids := opsInventoryOperationIDs(t, rec.Body.Bytes())
	got := make(map[string]bool, len(ids))
	for _, id := range ids {
		got[id] = true
	}

	for _, want := range []string{"restore-alice-own", "restore-bob-peer", "upgrade-alice-platform-self"} {
		if !got[want] {
			t.Errorf("tenant-admin operations list missing %s (own or own-tenant operation): ids=%v", want, ids)
		}
	}
	for _, forbidden := range []string{"restore-carol-other-tenant", "upgrade-dave-platform"} {
		if got[forbidden] {
			t.Errorf("tenant-admin operations list leaked %s (other-tenant or other-principal platform-scoped operation): ids=%v",
				forbidden, ids)
		}
	}

	// Positive control: platform-admin sees every seeded operation,
	// confirming the tenant-admin exclusions above are not vacuously true
	// because the fixture never surfaces those rows to anyone.
	platformAdmin := mintOpsRBACToken(t, signer, "erin@platform", "platform", "", auth.RolePlatformAdmin)
	req = httptest.NewRequest(http.MethodGet, "/v1/admin/operations?actor=*", nil)
	req.Header.Set("Authorization", "Bearer "+platformAdmin)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("platform-admin operations list: status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	allIDs := opsInventoryOperationIDs(t, rec.Body.Bytes())
	if len(allIDs) != len(opsInventoryFixture()) {
		t.Fatalf("platform-admin actor=* operations list = %v, want all %d seeded operations",
			allIDs, len(opsInventoryFixture()))
	}
}

// spec: §25.4 (Filters table) — "`actor` | ... a specific `sub`
// (`platform-admin` only), or `*` (all callers; `platform-admin` only).
// Tenant-admin is auto-restricted to `me`." A tenant-admin requesting
// ?actor=* against the live server must not receive other principals'
// operations: the server auto-restricts the effective actor rather than
// honoring the requested wildcard.
//
// diagnosis: a tenant-admin's ?actor=* request returned another
// principal's operation through the live §25.4 Operations Inventory
// endpoint. The documented actor auto-restriction for tenant-admin is
// not applied on the JWT-authenticated path, so a tenant-admin can
// request every caller's operations by supplying actor=* alone.
func TestOpsTenantAdminActorWildcardAutoRestricted_spec_25_4_1840(t *testing.T) {
	srv, signer := authedOpsInventoryServer(opsInventoryFixture())
	tenantAdmin := mintOpsRBACToken(t, signer, "alice@acme.com", "acme", "", auth.RoleTenantAdmin)

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/operations?actor=*", nil)
	req.Header.Set("Authorization", "Bearer "+tenantAdmin)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("tenant-admin actor=* operations list: status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	ids := opsInventoryOperationIDs(t, rec.Body.Bytes())
	for _, id := range ids {
		if id == "restore-carol-other-tenant" || id == "upgrade-dave-platform" {
			t.Fatalf("tenant-admin actor=* leaked %s: ids=%v", id, ids)
		}
	}
}
