// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration coverage for the §10.1 intent-row coordination fence.
//
// The gateway writes the §10.1 intent row with the session's coordinator
// generation, recovery counter, and last_checkpoint_workspace_bytes baseline.
// The supersede fence rejects a write whose coordination_generation sits below
// an already-active strictly-higher generation, and the resume path selects
// the highest-generation row, so a stale coordinator cannot orphan a live
// newer writer's active manifest. Pinning every intent row at generation 0
// defeats the fence: with all generations equal the supersede predicate always
// matches and any writer supersedes any other.
//
// spec: §10.1 lines 137, 155 (generation fence, MAX(coordination_generation)
// resume selector, baseline denominator).

package tier4_integration_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// spec: §10.1 lines 137, 155 — the gateway writes the intent row with the
// session's coordination_generation, recovery_generation, and baseline, so a
// newer coordinator supersedes an older active row and the finalised row
// carries the session's generation rather than 0.
// diagnosis: a failure here (Checkpoint rejected as stale, or the finalised
// row carries coordination_generation 0 / no baseline) means the driver wrote
// the intent row with generation 0, so the split-brain fence can never fire
// and a stale coordinator can supersede and orphan a live newer coordinator's
// manifest row.
func TestCheckpointIntentRowCarriesSessionCoordinationGeneration(t *testing.T) {
	adapter := &cpChunkedAdapter{probeBytes: 20, chunkLens: []int64{10, 10}, failAfter: -1, truncateAfter: -1}
	h := newCPDriverHarness(t, adapter)

	// This coordinator holds generation 10, recovery counter 3, and a prior
	// successful full checkpoint of 4096 bytes recorded as the session's
	// last_checkpoint_workspace_bytes.
	if _, err := h.sessions.Update(context.Background(), cpTenant, cpSession, func(row *sessionstore.Session) error {
		row.CoordinationGeneration = 10
		row.RecoveryGeneration = 3
		row.WorkspaceSnapshot = &sessionstore.WorkspaceSnapshot{
			Ref:    "prior-checkpoint",
			Source: sessionstore.WorkspaceSnapshotCheckpoint,
			Bytes:  4096,
		}
		return nil
	}); err != nil {
		t.Fatalf("seed session generation: %v", err)
	}
	// A leftover active partial row from an older generation-5 coordinator
	// still sits in the store. The generation-10 coordinator must supersede it.
	if err := h.manifests.Put(context.Background(), partialmanifeststore.Record{
		TenantID:               cpTenant,
		CheckpointID:           "older-generation-attempt",
		SessionID:              cpSession,
		SlotID:                 partialmanifeststore.SlotDefault,
		CoordinationGeneration: 5,
		ChunkObjectKeyPrefix:   "/acme/checkpoints/s1/older/",
	}); err != nil {
		t.Fatalf("seed older active manifest: %v", err)
	}

	if err := h.cp.Checkpoint(context.Background(), cpTenant, cpSession); err != nil {
		t.Fatalf("Checkpoint: a generation-10 coordinator must supersede the older generation-5 row, got %v", err)
	}

	rec := h.latestManifest(t)
	if rec.CoordinationGeneration != 10 {
		t.Errorf("intent-row coordination_generation = %d, want 10 (the session's generation, not 0)", rec.CoordinationGeneration)
	}
	if rec.RecoveryGeneration != 3 {
		t.Errorf("intent-row recovery_generation = %d, want 3", rec.RecoveryGeneration)
	}
	if rec.BaselineFullCheckpointBytes == nil {
		t.Errorf("intent-row baseline_full_checkpoint_bytes = nil, want 4096 (the session's prior full-checkpoint size)")
	} else if *rec.BaselineFullCheckpointBytes != 4096 {
		t.Errorf("intent-row baseline_full_checkpoint_bytes = %d, want 4096", *rec.BaselineFullCheckpointBytes)
	}

	// The older generation-5 row was superseded by the generation-10 write,
	// not left active alongside it.
	older, err := h.manifests.Get(context.Background(), cpTenant, "older-generation-attempt")
	if err != nil {
		t.Fatalf("get older manifest: %v", err)
	}
	if older.DeletedAt.IsZero() {
		t.Errorf("older generation-5 row still active, want superseded by the generation-10 coordinator")
	}
	if older.ManifestReason != partialmanifeststore.ReasonSuperseded {
		t.Errorf("older row manifest_reason = %q, want superseded", older.ManifestReason)
	}
}

// spec: §10.1 lines 137, 155 — the supersede predicate fences on
// coordination_generation, so a stale coordinator (a lower generation) must
// not release the reservation or sweep the chunk objects of a live newer
// writer's active manifest; its own intent-row Put is rejected as stale.
// diagnosis: a failure here (the higher-generation row's reservation
// released, its row soft-deleted, or its chunk prefix swept) means the
// gateway's supersede release ran destructively against a fenced newer
// writer before its own Put rejected the write, orphaning the live writer's
// reservation and chunks — the outcome the generation fence exists to
// prevent.
func TestCheckpointStaleCoordinatorDoesNotOrphanNewerWriter(t *testing.T) {
	adapter := &cpChunkedAdapter{probeBytes: 20, chunkLens: []int64{10, 10}, failAfter: -1, truncateAfter: -1}
	h := newCPDriverHarness(t, adapter)

	// The session's committed generation is 3, but a newer coordinator has
	// already written an active generation-7 manifest for the same
	// (session, slot). This attempt reads the lagging generation-3 session row.
	if _, err := h.sessions.Update(context.Background(), cpTenant, cpSession, func(row *sessionstore.Session) error {
		row.CoordinationGeneration = 3
		return nil
	}); err != nil {
		t.Fatalf("seed session generation: %v", err)
	}
	const newerPrefix = "/acme/checkpoints/s1/newer-writer/"
	if err := h.manifests.Put(context.Background(), partialmanifeststore.Record{
		TenantID:               cpTenant,
		CheckpointID:           "newer-writer",
		SessionID:              cpSession,
		SlotID:                 partialmanifeststore.SlotDefault,
		CoordinationGeneration: 7,
		ChunkObjectKeyPrefix:   newerPrefix,
		ReservedBytes:          4096,
	}); err != nil {
		t.Fatalf("seed newer-generation active manifest: %v", err)
	}

	if err := h.cp.Checkpoint(context.Background(), cpTenant, cpSession); err == nil {
		t.Fatal("a stale generation-3 coordinator's Checkpoint succeeded, want a stale-generation rejection")
	}

	newer, err := h.manifests.Get(context.Background(), cpTenant, "newer-writer")
	if err != nil {
		t.Fatalf("get newer-generation row: %v", err)
	}
	if !newer.ReservationReleasedAt.IsZero() {
		t.Errorf("newer writer's reservation was released by the stale coordinator, want untouched")
	}
	if !newer.DeletedAt.IsZero() {
		t.Errorf("newer writer's active row was soft-deleted by the stale coordinator, want left active")
	}
	if h.deleter.sweptPrefix(newerPrefix) {
		t.Errorf("newer writer's chunk prefix was swept by the stale coordinator, want untouched")
	}
}
