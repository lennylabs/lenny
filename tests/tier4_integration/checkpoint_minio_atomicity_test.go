// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration coverage for the §4.4 / §10.1 checkpoint-atomicity
// contract, re-expressed against the gateway-driven grant/confirm upload
// driver that replaced the deleted in-adapter single-object upload bridge.
//
// The checkpoint upload no longer flows through an in-adapter sink that
// PUTs one object: the gateway drives the bidirectional Checkpoint stream,
// mints one presigned PUT capability per chunk, confirms each committed
// chunk with Stat, and finalises the manifest row on the terminal frame.
// This file drives the real gateway checkpointer over a live gRPC stream
// against a chunked producer and a prefix-capable object store, and pins
// the atomicity contract:
//
//   - the intent row is partial = true from INSERT, before the first
//     chunk confirms;
//   - it flips to partial = false only after every declared byte is
//     Stat-confirmed;
//   - a chunk PUT that fails past the retry budget leaves partial = true,
//     and the DeleteObject sweep removes the chunks the aborted attempt
//     wrote.
//
// The §13.2 signature-binding enforcement (a live object store refusing a
// widened capability) is covered separately by
// checkpoint_capability_enforcement_test.go against a live MinIO
// container; this file exercises the store-agnostic driver contract.
//
// spec: §4.4 (Event / Checkpoint Store atomicity), §10.1 (gateway-driven
// chunked checkpoint upload, intent-row-first ordering, finalisation).

package tier4_integration_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
)

// spec: §10.1 — the intent row is partial = true from INSERT and flips to
// partial = false / complete only after every declared byte is
// Stat-confirmed.
func TestCheckpointFinalisesCompleteOnlyAfterEveryByteConfirmed(t *testing.T) {
	adapter := &cpChunkedAdapter{probeBytes: 30, chunkLens: []int64{10, 10, 10}, failAfter: -1, truncateAfter: -1}
	h := newCPDriverHarness(t, adapter)

	if err := h.cp.Checkpoint(context.Background(), cpTenant, cpSession); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	rec := h.latestManifest(t)
	if rec.Partial {
		t.Errorf("manifest partial = true, want false after every declared byte confirmed")
	}
	if rec.ManifestReason != partialmanifeststore.ReasonComplete {
		t.Errorf("manifest_reason = %q, want complete", rec.ManifestReason)
	}
	if rec.ChunkCount != 3 {
		t.Errorf("chunk_count = %d, want 3", rec.ChunkCount)
	}
	// Every chunk object is present in the store: a complete checkpoint is
	// not swept.
	if got := h.store.count(rec.ChunkObjectKeyPrefix); got != 3 {
		t.Errorf("objects under prefix = %d, want 3 (complete checkpoint retained)", got)
	}
}

// spec: §4.4 / §10.1 line 157 — a PUT that fails past the retry budget
// leaves partial = true and the DeleteObject sweep removes the chunks the
// aborted attempt confirmed before the failure.
func TestCheckpointLeavesPartialAndSweepsChunksOnPutFailure(t *testing.T) {
	// Chunk 0 commits; chunk 1's PUT is reported as a retry-exhausted
	// object-store failure (a CheckpointFailed frame), so the stream
	// terminates without a Summary.
	adapter := &cpChunkedAdapter{
		probeBytes:    30,
		chunkLens:     []int64{10, 10, 10},
		failAfter:     0,
		failCode:      "SlowDown",
		truncateAfter: -1,
	}
	h := newCPDriverHarness(t, adapter)

	if err := h.cp.Checkpoint(context.Background(), cpTenant, cpSession); err == nil {
		t.Fatal("Checkpoint succeeded despite a retry-exhausted PUT, want failure")
	}
	rec := h.latestManifest(t)
	if !rec.Partial {
		t.Errorf("manifest partial = false, want true after a PUT failure")
	}
	// The abort sweep ran over the attempt's prefix, so the confirmed chunk
	// object is gone.
	if got := h.store.count(rec.ChunkObjectKeyPrefix); got != 0 {
		t.Errorf("objects under prefix = %d after the sweep, want 0", got)
	}
	if !h.deleter.sweptPrefix(rec.ChunkObjectKeyPrefix) {
		t.Errorf("abort sweep did not run over prefix %q", rec.ChunkObjectKeyPrefix)
	}
}

// spec: §10.1 — a stream that truncates before the Summary leaves
// partial = true; the confirmed chunks are swept because no resume will
// consume a stream_truncated attempt.
func TestCheckpointLeavesPartialOnTruncatedStream(t *testing.T) {
	adapter := &cpChunkedAdapter{probeBytes: 30, chunkLens: []int64{10, 10, 10}, failAfter: -1, truncateAfter: 0}
	h := newCPDriverHarness(t, adapter)

	if err := h.cp.Checkpoint(context.Background(), cpTenant, cpSession); err == nil {
		t.Fatal("Checkpoint succeeded on a truncated stream, want failure")
	}
	rec := h.latestManifest(t)
	if !rec.Partial {
		t.Errorf("manifest partial = false, want true after a truncated stream")
	}
	if rec.ManifestReason != partialmanifeststore.ReasonStreamTruncated {
		t.Errorf("manifest_reason = %q, want stream_truncated", rec.ManifestReason)
	}
	if got := h.store.count(rec.ChunkObjectKeyPrefix); got != 0 {
		t.Errorf("objects under prefix = %d after the sweep, want 0", got)
	}
}
