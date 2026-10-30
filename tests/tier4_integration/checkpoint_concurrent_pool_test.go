// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration coverage for the checkpoint capture path on a
// concurrent (maxConcurrentSessions > 1) pool. Every session is bound to a
// slot on the shared pod and a session-mode slot's identifier is its
// session's identifier, so the gateway drives one Checkpoint stream per
// session, addresses it by that session identifier, and finalises a manifest
// keyed on session_id alone. This file drives two co-tenant sessions against
// the real gateway checkpointer over live gRPC streams and pins that each is
// captured independently: its manifest chunk_count and workspace_bytes match
// its own producer, its chunk objects live under its own prefix, and neither
// session's checkpoint carries the other's content.
//
// spec: §4.4 (checkpoint store durability), §5.2 (per-session checkpoint
// granularity: each session's checkpoint includes only that session's
// workspace state), §10.1 (the manifest row is keyed on session_id).

package tier4_integration_test

import (
	"context"
	"strings"
	"testing"
)

// spec: §5.2 (per-session checkpoint granularity), §4.4 (checkpoint store
// durability), §10.1 (manifest rows keyed on session_id). A concurrent pool
// checkpoints each co-tenant session independently: each manifest carries
// that session's own chunk_count and workspace_bytes, and its chunk objects
// sit under its own prefix with none of the other session's content.
// diagnosis: a failure here means a concurrent-session pod's checkpoint did
// not resolve per session — a manifest carries the wrong chunk_count or byte
// total, or its chunk prefix bled the other session's objects — so the
// pod-global capture defect is not fixed end to end.
func TestConcurrentPoolCapturesEachSessionIndependently(t *testing.T) {
	// Two co-tenant sessions with distinct content: sess-a uploads three
	// 10-byte chunks (30 bytes), sess-b uploads two 20-byte chunks (40
	// bytes). The distinct sizes let each assertion tell the sessions apart.
	producerA := &cpChunkedAdapter{probeBytes: 30, chunkLens: []int64{10, 10, 10}, failAfter: -1, truncateAfter: -1}
	producerB := &cpChunkedAdapter{probeBytes: 40, chunkLens: []int64{20, 20}, failAfter: -1, truncateAfter: -1}
	h := newCPConcurrentHarness(t, []cpSlotSpec{
		{sessionID: "sess-a", adapter: producerA},
		{sessionID: "sess-b", adapter: producerB},
	})

	ctx := context.Background()
	if err := h.cp.Checkpoint(ctx, cpTenant, "sess-a"); err != nil {
		t.Fatalf("checkpoint sess-a: %v", err)
	}
	if err := h.cp.Checkpoint(ctx, cpTenant, "sess-b"); err != nil {
		t.Fatalf("checkpoint sess-b: %v", err)
	}

	// Each manifest is resolved by its own session identifier, which is the
	// whole key after the duplicate slot column is dropped.
	recA := h.manifestForSession(t, "sess-a")
	recB := h.manifestForSession(t, "sess-b")
	if recA.SessionID != "sess-a" || recB.SessionID != "sess-b" {
		t.Fatalf("manifest session ids = %q/%q, want sess-a/sess-b", recA.SessionID, recB.SessionID)
	}

	// chunk_count and workspace_bytes match each session's own producer,
	// neither coalesced nor crossed with the other session's content.
	if recA.ChunkCount != 3 || recA.WorkspaceBytesUploaded != 30 {
		t.Errorf("sess-a manifest chunk_count=%d bytes=%d, want 3/30", recA.ChunkCount, recA.WorkspaceBytesUploaded)
	}
	if recB.ChunkCount != 2 || recB.WorkspaceBytesUploaded != 40 {
		t.Errorf("sess-b manifest chunk_count=%d bytes=%d, want 2/40", recB.ChunkCount, recB.WorkspaceBytesUploaded)
	}

	// The bucket objects under each session's prefix match its own chunk
	// count.
	if got := h.store.count(recA.ChunkObjectKeyPrefix); got != 3 {
		t.Errorf("objects under sess-a prefix = %d, want 3", got)
	}
	if got := h.store.count(recB.ChunkObjectKeyPrefix); got != 2 {
		t.Errorf("objects under sess-b prefix = %d, want 2", got)
	}

	// The prefixes are disjoint, so neither session's checkpoint carries the
	// other's content.
	if recA.ChunkObjectKeyPrefix == recB.ChunkObjectKeyPrefix {
		t.Fatalf("both sessions share chunk prefix %q; each must checkpoint under its own key", recA.ChunkObjectKeyPrefix)
	}
	if strings.HasPrefix(recB.ChunkObjectKeyPrefix, recA.ChunkObjectKeyPrefix) ||
		strings.HasPrefix(recA.ChunkObjectKeyPrefix, recB.ChunkObjectKeyPrefix) {
		t.Errorf("session prefixes nest (%q vs %q); one session's objects fall under the other's prefix",
			recA.ChunkObjectKeyPrefix, recB.ChunkObjectKeyPrefix)
	}
}

// spec: §5.2 (a session-mode slot's identifier is its session's identifier)
// The two checkpoint harness arms model the two binds production mints. A
// binding's slot identifier is populated exactly when the slot reservation
// returned a slot result, so it records whether the pod keeps a counted slot
// ledger, and it is the key the two release paths dispatch on: an exclusive
// bind that carried it would send its release down the counted-slot release
// path. The exclusive-pool harness therefore registers a binding whose slot
// identifier is empty, and the concurrent-pool harness registers each session
// under a slot the session's own identifier names.
// diagnosis: a failure here means the tier-4 checkpoint fixtures no longer
// model the binding contract — an exclusive bind was registered carrying a
// slot identifier, so its release would dispatch onto the counted-slot path,
// or a concurrent bind was registered without one — so every checkpoint case
// in this tier runs against a binding the binder never produces.
func TestCheckpointHarnessBindingsMatchTheirPoolMode(t *testing.T) {
	base := newCPDriverHarness(t, &cpChunkedAdapter{probeBytes: 10, chunkLens: []int64{10}, failAfter: -1, truncateAfter: -1})
	bind, ok := base.registry.Get(cpSession)
	if !ok {
		t.Fatalf("exclusive bind for %s is not registered", cpSession)
	}
	if bind.SlotID != "" {
		t.Errorf("exclusive bind SlotID = %q, want empty (it is populated only when a slot reservation returned a slot result, and the release paths dispatch on it)", bind.SlotID)
	}

	pool := newCPConcurrentHarness(t, []cpSlotSpec{
		{sessionID: "sess-a", adapter: &cpChunkedAdapter{probeBytes: 10, chunkLens: []int64{10}, failAfter: -1, truncateAfter: -1}},
		{sessionID: "sess-b", adapter: &cpChunkedAdapter{probeBytes: 10, chunkLens: []int64{10}, failAfter: -1, truncateAfter: -1}},
	})
	for _, id := range []string{"sess-a", "sess-b"} {
		b, ok := pool.registry.Get(id)
		if !ok {
			t.Fatalf("concurrent bind for %s is not registered", id)
		}
		if b.SlotID != id {
			t.Errorf("concurrent bind SlotID for %s = %q, want %q (a session-mode slot's identifier is its session's identifier)", id, b.SlotID, id)
		}
	}
}
