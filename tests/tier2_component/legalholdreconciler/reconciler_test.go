//go:build component

// SPDX-License-Identifier: MIT

// Component test for the §12.8 line 739 legal-hold checkpoint-gap
// reconciler against a real Postgres container with the production
// migrations applied. It exercises the §12.5 scoping rule: a rotated
// chunk row that belongs to a §10.1 checkpoint_manifest finalised
// `partial = true` is excluded from the gap count, while a rotated chunk
// of a complete checkpoint still trips the detector.
package legalholdreconciler_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/blobstore/artifactcatalog"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	manifestpg "github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/legalholdreconciler"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// countingAppender records the audit rows the reconciler commits.
type countingAppender struct {
	rows []string
}

func (a *countingAppender) Append(_ context.Context, tenantID, eventType string, _ json.RawMessage, _ time.Time) (audit.Row, error) {
	a.rows = append(a.rows, tenantID+"|"+eventType)
	return audit.Row{}, nil
}

// countingMetrics counts gap increments per tenant.
type countingMetrics struct{ counts map[string]int }

func (m *countingMetrics) IncLegalHoldCheckpointGap(tenantID string) {
	if m.counts == nil {
		m.counts = map[string]int{}
	}
	m.counts[tenantID]++
}

const (
	cpPartial  = "11111111-1111-1111-1111-111111111111"
	cpComplete = "22222222-2222-2222-2222-222222222222"
)

func seedTenant(t *testing.T, ctx context.Context, pg *containers.Postgres, id string) {
	t.Helper()
	if _, err := pg.Pool.Exec(ctx,
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x00')`, id); err != nil {
		t.Fatalf("seed tenant %q: %v", id, err)
	}
}

func seedManifest(t *testing.T, ctx context.Context, store *manifestpg.Store, tenant, session, checkpointID, slot string, complete bool) {
	t.Helper()
	started := time.Date(2026, 5, 24, 11, 0, 0, 0, time.UTC)
	if err := store.Put(ctx, partialmanifeststore.Record{
		TenantID:             tenant,
		CheckpointID:         checkpointID,
		SessionID:            session,
		SlotID:               slot,
		ChunkObjectKeyPrefix: "/" + tenant + "/checkpoints/" + session + "/" + checkpointID + "/",
		ChunkSizeBytes:       1024,
		ChunkCount:           1,
		CheckpointStartedAt:  started,
		CheckpointTimeoutAt:  started.Add(time.Hour),
	}); err != nil {
		t.Fatalf("Put manifest %s: %v", checkpointID, err)
	}
	if complete {
		if err := store.Finalise(ctx, tenant, checkpointID, false, partialmanifeststore.ReasonComplete); err != nil {
			t.Fatalf("Finalise complete %s: %v", checkpointID, err)
		}
		return
	}
	if err := store.Finalise(ctx, tenant, checkpointID, true, partialmanifeststore.ReasonSuperseded); err != nil {
		t.Fatalf("Finalise partial %s: %v", checkpointID, err)
	}
}

// seedRotatedCheckpointRow inserts a legal-held checkpoint-typed
// artifact_store chunk row and soft-deletes it so the reconciler sees a
// rotated row.
func seedRotatedCheckpointRow(t *testing.T, ctx context.Context, cat *artifactcatalog.PgStore, tenant, session, checkpointID string) {
	t.Helper()
	uri := "lenny-blob://" + tenant + "/" + session + "/" + checkpointID + "/chunk-00000.tar"
	if err := cat.Insert(ctx, artifactcatalog.Record{
		URI:          uri,
		TenantID:     tenant,
		SessionID:    session,
		PartID:       checkpointID + "/chunk-00000.tar",
		ArtifactType: artifactcatalog.ArtifactTypeCheckpoint,
		LegalHold:    true,
		SizeBytes:    1024,
	}); err != nil {
		t.Fatalf("Insert catalog row %s: %v", uri, err)
	}
	if err := cat.SoftDelete(ctx, uri, time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("SoftDelete %s: %v", uri, err)
	}
}

// spec: §12.8 line 739; §10.1 line 141 — the reconciler counts a rotated
// chunk of a complete checkpoint as a gap and excludes a rotated chunk of
// a partial-manifest attempt.
// diagnosis: a failure means the legal-hold reconciler mis-scopes the gap
// detector — either it emits a false-positive checkpoint-gap audit event
// for a cleaned-up partial checkpoint, or it fails to detect a genuine
// rotated complete checkpoint under legal hold.
func TestReconcilerExcludesPartialManifestRotation(t *testing.T) {
	t.Parallel()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	ctx := context.Background()
	clock := func() time.Time { return time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC) }
	cat := artifactcatalog.New(pg.Pool, clock)
	manifests := manifestpg.New(pg.Pool, clock)
	seedTenant(t, ctx, pg, "acme")

	t.Run("partial rotation is not a gap", func(t *testing.T) {
		seedManifest(t, ctx, manifests, "acme", "sess-partial", cpPartial, "slot-p", false)
		seedRotatedCheckpointRow(t, ctx, cat, "acme", "sess-partial", cpPartial)

		app := &countingAppender{}
		m := &countingMetrics{}
		r := legalholdreconciler.New(cat, app, m, manifests, legalholdreconciler.Options{Clock: clock})
		emitted, err := r.Tick(ctx)
		if err != nil {
			t.Fatalf("Tick: %v", err)
		}
		if emitted != 0 {
			t.Errorf("emitted = %d, want 0 (partial-manifest chunk is not a gap)", emitted)
		}
		if len(app.rows) != 0 {
			t.Errorf("audit rows = %v, want none", app.rows)
		}
		if m.counts["acme"] != 0 {
			t.Errorf("metric counts = %v, want acme=0", m.counts)
		}
	})

	t.Run("complete rotation is a gap", func(t *testing.T) {
		seedManifest(t, ctx, manifests, "acme", "sess-complete", cpComplete, "slot-c", true)
		seedRotatedCheckpointRow(t, ctx, cat, "acme", "sess-complete", cpComplete)

		app := &countingAppender{}
		m := &countingMetrics{}
		r := legalholdreconciler.New(cat, app, m, manifests, legalholdreconciler.Options{Clock: clock})
		emitted, err := r.Tick(ctx)
		if err != nil {
			t.Fatalf("Tick: %v", err)
		}
		// sess-complete is the only session whose rotated chunk survives
		// the partial-manifest exclusion, so exactly one gap emits.
		if emitted != 1 {
			t.Errorf("emitted = %d, want 1 (complete checkpoint rotated under hold)", emitted)
		}
		found := false
		for _, row := range app.rows {
			if row == "acme|legal_hold.checkpoint_gap_detected" {
				found = true
			}
		}
		if !found {
			t.Errorf("audit rows = %v, want a legal_hold.checkpoint_gap_detected for acme", app.rows)
		}
		if m.counts["acme"] != 1 {
			t.Errorf("metric counts = %v, want acme=1", m.counts)
		}
	})
}
