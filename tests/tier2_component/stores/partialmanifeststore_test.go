//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §10.1 checkpoint_manifest store, exercising the
// Postgres-backed pkg/gateway/checkpoint/partialmanifeststore/pgstore
// against a real container with the production migrations (0175)
// applied. Covers the intent-row Put + Get round-trip on the
// (tenant_id, checkpoint_id) key, the supersede-on-write and fencing
// invariants under the real partial_manifest_active_uniq index, the
// §10.1 line 131 monotonic ConfirmChunk counter, Finalise, the
// exactly-once ReleaseReservation guard, SumOutstandingReservations,
// the §12.5 ListReclaimable backstop predicate with its terminal-state
// join, the soft-delete idempotency guard, and cross-tenant RLS
// isolation.
package stores_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	partialmanifestpg "github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	sessionpg "github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
)

// manifestRow returns a valid intent-row Record for the supplied ids.
func manifestRow(now time.Time, tenant, checkpointID, session string, gen int64) partialmanifeststore.Record {
	return partialmanifeststore.Record{
		TenantID:               tenant,
		CheckpointID:           checkpointID,
		SessionID:              session,
		CoordinationGeneration: gen,
		RecoveryGeneration:     gen,
		ChunkObjectKeyPrefix:   "/" + tenant + "/checkpoints/" + session + "/" + checkpointID + "/",
		ChunkSizeBytes:         16 << 20,
		ChunkEncoding:          partialmanifeststore.ChunkEncodingTar,
		CheckpointStartedAt:    now,
		CheckpointTimeoutAt:    now.Add(90 * time.Second),
	}
}

// seedSessionInState creates a session row in the given state so the
// ListReclaimable terminal-state join has a row to match against.
func seedSessionInState(t *testing.T, ctx context.Context, sess *sessionpg.Store, tenant string, state session.State) string {
	t.Helper()
	id := newUUID(t)
	if err := sess.Create(ctx, sessionstore.Session{
		ID: id, TenantID: tenant, State: state, RuntimeRef: "echo",
	}); err != nil {
		t.Fatalf("seed session (%s): %v", state, err)
	}
	return id
}

func TestCheckpointManifestStoreContract(t *testing.T) {
	t.Parallel()
	sessStore, pg := startStore(t)
	store := partialmanifestpg.New(pg.Pool, nil)
	ctx := context.Background()

	t.Run("put and get round-trip on the checkpoint_id key", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		checkpointID := newUUID(t)
		baseline := int64(4096)
		want := manifestRow(time.Now().UTC(), tenant, checkpointID, newUUID(t), 3)
		want.ChunkEncoding = partialmanifeststore.ChunkEncodingTarGz
		want.ReservedBytes = 1 << 20
		want.BaselineFullCheckpointBytes = &baseline
		if err := store.Put(ctx, want); err != nil {
			t.Fatalf("Put: %v", err)
		}
		got, err := store.Get(ctx, tenant, checkpointID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.ChunkObjectKeyPrefix != want.ChunkObjectKeyPrefix {
			t.Errorf("prefix = %q, want %q", got.ChunkObjectKeyPrefix, want.ChunkObjectKeyPrefix)
		}
		if got.ChunkEncoding != partialmanifeststore.ChunkEncodingTarGz {
			t.Errorf("chunk_encoding = %q, want tar.gz", got.ChunkEncoding)
		}
		if got.SlotID != partialmanifeststore.SlotDefault {
			t.Errorf("slot_id = %q, want default", got.SlotID)
		}
		if !got.Partial || got.ManifestReason != partialmanifeststore.ReasonInProgress {
			t.Errorf("intent row = partial %v reason %q, want true/in_progress", got.Partial, got.ManifestReason)
		}
		if got.BaselineFullCheckpointBytes == nil || *got.BaselineFullCheckpointBytes != 4096 {
			t.Errorf("baseline = %v, want 4096", got.BaselineFullCheckpointBytes)
		}
		if got.CreatedAt.IsZero() {
			t.Error("CreatedAt should be stamped on insert")
		}
		if !got.DeletedAt.IsZero() || !got.ReservationReleasedAt.IsZero() {
			t.Error("an active, unreleased intent row must have zero deleted_at and reservation_released_at")
		}
	})

	t.Run("get missing returns ErrNotFound", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		if _, err := store.Get(ctx, tenant, newUUID(t)); !errors.Is(err, partialmanifeststore.ErrNotFound) {
			t.Errorf("Get missing: got %v, want ErrNotFound", err)
		}
	})

	// spec: §10.1 lines 137, 143-151, 155 — supersede-on-write collapses
	// the active partial set to one row under the real
	// partial_manifest_active_uniq index (two same-generation attempts on
	// one coordinator), and a fenced strictly-lower write is rejected.
	t.Run("supersede-on-write and fencing under the partial unique index", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		session := newUUID(t)
		first := newUUID(t)
		second := newUUID(t)
		// Two attempts share coordination_generation 5 (one coordinator).
		if err := store.Put(ctx, manifestRow(time.Now().UTC(), tenant, first, session, 5)); err != nil {
			t.Fatalf("Put first: %v", err)
		}
		if err := store.Put(ctx, manifestRow(time.Now().UTC(), tenant, second, session, 5)); err != nil {
			t.Fatalf("Put second: %v", err)
		}
		f, _ := store.Get(ctx, tenant, first)
		if f.DeletedAt.IsZero() {
			t.Error("first attempt should be superseded (soft-deleted)")
		}
		if f.ManifestReason != partialmanifeststore.ReasonSuperseded {
			t.Errorf("superseded reason = %q, want superseded", f.ManifestReason)
		}
		active, err := store.LatestActive(ctx, tenant, session)
		if err != nil {
			t.Fatalf("LatestActive: %v", err)
		}
		if active.CheckpointID != second {
			t.Errorf("LatestActive = %q, want the second attempt", active.CheckpointID)
		}

		// A strictly-lower-generation write is a fenced stale coordinator.
		if err := store.Put(ctx, manifestRow(time.Now().UTC(), tenant, newUUID(t), session, 4)); !errors.Is(err, partialmanifeststore.ErrStaleGeneration) {
			t.Errorf("stale Put gen 4: got %v, want ErrStaleGeneration", err)
		}
	})

	// spec: §10.1 line 131 — ConfirmChunk advances the counters
	// monotonically under the `chunk_count < n + 1` guard.
	t.Run("confirm chunk is monotonic", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		checkpointID := newUUID(t)
		if err := store.Put(ctx, manifestRow(time.Now().UTC(), tenant, checkpointID, newUUID(t), 1)); err != nil {
			t.Fatalf("Put: %v", err)
		}
		if err := store.ConfirmChunk(ctx, tenant, checkpointID, 2, 300); err != nil {
			t.Fatalf("ConfirmChunk 2: %v", err)
		}
		// A late index-1 ack must not decrement.
		if err := store.ConfirmChunk(ctx, tenant, checkpointID, 1, 200); err != nil {
			t.Fatalf("ConfirmChunk 1: %v", err)
		}
		got, _ := store.Get(ctx, tenant, checkpointID)
		if got.ChunkCount != 3 || got.WorkspaceBytesUploaded != 300 {
			t.Errorf("chunk_count=%d bytes=%d, want 3/300", got.ChunkCount, got.WorkspaceBytesUploaded)
		}
	})

	// spec: §10.1 line 141 — Finalise stamps the terminal disposition.
	t.Run("finalise flips partial and reason", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		checkpointID := newUUID(t)
		if err := store.Put(ctx, manifestRow(time.Now().UTC(), tenant, checkpointID, newUUID(t), 1)); err != nil {
			t.Fatalf("Put: %v", err)
		}
		if err := store.Finalise(ctx, tenant, checkpointID, false, partialmanifeststore.ReasonComplete); err != nil {
			t.Fatalf("Finalise: %v", err)
		}
		got, _ := store.Get(ctx, tenant, checkpointID)
		if got.Partial || got.ManifestReason != partialmanifeststore.ReasonComplete {
			t.Errorf("finalised = partial %v reason %q, want false/complete", got.Partial, got.ManifestReason)
		}
	})

	// spec: §11.2 / §12.5 GC rule 4 — ReleaseReservation reports
	// rows_affected == 1 on the first release and 0 on every retry, so
	// the reservation decrement fires exactly once.
	t.Run("release reservation is exactly-once", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		checkpointID := newUUID(t)
		r := manifestRow(time.Now().UTC(), tenant, checkpointID, newUUID(t), 1)
		r.ReservedBytes = 2048
		if err := store.Put(ctx, r); err != nil {
			t.Fatalf("Put: %v", err)
		}
		if n, err := store.ReleaseReservation(ctx, tenant, checkpointID); err != nil || n != 1 {
			t.Fatalf("first ReleaseReservation = (%d, %v), want (1, nil)", n, err)
		}
		if n, err := store.ReleaseReservation(ctx, tenant, checkpointID); err != nil || n != 0 {
			t.Fatalf("second ReleaseReservation = (%d, %v), want (0, nil)", n, err)
		}
	})

	// spec: §11.2 — SumOutstandingReservations sums the tenant's
	// unreleased reservation headroom over active rows.
	t.Run("sum outstanding reservations", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		a := newUUID(t)
		ra := manifestRow(time.Now().UTC(), tenant, a, newUUID(t), 1)
		ra.ReservedBytes = 1000
		if err := store.Put(ctx, ra); err != nil {
			t.Fatalf("Put a: %v", err)
		}
		if err := store.ConfirmChunk(ctx, tenant, a, 0, 400); err != nil { // 600 outstanding
			t.Fatalf("ConfirmChunk: %v", err)
		}
		b := newUUID(t)
		rb := manifestRow(time.Now().UTC(), tenant, b, newUUID(t), 1)
		rb.ReservedBytes = 2000 // 2000 outstanding
		if err := store.Put(ctx, rb); err != nil {
			t.Fatalf("Put b: %v", err)
		}
		// A released row contributes nothing.
		c := newUUID(t)
		rc := manifestRow(time.Now().UTC(), tenant, c, newUUID(t), 1)
		rc.ReservedBytes = 5000
		if err := store.Put(ctx, rc); err != nil {
			t.Fatalf("Put c: %v", err)
		}
		if _, err := store.ReleaseReservation(ctx, tenant, c); err != nil {
			t.Fatalf("ReleaseReservation c: %v", err)
		}
		sum, err := store.SumOutstandingReservations(ctx, tenant)
		if err != nil {
			t.Fatalf("SumOutstandingReservations: %v", err)
		}
		if sum != 2600 {
			t.Errorf("sum = %d, want 2600 (600 + 2000)", sum)
		}
	})

	// spec: §12.5 GC rule 6 — ListReclaimable returns an abandoned active
	// row whose session has reached a terminal state and whose
	// checkpoint_timeout_at has passed, and holds off on a live
	// in_progress row inside its timeout on a running session.
	t.Run("list reclaimable joins terminal session state", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)

		// A terminal (completed) session with an in_progress row past its
		// checkpoint_timeout_at: reclaimable.
		terminalSess := seedSessionInState(t, ctx, sessStore, tenant, session.StateCompleted)
		reclaimable := newUUID(t)
		past := time.Now().UTC().Add(-2 * time.Hour)
		rr := manifestRow(past, tenant, reclaimable, terminalSess, 1)
		rr.CheckpointTimeoutAt = past.Add(time.Minute) // already elapsed
		if err := store.Put(ctx, rr); err != nil {
			t.Fatalf("Put reclaimable: %v", err)
		}

		// A running session with an in_progress row inside its timeout:
		// NOT reclaimable.
		liveSess := seedSessionInState(t, ctx, sessStore, tenant, session.StateRunning)
		live := newUUID(t)
		if err := store.Put(ctx, manifestRow(time.Now().UTC(), tenant, live, liveSess, 1)); err != nil {
			t.Fatalf("Put live: %v", err)
		}

		got, err := store.ListReclaimable(ctx, time.Hour)
		if err != nil {
			t.Fatalf("ListReclaimable: %v", err)
		}
		seen := map[string]bool{}
		for _, r := range got {
			seen[r.CheckpointID] = true
		}
		if !seen[reclaimable] {
			t.Errorf("reclaimable row (terminal session, past timeout) not returned: got %+v", got)
		}
		if seen[live] {
			t.Error("live in_progress row on a running session must not be reclaimable")
		}
	})

	// spec: §12.5 hard-prune — SoftDelete is idempotent under the
	// deleted_at IS NULL guard, re-keyed on checkpoint_id.
	t.Run("soft-delete idempotent under deleted_at IS NULL", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		checkpointID := newUUID(t)
		if err := store.Put(ctx, manifestRow(time.Now().UTC(), tenant, checkpointID, newUUID(t), 1)); err != nil {
			t.Fatalf("Put: %v", err)
		}
		if err := store.SoftDelete(ctx, tenant, checkpointID); err != nil {
			t.Fatalf("SoftDelete: %v", err)
		}
		first, _ := store.Get(ctx, tenant, checkpointID)
		if first.DeletedAt.IsZero() {
			t.Fatal("SoftDelete did not stamp deleted_at")
		}
		time.Sleep(2 * time.Millisecond)
		if err := store.SoftDelete(ctx, tenant, checkpointID); err != nil {
			t.Fatalf("Replay SoftDelete: %v", err)
		}
		second, _ := store.Get(ctx, tenant, checkpointID)
		if !second.DeletedAt.Equal(first.DeletedAt) {
			t.Errorf("deleted_at = %v, want stable %v across replays", second.DeletedAt, first.DeletedAt)
		}
	})

	t.Run("cross-tenant get returns ErrNotFound under RLS", func(t *testing.T) {
		a := freshTenant(t, ctx, pg)
		b := freshTenant(t, ctx, pg)
		checkpointID := newUUID(t)
		if err := store.Put(ctx, manifestRow(time.Now().UTC(), a, checkpointID, newUUID(t), 1)); err != nil {
			t.Fatalf("Put: %v", err)
		}
		if _, err := store.Get(ctx, b, checkpointID); !errors.Is(err, partialmanifeststore.ErrNotFound) {
			t.Errorf("cross-tenant Get: got %v, want ErrNotFound", err)
		}
	})

	t.Run("DeleteByTenant clears the tenant's rows", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		other := freshTenant(t, ctx, pg)
		keep := newUUID(t)
		for _, tid := range []struct{ tenant, checkpoint string }{
			{tenant, newUUID(t)}, {tenant, newUUID(t)}, {other, keep},
		} {
			if err := store.Put(ctx, manifestRow(time.Now().UTC(), tid.tenant, tid.checkpoint, newUUID(t), 1)); err != nil {
				t.Fatalf("Put: %v", err)
			}
		}
		if err := store.DeleteByTenant(ctx, tenant); err != nil {
			t.Fatalf("DeleteByTenant: %v", err)
		}
		if _, err := store.LatestActive(ctx, tenant, "any"); !errors.Is(err, partialmanifeststore.ErrNotFound) {
			t.Error("tenant rows should be deleted")
		}
		if _, err := store.Get(ctx, other, keep); err != nil {
			t.Errorf("the other tenant's row should survive: %v", err)
		}
	})
}
