// SPDX-License-Identifier: MIT

package migrations_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/migrations"
	embpostgres "github.com/lennylabs/lenny/tests/testinfra/embpg"
)

// TestCredentialLeasesExpiresAtMigrationSQL_spec_4_9 asserts the static SQL
// surface of migration 0175: the up adds a nullable expires_at TIMESTAMPTZ
// projection column to credential_leases plus the
// credential_leases_expires_at_idx index the sweep and existence-count
// queries filter on; the down drops both. The column must stay nullable so a
// row predating the migration reads NULL (an unknown-expiry sentinel the
// existence guard counts as active to fail closed) until the later backfill
// pass populates it.
//
// spec: §4.9 (deny-list entries expire when the credential's natural lease
// TTL lapses; the startup rebuild counts leases still active as of a cutoff)
func TestCredentialLeasesExpiresAtMigrationSQL_spec_4_9(t *testing.T) {
	up, err := migrations.FS.ReadFile("0175_credential_leases_expires_at.up.sql")
	if err != nil {
		t.Fatalf("read 0175 up: %v", err)
	}
	ups := string(up)
	for _, want := range []string{
		"ALTER TABLE credential_leases",
		"ADD COLUMN expires_at TIMESTAMPTZ",
		"CREATE INDEX credential_leases_expires_at_idx",
		"ON credential_leases (expires_at)",
	} {
		if !strings.Contains(ups, want) {
			t.Errorf("0175 up missing %q", want)
		}
	}
	// The projection column must be nullable: a pre-migration row carries a
	// NULL expires_at that the existence guard counts as active. A NOT NULL
	// DEFAULT would forge a concrete expiry on every legacy row.
	if strings.Contains(ups, "expires_at TIMESTAMPTZ NOT NULL") {
		t.Error("0175 up must keep expires_at nullable; a NOT NULL default forges an expiry on pre-backfill rows")
	}

	down, err := migrations.FS.ReadFile("0175_credential_leases_expires_at.down.sql")
	if err != nil {
		t.Fatalf("read 0175 down: %v", err)
	}
	downs := string(down)
	for _, want := range []string{
		"DROP INDEX IF EXISTS credential_leases_expires_at_idx",
		"DROP COLUMN IF EXISTS expires_at",
	} {
		if !strings.Contains(downs, want) {
			t.Errorf("0175 down missing %q", want)
		}
	}
}

// TestCredentialLeasesExpiresAtMigrationDB_spec_4_9 applies the migration
// chain through 0175 against a real Postgres and verifies credential_leases
// gains a nullable expires_at column and the credential_leases_expires_at_idx
// index, that a lease row inserted without an explicit expires_at reads NULL
// (the pre-backfill unknown-expiry sentinel), and that the down drops both.
//
// diagnosis: a failure means migration 0175 did not add the §4.9 expires_at
// projection column or its index, made the column non-nullable (forging a
// concrete expiry on pre-backfill rows the existence guard must treat as
// active), or the down did not reverse it; the deny-list sweep and the
// fail-closed lease-existence count would be unable to reason about lease
// expiry without decrypting the envelope-encrypted lease body.
//
// spec: §4.9 (expires_at projection enabling the bounded expired-lease sweep
// and the fail-closed lease-existence count)
func TestCredentialLeasesExpiresAtMigrationDB_spec_4_9(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	// Port 0 asks the kernel for a free ephemeral port so this test does not
	// collide with other embedded-Postgres tests under parallel execution
	// (§17.4 forbids hardcoded ports).
	pg := embpostgres.New(embpostgres.Config{
		DataDir:      t.TempDir(),
		Port:         0,
		Database:     "lenny",
		Username:     "lenny",
		Password:     "lenny",
		StartTimeout: 3 * time.Minute,
	})
	if err := pg.Start(); err != nil {
		t.Fatalf("embedded postgres Start: %v", err)
	}
	defer func() { _ = pg.Stop() }()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, pg.DSN())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	// 0001 (base schema), 0002 (the lenny_app role the credential_leases
	// grants reference), 0038 (the credential_leases table), and 0129 (the
	// §12.9 envelope conversion that makes the lease body ciphertext, which
	// is why expires_at must be projected out as a plain column).
	applyMigrations(
		t, ctx, pool,
		"0001_initial_schema.up.sql",
		"0002_rls_immutability_roles.up.sql",
		"0038_credential_leases.up.sql",
		"0129_credential_leases_envelope.up.sql",
		"0175_credential_leases_expires_at.up.sql",
	)

	if !columnExists(t, ctx, pool, "credential_leases", "expires_at") {
		t.Fatal("credential_leases.expires_at must be added by 0175")
	}
	if !columnNullable(t, ctx, pool, "credential_leases", "expires_at") {
		t.Error("credential_leases.expires_at must be nullable (the pre-backfill unknown-expiry sentinel)")
	}
	if !credentialLeasesIndexExists(t, ctx, pool, "credential_leases_expires_at_idx") {
		t.Error("credential_leases_expires_at_idx must be created by 0175")
	}

	// A lease row inserted with no explicit expires_at reads NULL: a
	// pre-backfill row's expiry is unknown until the later backfill pass
	// decrypts the envelope.
	if _, err := pool.Exec(ctx,
		`INSERT INTO credential_leases (lease_id, delivery_mode, lease)
		 VALUES ('lease-0175', 'direct', '\x')`); err != nil {
		t.Fatalf("insert lease row: %v", err)
	}
	var expiresAt *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT expires_at FROM credential_leases WHERE lease_id = 'lease-0175'`).
		Scan(&expiresAt); err != nil {
		t.Fatalf("read back lease row: %v", err)
	}
	if expiresAt != nil {
		t.Errorf("expires_at default: want NULL (unknown-expiry sentinel), got %v", *expiresAt)
	}

	// The down reverses the column and the index against a live database.
	applyMigrations(t, ctx, pool, "0175_credential_leases_expires_at.down.sql")
	if columnExists(t, ctx, pool, "credential_leases", "expires_at") {
		t.Error("down must drop credential_leases.expires_at")
	}
	if credentialLeasesIndexExists(t, ctx, pool, "credential_leases_expires_at_idx") {
		t.Error("down must drop credential_leases_expires_at_idx")
	}
}

// credentialLeasesIndexExists reports whether the named index is present.
func credentialLeasesIndexExists(t *testing.T, ctx context.Context, pool *pgxpool.Pool, index string) bool {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (
			SELECT 1 FROM pg_indexes WHERE indexname = $1
		)`, index).Scan(&exists); err != nil {
		t.Fatalf("query index %s: %v", index, err)
	}
	return exists
}
