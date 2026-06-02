//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §12.5 artifact_store catalog. Exercises
// pkg/blobstore/artifactcatalog against a real Postgres container
// with migration 0049 applied.
package stores_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore/artifactcatalog"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

func newArtifactCatalog(t *testing.T) (*artifactcatalog.PgStore, *containers.Postgres) {
	t.Helper()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	clock := func() time.Time { return time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC) }
	return artifactcatalog.New(pg.Pool, clock), pg
}

func seedArtifactTenant(t *testing.T, ctx context.Context, pg *containers.Postgres, id string) {
	t.Helper()
	if _, err := pg.Pool.Exec(ctx,
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x00')`, id); err != nil {
		t.Fatalf("seed tenant %q: %v", id, err)
	}
}

// spec: §12.5 (insert + get round-trips a catalog row)
// diagnosis: §12.5 artifact_store catalog table — Postgres-backed soft-delete + tombstone lifecycle exercised against a real PG container.
func TestArtifactCatalogContract(t *testing.T) {
	t.Parallel()
	cat, pg := newArtifactCatalog(t)
	ctx := context.Background()
	seedArtifactTenant(t, ctx, pg, "acme")

	t.Run("Insert + Get round-trip", func(t *testing.T) {
		row := artifactcatalog.Record{
			URI:         "lenny-blob://acme/sess_1/p_1",
			TenantID:    "acme",
			SessionID:   "sess_1",
			PartID:      "p_1",
			MimeType:    "application/json",
			SizeBytes:   42,
			KMSKeyAlias: "tenant:acme",
		}
		if err := cat.Insert(ctx, row); err != nil {
			t.Fatalf("Insert: %v", err)
		}
		got, err := cat.Get(ctx, row.URI)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.State != artifactcatalog.StateLive {
			t.Errorf("State = %q, want live (default)", got.State)
		}
		if got.KMSKeyAlias != "tenant:acme" {
			t.Errorf("KMSKeyAlias = %q, want tenant:acme", got.KMSKeyAlias)
		}
		if got.SizeBytes != 42 {
			t.Errorf("SizeBytes = %d, want 42", got.SizeBytes)
		}
	})

	t.Run("SoftDelete transitions state and starts the retention timer", func(t *testing.T) {
		uri := "lenny-blob://acme/sess_2/p_2"
		if err := cat.Insert(ctx, artifactcatalog.Record{
			URI: uri, TenantID: "acme", SessionID: "sess_2", PartID: "p_2",
		}); err != nil {
			t.Fatalf("Insert: %v", err)
		}
		deadline := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
		if err := cat.SoftDelete(ctx, uri, deadline); err != nil {
			t.Fatalf("SoftDelete: %v", err)
		}
		got, _ := cat.Get(ctx, uri)
		if got.State != artifactcatalog.StateSoftDeleted {
			t.Errorf("State = %q, want soft_deleted", got.State)
		}
		if !got.TombstoneDeadline.Equal(deadline) {
			t.Errorf("TombstoneDeadline = %v, want %v", got.TombstoneDeadline, deadline)
		}
		if got.SoftDeletedAt.IsZero() {
			t.Errorf("SoftDeletedAt not set")
		}
		// A second SoftDelete on the same row reports ErrNotFound
		// because the state guard rejects already-soft-deleted rows.
		if err := cat.SoftDelete(ctx, uri, deadline); !errors.Is(err, artifactcatalog.ErrNotFound) {
			t.Errorf("second SoftDelete = %v, want ErrNotFound", err)
		}
	})

	t.Run("Tombstone transitions soft_deleted to tombstoned", func(t *testing.T) {
		uri := "lenny-blob://acme/sess_3/p_3"
		if err := cat.Insert(ctx, artifactcatalog.Record{
			URI: uri, TenantID: "acme", SessionID: "sess_3", PartID: "p_3",
		}); err != nil {
			t.Fatalf("Insert: %v", err)
		}
		if err := cat.SoftDelete(ctx, uri, time.Now().Add(24*time.Hour)); err != nil {
			t.Fatalf("SoftDelete: %v", err)
		}
		if err := cat.Tombstone(ctx, uri); err != nil {
			t.Fatalf("Tombstone: %v", err)
		}
		got, _ := cat.Get(ctx, uri)
		if got.State != artifactcatalog.StateTombstoned {
			t.Errorf("State = %q, want tombstoned", got.State)
		}
	})

	t.Run("HardPruneExpired removes tombstoned rows past the deadline", func(t *testing.T) {
		uri := "lenny-blob://acme/sess_4/p_4"
		if err := cat.Insert(ctx, artifactcatalog.Record{
			URI: uri, TenantID: "acme", SessionID: "sess_4", PartID: "p_4",
		}); err != nil {
			t.Fatalf("Insert: %v", err)
		}
		past := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
		if err := cat.SoftDelete(ctx, uri, past); err != nil {
			t.Fatalf("SoftDelete: %v", err)
		}
		if err := cat.Tombstone(ctx, uri); err != nil {
			t.Fatalf("Tombstone: %v", err)
		}
		now := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
		n, err := cat.HardPruneExpired(ctx, now)
		if err != nil {
			t.Fatalf("HardPruneExpired: %v", err)
		}
		if n != 1 {
			t.Errorf("pruned = %d, want 1", n)
		}
		if _, err := cat.Get(ctx, uri); !errors.Is(err, artifactcatalog.ErrNotFound) {
			t.Errorf("Get after prune = %v, want ErrNotFound", err)
		}
	})

	// Regression for F-12.5.25: the production retention GC sweep
	// soft-deletes rows but never runs the optional Tombstone step, so
	// hard-prune MUST remove soft_deleted rows past their deadline
	// directly. A 'tombstoned'-only predicate would leak every
	// retention-swept row. spec: §12.5 lines 333, 341.
	t.Run("HardPruneExpired removes soft_deleted rows without an intervening Tombstone", func(t *testing.T) {
		uri := "lenny-blob://acme/sess_sd/p_sd"
		if err := cat.Insert(ctx, artifactcatalog.Record{
			URI: uri, TenantID: "acme", SessionID: "sess_sd", PartID: "p_sd",
		}); err != nil {
			t.Fatalf("Insert: %v", err)
		}
		past := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
		if err := cat.SoftDelete(ctx, uri, past); err != nil {
			t.Fatalf("SoftDelete: %v", err)
		}
		got, _ := cat.Get(ctx, uri)
		if got.State != artifactcatalog.StateSoftDeleted {
			t.Fatalf("State = %q, want soft_deleted (no Tombstone called)", got.State)
		}
		now := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
		n, err := cat.HardPruneExpired(ctx, now)
		if err != nil {
			t.Fatalf("HardPruneExpired: %v", err)
		}
		if n != 1 {
			t.Errorf("pruned = %d, want 1 (soft_deleted row past deadline)", n)
		}
		if _, err := cat.Get(ctx, uri); !errors.Is(err, artifactcatalog.ErrNotFound) {
			t.Errorf("Get after prune = %v, want ErrNotFound", err)
		}
	})

	t.Run("HardPruneExpired retains soft_deleted rows before the deadline", func(t *testing.T) {
		uri := "lenny-blob://acme/sess_future/p_f"
		if err := cat.Insert(ctx, artifactcatalog.Record{
			URI: uri, TenantID: "acme", SessionID: "sess_future", PartID: "p_f",
		}); err != nil {
			t.Fatalf("Insert: %v", err)
		}
		future := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
		if err := cat.SoftDelete(ctx, uri, future); err != nil {
			t.Fatalf("SoftDelete: %v", err)
		}
		now := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
		n, err := cat.HardPruneExpired(ctx, now)
		if err != nil {
			t.Fatalf("HardPruneExpired: %v", err)
		}
		if n != 0 {
			t.Errorf("pruned = %d, want 0 (deadline in the future)", n)
		}
		if _, err := cat.Get(ctx, uri); err != nil {
			t.Errorf("row removed before its deadline: %v", err)
		}
	})

	t.Run("HardPruneExpired skips legal-held rows", func(t *testing.T) {
		uri := "lenny-blob://acme/sess_5/p_5"
		if err := cat.Insert(ctx, artifactcatalog.Record{
			URI: uri, TenantID: "acme", SessionID: "sess_5", PartID: "p_5",
		}); err != nil {
			t.Fatalf("Insert: %v", err)
		}
		past := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
		_ = cat.SoftDelete(ctx, uri, past)
		_ = cat.Tombstone(ctx, uri)
		if err := cat.SetLegalHold(ctx, uri, true); err != nil {
			t.Fatalf("SetLegalHold: %v", err)
		}
		now := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
		n, err := cat.HardPruneExpired(ctx, now)
		if err != nil {
			t.Fatalf("HardPruneExpired: %v", err)
		}
		if n != 0 {
			t.Errorf("pruned = %d, want 0 (legal-held row must survive)", n)
		}
		got, err := cat.Get(ctx, uri)
		if err != nil {
			t.Errorf("legal-held row missing after prune: %v", err)
		}
		if !got.LegalHold {
			t.Errorf("legal_hold = false, want true")
		}
	})

	// F-12.5.23: the catalog-driven hard-prune sweep lists the URIs
	// past their TTL (so the GC deletes exactly those bucket objects)
	// then drops the rows by URI. ListPrunable must report the same set
	// HardPruneExpired would remove, and skip future-deadline / live /
	// legal-held rows. spec: §12.5 lines 320, 341.
	t.Run("ListPrunable + HardPruneURIs drive a targeted catalog-driven prune", func(t *testing.T) {
		ripe := "lenny-blob://acme/sess_lp_ripe/p"
		future := "lenny-blob://acme/sess_lp_future/p"
		live := "lenny-blob://acme/sess_lp_live/p"
		held := "lenny-blob://acme/sess_lp_held/p"
		for _, uri := range []string{ripe, future, live, held} {
			if err := cat.Insert(ctx, artifactcatalog.Record{
				URI: uri, TenantID: "acme", SessionID: uri, PartID: "p",
			}); err != nil {
				t.Fatalf("Insert %s: %v", uri, err)
			}
		}
		past := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
		later := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
		if err := cat.SoftDelete(ctx, ripe, past); err != nil {
			t.Fatalf("SoftDelete ripe: %v", err)
		}
		if err := cat.SoftDelete(ctx, future, later); err != nil {
			t.Fatalf("SoftDelete future: %v", err)
		}
		_ = cat.SoftDelete(ctx, held, past)
		if err := cat.SetLegalHold(ctx, held, true); err != nil {
			t.Fatalf("SetLegalHold: %v", err)
		}
		// live is left in the live state.

		now := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
		uris, err := cat.ListPrunable(ctx, now)
		if err != nil {
			t.Fatalf("ListPrunable: %v", err)
		}
		if len(uris) != 1 || uris[0] != ripe {
			t.Fatalf("ListPrunable = %v, want [%s]", uris, ripe)
		}

		// Dropping the reported URI removes the ripe row.
		n, err := cat.HardPruneURIs(ctx, uris)
		if err != nil {
			t.Fatalf("HardPruneURIs: %v", err)
		}
		if n != 1 {
			t.Errorf("HardPruneURIs = %d, want 1", n)
		}
		if _, err := cat.Get(ctx, ripe); !errors.Is(err, artifactcatalog.ErrNotFound) {
			t.Errorf("ripe row survived prune: %v", err)
		}
		// The live, future, and held rows all survive.
		for _, uri := range []string{future, live, held} {
			if _, err := cat.Get(ctx, uri); err != nil {
				t.Errorf("row %s removed unexpectedly: %v", uri, err)
			}
		}
		// HardPruneURIs will not drop a live row even if named.
		if n, err := cat.HardPruneURIs(ctx, []string{live}); err != nil || n != 0 {
			t.Errorf("HardPruneURIs(live) = (%d, %v), want (0, nil)", n, err)
		}
	})

	t.Run("ListBySession returns the session's rows in URI order", func(t *testing.T) {
		tenant := "acme"
		session := "sess_list"
		for _, uri := range []string{
			"lenny-blob://acme/sess_list/p_z",
			"lenny-blob://acme/sess_list/p_a",
			"lenny-blob://acme/sess_list/p_m",
		} {
			if err := cat.Insert(ctx, artifactcatalog.Record{
				URI: uri, TenantID: tenant, SessionID: session,
			}); err != nil {
				t.Fatalf("Insert %s: %v", uri, err)
			}
		}
		rows, err := cat.ListBySession(ctx, tenant, session)
		if err != nil {
			t.Fatalf("ListBySession: %v", err)
		}
		if len(rows) != 3 {
			t.Fatalf("len = %d, want 3", len(rows))
		}
		want := []string{
			"lenny-blob://acme/sess_list/p_a",
			"lenny-blob://acme/sess_list/p_m",
			"lenny-blob://acme/sess_list/p_z",
		}
		for i, u := range want {
			if rows[i].URI != u {
				t.Errorf("rows[%d].URI = %q, want %q", i, rows[i].URI, u)
			}
		}
	})
}
