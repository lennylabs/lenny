// SPDX-License-Identifier: MIT

package credentialstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialstore"
)

func newStore() *credentialstore.Memory {
	return credentialstore.NewMemory(func() time.Time {
		return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	})
}

func TestRegisterAndGet(t *testing.T) {
	s := newStore()
	c, err := s.Register(context.Background(), "acme", "alice", credential.ProviderAnthropicDirect, "", "sk-secret")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if c.Ref == "" || c.Status != credentialstore.StatusActive {
		t.Errorf("registered: %+v", c)
	}
	got, err := s.Get(context.Background(), "acme", c.Ref)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Provider != credential.ProviderAnthropicDirect || got.Secret != "sk-secret" {
		t.Errorf("get: %+v", got)
	}
}

func TestRegisterRejectsUnknownProvider(t *testing.T) {
	s := newStore()
	_, err := s.Register(context.Background(), "acme", "alice", credential.Provider("made-up"), "", "x")
	if err == nil {
		t.Error("unknown provider should be rejected")
	}
}

func TestReRegisterReplacesSecret(t *testing.T) {
	s := newStore()
	c1, _ := s.Register(context.Background(), "acme", "alice", credential.ProviderGitHub, "", "secret-v1")
	c2, _ := s.Register(context.Background(), "acme", "alice", credential.ProviderGitHub, "", "secret-v2")
	// Same triple → same ref, new secret.
	if c1.Ref != c2.Ref {
		t.Errorf("re-register should reuse ref: %q vs %q", c1.Ref, c2.Ref)
	}
	got, _ := s.Get(context.Background(), "acme", c1.Ref)
	if got.Secret != "secret-v2" {
		t.Errorf("secret not replaced: %q", got.Secret)
	}
}

func TestListUserScoped(t *testing.T) {
	s := newStore()
	_, _ = s.Register(context.Background(), "acme", "alice", credential.ProviderGitHub, "", "x")
	_, _ = s.Register(context.Background(), "acme", "alice", credential.ProviderAWSBedrock, "", "y")
	_, _ = s.Register(context.Background(), "acme", "bob", credential.ProviderGitHub, "", "z")

	aliceCreds, _ := s.List(context.Background(), "acme", "alice")
	if len(aliceCreds) != 2 {
		t.Errorf("alice should have 2 credentials: %d", len(aliceCreds))
	}
	bobCreds, _ := s.List(context.Background(), "acme", "bob")
	if len(bobCreds) != 1 {
		t.Errorf("bob should have 1 credential: %d", len(bobCreds))
	}
}

func TestRotate(t *testing.T) {
	s := newStore()
	c, _ := s.Register(context.Background(), "acme", "alice", credential.ProviderGitHub, "", "old")
	rotated, err := s.Rotate(context.Background(), "acme", c.Ref, "new")
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if rotated.Secret != "new" || rotated.RotatedAt.IsZero() {
		t.Errorf("rotate: %+v", rotated)
	}
}

func TestRevoke(t *testing.T) {
	s := newStore()
	c, _ := s.Register(context.Background(), "acme", "alice", credential.ProviderGitHub, "", "x")
	revoked, err := s.Revoke(context.Background(), "acme", c.Ref)
	if err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if revoked.Status != credentialstore.StatusRevoked || revoked.RevokedAt.IsZero() {
		t.Errorf("revoke: %+v", revoked)
	}
}

// spec: §4.9 line 1349, 1365 — last_used_at is recorded by MarkUsed and
// surfaces on the GET /v1/credentials response.
func TestMarkUsed(t *testing.T) {
	s := newStore()
	c, _ := s.Register(context.Background(), "acme", "alice", credential.ProviderGitHub, "", "x")
	if !c.LastUsedAt.IsZero() {
		t.Errorf("a fresh credential must have a zero lastUsedAt: %v", c.LastUsedAt)
	}
	at := time.Date(2026, 5, 24, 9, 30, 0, 0, time.UTC)
	if err := s.MarkUsed(context.Background(), "acme", c.Ref, at); err != nil {
		t.Fatalf("MarkUsed: %v", err)
	}
	got, err := s.Get(context.Background(), "acme", c.Ref)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.LastUsedAt.Equal(at) {
		t.Errorf("LastUsedAt = %v, want %v", got.LastUsedAt, at)
	}
	// An unknown ref is a no-op error.
	if err := s.MarkUsed(context.Background(), "acme", "cred-missing", at); !errors.Is(err, credentialstore.ErrNotFound) {
		t.Errorf("MarkUsed unknown ref = %v, want ErrNotFound", err)
	}
}

func TestDelete(t *testing.T) {
	s := newStore()
	c, _ := s.Register(context.Background(), "acme", "alice", credential.ProviderGitHub, "", "x")
	if err := s.Delete(context.Background(), "acme", c.Ref); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(context.Background(), "acme", c.Ref); !errors.Is(err, credentialstore.ErrNotFound) {
		t.Errorf("Get after Delete: %v", err)
	}
	// After delete, re-register creates a fresh ref (triple freed).
	c2, _ := s.Register(context.Background(), "acme", "alice", credential.ProviderGitHub, "", "y")
	if c2.Ref == c.Ref {
		t.Error("re-register after delete should mint a fresh ref")
	}
}

func TestGetCrossTenantIsolation(t *testing.T) {
	s := newStore()
	c, _ := s.Register(context.Background(), "acme", "alice", credential.ProviderGitHub, "", "x")
	if _, err := s.Get(context.Background(), "globex", c.Ref); !errors.Is(err, credentialstore.ErrNotFound) {
		t.Errorf("cross-tenant Get should be ErrNotFound: %v", err)
	}
}

func TestGetMissing(t *testing.T) {
	s := newStore()
	if _, err := s.Get(context.Background(), "acme", "cred_missing"); !errors.Is(err, credentialstore.ErrNotFound) {
		t.Errorf("Get missing: %v", err)
	}
}

// TestRegisterEnvironmentScopedCredentials covers the §4.3 line 202
// environment scoping: the same (tenant, user, provider) triple can
// hold one credential per environment. Registering with environment
// "staging" and again with environment "production" yields two
// independent refs and secrets; the "" no-environment scope is also
// independent.
// spec: §4.3 line 202.
func TestRegisterEnvironmentScopedCredentials(t *testing.T) {
	s := newStore()
	ctx := context.Background()
	noEnv, _ := s.Register(ctx, "acme", "alice", credential.ProviderGitHub, "", "no-env-secret")
	staging, _ := s.Register(ctx, "acme", "alice", credential.ProviderGitHub, "staging", "staging-secret")
	prod, _ := s.Register(ctx, "acme", "alice", credential.ProviderGitHub, "production", "prod-secret")
	if noEnv.Ref == staging.Ref || staging.Ref == prod.Ref || noEnv.Ref == prod.Ref {
		t.Fatalf("environment-scoped Register collapsed to one ref: noEnv=%q staging=%q prod=%q",
			noEnv.Ref, staging.Ref, prod.Ref)
	}
	gotStaging, _ := s.Get(ctx, "acme", staging.Ref)
	if gotStaging.Environment != "staging" || gotStaging.Secret != "staging-secret" {
		t.Errorf("staging credential = %+v, want environment=staging secret=staging-secret", gotStaging)
	}
	gotProd, _ := s.Get(ctx, "acme", prod.Ref)
	if gotProd.Environment != "production" || gotProd.Secret != "prod-secret" {
		t.Errorf("production credential = %+v, want environment=production secret=prod-secret", gotProd)
	}
	// Re-registering the same (tenant, user, provider, environment)
	// four-tuple replaces the secret and reuses the ref.
	staging2, _ := s.Register(ctx, "acme", "alice", credential.ProviderGitHub, "staging", "staging-v2")
	if staging2.Ref != staging.Ref {
		t.Errorf("re-register reused: got ref %q, want %q", staging2.Ref, staging.Ref)
	}
	got2, _ := s.Get(ctx, "acme", staging2.Ref)
	if got2.Secret != "staging-v2" {
		t.Errorf("re-registered secret = %q, want staging-v2", got2.Secret)
	}
}

// spec: §12.1 line 5 — DeleteByUser is mandatory on Store and the
// user is the load-bearing key for the §4.9 credential registry.
func TestDeleteByUserRemovesUserCredentials_spec_12_1(t *testing.T) {
	s := newStore()
	ctx := context.Background()
	_, _ = s.Register(ctx, "acme", "alice", credential.ProviderAnthropicDirect, "", "sk-1")
	_, _ = s.Register(ctx, "acme", "alice", credential.ProviderGitHub, "staging", "gh-1")
	_, _ = s.Register(ctx, "acme", "bob", credential.ProviderAnthropicDirect, "", "sk-2")
	n, err := s.DeleteByUser(ctx, "acme", "alice")
	if err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}
	if n != 2 {
		t.Errorf("DeleteByUser should remove 2 alice rows, got %d", n)
	}
	if got, _ := s.List(ctx, "acme", "alice"); len(got) != 0 {
		t.Errorf("alice credentials should be gone, got %d", len(got))
	}
	if got, _ := s.List(ctx, "acme", "bob"); len(got) != 1 {
		t.Errorf("bob credential should survive, got %d", len(got))
	}
}

// spec: §12.1 line 5 — DeleteByUser is idempotent — a second call on
// an already-erased scope returns 0 and nil per §12.8 semantics.
func TestDeleteByUserIdempotent_spec_12_1(t *testing.T) {
	s := newStore()
	ctx := context.Background()
	n, err := s.DeleteByUser(ctx, "acme", "alice")
	if err != nil || n != 0 {
		t.Errorf("DeleteByUser on empty store: n=%d err=%v", n, err)
	}
}

// spec: §12.1 line 5 — DeleteByUser rejects empty scope ids; the
// spec is explicit that empty arguments are not "delete everything".
func TestDeleteByUserRejectsEmptyScope_spec_12_1(t *testing.T) {
	s := newStore()
	if _, err := s.DeleteByUser(context.Background(), "", "alice"); err == nil {
		t.Error("DeleteByUser must reject empty tenantID")
	}
	if _, err := s.DeleteByUser(context.Background(), "acme", ""); err == nil {
		t.Error("DeleteByUser must reject empty userID")
	}
}

// spec: §12.1 line 5 / §12.8 Phase 4 — DeleteByTenant hard-deletes
// every credential row belonging to the tenant.
func TestDeleteByTenantRemovesAll_spec_12_1(t *testing.T) {
	s := newStore()
	ctx := context.Background()
	_, _ = s.Register(ctx, "acme", "alice", credential.ProviderAnthropicDirect, "", "sk-1")
	_, _ = s.Register(ctx, "acme", "bob", credential.ProviderGitHub, "", "gh-1")
	_, _ = s.Register(ctx, "globex", "carol", credential.ProviderAnthropicDirect, "", "sk-2")
	n, err := s.DeleteByTenant(ctx, "acme")
	if err != nil {
		t.Fatalf("DeleteByTenant: %v", err)
	}
	if n != 2 {
		t.Errorf("DeleteByTenant should remove 2 acme rows, got %d", n)
	}
	if got, _ := s.List(ctx, "globex", "carol"); len(got) != 1 {
		t.Errorf("globex/carol credential should survive: %d", len(got))
	}
}

// spec: §12.1 line 5 — re-registering after DeleteByUser yields a
// fresh credential ref (no scope-key residue).
func TestDeleteByUserClearsScopeIndex_spec_12_1(t *testing.T) {
	s := newStore()
	ctx := context.Background()
	c1, _ := s.Register(ctx, "acme", "alice", credential.ProviderGitHub, "", "gh-1")
	_, _ = s.DeleteByUser(ctx, "acme", "alice")
	c2, err := s.Register(ctx, "acme", "alice", credential.ProviderGitHub, "", "gh-2")
	if err != nil {
		t.Fatalf("re-Register: %v", err)
	}
	if c1.Ref == c2.Ref {
		t.Errorf("DeleteByUser must purge scope index; got reused ref %q", c1.Ref)
	}
	if _, err := s.Get(ctx, "acme", c1.Ref); !errors.Is(err, credentialstore.ErrNotFound) {
		t.Errorf("old ref should be gone: %v", err)
	}
}

// TestLookupResolvesFourTuple_spec_4_9_1347 covers the §4.9 session-creation
// resolution Lookup: it finds the active credential by
// (tenant, user, provider, environment), reports the revoked status (the
// caller treats it as not-found), and returns ErrNotFound on a miss.
func TestLookupResolvesFourTuple_spec_4_9_1347(t *testing.T) {
	store := newStore()
	ctx := context.Background()
	reg, err := store.Register(ctx, "acme", "alice", credential.ProviderAnthropicDirect, "", "sk-ant")
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	got, err := store.Lookup(ctx, "acme", "alice", credential.ProviderAnthropicDirect, "")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if got.Ref != reg.Ref || got.Secret != "sk-ant" || got.Status != credentialstore.StatusActive {
		t.Fatalf("Lookup got %+v, want active ref=%s secret=sk-ant", got, reg.Ref)
	}

	// Wrong user, wrong provider, and wrong environment all miss.
	for _, miss := range []struct {
		tenant, user, env string
		p                 credential.Provider
	}{
		{"acme", "bob", "", credential.ProviderAnthropicDirect},
		{"acme", "alice", "", credential.ProviderVertexAI},
		{"acme", "alice", "prod", credential.ProviderAnthropicDirect},
		{"globex", "alice", "", credential.ProviderAnthropicDirect},
	} {
		if _, err := store.Lookup(ctx, miss.tenant, miss.user, miss.p, miss.env); !errors.Is(err, credentialstore.ErrNotFound) {
			t.Errorf("Lookup(%v) err = %v, want ErrNotFound", miss, err)
		}
	}

	// A revoked credential is still returned by Lookup with its status; the
	// resolution path decides to skip it (§4.9 line 1379).
	if _, err := store.Revoke(ctx, "acme", reg.Ref); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	rev, err := store.Lookup(ctx, "acme", "alice", credential.ProviderAnthropicDirect, "")
	if err != nil {
		t.Fatalf("Lookup after revoke: %v", err)
	}
	if rev.Status != credentialstore.StatusRevoked {
		t.Fatalf("Lookup status = %q, want revoked", rev.Status)
	}
}
