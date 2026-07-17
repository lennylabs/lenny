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
	"fmt"
	"path/filepath"
	"strings"
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

// countingAppender records the audit rows the reconciler commits,
// retaining each row's payload so tests can assert the reported gap size.
type countingAppender struct {
	rows     []string
	payloads []string
}

func (a *countingAppender) Append(_ context.Context, tenantID, eventType string, payload json.RawMessage, _ time.Time) (audit.Row, error) {
	a.rows = append(a.rows, tenantID+"|"+eventType)
	a.payloads = append(a.payloads, string(payload))
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
	cpChunked  = "33333333-3333-3333-3333-333333333333"
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
	seedRotatedCheckpointChunk(t, ctx, cat, tenant, session, checkpointID, 0)
}

// seedRotatedCheckpointChunk inserts one legal-held checkpoint chunk row
// keyed "{checkpoint_id}/chunk-{n}" and soft-deletes it. A checkpoint is
// stored as many such chunk rows sharing one checkpoint_id.
func seedRotatedCheckpointChunk(t *testing.T, ctx context.Context, cat *artifactcatalog.PgStore, tenant, session, checkpointID string, chunkIdx int) {
	t.Helper()
	chunk := fmt.Sprintf("chunk-%05d.tar", chunkIdx)
	uri := "lenny-blob://" + tenant + "/" + session + "/" + checkpointID + "/" + chunk
	if err := cat.Insert(ctx, artifactcatalog.Record{
		URI:          uri,
		TenantID:     tenant,
		SessionID:    session,
		PartID:       checkpointID + "/" + chunk,
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

	t.Run("chunked checkpoint counts once", func(t *testing.T) {
		// A complete checkpoint is stored as many chunk rows sharing one
		// checkpoint_id. All chunks rotate together, so the gap is one
		// retained checkpoint (one §12.5 retention-catalog row), not one
		// per chunk. The reported rotated_checkpoints must be 1 even though
		// three chunk rows rotated. Before the distinct-checkpoint fix the
		// reconciler counted per chunk row and would have reported 3.
		seedManifest(t, ctx, manifests, "acme", "sess-chunked", cpChunked, "slot-k", true)
		seedRotatedCheckpointChunk(t, ctx, cat, "acme", "sess-chunked", cpChunked, 0)
		seedRotatedCheckpointChunk(t, ctx, cat, "acme", "sess-chunked", cpChunked, 1)
		seedRotatedCheckpointChunk(t, ctx, cat, "acme", "sess-chunked", cpChunked, 2)

		app := &countingAppender{}
		m := &countingMetrics{}
		r := legalholdreconciler.New(cat, app, m, manifests, legalholdreconciler.Options{Clock: clock})
		if _, err := r.Tick(ctx); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		var chunkedPayload string
		for i, row := range app.rows {
			if row == "acme|legal_hold.checkpoint_gap_detected" &&
				strings.Contains(app.payloads[i], `"session_id":"sess-chunked"`) {
				chunkedPayload = app.payloads[i]
			}
		}
		if chunkedPayload == "" {
			t.Fatalf("no gap event for sess-chunked; rows = %v", app.rows)
		}
		if !strings.Contains(chunkedPayload, `"rotated_checkpoints":1`) {
			t.Errorf("three rotated chunks of one checkpoint must report rotated_checkpoints=1: %q", chunkedPayload)
		}
	})
}
