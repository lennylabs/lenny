// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
)

// decodeJSON decodes a JSON response body into a generic map.
func decodeJSON(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v; body %s", err, rr.Body.String())
	}
	return out
}

// spec: §4.9 lines 1626-1659, 1742-1743 — Emergency Credential
// Revocation admin endpoints and the credential.revoked /
// credential.re_enabled audit events.

// fakePoolRevoker records the §4.9 lease-termination calls so tests can
// assert the (poolID, credentialIDs) the handler passed and return a
// fixed terminated count.
type fakePoolRevoker struct {
	pools     []string
	credIDs   [][]string
	terminate int
}

func (f *fakePoolRevoker) RevokePoolCredentials(_ context.Context, poolID string, credentialIDs []string) int {
	f.pools = append(f.pools, poolID)
	f.credIDs = append(f.credIDs, append([]string(nil), credentialIDs...))
	return f.terminate
}

func newRevocationAdmin(t *testing.T, terminate int) (*admin.Router, *credentialpoolstore.Memory, *recordingAudit, *fakePoolRevoker) {
	t.Helper()
	store := credentialpoolstore.NewMemory()
	audit := &recordingAudit{}
	rev := &fakePoolRevoker{terminate: terminate}
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Audit: audit,
	}).WithCredentialPools(store).WithPoolCredentialRevocation(rev)
	return router, store, audit, rev
}

// seedRevocationPool registers an acme pool with two active key-based
// credentials.
func seedRevocationPool(t *testing.T, store *credentialpoolstore.Memory, name string) {
	t.Helper()
	if err := store.Create(context.Background(), credentialpoolstore.CredentialPool{
		TenantID: "acme",
		Name:     name,
		Provider: "anthropic_direct",
		Credentials: []credentialpoolstore.Credential{
			{ID: "key-1", SecretRef: "lenny-system/k1"},
			{ID: "key-2", SecretRef: "lenny-system/k2"},
		},
	}); err != nil {
		t.Fatalf("seed pool: %v", err)
	}
}

func getCred(t *testing.T, store *credentialpoolstore.Memory, pool, credID string) credentialpoolstore.Credential {
	t.Helper()
	row, err := store.Get(context.Background(), "acme", pool)
	if err != nil {
		t.Fatalf("Get pool %s: %v", pool, err)
	}
	for _, c := range row.Credentials {
		if c.ID == credID {
			return c
		}
	}
	t.Fatalf("credential %s not found in pool %s", credID, pool)
	return credentialpoolstore.Credential{}
}

// TestRevokeCredentialMarksRevokesAndAudits is the §4.9 7-step contract
// for a single credential: store status flips to revoked with the
// revoker/reason recorded, the deny-list terminator is called with the
// (poolID, credId), the credential.revoked audit event carries the spec
// field set, and the 200 summary reports leasesTerminated.
func TestRevokeCredentialMarksRevokesAndAudits(t *testing.T) {
	router, store, audit, rev := newRevocationAdmin(t, 3)
	seedRevocationPool(t, store, "claude-prod")

	rr := doAdminReq(t, router.Handler(), http.MethodPost,
		"/v1/admin/credential-pools/claude-prod/credentials/key-1/revoke",
		map[string]string{"reason": "suspected_exfiltration", "note": "in logs"},
		withTenantAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, want 200; body %s", rr.Code, rr.Body.String())
	}

	summary := decodeJSON(t, rr)
	if summary["revokedCredential"] != "key-1" {
		t.Errorf("revokedCredential = %v, want key-1", summary["revokedCredential"])
	}
	if summary["leasesTerminated"] != float64(3) {
		t.Errorf("leasesTerminated = %v, want 3", summary["leasesTerminated"])
	}
	if summary["propagatedAt"] == "" {
		t.Error("summary missing propagatedAt")
	}

	c := getCred(t, store, "claude-prod", "key-1")
	if !c.IsRevoked() {
		t.Error("credential not marked revoked in store")
	}
	if c.RevokedBy != "user@acme.com" || c.RevocationReason != "suspected_exfiltration" || c.RevokedAt.IsZero() {
		t.Errorf("revocation metadata = %+v", c)
	}
	if other := getCred(t, store, "claude-prod", "key-2"); other.IsRevoked() {
		t.Error("revoking key-1 must not revoke key-2")
	}

	if len(rev.pools) != 1 || rev.pools[0] != "claude-prod" {
		t.Errorf("revoker pools = %v, want [claude-prod]", rev.pools)
	}
	if len(rev.credIDs) != 1 || len(rev.credIDs[0]) != 1 || rev.credIDs[0][0] != "key-1" {
		t.Errorf("revoker credIDs = %v, want [[key-1]]", rev.credIDs)
	}

	ev := findAudit(t, audit, "credential.revoked")
	wantFields := map[string]any{
		"tenant_id": "acme", "pool_id": "claude-prod", "credential_id": "key-1",
		"revoked_by": "user@acme.com", "reason": "suspected_exfiltration",
		"active_leases_terminated": 3,
	}
	for k, want := range wantFields {
		if ev.Detail[k] != want {
			t.Errorf("audit detail[%q] = %v, want %v", k, ev.Detail[k], want)
		}
	}
}

// TestRevokeCredentialUnknownCredential returns 404 for a missing
// credential id and emits no audit event.
func TestRevokeCredentialUnknownCredential(t *testing.T) {
	router, store, audit, rev := newRevocationAdmin(t, 0)
	seedRevocationPool(t, store, "claude-prod")
	rr := doAdminReq(t, router.Handler(), http.MethodPost,
		"/v1/admin/credential-pools/claude-prod/credentials/nope/revoke", nil, withTenantAdminPrincipal)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404; body %s", rr.Code, rr.Body.String())
	}
	if len(rev.pools) != 0 {
		t.Error("revoker called for an unknown credential")
	}
	if len(audit.snapshot()) != 0 {
		t.Error("audit emitted for an unknown credential")
	}
}

// TestRevokeCredentialUnknownPool returns 404 for a missing pool.
func TestRevokeCredentialUnknownPool(t *testing.T) {
	router, _, _, _ := newRevocationAdmin(t, 0)
	rr := doAdminReq(t, router.Handler(), http.MethodPost,
		"/v1/admin/credential-pools/ghost/credentials/key-1/revoke", nil, withTenantAdminPrincipal)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404; body %s", rr.Code, rr.Body.String())
	}
}

// TestRevokeCredentialIdempotent confirms a repeated revoke keeps the
// original revoker and timestamp (the §4.9 store mutate is idempotent).
func TestRevokeCredentialIdempotent(t *testing.T) {
	router, store, _, _ := newRevocationAdmin(t, 1)
	seedRevocationPool(t, store, "claude-prod")
	path := "/v1/admin/credential-pools/claude-prod/credentials/key-1/revoke"
	if rr := doAdminReq(t, router.Handler(), http.MethodPost, path,
		map[string]string{"reason": "first"}, withTenantAdminPrincipal); rr.Code != http.StatusOK {
		t.Fatalf("first revoke: status %d", rr.Code)
	}
	first := getCred(t, store, "claude-prod", "key-1")
	if rr := doAdminReq(t, router.Handler(), http.MethodPost, path,
		map[string]string{"reason": "second"}, withTenantAdminPrincipal); rr.Code != http.StatusOK {
		t.Fatalf("second revoke: status %d", rr.Code)
	}
	second := getCred(t, store, "claude-prod", "key-1")
	if second.RevocationReason != first.RevocationReason || !second.RevokedAt.Equal(first.RevokedAt) {
		t.Errorf("re-revoke changed metadata: first=%+v second=%+v", first, second)
	}
}

// TestRevokePoolWideRevokesAll is the §4.9 pool-wide force-rotate: every
// active credential is revoked, each emits credential.revoked, and the
// terminator is called with all credential ids.
func TestRevokePoolWideRevokesAll(t *testing.T) {
	router, store, audit, rev := newRevocationAdmin(t, 5)
	seedRevocationPool(t, store, "claude-prod")
	rr := doAdminReq(t, router.Handler(), http.MethodPost,
		"/v1/admin/credential-pools/claude-prod/revoke",
		map[string]string{"reason": "pool_compromised"}, withTenantAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	summary := decodeJSON(t, rr)
	revoked, _ := summary["revokedCredentials"].([]any)
	if len(revoked) != 2 {
		t.Fatalf("revokedCredentials = %v, want 2 entries", summary["revokedCredentials"])
	}
	for _, id := range []string{"key-1", "key-2"} {
		if !getCred(t, store, "claude-prod", id).IsRevoked() {
			t.Errorf("credential %s not revoked", id)
		}
	}
	if len(rev.credIDs) != 1 || len(rev.credIDs[0]) != 2 {
		t.Errorf("revoker credIDs = %v, want both ids in one call", rev.credIDs)
	}
	count := 0
	for _, ev := range audit.snapshot() {
		if ev.Type == "credential.revoked" {
			count++
		}
	}
	if count != 2 {
		t.Errorf("credential.revoked events = %d, want 2", count)
	}
}

// TestReEnableCredentialClearsRevocation returns a revoked credential to
// active, clears the revocation metadata, and emits credential.re_enabled.
func TestReEnableCredentialClearsRevocation(t *testing.T) {
	router, store, audit, _ := newRevocationAdmin(t, 0)
	seedRevocationPool(t, store, "claude-prod")
	if rr := doAdminReq(t, router.Handler(), http.MethodPost,
		"/v1/admin/credential-pools/claude-prod/credentials/key-1/revoke",
		map[string]string{"reason": "x"}, withTenantAdminPrincipal); rr.Code != http.StatusOK {
		t.Fatalf("revoke: status %d", rr.Code)
	}
	rr := doAdminReq(t, router.Handler(), http.MethodPost,
		"/v1/admin/credential-pools/claude-prod/credentials/key-1/re-enable",
		map[string]string{"reason": "rotated_secret"}, withTenantAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("re-enable: status %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	c := getCred(t, store, "claude-prod", "key-1")
	if c.IsRevoked() || c.RevokedBy != "" || c.RevocationReason != "" || !c.RevokedAt.IsZero() {
		t.Errorf("re-enable did not clear revocation: %+v", c)
	}
	ev := findAudit(t, audit, "credential.re_enabled")
	if ev.Detail["credential_id"] != "key-1" || ev.Detail["re_enabled_by"] != "user@acme.com" || ev.Detail["reason"] != "rotated_secret" {
		t.Errorf("credential.re_enabled detail = %+v", ev.Detail)
	}
}

// TestReEnableUnknownCredential returns 404.
func TestReEnableUnknownCredential(t *testing.T) {
	router, store, _, _ := newRevocationAdmin(t, 0)
	seedRevocationPool(t, store, "claude-prod")
	rr := doAdminReq(t, router.Handler(), http.MethodPost,
		"/v1/admin/credential-pools/claude-prod/credentials/ghost/re-enable", nil, withTenantAdminPrincipal)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rr.Code)
	}
}

// TestUpdatePoolPreservesRevocation confirms a PUT that round-trips a
// revoked credential without a status field does not silently re-enable
// it (§4.9 revocation lifecycle is owned by the dedicated endpoints).
func TestUpdatePoolPreservesRevocation(t *testing.T) {
	router, store, _, _ := newRevocationAdmin(t, 0)
	seedRevocationPool(t, store, "claude-prod")
	if rr := doAdminReq(t, router.Handler(), http.MethodPost,
		"/v1/admin/credential-pools/claude-prod/credentials/key-1/revoke",
		map[string]string{"reason": "x"}, withTenantAdminPrincipal); rr.Code != http.StatusOK {
		t.Fatalf("revoke: status %d", rr.Code)
	}
	// A PUT replacing the credential set, omitting any status field.
	put := admin.CredentialPoolPayload{
		TenantID: "acme", Name: "claude-prod", Provider: "anthropic_direct",
		AssignmentStrategy: "least-loaded",
		Credentials: []admin.CredentialEntryPayload{
			{ID: "key-1", SecretRef: "lenny-system/k1"},
			{ID: "key-2", SecretRef: "lenny-system/k2"},
		},
	}
	if rr := doAdminReq(t, router.Handler(), http.MethodPut,
		"/v1/admin/credential-pools/claude-prod", put, withTenantAdminPrincipal); rr.Code != http.StatusOK {
		t.Fatalf("PUT: status %d; body %s", rr.Code, rr.Body.String())
	}
	if !getCred(t, store, "claude-prod", "key-1").IsRevoked() {
		t.Error("PUT silently re-enabled a revoked credential")
	}
}

// findAudit returns the first recorded event of the given type, failing
// the test when none is present.
func findAudit(t *testing.T, audit *recordingAudit, eventType string) admin.AuditEvent {
	t.Helper()
	for _, ev := range audit.snapshot() {
		if ev.Type == eventType {
			return ev
		}
	}
	t.Fatalf("no %s audit event; events: %+v", eventType, audit.snapshot())
	return admin.AuditEvent{}
}
