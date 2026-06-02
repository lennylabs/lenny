// SPDX-License-Identifier: MIT

package playground

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestSessionCookieIsOpaqueAndDoesNotEmbedTenant asserts the §27.3.1
// line 81 contract that the lenny_playground_session cookie carries only
// the opaque session id and never the tenant. The tenant is recovered
// server-side from the fan-in index. F-27.3.8.
func TestSessionCookieIsOpaqueAndDoesNotEmbedTenant_spec_27_3_1_81(t *testing.T) {
	store := NewMemorySessionStore()
	oidc := &fakeOIDC{subject: OIDCSubject{UserID: "alice", TenantID: "acme", Scope: "sessions:create"}}
	h := New(Config{Enabled: true, AuthMode: AuthModeOIDC, OIDCSessionTTL: time.Hour, BearerTTL: 900 * time.Second}, Options{
		Signer:   devSigner(),
		Sessions: store,
		OIDC:     oidc,
		Tenants:  fakeTenants{registered: map[string]bool{"acme": true}},
	})
	pgSrv := httptest.NewServer(h.PlaygroundRoutes())
	defer pgSrv.Close()

	cookie := completeOIDCLogin(t, h, pgSrv, oidc)
	if cookie == "" {
		t.Fatal("login set no session cookie")
	}
	// The opaque id is base64url and never contains the dotted tenant
	// separator the prior format used.
	if strings.Contains(cookie, ".") {
		t.Fatalf("cookie value %q contains a '.', the retired tenant-embedding separator", cookie)
	}
	// The tenant id must not appear anywhere in the cookie value.
	if strings.Contains(cookie, "acme") {
		t.Fatalf("cookie value %q leaks the tenant id", cookie)
	}
	// The tenant is recoverable server-side from the fan-in index alone.
	tenant, ok, err := store.TenantForSession(context.Background(), cookie)
	if err != nil || !ok || tenant != "acme" {
		t.Fatalf("TenantForSession(%q) = (%q, %v, %v); want (acme, true, nil)", cookie, tenant, ok, err)
	}
}

// TestTenantForSessionIndexRoundTripAndRevocation covers the §27.3.1
// fan-in index lifecycle on the in-memory store: PutSession writes the
// id→tenant mapping, RevokeSession removes it, and an unknown id reports
// not-found. F-27.3.8.
func TestTenantForSessionIndexRoundTripAndRevocation_spec_27_3_1_81(t *testing.T) {
	store := NewMemorySessionStore()
	ctx := context.Background()
	rec := SessionRecord{UserID: "bob", TenantID: "globex", Origin: PlaygroundOrigin, BearerJTIs: []string{"jti-1"}}
	if err := store.PutSession(ctx, "globex", "opaque-1", rec, time.Hour); err != nil {
		t.Fatalf("PutSession: %v", err)
	}
	tenant, ok, err := store.TenantForSession(ctx, "opaque-1")
	if err != nil || !ok || tenant != "globex" {
		t.Fatalf("TenantForSession after put = (%q, %v, %v); want (globex, true, nil)", tenant, ok, err)
	}
	// An unknown opaque id is not found.
	if _, ok, _ := store.TenantForSession(ctx, "never-issued"); ok {
		t.Fatal("TenantForSession resolved an id that was never issued")
	}
	// Revocation removes the index entry alongside the record.
	if err := store.RevokeSession(ctx, "globex", "opaque-1", rec.BearerJTIs, time.Hour); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	if _, ok, _ := store.TenantForSession(ctx, "opaque-1"); ok {
		t.Fatal("TenantForSession resolved a revoked session id; index entry survived RevokeSession")
	}
}

// TestTenantForSessionExpires covers the §27.3.1 index entry expiring
// with the session TTL so a stale id self-clears. F-27.3.8.
func TestTenantForSessionExpires_spec_27_3_1_81(t *testing.T) {
	store := NewMemorySessionStore()
	now := time.Unix(1_700_000_000, 0)
	store.now = func() time.Time { return now }
	ctx := context.Background()
	rec := SessionRecord{UserID: "carol", TenantID: "acme", Origin: PlaygroundOrigin}
	if err := store.PutSession(ctx, "acme", "opaque-2", rec, time.Minute); err != nil {
		t.Fatalf("PutSession: %v", err)
	}
	now = now.Add(2 * time.Minute)
	if _, ok, _ := store.TenantForSession(ctx, "opaque-2"); ok {
		t.Fatal("TenantForSession resolved an expired index entry")
	}
}
