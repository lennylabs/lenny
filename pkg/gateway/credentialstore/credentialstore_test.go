// SPDX-License-Identifier: MIT

package credentialstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentialstore"
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
