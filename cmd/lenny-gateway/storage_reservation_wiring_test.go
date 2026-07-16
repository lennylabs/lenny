// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/quota/storagequota"
)

// spec: §11.2 reservation-aware rebuild — outstandingReservationSource adapts
// the checkpoint manifest store's SumOutstandingReservations into the
// storagequota.LiveBytesSource seam so buildRedisAndQuota can fold outstanding
// checkpoint reservations into the durable artifact_store byte sum. A nil
// store yields a nil source (the composed seam degrades to the artifact_store
// sum alone) rather than a panic.
func TestOutstandingReservationSource_NilStoreYieldsNil(t *testing.T) {
	if got := outstandingReservationSource(nil); got != nil {
		t.Errorf("outstandingReservationSource(nil) = %v, want nil", got)
	}
}

// spec: §12.4 line 222 — the seam composed from the artifact-store live-byte
// sum and outstandingReservationSource folds a tenant's held checkpoint
// reservation into the during-outage enforcement total, so the reserved
// headroom counts against the quota rather than being handed back invisibly
// while Redis is down. Regression against the reservation-free wiring that
// summed live artifact bytes alone.
func TestOutstandingReservationSource_FoldsIntoLiveBytesSeam(t *testing.T) {
	manifests := partialmanifeststore.NewMemoryStore(nil)
	// An intent row that reserved 500 bytes and has uploaded 120 so far leaves
	// 380 bytes outstanding against the tenant's quota.
	if err := manifests.Put(context.Background(), partialmanifeststore.Record{
		TenantID:             "acme",
		SessionID:            "sess-1",
		CheckpointID:         "chk-1",
		ChunkObjectKeyPrefix: "checkpoints/acme/sess-1/chk-1/",
		ReservedBytes:        500,
	}); err != nil {
		t.Fatalf("seed manifest row: %v", err)
	}
	if err := manifests.ConfirmChunk(context.Background(), "acme", "chk-1", 0, 120); err != nil {
		t.Fatalf("confirm chunk: %v", err)
	}

	liveBytes := storagequota.LiveBytesSource(
		func(context.Context, string) (int64, error) { return 400, nil },
	)
	seam := storagequota.ReservationAwareLiveBytes(liveBytes, outstandingReservationSource(manifests))
	got, err := seam(context.Background(), "acme")
	if err != nil {
		t.Fatalf("composed seam: %v", err)
	}
	if got != 780 {
		t.Errorf("composed total = %d, want 780 (400 live + 380 outstanding reservation)", got)
	}
}
