//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §4.9 / §15.1 end-user credential registry,
// exercising the Postgres-backed pkg/gateway/credentialstore/pgstore
// against a real container with the production migrations applied.
// Covers the register/get round-trip, re-registration replacing the
// secret in place, the sentinel error, cross-tenant isolation, the
// Rotate/Revoke/Delete lifecycle, and the user-scoped ref-ordered List.
package stores_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentialstore"
	credentialpg "github.com/lennylabs/lenny/pkg/gateway/credentialstore/pgstore"
)

// spec: 4.9
// diagnosis: the Postgres-backed end-user credential registry in
// pkg/gateway/credentialstore/pgstore did not behave as specified.
// Register and Get must round-trip a credential including its secret,
// an unknown provider must be rejected, re-registering the same
// (tenant, user, provider) triple must reuse the ref and replace the
// secret per §15.1, Get must return ErrNotFound for a missing or
// cross-tenant ref, Rotate must replace the secret and refresh
// RotatedAt, Revoke must mark the credential revoked, Delete must
// remove the row and free the triple, and List must return the user's
// credentials ref-ordered.
func TestCredentialStoreContract(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := credentialpg.New(pg.Pool)
	ctx := context.Background()

	t.Run("register and get round-trip", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		c, err := store.Register(ctx, tenant, "alice", credential.ProviderAnthropicDirect, "sk-secret")
		if err != nil {
			t.Fatalf("Register: %v", err)
		}
		if c.Ref == "" || c.Status != credentialstore.StatusActive {
			t.Errorf("registered credential malformed: %+v", c)
		}
		if c.CreatedAt.IsZero() {
			t.Error("Register must stamp CreatedAt")
		}
		if !c.RotatedAt.IsZero() || !c.RevokedAt.IsZero() {
			t.Errorf("fresh credential must have zero RotatedAt/RevokedAt: %+v", c)
		}
		got, err := store.Get(ctx, tenant, c.Ref)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.TenantID != tenant || got.UserID != "alice" ||
			got.Provider != credential.ProviderAnthropicDirect ||
			got.Secret != "sk-secret" || got.Status != credentialstore.StatusActive {
			t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", got, c)
		}
		if !got.CreatedAt.Equal(c.CreatedAt) {
			t.Errorf("CreatedAt mismatch: got %v want %v", got.CreatedAt, c.CreatedAt)
		}
	})

	t.Run("register rejects an unknown provider", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		if _, err := store.Register(ctx, tenant, "alice", credential.Provider("made-up"), "x"); err == nil {
			t.Error("Register with an unknown provider should be rejected")
		}
	})

	t.Run("re-register reuses the ref and replaces the secret", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		c1, err := store.Register(ctx, tenant, "alice", credential.ProviderGitHub, "secret-v1")
		if err != nil {
			t.Fatalf("first Register: %v", err)
		}
		c2, err := store.Register(ctx, tenant, "alice", credential.ProviderGitHub, "secret-v2")
		if err != nil {
			t.Fatalf("re-Register: %v", err)
		}
		// Same triple → same ref, refreshed secret + RotatedAt.
		if c1.Ref != c2.Ref {
			t.Errorf("re-register should reuse ref: %q vs %q", c1.Ref, c2.Ref)
		}
		if c2.RotatedAt.IsZero() {
			t.Error("re-register must refresh RotatedAt")
		}
		got, err := store.Get(ctx, tenant, c1.Ref)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Secret != "secret-v2" {
			t.Errorf("secret not replaced: got %q, want secret-v2", got.Secret)
		}
	})

	t.Run("re-register reactivates a revoked credential", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		c, err := store.Register(ctx, tenant, "alice", credential.ProviderGitHub, "x")
		if err != nil {
			t.Fatalf("Register: %v", err)
		}
		if _, err := store.Revoke(ctx, tenant, c.Ref); err != nil {
			t.Fatalf("Revoke: %v", err)
		}
		again, err := store.Register(ctx, tenant, "alice", credential.ProviderGitHub, "y")
		if err != nil {
			t.Fatalf("re-Register: %v", err)
		}
		if again.Ref != c.Ref {
			t.Errorf("re-register should reuse ref: %q vs %q", c.Ref, again.Ref)
		}
		if again.Status != credentialstore.StatusActive || !again.RevokedAt.IsZero() {
			t.Errorf("re-register must clear the revoked state: %+v", again)
		}
	})

	t.Run("get missing and cross-tenant return ErrNotFound", func(t *testing.T) {
		owner := freshTenant(t, ctx, pg)
		intruder := freshTenant(t, ctx, pg)
		if _, err := store.Get(ctx, owner, "cred_missing"); !errors.Is(err, credentialstore.ErrNotFound) {
			t.Errorf("Get missing: got %v, want ErrNotFound", err)
		}
		c, err := store.Register(ctx, owner, "alice", credential.ProviderGitHub, "x")
		if err != nil {
			t.Fatalf("Register: %v", err)
		}
		if _, err := store.Get(ctx, intruder, c.Ref); !errors.Is(err, credentialstore.ErrNotFound) {
			t.Errorf("cross-tenant Get: got %v, want ErrNotFound", err)
		}
		// The same triple under another tenant is independent.
		if _, err := store.Register(ctx, intruder, "alice", credential.ProviderGitHub, "y"); err != nil {
			t.Errorf("same triple, different tenant: got %v, want nil", err)
		}
	})

	t.Run("rotate replaces the secret and refreshes RotatedAt", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		c, err := store.Register(ctx, tenant, "alice", credential.ProviderGitHub, "old")
		if err != nil {
			t.Fatalf("Register: %v", err)
		}
		rotated, err := store.Rotate(ctx, tenant, c.Ref, "new")
		if err != nil {
			t.Fatalf("Rotate: %v", err)
		}
		if rotated.Secret != "new" || rotated.RotatedAt.IsZero() {
			t.Errorf("rotate did not apply: %+v", rotated)
		}
		if rotated.Status != credentialstore.StatusActive {
			t.Errorf("rotate must leave the credential active: %+v", rotated)
		}
		persisted, _ := store.Get(ctx, tenant, c.Ref)
		if persisted.Secret != "new" {
			t.Errorf("rotate not persisted: got %q, want new", persisted.Secret)
		}
		if _, err := store.Rotate(ctx, tenant, "cred_missing", "x"); !errors.Is(err, credentialstore.ErrNotFound) {
			t.Errorf("Rotate missing: got %v, want ErrNotFound", err)
		}
	})

	t.Run("revoke marks the credential revoked", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		c, err := store.Register(ctx, tenant, "alice", credential.ProviderGitHub, "x")
		if err != nil {
			t.Fatalf("Register: %v", err)
		}
		revoked, err := store.Revoke(ctx, tenant, c.Ref)
		if err != nil {
			t.Fatalf("Revoke: %v", err)
		}
		if revoked.Status != credentialstore.StatusRevoked || revoked.RevokedAt.IsZero() {
			t.Errorf("revoke not applied: %+v", revoked)
		}
		persisted, _ := store.Get(ctx, tenant, c.Ref)
		if persisted.Status != credentialstore.StatusRevoked || persisted.RevokedAt.IsZero() {
			t.Errorf("revoke not persisted: %+v", persisted)
		}
		if _, err := store.Revoke(ctx, tenant, "cred_missing"); !errors.Is(err, credentialstore.ErrNotFound) {
			t.Errorf("Revoke missing: got %v, want ErrNotFound", err)
		}
	})

	t.Run("delete removes the row and frees the triple", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		c, err := store.Register(ctx, tenant, "alice", credential.ProviderGitHub, "x")
		if err != nil {
			t.Fatalf("Register: %v", err)
		}
		if err := store.Delete(ctx, tenant, c.Ref); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := store.Get(ctx, tenant, c.Ref); !errors.Is(err, credentialstore.ErrNotFound) {
			t.Errorf("Get after Delete: got %v, want ErrNotFound", err)
		}
		if err := store.Delete(ctx, tenant, c.Ref); !errors.Is(err, credentialstore.ErrNotFound) {
			t.Errorf("Delete missing: got %v, want ErrNotFound", err)
		}
		// After delete the triple is free: re-register mints a fresh ref.
		c2, err := store.Register(ctx, tenant, "alice", credential.ProviderGitHub, "y")
		if err != nil {
			t.Fatalf("re-Register after delete: %v", err)
		}
		if c2.Ref == c.Ref {
			t.Error("re-register after delete should mint a fresh ref")
		}
	})

	t.Run("list is user-scoped and ref-ordered", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		if _, err := store.Register(ctx, tenant, "alice", credential.ProviderGitHub, "x"); err != nil {
			t.Fatalf("Register alice/github: %v", err)
		}
		if _, err := store.Register(ctx, tenant, "alice", credential.ProviderAWSBedrock, "y"); err != nil {
			t.Fatalf("Register alice/bedrock: %v", err)
		}
		if _, err := store.Register(ctx, tenant, "bob", credential.ProviderGitHub, "z"); err != nil {
			t.Fatalf("Register bob/github: %v", err)
		}
		aliceCreds, err := store.List(ctx, tenant, "alice")
		if err != nil {
			t.Fatalf("List alice: %v", err)
		}
		if len(aliceCreds) != 2 {
			t.Fatalf("alice should have 2 credentials: got %d", len(aliceCreds))
		}
		if aliceCreds[0].Ref >= aliceCreds[1].Ref {
			t.Errorf("List must be ref-ascending: got %q, %q", aliceCreds[0].Ref, aliceCreds[1].Ref)
		}
		bobCreds, err := store.List(ctx, tenant, "bob")
		if err != nil {
			t.Fatalf("List bob: %v", err)
		}
		if len(bobCreds) != 1 {
			t.Errorf("bob should have 1 credential: got %d", len(bobCreds))
		}
		// A user with no credentials yields an empty, non-nil slice.
		empty, err := store.List(ctx, tenant, "carol")
		if err != nil {
			t.Fatalf("List carol: %v", err)
		}
		if empty == nil || len(empty) != 0 {
			t.Errorf("List for a credential-less user: got %v, want empty slice", empty)
		}
	})

	t.Run("list is tenant-scoped", func(t *testing.T) {
		owner := freshTenant(t, ctx, pg)
		intruder := freshTenant(t, ctx, pg)
		if _, err := store.Register(ctx, owner, "alice", credential.ProviderGitHub, "x"); err != nil {
			t.Fatalf("Register: %v", err)
		}
		isolated, err := store.List(ctx, intruder, "alice")
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(isolated) != 0 {
			t.Errorf("List must not cross tenants: got %d rows", len(isolated))
		}
	})
}
