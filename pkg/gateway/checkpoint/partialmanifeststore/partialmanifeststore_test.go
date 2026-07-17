// SPDX-License-Identifier: MIT

package partialmanifeststore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
)

// intentRow returns a valid intent-row Record with the required
// timestamp fields stamped, so tests can vary only the fields they
// exercise.
func intentRow(clock time.Time, tenant, checkpointID, session string, gen int64) partialmanifeststore.Record {
	return partialmanifeststore.Record{
		TenantID:               tenant,
		CheckpointID:           checkpointID,
		SessionID:              session,
		CoordinationGeneration: gen,
		ChunkObjectKeyPrefix:   "/" + tenant + "/checkpoints/" + session + "/" + checkpointID + "/",
		ChunkSizeBytes:         16 << 20,
		ChunkEncoding:          partialmanifeststore.ChunkEncodingTar,
		CheckpointStartedAt:    clock,
		CheckpointTimeoutAt:    clock.Add(90 * time.Second),
	}
}

// spec: §10.1 lines 141-151 — the intent-row Put + Get round-trip must
// persist every spec-mandated field, keyed on (tenant_id, checkpoint_id).
func TestPutAndGet(t *testing.T) {
	clock := time.Date(2026, 5, 22, 13, 0, 0, 0, time.UTC)
	store := partialmanifeststore.NewMemoryStore(func() time.Time { return clock })

	r := intentRow(clock, "acme", "cp-1", "sess_42", 7)
	r.ChunkEncoding = partialmanifeststore.ChunkEncodingTarGz
	if err := store.Put(context.Background(), r); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := store.Get(context.Background(), "acme", "cp-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ChunkObjectKeyPrefix != r.ChunkObjectKeyPrefix {
		t.Errorf("ChunkObjectKeyPrefix = %q, want %q", got.ChunkObjectKeyPrefix, r.ChunkObjectKeyPrefix)
	}
	if got.ChunkEncoding != partialmanifeststore.ChunkEncodingTarGz {
		t.Errorf("ChunkEncoding = %q, want tar.gz", got.ChunkEncoding)
	}
	if got.SlotID != partialmanifeststore.SlotDefault {
		t.Errorf("SlotID = %q, want %q", got.SlotID, partialmanifeststore.SlotDefault)
	}
	if got.ManifestReason != partialmanifeststore.ReasonInProgress {
		t.Errorf("ManifestReason = %q, want in_progress", got.ManifestReason)
	}
	if !got.Partial {
		t.Error("Partial should be true on the intent row")
	}
	if !got.CreatedAt.Equal(clock) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, clock)
	}
	if !got.DeletedAt.IsZero() {
		t.Errorf("DeletedAt = %v, want zero on an active row", got.DeletedAt)
	}
}

// spec: §10.1 line 141 — baseline_full_checkpoint_bytes is nullable and
// the NULL state is load-bearing; a nil pointer round-trips as NULL.
func TestPutRoundTripsNullBaseline(t *testing.T) {
	clock := time.Date(2026, 5, 22, 13, 0, 0, 0, time.UTC)
	store := partialmanifeststore.NewMemoryStore(func() time.Time { return clock })
	r := intentRow(clock, "acme", "cp-1", "s1", 1)
	if err := store.Put(context.Background(), r); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, _ := store.Get(context.Background(), "acme", "cp-1")
	if got.BaselineFullCheckpointBytes != nil {
		t.Errorf("BaselineFullCheckpointBytes = %v, want nil", *got.BaselineFullCheckpointBytes)
	}

	baseline := int64(4096)
	r2 := intentRow(clock, "acme", "cp-2", "s2", 1)
	r2.BaselineFullCheckpointBytes = &baseline
	if err := store.Put(context.Background(), r2); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got2, _ := store.Get(context.Background(), "acme", "cp-2")
	if got2.BaselineFullCheckpointBytes == nil || *got2.BaselineFullCheckpointBytes != 4096 {
		t.Errorf("BaselineFullCheckpointBytes = %v, want 4096", got2.BaselineFullCheckpointBytes)
	}
}

// spec: §12.5 GC rule 6 — partial-manifest cleanup soft-deletes the row
// rather than hard-deleting. The §12.5 backstop sweep races the primary
// cleanup path; the second writer must observe the row already
// soft-deleted and skip side effects.
func TestSoftDeleteIsIdempotent(t *testing.T) {
	clock := time.Date(2026, 5, 22, 13, 0, 0, 0, time.UTC)
	store := partialmanifeststore.NewMemoryStore(func() time.Time { return clock })

	_ = store.Put(context.Background(), intentRow(clock, "acme", "cp-1", "s1", 1))

	clock = clock.Add(time.Minute)
	if err := store.SoftDelete(context.Background(), "acme", "cp-1"); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	got, _ := store.Get(context.Background(), "acme", "cp-1")
	first := got.DeletedAt
	if first.IsZero() {
		t.Fatal("SoftDelete did not stamp DeletedAt")
	}

	// Replay: second writer observes the row already soft-deleted
	// and does not bump the timestamp.
	clock = clock.Add(time.Minute)
	if err := store.SoftDelete(context.Background(), "acme", "cp-1"); err != nil {
		t.Fatalf("Replay SoftDelete: %v", err)
	}
	got, _ = store.Get(context.Background(), "acme", "cp-1")
	if !got.DeletedAt.Equal(first) {
		t.Errorf("DeletedAt = %v, want stable %v across replays", got.DeletedAt, first)
	}

	// SoftDelete on a missing row is also idempotent.
	if err := store.SoftDelete(context.Background(), "acme", "missing"); err != nil {
		t.Errorf("SoftDelete on missing: %v", err)
	}
}

// spec: §10.1 line 157 — LatestActive is the resume-time cleanup
// selector and carries the `partial = TRUE` predicate, so a completed
// checkpoint (partial = false) is never returned to the cleaner and thus
// never destroyed by its own restore.
func TestLatestActiveSelectsPartialOnly(t *testing.T) {
	clock := time.Date(2026, 5, 22, 13, 0, 0, 0, time.UTC)
	store := partialmanifeststore.NewMemoryStore(func() time.Time { return clock })

	// A completed checkpoint at gen 2.
	complete := intentRow(clock, "acme", "cp-complete", "s1", 2)
	if err := store.Put(context.Background(), complete); err != nil {
		t.Fatalf("Put complete intent: %v", err)
	}
	if err := store.Finalise(context.Background(), "acme", "cp-complete", false, partialmanifeststore.ReasonComplete); err != nil {
		t.Fatalf("Finalise complete: %v", err)
	}

	// No partial row exists, so the cleanup selector finds nothing even
	// though an active (partial = false) row is present.
	if _, err := store.LatestActive(context.Background(), "acme", "s1"); !errors.Is(err, partialmanifeststore.ErrNotFound) {
		t.Fatalf("LatestActive with only a completed row: got %v, want ErrNotFound", err)
	}

	// A later partial drain attempt at gen 3 is the one the cleaner sees.
	partial := intentRow(clock, "acme", "cp-partial", "s1", 3)
	if err := store.Put(context.Background(), partial); err != nil {
		t.Fatalf("Put partial intent: %v", err)
	}
	got, err := store.LatestActive(context.Background(), "acme", "s1")
	if err != nil {
		t.Fatalf("LatestActive: %v", err)
	}
	if got.CheckpointID != "cp-partial" {
		t.Errorf("LatestActive.CheckpointID = %q, want cp-partial", got.CheckpointID)
	}
}

// spec: §10.1 line 154 — LatestActiveAny is the resume-reassembly selector.
// Unlike LatestActive (partial = TRUE), it returns the highest-generation
// active row regardless of partial, so a completed checkpoint is returned
// when it is the only active row, and a newer partial drain attempt at a
// higher generation wins over it.
func TestLatestActiveAnyIgnoresPartialPredicate(t *testing.T) {
	clock := time.Date(2026, 5, 22, 13, 0, 0, 0, time.UTC)
	store := partialmanifeststore.NewMemoryStore(func() time.Time { return clock })

	// A completed checkpoint at gen 2: LatestActive (partial-only) skips it,
	// but LatestActiveAny returns it because it is the sole active row.
	if err := store.Put(context.Background(), intentRow(clock, "acme", "cp-complete", "s1", 2)); err != nil {
		t.Fatalf("Put complete intent: %v", err)
	}
	// A confirmed chunk keeps the completed row active (a zero-chunk row is
	// soft-deleted by Finalise, §10.1 line 132).
	if err := store.ConfirmChunk(context.Background(), "acme", "cp-complete", 0, 4096); err != nil {
		t.Fatalf("ConfirmChunk complete: %v", err)
	}
	if err := store.Finalise(context.Background(), "acme", "cp-complete", false, partialmanifeststore.ReasonComplete); err != nil {
		t.Fatalf("Finalise complete: %v", err)
	}
	got, err := store.LatestActiveAny(context.Background(), "acme", "s1")
	if err != nil {
		t.Fatalf("LatestActiveAny with only a completed row: %v", err)
	}
	if got.CheckpointID != "cp-complete" {
		t.Errorf("LatestActiveAny.CheckpointID = %q, want cp-complete (returned regardless of partial)", got.CheckpointID)
	}

	// A newer partial drain attempt at gen 3 wins on the generation fence.
	clock = clock.Add(time.Minute)
	if err := store.Put(context.Background(), intentRow(clock, "acme", "cp-drain", "s1", 3)); err != nil {
		t.Fatalf("Put drain intent: %v", err)
	}
	got, err = store.LatestActiveAny(context.Background(), "acme", "s1")
	if err != nil {
		t.Fatalf("LatestActiveAny: %v", err)
	}
	if got.CheckpointID != "cp-drain" {
		t.Errorf("LatestActiveAny.CheckpointID = %q, want cp-drain (higher generation wins)", got.CheckpointID)
	}

	// No active row for an unknown session returns ErrNotFound.
	if _, err := store.LatestActiveAny(context.Background(), "acme", "s2"); !errors.Is(err, partialmanifeststore.ErrNotFound) {
		t.Errorf("LatestActiveAny for empty session = %v, want ErrNotFound", err)
	}
}

// spec: §10.1 line 137 — supersede-on-write collapses the active partial
// set to the highest-generation row for the (session, slot). A same-slot
// write at or above the incoming generation soft-deletes the prior
// active partial row, even at the same coordination_generation (two
// attempts on one coordinator share a generation).
func TestPutSupersedesAtOrBelowGeneration(t *testing.T) {
	clock := time.Date(2026, 5, 22, 13, 0, 0, 0, time.UTC)
	store := partialmanifeststore.NewMemoryStore(func() time.Time { return clock })

	// Two attempts at the SAME coordination_generation on one coordinator.
	if err := store.Put(context.Background(), intentRow(clock, "acme", "cp-a", "s1", 5)); err != nil {
		t.Fatalf("Put cp-a: %v", err)
	}
	clock = clock.Add(time.Minute)
	if err := store.Put(context.Background(), intentRow(clock, "acme", "cp-b", "s1", 5)); err != nil {
		t.Fatalf("Put cp-b: %v", err)
	}

	// cp-a is superseded (at or below the incoming generation).
	a, _ := store.Get(context.Background(), "acme", "cp-a")
	if a.DeletedAt.IsZero() {
		t.Error("cp-a should be superseded by the same-generation cp-b write")
	}
	if a.ManifestReason != partialmanifeststore.ReasonSuperseded {
		t.Errorf("cp-a ManifestReason = %q, want superseded", a.ManifestReason)
	}
	got, err := store.LatestActive(context.Background(), "acme", "s1")
	if err != nil {
		t.Fatalf("LatestActive: %v", err)
	}
	if got.CheckpointID != "cp-b" {
		t.Errorf("LatestActive.CheckpointID = %q, want cp-b", got.CheckpointID)
	}
}

// spec: §10.1 line 155 — a write whose coordination_generation sits
// below an already-active strictly-higher generation is a fenced
// stale-coordinator write; Put rejects it with ErrStaleGeneration
// without mutating the active manifest.
func TestPutRejectsStaleLowerGeneration(t *testing.T) {
	clock := time.Date(2026, 5, 22, 13, 0, 0, 0, time.UTC)
	store := partialmanifeststore.NewMemoryStore(func() time.Time { return clock })
	if err := store.Put(context.Background(), intentRow(clock, "acme", "cp-5", "s1", 5)); err != nil {
		t.Fatalf("Put gen 5: %v", err)
	}
	err := store.Put(context.Background(), intentRow(clock, "acme", "cp-3", "s1", 3))
	if !errors.Is(err, partialmanifeststore.ErrStaleGeneration) {
		t.Fatalf("Put stale gen 3: got %v, want ErrStaleGeneration", err)
	}
	// cp-3 was never inserted; cp-5 remains the sole active manifest.
	if _, gerr := store.Get(context.Background(), "acme", "cp-3"); !errors.Is(gerr, partialmanifeststore.ErrNotFound) {
		t.Errorf("cp-3 Get: got %v, want ErrNotFound (stale write not persisted)", gerr)
	}
	got, _ := store.LatestActive(context.Background(), "acme", "s1")
	if got.CheckpointID != "cp-5" {
		t.Errorf("LatestActive.CheckpointID = %q, want cp-5 (active manifest unchanged)", got.CheckpointID)
	}
}

// spec: §10.1 line 147 — the supersede predicate is scoped to
// (session_id, slot_id); a write in a different slot does not supersede
// another slot's active partial row.
func TestPutSupersedeScopedToSlot(t *testing.T) {
	clock := time.Date(2026, 5, 22, 13, 0, 0, 0, time.UTC)
	store := partialmanifeststore.NewMemoryStore(func() time.Time { return clock })

	a := intentRow(clock, "acme", "cp-a", "s1", 5)
	a.SlotID = "slot-0"
	b := intentRow(clock, "acme", "cp-b", "s1", 5)
	b.SlotID = "slot-1"
	if err := store.Put(context.Background(), a); err != nil {
		t.Fatalf("Put slot-0: %v", err)
	}
	if err := store.Put(context.Background(), b); err != nil {
		t.Fatalf("Put slot-1: %v", err)
	}
	// Both survive: distinct slots hold independent active partials.
	got, _ := store.Get(context.Background(), "acme", "cp-a")
	if !got.DeletedAt.IsZero() {
		t.Error("slot-0 row should not be superseded by a slot-1 write")
	}
}

// spec: §10.1 line 137 — LatestActiveForSlot returns the active partial row
// for the exact (session, slot) the supersede path scopes on, even when
// another slot in the same session holds a higher-generation active row that
// the session-wide LatestActive would return first.
func TestLatestActiveForSlotIsSlotScoped(t *testing.T) {
	clock := time.Date(2026, 5, 22, 13, 0, 0, 0, time.UTC)
	store := partialmanifeststore.NewMemoryStore(func() time.Time { return clock })

	target := intentRow(clock, "acme", "cp-target", "s1", 0)
	target.SlotID = "slot-0"
	other := intentRow(clock, "acme", "cp-other", "s1", 9) // higher generation, different slot
	other.SlotID = "slot-1"
	if err := store.Put(context.Background(), target); err != nil {
		t.Fatalf("Put slot-0: %v", err)
	}
	if err := store.Put(context.Background(), other); err != nil {
		t.Fatalf("Put slot-1: %v", err)
	}

	// The session-wide selector returns the higher-generation slot-1 row.
	if latest, err := store.LatestActive(context.Background(), "acme", "s1"); err != nil {
		t.Fatalf("LatestActive: %v", err)
	} else if latest.CheckpointID != "cp-other" {
		t.Fatalf("LatestActive = %q, want cp-other (the higher-generation row)", latest.CheckpointID)
	}
	// The slot-scoped selector returns slot-0's own row, not the winner.
	got, err := store.LatestActiveForSlot(context.Background(), "acme", "s1", "slot-0")
	if err != nil {
		t.Fatalf("LatestActiveForSlot slot-0: %v", err)
	}
	if got.CheckpointID != "cp-target" {
		t.Errorf("LatestActiveForSlot(slot-0) = %q, want cp-target", got.CheckpointID)
	}

	// A slot with no active partial row returns ErrNotFound.
	if _, err := store.LatestActiveForSlot(context.Background(), "acme", "s1", "slot-2"); !errors.Is(err, partialmanifeststore.ErrNotFound) {
		t.Errorf("LatestActiveForSlot(empty slot) = %v, want ErrNotFound", err)
	}
}

// spec: §10.1 line 131 — ConfirmChunk advances chunk_count and
// workspace_bytes_uploaded monotonically under the `chunk_count < n + 1`
// guard, so an out-of-order acknowledgement cannot decrement the counter.
func TestConfirmChunkIsMonotonic(t *testing.T) {
	clock := time.Date(2026, 5, 22, 13, 0, 0, 0, time.UTC)
	store := partialmanifeststore.NewMemoryStore(func() time.Time { return clock })
	_ = store.Put(context.Background(), intentRow(clock, "acme", "cp-1", "s1", 1))

	if err := store.ConfirmChunk(context.Background(), "acme", "cp-1", 0, 100); err != nil {
		t.Fatalf("ConfirmChunk 0: %v", err)
	}
	if err := store.ConfirmChunk(context.Background(), "acme", "cp-1", 2, 300); err != nil {
		t.Fatalf("ConfirmChunk 2: %v", err)
	}
	got, _ := store.Get(context.Background(), "acme", "cp-1")
	if got.ChunkCount != 3 || got.WorkspaceBytesUploaded != 300 {
		t.Fatalf("after confirming index 2: chunk_count=%d bytes=%d, want 3/300", got.ChunkCount, got.WorkspaceBytesUploaded)
	}

	// A late, out-of-order ack for index 1 must not decrement the counter.
	if err := store.ConfirmChunk(context.Background(), "acme", "cp-1", 1, 200); err != nil {
		t.Fatalf("ConfirmChunk 1 (late): %v", err)
	}
	got, _ = store.Get(context.Background(), "acme", "cp-1")
	if got.ChunkCount != 3 || got.WorkspaceBytesUploaded != 300 {
		t.Errorf("out-of-order ack decremented the counter: chunk_count=%d bytes=%d, want 3/300", got.ChunkCount, got.WorkspaceBytesUploaded)
	}
}

// spec: §10.1 line 141 — Finalise stamps the terminal disposition: a
// complete checkpoint finalises partial = false, and every other arm
// finalises partial = true with its reason.
func TestFinalise(t *testing.T) {
	clock := time.Date(2026, 5, 22, 13, 0, 0, 0, time.UTC)
	store := partialmanifeststore.NewMemoryStore(func() time.Time { return clock })
	_ = store.Put(context.Background(), intentRow(clock, "acme", "cp-1", "s1", 1))

	if err := store.Finalise(context.Background(), "acme", "cp-1", false, partialmanifeststore.ReasonComplete); err != nil {
		t.Fatalf("Finalise: %v", err)
	}
	got, _ := store.Get(context.Background(), "acme", "cp-1")
	if got.Partial || got.ManifestReason != partialmanifeststore.ReasonComplete {
		t.Errorf("Finalise complete: partial=%v reason=%q, want false/complete", got.Partial, got.ManifestReason)
	}

	// An invalid reason is rejected.
	if err := store.Finalise(context.Background(), "acme", "cp-1", true, "bogus"); err == nil {
		t.Error("Finalise accepted an invalid manifest_reason")
	}
}

// spec: §10.1 line 132 — a terminal arm that finalises with chunk_count == 0
// soft-deletes the row in the same transaction, so an empty partial manifest
// (an adapter crash or a quota refusal before the first chunk confirmed) is
// never left active occupying the partial_manifest_active_uniq slot for a
// later supersede or the §12.5 backstop to reclaim. A row that confirmed at
// least one chunk stays active for the resume path.
func TestFinaliseSoftDeletesZeroChunkRow(t *testing.T) {
	clock := time.Date(2026, 5, 22, 13, 0, 0, 0, time.UTC)
	store := partialmanifeststore.NewMemoryStore(func() time.Time { return clock })

	// A zero-chunk abort: no ConfirmChunk ran, so chunk_count is 0 at
	// finalisation. The finalising UPDATE soft-deletes the row.
	_ = store.Put(context.Background(), intentRow(clock, "acme", "cp-empty", "s1", 1))
	if err := store.Finalise(context.Background(), "acme", "cp-empty", true,
		partialmanifeststore.ReasonStreamTruncated); err != nil {
		t.Fatalf("Finalise zero-chunk: %v", err)
	}
	empty, _ := store.Get(context.Background(), "acme", "cp-empty")
	if empty.DeletedAt.IsZero() {
		t.Error("zero-chunk finalisation left the row active, want soft-deleted in the same transaction")
	}
	if empty.ManifestReason != partialmanifeststore.ReasonStreamTruncated {
		t.Errorf("ManifestReason = %q, want stream_truncated", empty.ManifestReason)
	}
	// The soft-deleted empty row is invisible to the resume-time cleanup
	// selector, so no reclaimer ever picks it up.
	if _, err := store.LatestActiveForSlot(context.Background(), "acme", "s1", partialmanifeststore.SlotDefault); !errors.Is(err, partialmanifeststore.ErrNotFound) {
		t.Errorf("LatestActiveForSlot after zero-chunk finalise = %v, want ErrNotFound", err)
	}

	// A row that confirmed a chunk finalises partial without soft-deleting:
	// the resume path consumes its contiguous chunk prefix.
	_ = store.Put(context.Background(), intentRow(clock, "acme", "cp-one", "s2", 1))
	if err := store.ConfirmChunk(context.Background(), "acme", "cp-one", 0, 128); err != nil {
		t.Fatalf("ConfirmChunk: %v", err)
	}
	if err := store.Finalise(context.Background(), "acme", "cp-one", true,
		partialmanifeststore.ReasonTimeout); err != nil {
		t.Fatalf("Finalise one-chunk: %v", err)
	}
	one, _ := store.Get(context.Background(), "acme", "cp-one")
	if !one.DeletedAt.IsZero() {
		t.Error("one-chunk finalisation soft-deleted the row, want left active for resume")
	}
}

// spec: §11.2 / §12.5 GC rule 4 — ReleaseReservation is exactly-once:
// the first release reports rows_affected == 1, and every retry (a
// stale-leader re-run, the §12.5 backstop racing the in-process arm)
// reports 0 so the relative reservation decrement never fires twice.
func TestReleaseReservationIsExactlyOnce(t *testing.T) {
	clock := time.Date(2026, 5, 22, 13, 0, 0, 0, time.UTC)
	store := partialmanifeststore.NewMemoryStore(func() time.Time { return clock })
	r := intentRow(clock, "acme", "cp-1", "s1", 1)
	r.ReservedBytes = 1024
	_ = store.Put(context.Background(), r)

	n, err := store.ReleaseReservation(context.Background(), "acme", "cp-1")
	if err != nil || n != 1 {
		t.Fatalf("first ReleaseReservation = (%d, %v); want (1, nil)", n, err)
	}
	n, err = store.ReleaseReservation(context.Background(), "acme", "cp-1")
	if err != nil || n != 0 {
		t.Fatalf("second ReleaseReservation = (%d, %v); want (0, nil)", n, err)
	}
}

// spec: §11.2 — SumOutstandingReservations sums
// (reserved_bytes - workspace_bytes_uploaded) over the tenant's active,
// unreleased rows, so the reservation-aware storage-counter rebuild
// folds the outstanding headroom into the absolute value it sets.
func TestSumOutstandingReservations(t *testing.T) {
	clock := time.Date(2026, 5, 22, 13, 0, 0, 0, time.UTC)
	store := partialmanifeststore.NewMemoryStore(func() time.Time { return clock })

	r1 := intentRow(clock, "acme", "cp-1", "s1", 1)
	r1.ReservedBytes = 1000
	_ = store.Put(context.Background(), r1)
	_ = store.ConfirmChunk(context.Background(), "acme", "cp-1", 0, 400) // 600 outstanding

	r2 := intentRow(clock, "acme", "cp-2", "s2", 1)
	r2.ReservedBytes = 2000
	_ = store.Put(context.Background(), r2) // 2000 outstanding

	// A released row contributes nothing.
	r3 := intentRow(clock, "acme", "cp-3", "s3", 1)
	r3.ReservedBytes = 5000
	_ = store.Put(context.Background(), r3)
	_, _ = store.ReleaseReservation(context.Background(), "acme", "cp-3")

	// Another tenant is excluded.
	r4 := intentRow(clock, "globex", "cp-4", "s4", 1)
	r4.ReservedBytes = 9000
	_ = store.Put(context.Background(), r4)

	sum, err := store.SumOutstandingReservations(context.Background(), "acme")
	if err != nil {
		t.Fatalf("SumOutstandingReservations: %v", err)
	}
	if sum != 2600 {
		t.Errorf("SumOutstandingReservations = %d, want 2600 (600 + 2000)", sum)
	}
}

// spec: §12.5 GC rule 6 — ListReclaimable returns an abandoned active
// row whose resume window has expired, and holds off on a live
// in_progress row still inside its checkpoint_timeout_at (a seal
// checkpoint holding an intent row open must not be swept out from
// under it).
func TestListReclaimableResumeWindow(t *testing.T) {
	base := time.Date(2026, 5, 22, 13, 0, 0, 0, time.UTC)
	clock := base
	store := partialmanifeststore.NewMemoryStore(func() time.Time { return clock })

	// An old finalised-timeout row created at base. It confirmed a chunk
	// before the deadline fired, so it is retained (not soft-deleted at
	// finalise) for the resume path and remains reclaimable by the backstop
	// once its resume window expires (§10.1 line 132 soft-deletes only the
	// zero-chunk finalisation).
	_ = store.Put(context.Background(), intentRow(clock, "acme", "cp-old", "s1", 1))
	_ = store.ConfirmChunk(context.Background(), "acme", "cp-old", 0, 128)
	_ = store.Finalise(context.Background(), "acme", "cp-old", true, partialmanifeststore.ReasonTimeout)

	// Advance two hours and write a young in_progress row whose timeout
	// lies in the future relative to the sweep instant.
	clock = base.Add(2 * time.Hour)
	_ = store.Put(context.Background(), intentRow(clock, "acme", "cp-young", "s2", 1))

	got, err := store.ListReclaimable(context.Background(), time.Hour)
	if err != nil {
		t.Fatalf("ListReclaimable: %v", err)
	}
	if len(got) != 1 || got[0].CheckpointID != "cp-old" {
		t.Fatalf("ListReclaimable = %+v, want [cp-old]", got)
	}

	// Advance past the young row's checkpoint_timeout_at and its resume
	// window: it too becomes reclaimable.
	clock = base.Add(4 * time.Hour)
	got, _ = store.ListReclaimable(context.Background(), time.Hour)
	ids := map[string]bool{}
	for _, r := range got {
		ids[r.CheckpointID] = true
	}
	if !ids["cp-old"] || !ids["cp-young"] || len(got) != 2 {
		t.Fatalf("ListReclaimable after timeout = %+v, want [cp-old cp-young]", got)
	}
}

// spec: §10.1 line 141 — a row with an empty chunk_object_key_prefix is
// meaningless (the cleanup path cannot locate the chunks); reject at the
// store boundary.
func TestPutRejectsEmptyPrefix(t *testing.T) {
	store := partialmanifeststore.NewMemoryStore(nil)
	r := partialmanifeststore.Record{
		TenantID: "acme", CheckpointID: "cp-1", SessionID: "s1",
		ChunkEncoding:       partialmanifeststore.ChunkEncodingTar,
		CheckpointStartedAt: time.Now(), CheckpointTimeoutAt: time.Now(),
	}
	if err := store.Put(context.Background(), r); err == nil {
		t.Error("Put accepted empty chunk_object_key_prefix")
	}
}

// spec: §10.1 — chunk_encoding is the closed enum {tar, tar.gz}; an
// invalid value cannot be silently coerced.
func TestPutRejectsInvalidChunkEncoding(t *testing.T) {
	store := partialmanifeststore.NewMemoryStore(nil)
	r := intentRow(time.Now(), "acme", "cp-1", "s1", 1)
	r.ChunkEncoding = "zip"
	if err := store.Put(context.Background(), r); err == nil {
		t.Error("Put accepted invalid chunk_encoding")
	}
}

// spec: §10.1 — chunk_encoding defaults to `tar` and manifest_reason to
// `in_progress` when the caller leaves them empty.
func TestPutDefaultsEncodingAndReason(t *testing.T) {
	store := partialmanifeststore.NewMemoryStore(nil)
	r := intentRow(time.Now(), "acme", "cp-1", "s1", 1)
	r.ChunkEncoding = ""
	r.ManifestReason = ""
	if err := store.Put(context.Background(), r); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, _ := store.Get(context.Background(), "acme", "cp-1")
	if got.ChunkEncoding != partialmanifeststore.ChunkEncodingTar {
		t.Errorf("default ChunkEncoding = %q, want tar", got.ChunkEncoding)
	}
	if got.ManifestReason != partialmanifeststore.ReasonInProgress {
		t.Errorf("default ManifestReason = %q, want in_progress", got.ManifestReason)
	}
}

// spec: §10.1 line 141 — empty tenant, session, or checkpoint id is a
// programming error; reject at the store boundary.
func TestPutRejectsEmptyIDs(t *testing.T) {
	store := partialmanifeststore.NewMemoryStore(nil)
	base := intentRow(time.Now(), "acme", "cp-1", "s1", 1)
	mut := []func(r *partialmanifeststore.Record){
		func(r *partialmanifeststore.Record) { r.TenantID = "" },
		func(r *partialmanifeststore.Record) { r.SessionID = "" },
		func(r *partialmanifeststore.Record) { r.CheckpointID = "" },
	}
	for i, m := range mut {
		r := base
		m(&r)
		if err := store.Put(context.Background(), r); err == nil {
			t.Errorf("case %d: Put accepted record with an empty id", i)
		}
	}
}

// spec: §10.1 — the intent row is written once per attempt; a duplicate
// checkpoint_id is a minting error.
func TestPutRejectsDuplicateCheckpointID(t *testing.T) {
	store := partialmanifeststore.NewMemoryStore(nil)
	r := intentRow(time.Now(), "acme", "cp-1", "s1", 1)
	if err := store.Put(context.Background(), r); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := store.Put(context.Background(), r); !errors.Is(err, partialmanifeststore.ErrAlreadyExists) {
		t.Errorf("duplicate Put: got %v, want ErrAlreadyExists", err)
	}
}

// spec: §12.5 hard-prune — ListSoftDeletedBefore returns every
// soft-deleted row whose DeletedAt is older than the cutoff.
func TestListSoftDeletedBeforeWalksTombstones(t *testing.T) {
	clock := time.Date(2026, 5, 22, 13, 0, 0, 0, time.UTC)
	store := partialmanifeststore.NewMemoryStore(func() time.Time { return clock })

	// One active partial row per session so supersede-on-write does not
	// auto-soft-delete them; the test controls each tombstone timestamp.
	_ = store.Put(context.Background(), intentRow(clock, "acme", "cp-1", "s1", 1))
	_ = store.Put(context.Background(), intentRow(clock, "acme", "cp-2", "s2", 1))

	_ = store.SoftDelete(context.Background(), "acme", "cp-1")
	clock = clock.Add(time.Hour)
	_ = store.SoftDelete(context.Background(), "acme", "cp-2")

	cutoff := clock.Add(-30 * time.Minute) // between the two deletes
	got, err := store.ListSoftDeletedBefore(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("ListSoftDeletedBefore: %v", err)
	}
	if len(got) != 1 || got[0].CheckpointID != "cp-1" {
		t.Errorf("ListSoftDeletedBefore = %+v, want cp-1 only", got)
	}
}

// spec: §12.8 — DeleteByUser removes every row tied to the supplied
// session ids.
func TestDeleteByUserScopes(t *testing.T) {
	clock := time.Now()
	store := partialmanifeststore.NewMemoryStore(nil)
	_ = store.Put(context.Background(), intentRow(clock, "acme", "cp-a", "a", 1))
	_ = store.Put(context.Background(), intentRow(clock, "acme", "cp-b", "b", 1))
	_ = store.Put(context.Background(), intentRow(clock, "acme", "cp-c", "c", 1))

	if err := store.DeleteByUser(context.Background(), "acme", "alice", []string{"a", "c"}); err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}
	if _, err := store.Get(context.Background(), "acme", "cp-a"); !errors.Is(err, partialmanifeststore.ErrNotFound) {
		t.Error("session 'a' should be removed")
	}
	if _, err := store.Get(context.Background(), "acme", "cp-b"); err != nil {
		t.Errorf("session 'b' should survive: %v", err)
	}
	if _, err := store.Get(context.Background(), "acme", "cp-c"); !errors.Is(err, partialmanifeststore.ErrNotFound) {
		t.Error("session 'c' should be removed")
	}
}

// spec: §12.8 — DeleteByTenant removes every row scoped to the tenant.
func TestDeleteByTenantSweepsAll(t *testing.T) {
	clock := time.Now()
	store := partialmanifeststore.NewMemoryStore(nil)
	_ = store.Put(context.Background(), intentRow(clock, "acme", "cp-1", "s1", 1))
	_ = store.Put(context.Background(), intentRow(clock, "acme", "cp-2", "s2", 1))
	_ = store.Put(context.Background(), intentRow(clock, "globex", "cp-3", "s3", 1))

	if err := store.DeleteByTenant(context.Background(), "acme"); err != nil {
		t.Fatalf("DeleteByTenant: %v", err)
	}
	if _, err := store.Get(context.Background(), "acme", "cp-1"); !errors.Is(err, partialmanifeststore.ErrNotFound) {
		t.Error("acme rows should be removed")
	}
	if _, err := store.Get(context.Background(), "globex", "cp-3"); err != nil {
		t.Errorf("globex row should survive: %v", err)
	}
}

// spec: §12.5 — HardDelete removes the row entirely (called by the
// hard-prune sweep on rows past the tombstone retention window).
func TestHardDeleteRemovesRow(t *testing.T) {
	store := partialmanifeststore.NewMemoryStore(nil)
	_ = store.Put(context.Background(), intentRow(time.Now(), "acme", "cp-1", "s1", 1))
	if err := store.HardDelete(context.Background(), "acme", "cp-1"); err != nil {
		t.Fatalf("HardDelete: %v", err)
	}
	if _, err := store.Get(context.Background(), "acme", "cp-1"); !errors.Is(err, partialmanifeststore.ErrNotFound) {
		t.Error("HardDelete did not remove the row")
	}
}

// spec: §10.1 chunk_encoding closed enum.
func TestChunkEncodingValidValues(t *testing.T) {
	if !partialmanifeststore.ChunkEncodingTar.IsValid() {
		t.Error("ChunkEncodingTar should be valid")
	}
	if !partialmanifeststore.ChunkEncodingTarGz.IsValid() {
		t.Error("ChunkEncodingTarGz should be valid")
	}
	if partialmanifeststore.ChunkEncoding("zip").IsValid() {
		t.Error("zip should not be a valid chunk encoding")
	}
}

// spec: §10.1 line 141 — manifest_reason is a closed enum.
func TestManifestReasonValidValues(t *testing.T) {
	for _, r := range []string{
		partialmanifeststore.ReasonInProgress, partialmanifeststore.ReasonComplete,
		partialmanifeststore.ReasonTimeout, partialmanifeststore.ReasonStreamTruncated,
		partialmanifeststore.ReasonSuperseded, partialmanifeststore.ReasonQuotaExceeded,
	} {
		if !partialmanifeststore.IsValidReason(r) {
			t.Errorf("%q should be a valid manifest_reason", r)
		}
	}
	if partialmanifeststore.IsValidReason("terminated_during_resume") {
		t.Error("terminated_during_resume was removed from the enum and must not validate")
	}
}

// spec: §10.1 partial-manifest path — HasActivePartialManifest is true
// while an active partial row exists and false once it is finalised
// complete (partial = false) or superseded.
func TestHasActivePartialManifest(t *testing.T) {
	clock := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	store := partialmanifeststore.NewMemoryStore(func() time.Time { return clock })
	ctx := context.Background()

	has, err := store.HasActivePartialManifest(ctx, "acme", "sess_h")
	if err != nil || has {
		t.Fatalf("HasActivePartialManifest on empty store = (%v, %v), want (false, nil)", has, err)
	}

	if err := store.Put(ctx, intentRow(clock, "acme", "cp-h", "sess_h", 1)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	has, err = store.HasActivePartialManifest(ctx, "acme", "sess_h")
	if err != nil || !has {
		t.Fatalf("HasActivePartialManifest after intent = (%v, %v), want (true, nil)", has, err)
	}

	// Confirm a chunk and finalise complete: the row is no longer partial.
	if err := store.ConfirmChunk(ctx, "acme", "cp-h", 0, 16); err != nil {
		t.Fatalf("ConfirmChunk: %v", err)
	}
	if err := store.Finalise(ctx, "acme", "cp-h", false, partialmanifeststore.ReasonComplete); err != nil {
		t.Fatalf("Finalise: %v", err)
	}
	has, err = store.HasActivePartialManifest(ctx, "acme", "sess_h")
	if err != nil || has {
		t.Fatalf("HasActivePartialManifest after complete = (%v, %v), want (false, nil)", has, err)
	}
}

// spec: §10.1 line 155 — LatestFull returns the most-recently-created
// active full checkpoint row (partial = false) and never a partial one.
func TestLatestFullSelectsNewestCompleteRow(t *testing.T) {
	clock := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	store := partialmanifeststore.NewMemoryStore(func() time.Time { return clock })
	ctx := context.Background()

	if _, err := store.LatestFull(ctx, "acme", "sess_f"); !errors.Is(err, partialmanifeststore.ErrNotFound) {
		t.Fatalf("LatestFull on empty store = %v, want ErrNotFound", err)
	}

	// A complete full checkpoint at generation 1.
	if err := store.Put(ctx, intentRow(clock, "acme", "cp-old", "sess_f", 1)); err != nil {
		t.Fatalf("Put old: %v", err)
	}
	if err := store.ConfirmChunk(ctx, "acme", "cp-old", 0, 16); err != nil {
		t.Fatalf("ConfirmChunk old: %v", err)
	}
	if err := store.Finalise(ctx, "acme", "cp-old", false, partialmanifeststore.ReasonComplete); err != nil {
		t.Fatalf("Finalise old: %v", err)
	}
	// A newer active partial drain row must be ignored by LatestFull.
	if err := store.Put(ctx, intentRow(clock, "acme", "cp-partial", "sess_f", 5)); err != nil {
		t.Fatalf("Put partial: %v", err)
	}

	got, err := store.LatestFull(ctx, "acme", "sess_f")
	if err != nil {
		t.Fatalf("LatestFull: %v", err)
	}
	if got.CheckpointID != "cp-old" {
		t.Fatalf("LatestFull.CheckpointID = %q, want cp-old (the complete row, not the active partial)", got.CheckpointID)
	}
	if got.Partial {
		t.Fatalf("LatestFull returned a partial row")
	}
}
