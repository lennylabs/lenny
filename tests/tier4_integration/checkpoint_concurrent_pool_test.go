// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration coverage for the per-slot checkpoint capture path on
// a concurrent (maxConcurrentSessions > 1) pool. Each occupied slot is a
// session bound to the shared pod under a distinct slotID; the gateway
// drives one Checkpoint stream per slot, carrying the raw binding slotID
// on CheckpointStart, and finalises a per-slot manifest keyed on the
// (session_id, slot_id) pair. This file drives two occupied slots against
// the real gateway checkpointer over live gRPC streams and pins that each
// slot is captured independently: its manifest chunk_count and
// workspace_bytes match its own producer, its chunk objects live under its
// own prefix, and neither slot's checkpoint carries the other slot's
// content.
//
// This covers the capture half of T-4.4.21. The symmetric per-slot
// restore path (ResumeRequest carries no slot_id today) is a companion
// change and is out of scope here, so no restore round trip is asserted.
//
// spec: §4.4 (checkpoint store durability), §5.2 (per-slot checkpoint
// granularity: each slot's checkpoint includes only that slot's workspace
// state, keyed on the (session_id, slot_id) pair).

package tier4_integration_test

import (
	"context"
	"strings"
	"testing"
)

// spec: §5.2 (per-slot checkpoint granularity), §4.4 (checkpoint store
// durability). A concurrent pool checkpoints each occupied slot
// independently: the gateway sends the slot's own slotID on
// CheckpointStart, finalises a manifest keyed on (session_id, slot_id) with
// that slot's chunk_count and workspace_bytes, and writes the slot's chunk
// objects under its own prefix with none of the other slot's content.
// diagnosis: a failure here means a concurrent-session pod's checkpoint did
// not resolve per slot — a slot's manifest carries the wrong slotID, the
// wrong chunk_count or byte total, or its chunk prefix bled the other
// slot's objects — so the pod-global capture defect T-4.4.21 names is not
// fixed end to end.
func TestConcurrentPoolCapturesEachSlotIndependently(t *testing.T) {
	// Two occupied slots with slot-distinct content: slot-a uploads three
	// 10-byte chunks (30 bytes), slot-b uploads two 20-byte chunks (40
	// bytes). The distinct sizes let each assertion tell the slots apart.
	slotA := &cpChunkedAdapter{probeBytes: 30, chunkLens: []int64{10, 10, 10}, failAfter: -1, truncateAfter: -1}
	slotB := &cpChunkedAdapter{probeBytes: 40, chunkLens: []int64{20, 20}, failAfter: -1, truncateAfter: -1}
	h := newCPConcurrentHarness(t, []cpSlotSpec{
		{sessionID: "sess-a", slotID: "slot-a", adapter: slotA},
		{sessionID: "sess-b", slotID: "slot-b", adapter: slotB},
	})

	ctx := context.Background()
	if err := h.cp.Checkpoint(ctx, cpTenant, "sess-a"); err != nil {
		t.Fatalf("checkpoint slot-a: %v", err)
	}
	if err := h.cp.Checkpoint(ctx, cpTenant, "sess-b"); err != nil {
		t.Fatalf("checkpoint slot-b: %v", err)
	}

	// The gateway addressed each slot's stream with its own slotID (never
	// the manifest-only "default" sentinel, and never the other slot's id).
	if got := slotA.receivedSlotID(); got != "slot-a" {
		t.Errorf("slot-a stream carried slot_id %q, want %q", got, "slot-a")
	}
	if got := slotB.receivedSlotID(); got != "slot-b" {
		t.Errorf("slot-b stream carried slot_id %q, want %q", got, "slot-b")
	}

	recA := h.manifestForSession(t, "sess-a")
	recB := h.manifestForSession(t, "sess-b")

	// Each manifest is keyed on its own (session_id, slot_id) pair.
	if recA.SlotID != "slot-a" {
		t.Errorf("slot-a manifest slot_id = %q, want %q", recA.SlotID, "slot-a")
	}
	if recB.SlotID != "slot-b" {
		t.Errorf("slot-b manifest slot_id = %q, want %q", recB.SlotID, "slot-b")
	}

	// chunk_count and workspace_bytes match each slot's own producer, not
	// coalesced or crossed with the other slot's content.
	if recA.ChunkCount != 3 || recA.WorkspaceBytesUploaded != 30 {
		t.Errorf("slot-a manifest chunk_count=%d bytes=%d, want 3/30", recA.ChunkCount, recA.WorkspaceBytesUploaded)
	}
	if recB.ChunkCount != 2 || recB.WorkspaceBytesUploaded != 40 {
		t.Errorf("slot-b manifest chunk_count=%d bytes=%d, want 2/40", recB.ChunkCount, recB.WorkspaceBytesUploaded)
	}

	// The bucket objects under each slot's prefix match the slot's own
	// chunk count.
	if got := h.store.count(recA.ChunkObjectKeyPrefix); got != 3 {
		t.Errorf("objects under slot-a prefix = %d, want 3", got)
	}
	if got := h.store.count(recB.ChunkObjectKeyPrefix); got != 2 {
		t.Errorf("objects under slot-b prefix = %d, want 2", got)
	}

	// The prefixes are disjoint, so neither slot's checkpoint carries the
	// other's content: counting slot-b's objects under slot-a's prefix (and
	// vice versa) must find nothing beyond each slot's own chunks.
	if recA.ChunkObjectKeyPrefix == recB.ChunkObjectKeyPrefix {
		t.Fatalf("both slots share chunk prefix %q; each slot must checkpoint under its own key", recA.ChunkObjectKeyPrefix)
	}
	if strings.HasPrefix(recB.ChunkObjectKeyPrefix, recA.ChunkObjectKeyPrefix) ||
		strings.HasPrefix(recA.ChunkObjectKeyPrefix, recB.ChunkObjectKeyPrefix) {
		t.Errorf("slot prefixes nest (%q vs %q); one slot's objects fall under the other's prefix",
			recA.ChunkObjectKeyPrefix, recB.ChunkObjectKeyPrefix)
	}
}
