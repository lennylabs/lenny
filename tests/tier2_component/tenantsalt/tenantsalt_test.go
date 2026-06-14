//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §12.8 erasure_salt envelope-encryption path on
// the Postgres-backed tenant registry: the salt round-trips through a
// KMS-wrapped BYTEA column, is stored as ciphertext (never plaintext), is
// destroyed to NULL by the immediate-deletion rule, and is rejected
// fail-closed when no KMS provider is wired. F-12.8.5.
//
// This test lives in its own package rather than tests/tier2_component/stores
// because that package currently has an unrelated compile break.
package tenantsalt_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	tenantpg "github.com/lennylabs/lenny/pkg/gateway/tenantstore/pgstore"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

func startPG(t *testing.T) *containers.Postgres {
	t.Helper()
	return containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
}

// spec: §12.8 lines 845-850 — erasure_salt is envelope-encrypted at rest,
// round-trips, is stored as ciphertext, and is destroyed to NULL. F-12.8.5.
// diagnosis: a failure means the tenant erasure_salt is persisted in
// plaintext rather than envelope-encrypted, or is not destroyed to NULL,
// breaking the §12.8 crypto-erasure guarantee.
func TestTenantStoreErasureSaltEnvelope(t *testing.T) {
	t.Parallel()
	pg := startPG(t)
	ctx := context.Background()
	provider, err := kms.NewLocalRandom()
	if err != nil {
		t.Fatalf("kms provider: %v", err)
	}
	store := tenantpg.New(pg.Pool, tenantpg.WithKMS(provider))

	const id = "acme-salt"
	if err := store.Create(ctx, tenantstore.Tenant{ID: id, DisplayName: "Acme"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	salt := bytes.Repeat([]byte{0x5A}, 32)
	if _, err := store.Update(ctx, id, func(tn *tenantstore.Tenant) error {
		tn.ErasureSalt = append([]byte(nil), salt...)
		return nil
	}); err != nil {
		t.Fatalf("Update set salt: %v", err)
	}

	// Get round-trips the decrypted salt.
	got, err := store.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got.ErasureSalt, salt) {
		t.Fatalf("salt round-trip mismatch: got %x want %x", got.ErasureSalt, salt)
	}

	// The stored column is the KMS-wrapped envelope blob, not the plaintext.
	var raw []byte
	if err := pg.Pool.QueryRow(ctx, `SELECT erasure_salt FROM tenants WHERE id = $1`, id).Scan(&raw); err != nil {
		t.Fatalf("read raw erasure_salt: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("erasure_salt column is empty after a salt write")
	}
	if bytes.Contains(raw, salt) {
		t.Fatal("§12.8 line 845: the stored erasure_salt must be ciphertext, not the plaintext salt")
	}

	// List does not decrypt the salt (§12.8 line 847).
	rows, err := store.List(ctx, tenantstore.ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, r := range rows {
		if r.ID == id && r.ErasureSalt != nil {
			t.Error("§12.8 line 847: List must not decrypt the erasure_salt")
		}
	}

	// §12.8 line 850: destroying the salt nulls the column.
	if _, err := store.Update(ctx, id, func(tn *tenantstore.Tenant) error {
		tn.ErasureSalt = nil
		return nil
	}); err != nil {
		t.Fatalf("Update destroy salt: %v", err)
	}
	var afterDestroy []byte
	if err := pg.Pool.QueryRow(ctx, `SELECT erasure_salt FROM tenants WHERE id = $1`, id).Scan(&afterDestroy); err != nil {
		t.Fatalf("read raw erasure_salt after destroy: %v", err)
	}
	if afterDestroy != nil {
		t.Fatalf("§12.8 line 850: erasure_salt must be NULL after destroy, got %x", afterDestroy)
	}
	reread, err := store.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get after destroy: %v", err)
	}
	if reread.ErasureSalt != nil {
		t.Errorf("destroyed salt re-opened to %x, want nil", reread.ErasureSalt)
	}
}

// spec: §12.8 line 845 — a non-empty salt write with no KMS provider is
// rejected rather than persisted in plaintext. F-12.8.5.
// diagnosis: a failure means a salt write with no KMS provider is
// persisted in plaintext instead of being rejected, so the store does
// not fail closed on the credential-handling path.
func TestTenantStoreErasureSaltFailsClosedWithoutKMS(t *testing.T) {
	t.Parallel()
	pg := startPG(t)
	ctx := context.Background()
	store := tenantpg.New(pg.Pool) // no KMS

	const id = "acme-nokms"
	if err := store.Create(ctx, tenantstore.Tenant{ID: id}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.Update(ctx, id, func(tn *tenantstore.Tenant) error {
		tn.ErasureSalt = bytes.Repeat([]byte{1}, 32)
		return nil
	}); err == nil {
		t.Fatal("§12.8 line 845: a salt write without a KMS provider must fail closed")
	}
}
