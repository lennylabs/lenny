// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// spec: §15.1 lines 876-878 / §24.5 rows 3-5 — per-credential
// subresource CRUD (add / update / remove a single credential in a
// pool) and §24.5 row 2 per-credential health and lease counts.

// fakeCredHealth returns canned per-credential lease counts so the GET
// handler's §24.5 row-2 enrichment can be asserted without a live lease
// store.
type fakeCredHealth struct {
	counts map[string]int
	pool   string
	ids    []string
}

func (f *fakeCredHealth) PoolCredentialLeaseCounts(poolName string, credentialIDs []string) map[string]int {
	f.pool = poolName
	f.ids = append([]string(nil), credentialIDs...)
	return f.counts
}

func newCredEntryAdmin(t *testing.T) (*admin.Router, *credentialpoolstore.Memory, *recordingAudit) {
	t.Helper()
	store := credentialpoolstore.NewMemory()
	audit := &recordingAudit{}
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Audit: audit,
	}).WithCredentialPools(store)
	return router, store, audit
}

func auditTypes(audit *recordingAudit) map[string]bool {
	out := map[string]bool{}
	for _, ev := range audit.snapshot() {
		out[ev.Type] = true
	}
	return out
}

// TestAddCredentialAppendsAndAudits asserts POST .../credentials appends
// a new credential, returns 201 with the updated pool, and emits the
// credential_added audit event. spec: §15.1 line 876, §24.5 row 3.
func TestAddCredentialAppendsAndAudits(t *testing.T) {
	router, store, audit := newCredEntryAdmin(t)
	seedRevocationPool(t, store, "claude-prod")

	rr := doAdminReq(t, router.Handler(), http.MethodPost,
		"/v1/admin/credential-pools/claude-prod/credentials",
		map[string]string{"id": "key-3", "secretRef": "lenny-system/k3"},
		withTenantAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status %d, want 201; body %s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(context.Background(), "acme", "claude-prod")
	if len(row.Credentials) != 3 {
		t.Fatalf("credential count = %d, want 3", len(row.Credentials))
	}
	if c := getCred(t, store, "claude-prod", "key-3"); c.SecretRef != "lenny-system/k3" {
		t.Errorf("key-3 secretRef = %q", c.SecretRef)
	}
	if !auditTypes(audit)["admin.credential_pool.credential_added"] {
		t.Errorf("missing credential_added audit event: %+v", audit.snapshot())
	}
}

// TestAddCredentialDuplicateIDConflicts asserts a duplicate credential
// id is rejected with 409 RESOURCE_ALREADY_EXISTS. spec: §15.1 line 876, 983.
func TestAddCredentialDuplicateIDConflicts(t *testing.T) {
	router, store, _ := newCredEntryAdmin(t)
	seedRevocationPool(t, store, "claude-prod")

	rr := doAdminReq(t, router.Handler(), http.MethodPost,
		"/v1/admin/credential-pools/claude-prod/credentials",
		map[string]string{"id": "key-1", "secretRef": "lenny-system/dup"},
		withTenantAdminPrincipal)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409; body %s", rr.Code, rr.Body.String())
	}
	if got := errorCode(t, rr.Body.Bytes()); got != "RESOURCE_ALREADY_EXISTS" {
		t.Fatalf("code %s, want RESOURCE_ALREADY_EXISTS; body %s", got, rr.Body.String())
	}
}

// TestAddCredentialRequiresID asserts a body without an id is a 400.
func TestAddCredentialRequiresID(t *testing.T) {
	router, store, _ := newCredEntryAdmin(t)
	seedRevocationPool(t, store, "claude-prod")

	rr := doAdminReq(t, router.Handler(), http.MethodPost,
		"/v1/admin/credential-pools/claude-prod/credentials",
		map[string]string{"secretRef": "lenny-system/k9"},
		withTenantAdminPrincipal)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400; body %s", rr.Code, rr.Body.String())
	}
}

// TestAddCredentialMissingPool404 asserts adding to an unknown pool 404s.
func TestAddCredentialMissingPool404(t *testing.T) {
	router, _, _ := newCredEntryAdmin(t)
	rr := doAdminReq(t, router.Handler(), http.MethodPost,
		"/v1/admin/credential-pools/ghost/credentials",
		map[string]string{"id": "key-1", "secretRef": "lenny-system/k1"},
		withTenantAdminPrincipal)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404; body %s", rr.Code, rr.Body.String())
	}
}

// TestAddCredentialRBACProbeDenied asserts the §4.9 admin-time RBAC
// live-probe runs on the new secretRef and a DENIED verdict rejects the
// add with 422 CREDENTIAL_SECRET_RBAC_MISSING. spec: §4.9 line 1212,
// §15.1 line 990.
func TestAddCredentialRBACProbeDenied(t *testing.T) {
	prober := &fakeSecretProber{verdicts: map[string]admin.SecretProbeVerdict{
		"lenny-system/forbidden": admin.SecretProbeDenied,
	}}
	store := credentialpoolstore.NewMemory()
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithCredentialPools(store).WithSecretAccessProber(prober)
	seedRevocationPool(t, store, "claude-prod")

	rr := doAdminReq(t, router.Handler(), http.MethodPost,
		"/v1/admin/credential-pools/claude-prod/credentials",
		map[string]string{"id": "key-3", "secretRef": "lenny-system/forbidden"},
		withTenantAdminPrincipal)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422; body %s", rr.Code, rr.Body.String())
	}
	if got := errorCode(t, rr.Body.Bytes()); got != "CREDENTIAL_SECRET_RBAC_MISSING" {
		t.Errorf("code = %v, want CREDENTIAL_SECRET_RBAC_MISSING", got)
	}
	// The pool must be unchanged — the unprobed credential was not added.
	if row, _ := store.Get(context.Background(), "acme", "claude-prod"); len(row.Credentials) != 2 {
		t.Errorf("credential count = %d, want 2 (add rejected)", len(row.Credentials))
	}
}

// TestUpdateCredentialReplacesFieldsPreservingRevocation asserts the
// row-level PUT updates the addressed credential's secretRef in place
// while preserving its revocation status (an update is not a re-enable).
// spec: §15.1 line 877, §24.5 row 4.
func TestUpdateCredentialReplacesFieldsPreservingRevocation(t *testing.T) {
	router, store, audit := newCredEntryAdmin(t)
	seedRevocationPool(t, store, "claude-prod")
	// Revoke key-1 directly in the store so the update must preserve it.
	if _, err := store.Update(context.Background(), "acme", "claude-prod", func(p *credentialpoolstore.CredentialPool) error {
		p.Credentials[0].Status = credentialpoolstore.CredentialRevoked
		p.Credentials[0].RevokedBy = "alice@acme.com"
		return nil
	}); err != nil {
		t.Fatalf("seed revoke: %v", err)
	}

	rr := doAdminReq(t, router.Handler(), http.MethodPut,
		"/v1/admin/credential-pools/claude-prod/credentials/key-1",
		map[string]string{"secretRef": "lenny-system/k1-rotated"},
		withTenantAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	c := getCred(t, store, "claude-prod", "key-1")
	if c.SecretRef != "lenny-system/k1-rotated" {
		t.Errorf("secretRef = %q, want lenny-system/k1-rotated", c.SecretRef)
	}
	if !c.IsRevoked() || c.RevokedBy != "alice@acme.com" {
		t.Errorf("update clobbered revocation state: %+v", c)
	}
	if !auditTypes(audit)["admin.credential_pool.credential_updated"] {
		t.Errorf("missing credential_updated audit event: %+v", audit.snapshot())
	}
}

// TestUpdateCredentialMissingCredential404 asserts updating an unknown
// credential id 404s. spec: §15.1 line 877.
func TestUpdateCredentialMissingCredential404(t *testing.T) {
	router, store, _ := newCredEntryAdmin(t)
	seedRevocationPool(t, store, "claude-prod")

	rr := doAdminReq(t, router.Handler(), http.MethodPut,
		"/v1/admin/credential-pools/claude-prod/credentials/ghost",
		map[string]string{"secretRef": "lenny-system/x"},
		withTenantAdminPrincipal)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404; body %s", rr.Code, rr.Body.String())
	}
}

// TestUpdateCredentialProbesChangedSecretRef asserts the §4.9 RBAC probe
// runs only when the PUT changes secretRef, and a DENIED verdict rejects
// the update. spec: §15.1 line 877 (probe on secretRef change).
func TestUpdateCredentialProbesChangedSecretRef(t *testing.T) {
	prober := &fakeSecretProber{verdicts: map[string]admin.SecretProbeVerdict{
		"lenny-system/forbidden": admin.SecretProbeDenied,
	}}
	store := credentialpoolstore.NewMemory()
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithCredentialPools(store).WithSecretAccessProber(prober)
	seedRevocationPool(t, store, "claude-prod")

	rr := doAdminReq(t, router.Handler(), http.MethodPut,
		"/v1/admin/credential-pools/claude-prod/credentials/key-1",
		map[string]string{"secretRef": "lenny-system/forbidden"},
		withTenantAdminPrincipal)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422; body %s", rr.Code, rr.Body.String())
	}
	// An update that re-sends the existing secretRef takes no probe.
	prober.calls = nil
	rr = doAdminReq(t, router.Handler(), http.MethodPut,
		"/v1/admin/credential-pools/claude-prod/credentials/key-1",
		map[string]string{"secretRef": "lenny-system/k1"},
		withTenantAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("unchanged-secretRef update status %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	if len(prober.calls) != 0 {
		t.Errorf("unchanged secretRef should not probe; calls = %v", prober.calls)
	}
}

// TestRemoveCredentialDropsAndAudits asserts DELETE .../credentials/{id}
// removes the credential, returns the updated pool, and audits.
// spec: §15.1 line 878, §24.5 row 5.
func TestRemoveCredentialDropsAndAudits(t *testing.T) {
	router, store, audit := newCredEntryAdmin(t)
	seedRevocationPool(t, store, "claude-prod")

	rr := doAdminReq(t, router.Handler(), http.MethodDelete,
		"/v1/admin/credential-pools/claude-prod/credentials/key-1", nil,
		withTenantAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(context.Background(), "acme", "claude-prod")
	if len(row.Credentials) != 1 || row.Credentials[0].ID != "key-2" {
		t.Fatalf("after remove credentials = %+v, want only key-2", row.Credentials)
	}
	if !auditTypes(audit)["admin.credential_pool.credential_removed"] {
		t.Errorf("missing credential_removed audit event: %+v", audit.snapshot())
	}
}

// TestRemoveCredentialMissing404 asserts removing an unknown credential
// 404s. spec: §15.1 line 878.
func TestRemoveCredentialMissing404(t *testing.T) {
	router, store, _ := newCredEntryAdmin(t)
	seedRevocationPool(t, store, "claude-prod")

	rr := doAdminReq(t, router.Handler(), http.MethodDelete,
		"/v1/admin/credential-pools/claude-prod/credentials/ghost", nil,
		withTenantAdminPrincipal)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404; body %s", rr.Code, rr.Body.String())
	}
}

// TestGetPoolSurfacesHealthAndLeaseCounts asserts the §24.5 row-2 GET
// surfaces per-credential health and lease counts when a health reader
// is wired. spec: §24.5 line 87.
func TestGetPoolSurfacesHealthAndLeaseCounts(t *testing.T) {
	store := credentialpoolstore.NewMemory()
	health := &fakeCredHealth{counts: map[string]int{"key-1": 4}}
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithCredentialPools(store).WithPoolCredentialHealth(health)
	seedRevocationPool(t, store, "claude-prod")
	// Revoke key-2 so its health reads "revoked".
	if _, err := store.Update(context.Background(), "acme", "claude-prod", func(p *credentialpoolstore.CredentialPool) error {
		p.Credentials[1].Status = credentialpoolstore.CredentialRevoked
		return nil
	}); err != nil {
		t.Fatalf("seed revoke: %v", err)
	}

	rr := doAdminReq(t, router.Handler(), http.MethodGet,
		"/v1/admin/credential-pools/claude-prod", nil, withTenantAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	var payload admin.CredentialPoolPayload
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	byID := map[string]admin.CredentialEntryPayload{}
	for _, c := range payload.Credentials {
		byID[c.ID] = c
	}
	if c := byID["key-1"]; c.Health != "healthy" || c.LeaseCount == nil || *c.LeaseCount != 4 {
		t.Errorf("key-1 health/leaseCount = %q/%v, want healthy/4", c.Health, c.LeaseCount)
	}
	if c := byID["key-2"]; c.Health != "revoked" {
		t.Errorf("key-2 health = %q, want revoked", c.Health)
	}
	if health.pool != "claude-prod" {
		t.Errorf("reader pool = %q, want claude-prod", health.pool)
	}
}

// TestGetPoolHealthOmittedWithoutReader asserts the GET derives a health
// string from the persisted status even when no lease-count reader is
// wired, and omits leaseCount. spec: §24.5 line 87.
func TestGetPoolHealthOmittedWithoutReader(t *testing.T) {
	router, store, _ := newCredEntryAdmin(t)
	seedRevocationPool(t, store, "claude-prod")

	rr := doAdminReq(t, router.Handler(), http.MethodGet,
		"/v1/admin/credential-pools/claude-prod", nil, withTenantAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	var payload admin.CredentialPoolPayload
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, c := range payload.Credentials {
		if c.Health != "healthy" {
			t.Errorf("%s health = %q, want healthy", c.ID, c.Health)
		}
		if c.LeaseCount != nil {
			t.Errorf("%s leaseCount = %v, want nil (no reader)", c.ID, c.LeaseCount)
		}
	}
}
