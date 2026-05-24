// SPDX-License-Identifier: MIT

package checkpointer_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/checkpointer"
	"github.com/lennylabs/lenny/pkg/gateway/partialmanifeststore"
)

// stubChunkDeleter captures every DeleteByPrefix call.
type stubChunkDeleter struct {
	mu       sync.Mutex
	prefixes []string
	err      error
}

func (d *stubChunkDeleter) DeleteByPrefix(_ context.Context, prefix string) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.prefixes = append(d.prefixes, prefix)
	if d.err != nil {
		return 0, d.err
	}
	return 1, nil
}

// stubMetrics captures every IncPartialManifestCleanup call.
type stubMetrics struct {
	mu       sync.Mutex
	outcomes []string
}

func (s *stubMetrics) IncPartialManifestCleanup(outcome string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.outcomes = append(s.outcomes, outcome)
}

// seedManifest puts a single active manifest row.
func seedManifest(t *testing.T, store partialmanifeststore.Store, tenant, session, prefix string) partialmanifeststore.Record {
	t.Helper()
	r := partialmanifeststore.Record{
		TenantID:               tenant,
		SessionID:              session,
		Generation:             1,
		PartialObjectKeyPrefix: prefix,
		ChunkEncoding:          partialmanifeststore.ChunkEncodingTar,
	}
	if err := store.Put(context.Background(), r); err != nil {
		t.Fatalf("Put: %v", err)
	}
	return r
}

// spec: §4.4 line 236 — successful cleanup deletes every chunk under
// the prefix and soft-deletes the manifest row.
func TestCleanupPartialManifestSuccess(t *testing.T) {
	store := partialmanifeststore.NewMemoryStore(nil)
	r := seedManifest(t, store, "acme", "s1", "/acme/checkpoints/s1/partial/ck-1/")
	deleter := &stubChunkDeleter{}
	metrics := &stubMetrics{}

	if err := checkpointer.CleanupPartialManifest(context.Background(), store, deleter, r, metrics, false); err != nil {
		t.Fatalf("CleanupPartialManifest: %v", err)
	}
	if len(deleter.prefixes) != 1 || deleter.prefixes[0] != r.PartialObjectKeyPrefix {
		t.Errorf("chunk deletion prefixes = %v, want [%q]", deleter.prefixes, r.PartialObjectKeyPrefix)
	}

	row, _ := store.Get(context.Background(), "acme", "s1", 1)
	if row.DeletedAt.IsZero() {
		t.Error("cleanup did not soft-delete the manifest row")
	}
	if len(metrics.outcomes) != 1 || metrics.outcomes[0] != string(checkpointer.PartialCleanupSuccess) {
		t.Errorf("metric outcomes = %v, want [success]", metrics.outcomes)
	}
}

// spec: §4.4 line 236 — a MinIO failure leaves the row active so the
// §12.5 backstop sweep can retry on the next cycle.
func TestCleanupPartialManifestMinIOFailure(t *testing.T) {
	store := partialmanifeststore.NewMemoryStore(nil)
	r := seedManifest(t, store, "acme", "s1", "/acme/checkpoints/s1/partial/ck-1/")
	deleter := &stubChunkDeleter{err: errors.New("minio down")}
	metrics := &stubMetrics{}

	if err := checkpointer.CleanupPartialManifest(context.Background(), store, deleter, r, metrics, false); err == nil {
		t.Fatal("expected an error when the chunk deleter fails")
	}
	row, _ := store.Get(context.Background(), "acme", "s1", 1)
	if !row.DeletedAt.IsZero() {
		t.Error("cleanup soft-deleted the row despite the MinIO failure")
	}
	if len(metrics.outcomes) != 1 || metrics.outcomes[0] != string(checkpointer.PartialCleanupFailedDeleted) {
		t.Errorf("metric outcomes = %v, want [failed_deleted]", metrics.outcomes)
	}
}

// spec: §4.4 line 236 — `deleted_at IS NULL` is the idempotency
// guard. Repeated cleanup invocations converge to a single
// state mutation; the second writer observes the row already
// soft-deleted.
func TestCleanupPartialManifestIsIdempotent(t *testing.T) {
	store := partialmanifeststore.NewMemoryStore(nil)
	r := seedManifest(t, store, "acme", "s1", "/acme/checkpoints/s1/partial/ck-1/")
	metrics := &stubMetrics{}

	for i := 0; i < 3; i++ {
		if err := checkpointer.CleanupPartialManifest(context.Background(), store, &stubChunkDeleter{}, r, metrics, false); err != nil {
			t.Fatalf("cleanup %d: %v", i, err)
		}
	}
	// All three calls succeed; the metric records three success
	// outcomes because the cleanup path is at-least-once-safe by
	// design (idempotent on the second writer).
	for i, outcome := range metrics.outcomes {
		if outcome != string(checkpointer.PartialCleanupSuccess) {
			t.Errorf("invocation %d: outcome %q, want success", i, outcome)
		}
	}
}

// spec: §4.4 line 236 — the §12.5 backstop sweep emits
// `gc_collected` rather than `success` so operators can distinguish
// resume-path cleanups from GC-backstop cleanups.
func TestCleanupPartialManifestGCCollectedOutcome(t *testing.T) {
	store := partialmanifeststore.NewMemoryStore(nil)
	r := seedManifest(t, store, "acme", "s1", "/acme/checkpoints/s1/partial/ck-1/")
	metrics := &stubMetrics{}

	if err := checkpointer.CleanupPartialManifest(context.Background(), store, &stubChunkDeleter{}, r, metrics, true); err != nil {
		t.Fatalf("CleanupPartialManifest: %v", err)
	}
	if len(metrics.outcomes) != 1 || metrics.outcomes[0] != string(checkpointer.PartialCleanupGCCollected) {
		t.Errorf("metric outcomes = %v, want [gc_collected]", metrics.outcomes)
	}
}

// spec: §4.4 line 236 — a nil deleter is honored as a "skip MinIO"
// path (tests / dev-mode); the row is still soft-deleted.
func TestCleanupPartialManifestNilDeleterSkipsMinIO(t *testing.T) {
	store := partialmanifeststore.NewMemoryStore(nil)
	r := seedManifest(t, store, "acme", "s1", "/acme/checkpoints/s1/partial/ck-1/")
	metrics := &stubMetrics{}

	if err := checkpointer.CleanupPartialManifest(context.Background(), store, nil, r, metrics, false); err != nil {
		t.Fatalf("CleanupPartialManifest: %v", err)
	}
	row, _ := store.Get(context.Background(), "acme", "s1", 1)
	if row.DeletedAt.IsZero() {
		t.Error("row was not soft-deleted when deleter is nil")
	}
}

// spec: §4.4 line 234 — input validation: an empty tenant/session id
// or prefix is a programming error.
func TestCleanupPartialManifestRejectsEmptyFields(t *testing.T) {
	store := partialmanifeststore.NewMemoryStore(nil)
	cases := []partialmanifeststore.Record{
		{SessionID: "s1", PartialObjectKeyPrefix: "/x/"},
		{TenantID: "acme", PartialObjectKeyPrefix: "/x/"},
		{TenantID: "acme", SessionID: "s1"},
	}
	for _, r := range cases {
		if err := checkpointer.CleanupPartialManifest(context.Background(), store, nil, r, nil, false); err == nil {
			t.Errorf("cleanup accepted record with empty fields: %+v", r)
		}
	}
}
