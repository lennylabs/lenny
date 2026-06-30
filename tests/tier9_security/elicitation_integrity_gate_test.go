// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security test for the §15.1 line 823,824 own-tenant gate and
// provenance body on the admin elicitation-content-integrity
// sub-resource (proposal 0019 finding ADM-3). The endpoints
//
//	GET /v1/admin/tenants/{id}/elicitation-content-integrity
//	PUT /v1/admin/tenants/{id}/elicitation-content-integrity
//
// are admitted to both platform-admin and tenant-admin by the §15.1 RBAC
// grant. The pre-fix code gated both platform-admin-only, so a
// tenant-admin could not manage its own tenant's integrity posture, and
// the GET body omitted the platformFloor, justification, changedAt, and
// changedBy fields the §15.1 line 824 body defines. The handler must now
// admit a tenant-admin confined to its own {id}, persist the provenance
// on the tenant row, and return the full body.
//
// The gate is exercised in-process against the genuine admin Router with
// injected Principals carrying distinct tenant ids, the same Principal
// the §10.2 auth middleware attaches after it validates the caller's JWT.
// Driving the real Router exercises the same authorization code path a
// Bearer-JWT caller exercises; the cross-tenant boundary and the
// provenance round-trip are the properties under test, independent of the
// JWT-parsing front door.
//
// spec: §15.1 (tenant-admin gate, line 823,824; full GET body, line 824),
// §9.2 (elicitation content integrity), §10.2 (tenant-admin cannot access
// other tenants' data, line 261).

package tier9_security_test

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
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

// elicitGateBody mirrors the §15.1 line 824 GET/PUT response body.
type elicitGateBody struct {
	TenantID      string `json:"tenantId"`
	StoredMode    string `json:"storedMode"`
	EffectiveMode string `json:"effectiveMode"`
	PlatformFloor string `json:"platformFloor"`
	Justification string `json:"justification"`
	ChangedAt     string `json:"changedAt"`
	ChangedBy     string `json:"changedBy"`
}

// elicitTenantAdminReq attaches a §10.2 tenant-admin Principal for the
// given tenant, the Principal the auth middleware builds from a validated
// tenant-admin JWT. The subject encodes the tenant so the test can assert
// changedBy carries the caller's sub.
func elicitTenantAdminReq(req *http.Request, tenant string) *http.Request {
	ctx := authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject:  "admin@" + tenant + ".example",
		TenantID: tenant,
		Roles:    []pkgauth.Role{pkgauth.RoleTenantAdmin},
	})
	return req.WithContext(ctx)
}

// elicitGateRouter builds the genuine admin Router over an in-memory
// tenant store seeded with the named tenants, with the SIEM/pgaudit gates
// configured so the regulated-profile compliance checks do not interfere.
func elicitGateRouter(t *testing.T, tenants ...string) (*admin.Router, *tenantstore.Memory) {
	t.Helper()
	store := tenantstore.NewMemory()
	for _, id := range tenants {
		if err := store.Create(context.Background(), tenantstore.Tenant{ID: id}); err != nil {
			t.Fatalf("seed tenant %s: %v", id, err)
		}
	}
	router := admin.NewRouter(store, admin.Options{
		Clock: func() time.Time { return time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC) },
	}).WithSIEMConfigured(true).WithPgauditConfigured(true).WithElicitationFloor("off")
	return router, store
}

// elicitGetETag fetches the elicitation sub-resource ETag for id as the
// given tenant-admin, so the subsequent PUT can supply If-Match.
func elicitGetETag(t *testing.T, router *admin.Router, id, tenant string) string {
	t.Helper()
	get := elicitTenantAdminReq(httptest.NewRequest(http.MethodGet,
		"/v1/admin/tenants/"+id+"/elicitation-content-integrity", nil), tenant)
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, get)
	if rr.Code != http.StatusOK {
		t.Fatalf("ETag GET as %s: status %d, body %s", tenant, rr.Code, rr.Body.String())
	}
	return rr.Header().Get("ETag")
}

// spec: 15.1, 9.2, 10.2
// diagnosis: the §15.1 admin elicitation-content-integrity gate was
// platform-admin-only and its GET body dropped the provenance. A failure
// here means either a tenant-admin cannot manage its own tenant's
// integrity posture (the pre-fix platform-admin-only gate), the GET body
// omits platformFloor/justification/changedAt/changedBy, or the
// provenance is not persisted on the tenant row. The own-tenant
// tenant-admin must succeed; a foreign tenant-admin must be rejected
// before any write.
func TestElicitationIntegrityGate_TenantAdminOwnTenant_ADM3(t *testing.T) {
	router, store := elicitGateRouter(t, "acme")

	relaxed, _ := json.Marshal(map[string]string{
		"mode": "detect-only", "justification": "staging tenant, integrity tooling offline",
	})

	// 1. A foreign tenant-admin (globex) PUTting acme's resource is
	//    rejected 403 before any write; the row is untouched.
	etag := elicitGetETag(t, router, "acme", "acme")
	foreign := elicitTenantAdminReq(httptest.NewRequest(http.MethodPut,
		"/v1/admin/tenants/acme/elicitation-content-integrity", bytes.NewReader(relaxed)), "globex")
	foreign.Header.Set("If-Match", etag)
	rrForeign := httptest.NewRecorder()
	router.Handler().ServeHTTP(rrForeign, foreign)
	if rrForeign.Code != http.StatusForbidden {
		t.Fatalf("foreign tenant-admin PUT: status %d, want 403 FORBIDDEN; body=%s",
			rrForeign.Code, rrForeign.Body.String())
	}
	if row, _ := store.Get(context.Background(), "acme"); row.ElicitationContentIntegrity != "" {
		t.Fatalf("foreign-tenant PUT mutated acme: stored mode = %q, want unchanged (cross-tenant write)",
			row.ElicitationContentIntegrity)
	}

	// 2. The own-tenant tenant-admin (acme) PUTs a relaxed mode with a
	//    justification and succeeds under the widened gate.
	own := elicitTenantAdminReq(httptest.NewRequest(http.MethodPut,
		"/v1/admin/tenants/acme/elicitation-content-integrity", bytes.NewReader(relaxed)), "acme")
	own.Header.Set("If-Match", etag)
	rrPut := httptest.NewRecorder()
	router.Handler().ServeHTTP(rrPut, own)
	if rrPut.Code != http.StatusOK {
		t.Fatalf("own-tenant tenant-admin PUT: status %d, want 200; body=%s", rrPut.Code, rrPut.Body.String())
	}

	// 3. The provenance is persisted on the tenant row, not just in the
	//    audit event.
	row, _ := store.Get(context.Background(), "acme")
	if row.ElicitationContentIntegrity != "detect-only" {
		t.Errorf("persisted mode = %q, want detect-only", row.ElicitationContentIntegrity)
	}
	if row.ElicitationContentIntegrityJustification != "staging tenant, integrity tooling offline" {
		t.Errorf("persisted justification = %q, want the supplied reason",
			row.ElicitationContentIntegrityJustification)
	}
	if row.ElicitationContentIntegrityChangedBy != "admin@acme.example" {
		t.Errorf("persisted changedBy = %q, want the operator's sub admin@acme.example",
			row.ElicitationContentIntegrityChangedBy)
	}
	if row.ElicitationContentIntegrityChangedAt.IsZero() {
		t.Error("persisted changedAt is zero; the write must stamp the change instant on the row")
	}

	// 4. The own-tenant tenant-admin GET returns the full §15.1 line 824
	//    body, including the provenance the pre-fix handler omitted.
	get := elicitTenantAdminReq(httptest.NewRequest(http.MethodGet,
		"/v1/admin/tenants/acme/elicitation-content-integrity", nil), "acme")
	rrGet := httptest.NewRecorder()
	router.Handler().ServeHTTP(rrGet, get)
	if rrGet.Code != http.StatusOK {
		t.Fatalf("own-tenant tenant-admin GET: status %d, want 200; body=%s", rrGet.Code, rrGet.Body.String())
	}
	var body elicitGateBody
	if err := json.Unmarshal(rrGet.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode GET body: %v", err)
	}
	if body.StoredMode != "detect-only" {
		t.Errorf("GET storedMode = %q, want detect-only", body.StoredMode)
	}
	if body.PlatformFloor != "off" {
		t.Errorf("GET platformFloor = %q, want off — the §15.1 body carries the floor", body.PlatformFloor)
	}
	if body.Justification != "staging tenant, integrity tooling offline" {
		t.Errorf("GET justification = %q, want the supplied reason — the pre-fix GET omitted it", body.Justification)
	}
	if body.ChangedBy != "admin@acme.example" {
		t.Errorf("GET changedBy = %q, want admin@acme.example — the pre-fix GET omitted it", body.ChangedBy)
	}
	if body.ChangedAt == "" {
		t.Error("GET changedAt is empty — the pre-fix GET omitted it")
	}

	// 5. A foreign tenant-admin GET on acme is rejected, confining reads
	//    to the caller's own tenant.
	foreignGet := elicitTenantAdminReq(httptest.NewRequest(http.MethodGet,
		"/v1/admin/tenants/acme/elicitation-content-integrity", nil), "globex")
	rrForeignGet := httptest.NewRecorder()
	router.Handler().ServeHTTP(rrForeignGet, foreignGet)
	if rrForeignGet.Code != http.StatusForbidden {
		t.Fatalf("foreign tenant-admin GET on acme: status %d, want 403 FORBIDDEN", rrForeignGet.Code)
	}
}
