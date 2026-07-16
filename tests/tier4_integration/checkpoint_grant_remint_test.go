// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration coverage for the §4.4 grant re-mint on expiry.
//
// A chunk PUT that outlives its grant's TTL is retried within the §4.4
// retry budget, and the retry requests a fresh grant for the same index on
// the open stream. The gateway re-signs the identical object key and
// Content-Length without taking a second storage reservation and without
// advancing the outstanding-grant window, and the chunk confirms exactly
// once.
//
// spec: §4.4 lines 261-264 (retry budget, grant re-mint), §10.1 line 131
// (monotonic confirm counter).

package tier4_integration_test

import (
	"context"
	"testing"
)

// spec: §4.4 — a grant that expires mid-retry is re-minted for the same
// index on the open stream; the gateway re-signs the identical key and
// Content-Length without a second reservation or grant-window advance, and
// the chunk confirms exactly once.
func TestCheckpointReMintsGrantForSameIndexWithoutSecondReservation(t *testing.T) {
	adapter := &cpChunkedAdapter{
		probeBytes:    20,
		chunkLens:     []int64{10, 10},
		remintFor:     map[uint32]bool{0: true}, // request a fresh grant for chunk 0 once
		failAfter:     -1,
		truncateAfter: -1,
	}
	h := newCPDriverHarness(t, adapter)

	if err := h.cp.Checkpoint(context.Background(), cpTenant, cpSession); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}

	// The adapter requested a fresh grant for chunk 0, so the gateway minted
	// two grants for index 0.
	if got := adapter.grantCount(0); got != 2 {
		t.Fatalf("grants minted for chunk 0 = %d, want 2 (original + re-mint)", got)
	}
	// The re-mint re-signed the identical Content-Length.
	lens := adapter.grantLengths(0)
	if len(lens) != 2 || lens[0] != lens[1] {
		t.Errorf("chunk 0 grant Content-Lengths = %v, want two identical values", lens)
	}
	// Chunk 1 was minted exactly once (no re-mint), so the re-mint did not
	// advance the grant window into chunk 1.
	if got := adapter.grantCount(1); got != 1 {
		t.Errorf("grants minted for chunk 1 = %d, want 1", got)
	}

	rec := h.latestManifest(t)
	// The chunk confirmed exactly once despite the re-mint: chunk_count is
	// the distinct confirmed index count, not the grant count.
	if rec.ChunkCount != 2 {
		t.Errorf("chunk_count = %d, want 2 (each index confirmed once)", rec.ChunkCount)
	}
	// No second reservation was taken for the re-minted chunk: the counter
	// reflects the confirmed total (reserved once, reconciled to 20), not a
	// double reservation.
	used, _ := h.quota.Used(context.Background(), cpTenant)
	if used != 20 {
		t.Errorf("storage counter = %d, want 20 (single reservation, reconciled)", used)
	}
}
