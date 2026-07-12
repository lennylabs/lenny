// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test for the §4.4 eviction-fallback degradation
// paths under real fault injection against the live stores:
//
//   1. MinIO unreachable mid-fallback with Postgres healthy. The writer
//      must degrade to the Postgres minimal-state record, truncate the
//      context inline, and set the conversation_only resume signals
//      (workspace_lost=true, context_truncated=true, conversation
//      cursor preserved). The §4.4 line 263 fallback-entry counter
//      fires; the total-loss counter and the session.lost event do not.
//   2. MinIO and Postgres both unreachable. The writer must fire the
//      §4.4 lines 283-289 total-loss orchestration: best-effort
//      session.lost with reason eviction_total_loss, the
//      lenny_session_eviction_total_loss_total counter, and the §4.4
//      line 279 partial-keys-logged counter, leaving no durable row.
//
// The unit tests in pkg/gateway/storage/evictionfallback cover the
// chooser and total-loss branches against in-memory fakes, and the
// tier-2 component test drives the writer against a real Postgres store
// on the healthy path. This tier-4 test adds genuine fault injection
// against the real stores: the MinIO failure is a real minio-go
// PutObject against a non-existent bucket, and the Postgres failure is
// a real closed connection pool, so the degradation paths are exercised
// against the live pgx and minio-go error surfaces rather than
// error-returning stubs. Both live-cluster variants of this scenario
// (tests/tier5_e2e_kind/checkpoint_resume_test.go) are skipped pending
// the eviction-checkpoint preStop wiring, so this is the lowest tier
// that exercises the degradation paths end to end today.
//
// spec: §4.4 lines 263-289.
package tier4_integration_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"

	"github.com/lennylabs/lenny/pkg/checkpoint"
	"github.com/lennylabs/lenny/pkg/gateway/storage/evictionfallback"
	"github.com/lennylabs/lenny/pkg/gateway/storage/evictionstatestore"
	evictionpg "github.com/lennylabs/lenny/pkg/gateway/storage/evictionstatestore/pgstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// liveMinIOUploader is a §4.4 line 271 ContextObjectUploader backed by
// the real minio-go client. Pointing bucket at a name that does not
// exist on the running container makes PutObject return a real
// NoSuchBucket error, so the writer observes the same TCP/API-level
// failure an in-cluster MinIO outage would surface, rather than a
// stubbed error return.
type liveMinIOUploader struct {
	client *minio.Client
	bucket string
}

func (u *liveMinIOUploader) Upload(ctx context.Context, tenantID, sessionID string, body io.Reader, sizeBytes int) error {
	_, err := u.client.PutObject(ctx, u.bucket,
		evictionfallback.EvictionContextObjectKey(tenantID, sessionID),
		body, int64(sizeBytes), minio.PutObjectOptions{})
	return err
}

// recordingEvictionMetrics captures the §4.4 counter increments so the
// test can assert which telemetry fired on each degradation path.
type recordingEvictionMetrics struct {
	fallback          int
	totalLoss         int
	totalLossPool     string
	totalLossHadPrior bool
	partialKeys       int
	partialKeysLabel  string
}

func (m *recordingEvictionMetrics) IncCheckpointEvictionFallback(_ string, _ bool) { m.fallback++ }

func (m *recordingEvictionMetrics) IncSessionEvictionTotalLoss(pool string, hadPrior bool) {
	m.totalLoss++
	m.totalLossPool = pool
	m.totalLossHadPrior = hadPrior
}

func (m *recordingEvictionMetrics) IncCheckpointEvictionPartialKeysLogged(pool, keys string) {
	m.partialKeys++
	m.partialKeysLabel = pool + "|" + keys
}

// lostEvent is one captured session.lost emission.
type lostEvent struct {
	sessionID string
	reason    string
}

// recordingEvictionEvents captures best-effort session.lost emissions.
type recordingEvictionEvents struct {
	lost []lostEvent
}

func (e *recordingEvictionEvents) EmitSessionLost(_ context.Context, sessionID, reason string, _ map[string]any) {
	e.lost = append(e.lost, lostEvent{sessionID: sessionID, reason: reason})
}

// spec: §4.4 (spec/04_system-components.md, Eviction fallback: Postgres
// minimal state record) — "On MinIO unavailability during a fallback
// write, the context is truncated to 2KB and stored inline with a
// context_truncated: true flag on the row"; and "If only the minimal
// state record exists, the session is resumed on a fresh pod with
// conversation context but without workspace files — the client
// receives a session.resumed event with resumeMode: 'conversation_only'
// and workspaceLost: true".
//
// diagnosis: a failure means the eviction-fallback writer does not
// degrade to the Postgres minimal-state record when a real MinIO
// PutObject fails mid-fallback. Either the row was not written (the
// conversation would be lost, not just the workspace), the
// context_truncated / workspace_lost signals that drive the
// conversation_only resume were not persisted, or the writer stored a
// MinIO object key rather than the truncated inline context — any of
// which breaks conversation continuity on resume.
func TestEvictionFallbackMinIOOutageWritesPostgresMinimalState(t *testing.T) {
	mio := containers.StartMinIO(t, containers.MinIOOptions{})
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	ctx := context.Background()
	const tenant = "acme"
	seedTenant(t, pg, tenant)

	store := evictionpg.New(pg.Pool, nil)
	uploader := &liveMinIOUploader{client: mio.Client, bucket: "no-such-eviction-bucket"}
	metrics := &recordingEvictionMetrics{}
	events := &recordingEvictionEvents{}
	w := &evictionfallback.Writer{
		Store:           store,
		ContextUploader: uploader,
		Metrics:         metrics,
		Events:          events,
	}

	sid := newSessionUUID(t)
	when := time.Now().UTC().Truncate(time.Microsecond)
	// > 2 KB forces the MinIO object path; the live PutObject fails
	// against the non-existent bucket, so the writer degrades to the
	// truncated-inline Postgres minimal-state record.
	big := strings.Repeat("z", evictionfallback.MaxInlineContextBytes+4096)
	res, err := w.Write(ctx, evictionfallback.WriteParams{
		Record: evictionstatestore.Record{
			TenantID:               tenant,
			SessionID:              sid,
			RecoveryGeneration:     4,
			CoordinationGeneration: 2,
			ConversationCursor:     "evt-777",
			EvictedAt:              when,
		},
		Context:            []byte(big),
		Pool:               "warm-a",
		HadPriorCheckpoint: false,
		MinIOError:         errors.New("checkpoint minio upload exhausted"),
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if res.Outcome != evictionfallback.OutcomeTruncated {
		t.Fatalf("Outcome = %q, want truncated (MinIO down, Postgres healthy)", res.Outcome)
	}

	// The minimal-state row is durable in Postgres and carries the
	// conversation_only resume signals.
	got, err := store.Get(ctx, tenant, sid)
	if err != nil {
		t.Fatalf("Get after fallback: %v", err)
	}
	if !got.WorkspaceLost {
		t.Error("WorkspaceLost = false; the minimal-state record must force it true for conversation_only resume")
	}
	if !got.ContextTruncated {
		t.Error("ContextTruncated = false; a MinIO-unavailable fallback must flag the truncation")
	}
	if got.IsMinIOKey {
		t.Error("IsMinIOKey = true; the context must be stored inline, not as a MinIO key, when MinIO is down")
	}
	if got.ConversationCursor != "evt-777" {
		t.Errorf("ConversationCursor = %q, want evt-777 (cursor drives conversation replay on resume)", got.ConversationCursor)
	}
	if got.RecoveryGeneration != 4 || got.CoordinationGeneration != 2 {
		t.Errorf("generation columns lost: recovery=%d coordination=%d, want 4/2",
			got.RecoveryGeneration, got.CoordinationGeneration)
	}
	if len(got.LastMessageContext) != evictionfallback.MaxTruncatedContextBytes {
		t.Errorf("LastMessageContext len = %d, want %d truncation cap",
			len(got.LastMessageContext), evictionfallback.MaxTruncatedContextBytes)
	}

	// The §4.4 line 263 fallback-entry counter fired; the recoverable
	// path did not touch the total-loss counter or emit session.lost.
	if metrics.fallback == 0 {
		t.Error("IncCheckpointEvictionFallback never fired; every fallback entry must bump the counter")
	}
	if metrics.totalLoss != 0 {
		t.Errorf("total-loss counter fired %d times on a Postgres-recoverable fallback; want 0", metrics.totalLoss)
	}
	if len(events.lost) != 0 {
		t.Errorf("session.lost emitted %d times on a recoverable fallback; want 0", len(events.lost))
	}
}

// spec: §4.4 (spec/04_system-components.md, Total-loss path: MinIO and
// Postgres both unavailable during eviction) — "When all Postgres retry
// attempts for the minimal state write are exhausted ... the gateway
// MUST: 1. Emit a session.lost event on the session's event stream with
// reason: 'eviction_total_loss' ... 2. Increment the
// lenny_session_eviction_total_loss_total counter (labels: pool,
// had_prior_checkpoint) ...". Also (MinIO object key logging for manual
// recovery) — "Before entering the total-loss path, the gateway logs a
// WARN-level structured message ... The
// lenny_checkpoint_eviction_partial_keys_logged_total counter ... tracks
// how often partial key sets were logged".
//
// diagnosis: a failure means the total-loss path does not fire when both
// stores are genuinely unreachable. Either the session.lost event was
// not emitted (the client never learns the session is unrecoverable),
// the total-loss counter did not increment with the had_prior_checkpoint
// label (the SessionEvictionTotalLoss alert would not fire and operators
// could not tell a resumable-from-earlier-snapshot loss from a complete
// one), the partial-keys WARN counter did not fire (operators lose the
// manual-recovery object-key trail), or a partial/durable row was left
// behind after an unrecoverable eviction.
func TestEvictionFallbackTotalLossWhenBothStoresDown(t *testing.T) {
	mio := containers.StartMinIO(t, containers.MinIOOptions{})
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	ctx := context.Background()
	const tenant = "acme"
	seedTenant(t, pg, tenant)

	// A dedicated pool the test owns and closes simulates a Postgres
	// outage against the real pgx driver: after Close, every Store.Put
	// returns a real "closed pool" error. The store runs on this pool,
	// distinct from pg.Pool (which the container cleanup closes and a
	// fresh store reads back through below).
	outagePool, err := pgxpool.New(ctx, pg.DSN)
	if err != nil {
		t.Fatalf("outage pool: %v", err)
	}
	store := evictionpg.New(outagePool, nil)
	outagePool.Close()

	uploader := &liveMinIOUploader{client: mio.Client, bucket: "no-such-eviction-bucket"}
	metrics := &recordingEvictionMetrics{}
	events := &recordingEvictionEvents{}
	w := &evictionfallback.Writer{
		Store:           store,
		ContextUploader: uploader,
		Metrics:         metrics,
		Events:          events,
		// A tiny budget with a no-op sleep exhausts the §4.4 line 277
		// Postgres retry loop deterministically without a wall-clock
		// wait, so the total-loss path fires promptly.
		RetryBudget: checkpoint.RetryBudget{
			Initial:     time.Millisecond,
			Cap:         time.Millisecond,
			TotalBudget: 2 * time.Millisecond,
		},
		Sleep: func(time.Duration) {},
	}

	sid := newSessionUUID(t)
	when := time.Now().UTC().Truncate(time.Microsecond)
	big := strings.Repeat("q", evictionfallback.MaxInlineContextBytes+2048)
	res, writeErr := w.Write(ctx, evictionfallback.WriteParams{
		Record: evictionstatestore.Record{
			TenantID:           tenant,
			SessionID:          sid,
			RecoveryGeneration: 9,
			EvictedAt:          when,
		},
		Context:            []byte(big),
		Pool:               "warm-b",
		HadPriorCheckpoint: true,
		MinIOError:         errors.New("checkpoint minio upload exhausted"),
		CommittedMinIOKeys: []string{"/acme/checkpoint/" + sid + "/partial-0.tar"},
		ChunkEncoding:      "tar",
	})
	if writeErr == nil {
		t.Fatal("Write returned nil error despite both MinIO and Postgres being unreachable")
	}
	if res.Outcome != evictionfallback.OutcomeTotalLoss {
		t.Fatalf("Outcome = %q, want total_loss when both stores are down", res.Outcome)
	}

	// The best-effort session.lost event fired with the total-loss reason.
	if len(events.lost) != 1 {
		t.Fatalf("session.lost emitted %d times, want exactly 1 on the total-loss path", len(events.lost))
	}
	if events.lost[0].reason != "eviction_total_loss" {
		t.Errorf("session.lost reason = %q, want eviction_total_loss", events.lost[0].reason)
	}
	if events.lost[0].sessionID != sid {
		t.Errorf("session.lost sessionID = %q, want %q", events.lost[0].sessionID, sid)
	}

	// The total-loss counter incremented with the had_prior_checkpoint label.
	if metrics.totalLoss != 1 {
		t.Errorf("lenny_session_eviction_total_loss_total fired %d times, want 1", metrics.totalLoss)
	}
	if metrics.totalLossPool != "warm-b" || !metrics.totalLossHadPrior {
		t.Errorf("total-loss labels = (pool=%q, had_prior=%v), want (warm-b, true)",
			metrics.totalLossPool, metrics.totalLossHadPrior)
	}

	// The partial-keys WARN counter fired with keys_committed = "1+"
	// because one partial chunk key was committed before the outage.
	if metrics.partialKeys != 1 {
		t.Errorf("partial-keys-logged counter fired %d times, want 1", metrics.partialKeys)
	}
	if metrics.partialKeysLabel != "warm-b|1+" {
		t.Errorf("partial-keys label = %q, want warm-b|1+", metrics.partialKeysLabel)
	}

	// No durable row survives the total loss: a healthy store on the
	// container pool sees ErrNotFound for the session.
	freshStore := evictionpg.New(pg.Pool, nil)
	if _, err := freshStore.Get(ctx, tenant, sid); !errors.Is(err, evictionstatestore.ErrNotFound) {
		t.Errorf("eviction row present after total loss: err=%v, want ErrNotFound", err)
	}
}
