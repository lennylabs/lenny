// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration coverage for the §11.2 checkpoint quota cap as a
// gateway observation rather than a server-enforcement assumption.
//
// The gateway does not rely on the object store to reject a body larger
// than the signed Content-Length: it clamps every adapter-declared chunk
// length to the chunk_size_bytes it chose itself, confirms each committed
// chunk with Stat, and aborts on a confirmed size larger than the size it
// signed. Against a non-enforcing store, that Stat confirm is the only
// thing standing between a compromised pod and unbounded storage, and the
// bound degrades from zero to the outstanding grant window
// (grant_window × chunk_size_bytes) rather than to unbounded.
//
// spec: §11.2 (the quota cap is a gateway observation), §10.1 line 139
// (fixed-size chunk clamp), §13.2 (residual-exposure bound).

package tier4_integration_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
)

// spec: §11.2 — a chunk that writes more bytes than the signed
// Content-Length against a non-enforcing store is observed by the Stat
// confirm, aborts the attempt, reconciles the excess into the counter, and
// mints no further grant.
// diagnosis: an oversize chunk against a non-enforcing store was not
// caught by the Stat confirm, so a pod exceeds its signed Content-Length
// without the reservation counter reconciling the excess.
func TestCheckpointQuotaCapObservesOverSizeAgainstNonEnforcingStore(t *testing.T) {
	adapter := &cpChunkedAdapter{
		probeBytes:    20,
		chunkLens:     []int64{10, 10},
		putBytes:      map[int]int64{0: 25}, // chunk 0 writes 25 bytes past the signed 10
		failAfter:     -1,
		truncateAfter: -1,
	}
	h := newCPDriverHarness(t, adapter)

	if err := h.cp.Checkpoint(context.Background(), cpTenant, cpSession); err == nil {
		t.Fatal("Checkpoint succeeded despite an over-size chunk on a non-enforcing store, want abort")
	}
	rec := h.latestManifest(t)
	if !rec.Partial {
		t.Errorf("manifest partial = false, want true after an over-size abort")
	}
	if rec.ManifestReason != partialmanifeststore.ReasonQuotaExceeded {
		t.Errorf("manifest_reason = %q, want quota_exceeded", rec.ManifestReason)
	}
	// The excess is reconciled into the counter: the store recorded the
	// actual 25 bytes of chunk 0 (and whatever else confirmed), so the
	// counter reflects the confirmed total rather than the 20-byte
	// reservation.
	used, _ := h.quota.Used(context.Background(), cpTenant)
	if used < 25 {
		t.Errorf("storage counter = %d, want >= 25 (the excess reconciled in)", used)
	}
}

// spec: §11.2 observation (2) — the gateway refuses to sign a chunk-upload
// capability whose Content-Length would carry the attempt's granted total
// past its reservation plus the remaining tenant headroom, and aborts with
// quota_exceeded before signing. A pod that probes a small workspace then
// declares more in-range chunks than it reserved is bounded cumulatively at
// signing, not only by the grant window at any instant.
// diagnosis: a failure here (the checkpoint succeeds, or the third grant is
// minted) means the at-signing cumulative quota gate is absent and the only
// bound left is the instantaneous grant window, so an attempt can sign and
// write past its reservation with no quota_exceeded arm firing.
func TestCheckpointRefusesToSignPastReservation(t *testing.T) {
	// Probe reports 20 bytes and the tenant limit is 20, so the attempt's
	// reservation is its whole budget (no remaining headroom). Three in-range
	// 10-byte chunks (each <= chunk_size_bytes) sum to 30, past the 20-byte
	// cap: the third declaration must be refused at signing.
	adapter := &cpChunkedAdapter{
		probeBytes:    20,
		chunkLens:     []int64{10, 10, 10},
		failAfter:     -1,
		truncateAfter: -1,
	}
	h := newCPDriverHarness(t, adapter)
	h.cp.QuotaLimitFor = func(context.Context, string) (int64, error) { return 20, nil }

	if err := h.cp.Checkpoint(context.Background(), cpTenant, cpSession); err == nil {
		t.Fatal("Checkpoint succeeded on cumulative signed length past the reservation, want abort")
	}
	rec := h.latestManifest(t)
	if !rec.Partial {
		t.Errorf("manifest partial = false, want true after an at-signing quota refusal")
	}
	if rec.ManifestReason != partialmanifeststore.ReasonQuotaExceeded {
		t.Errorf("manifest_reason = %q, want quota_exceeded", rec.ManifestReason)
	}
	// The first two chunks were signed (cumulative 20 within the cap); the
	// third was refused before signing, so no third capability was minted.
	if got := adapter.grantCount(2); got != 0 {
		t.Errorf("grants minted for chunk 2 = %d, want 0 (refused at signing)", got)
	}
	if got := adapter.grantCount(0); got != 1 {
		t.Errorf("grants minted for chunk 0 = %d, want 1", got)
	}
}

// spec: §10.1 line 139 / §13.2 — a ChunkReady whose declared length
// exceeds the gateway-chosen chunk_size_bytes gets no capability, so the
// overage a compromised pod can write is bounded by the grant window times
// the gateway's own chunk size rather than by a pod self-report.
// diagnosis: a ChunkReady declaring more than the gateway's
// chunk_size_bytes still received a capability, so a compromised pod's
// self-reported length rather than the gateway's own chunk size bounds
// what it can write.
func TestCheckpointRejectsOverChunkSizeDeclaration(t *testing.T) {
	adapter := &cpChunkedAdapter{
		probeBytes:    100,
		chunkLens:     []int64{2 << 20}, // 2 MiB declared, but the gateway chunk size is 1 MiB
		failAfter:     -1,
		truncateAfter: -1,
	}
	h := newCPDriverHarness(t, adapter)
	h.cp.ChunkSizeBytes = 1 << 20

	if err := h.cp.Checkpoint(context.Background(), cpTenant, cpSession); err == nil {
		t.Fatal("Checkpoint succeeded on an over-chunk_size declaration, want abort")
	}
	rec := h.latestManifest(t)
	if rec.ManifestReason != partialmanifeststore.ReasonStreamTruncated {
		t.Errorf("manifest_reason = %q, want stream_truncated", rec.ManifestReason)
	}
	// No capability was minted for the over-size declaration.
	if got := adapter.grantCount(0); got != 0 {
		t.Errorf("grants minted for the over-chunk_size declaration = %d, want 0", got)
	}
	if rec.ChunkCount != 0 {
		t.Errorf("chunk_count = %d, want 0 (no grant, no confirm)", rec.ChunkCount)
	}
	// spec: §10.1 line 132 — the zero-chunk abort soft-deletes the row in the
	// finalising transaction, so no empty partial manifest is left active for
	// the resume path or the §12.5 backstop to reclaim.
	if rec.DeletedAt.IsZero() {
		t.Errorf("zero-chunk abort left the manifest row active, want soft-deleted at finalisation")
	}
}
