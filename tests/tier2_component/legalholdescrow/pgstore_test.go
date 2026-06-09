//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §12.8 legal_hold_escrow_records store
// (pkg/legalholdescrow/pgstore) against a real Postgres container with the
// production migrations (including 0166) applied: the Save round-trip, the
// active-set lookups by session and by artifact, tenant isolation, the
// idempotent re-save, and the MarkReleased transition that excludes a row
// from the active set.
package legalholdescrow_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/legalholdescrow"
	"github.com/lennylabs/lenny/pkg/legalholdescrow/pgstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

func rec(tenant, session, uri, key string) legalholdescrow.Record {
	return legalholdescrow.Record{
		TenantID:        tenant,
		ResourceType:    "artifact",
		ResourceID:      "rid-" + key,
		EscrowObjectKey: key,
		EscrowRegion:    "eu-west-1",
		EscrowKEKID:     "platform:legal_hold_escrow:eu-west-1",
		TenantDeleteJob: "job-1",
		SessionID:       session,
		ArtifactURI:     uri,
		OriginalHoldSet: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		MigratedAt:      time.Date(2026, 6, 9, 9, 0, 0, 0, time.UTC),
	}
}

// spec: §12.8 lines 884-885 — the escrow record store round-trips the
// release-lookup keys and the MarkReleased transition is idempotent.
func TestLegalHoldEscrowRecordsPgStore_spec_12_8_884(t *testing.T) {
	t.Parallel()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	store := pgstore.New(pg.Pool)
	ctx := context.Background()

	for _, r := range []legalholdescrow.Record{
		rec("acme", "sess-1", "blob://acme/sess-1/a", "k1"),
		rec("acme", "sess-1", "blob://acme/sess-1/b", "k2"),
		rec("acme", "sess-2", "blob://acme/sess-2/c", "k3"),
		rec("globex", "sess-1", "blob://globex/sess-1/a", "k9"),
	} {
		if err := store.Save(ctx, r); err != nil {
			t.Fatalf("Save %s: %v", r.EscrowObjectKey, err)
		}
	}

	bySession, err := store.ActiveForSession(ctx, "acme", "sess-1")
	if err != nil {
		t.Fatalf("ActiveForSession: %v", err)
	}
	if len(bySession) != 2 {
		t.Fatalf("ActiveForSession(acme,sess-1) = %d, want 2 (tenant + session scoped)", len(bySession))
	}
	if bySession[0].EscrowRegion != "eu-west-1" || bySession[0].ArtifactURI == "" {
		t.Errorf("record fields not round-tripped: %+v", bySession[0])
	}

	byArtifact, err := store.ActiveForArtifact(ctx, "acme", "blob://acme/sess-1/a")
	if err != nil {
		t.Fatalf("ActiveForArtifact: %v", err)
	}
	if len(byArtifact) != 1 || byArtifact[0].EscrowObjectKey != "k1" {
		t.Fatalf("ActiveForArtifact = %v, want [k1]", byArtifact)
	}

	// Idempotent re-save: ON CONFLICT overwrites, no duplicate row.
	if err := store.Save(ctx, rec("acme", "sess-1", "blob://acme/sess-1/a", "k1")); err != nil {
		t.Fatalf("re-Save: %v", err)
	}
	if again, _ := store.ActiveForSession(ctx, "acme", "sess-1"); len(again) != 2 {
		t.Errorf("after re-save ActiveForSession = %d, want 2 (no duplicate)", len(again))
	}

	// Release k1: it drops out of the active set; re-release is a no-op.
	at := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	if err := store.MarkReleased(ctx, "acme", "k1", "alice@acme.com", at); err != nil {
		t.Fatalf("MarkReleased: %v", err)
	}
	if got, _ := store.ActiveForArtifact(ctx, "acme", "blob://acme/sess-1/a"); len(got) != 0 {
		t.Errorf("ActiveForArtifact after release = %d, want 0", len(got))
	}
	if got, _ := store.ActiveForSession(ctx, "acme", "sess-1"); len(got) != 1 {
		t.Errorf("ActiveForSession after releasing one = %d, want 1", len(got))
	}
	// MarkReleased is idempotent (released_at IS NULL guard).
	if err := store.MarkReleased(ctx, "acme", "k1", "bob@acme.com", at.Add(time.Hour)); err != nil {
		t.Fatalf("idempotent MarkReleased: %v", err)
	}
}
