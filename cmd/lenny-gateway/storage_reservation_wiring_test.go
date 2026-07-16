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

// spec: §11.2 step 3 (line 37) / §12.4 line 222 — the reservation-aware storage
// LiveBytesSource is composed whenever the Postgres artifact catalog is wired,
// independent of whether Redis is configured. A Postgres-catalog deployment
// without Redis rebuilds its in-memory storage counter from Postgres at
// startup, so the counter does not begin at zero and under-count (failing the
// storage-quota gate open) after a restart. Regression against the wiring that
// composed the seam only inside the Redis branch, which left w.storageLiveBytes
// nil in the no-Redis config and skipped the startup rebuild entirely.
func TestBuildRedisAndQuota_ComposesStorageSeamWithoutRedis(t *testing.T) {
	empty := ""
	w := &gatewayWiring{f: &gatewayFlags{
		redisURL:           &empty,
		redisSentinelAddrs: &empty,
		redisClusterAddrs:  &empty,
	}}
	// A Postgres artifact catalog is wired (pgPool would be non-nil in
	// production); Redis is not. The seam must still be composed.
	w.artifactCatalog = stubCatalog{}
	manifests := partialmanifeststore.NewMemoryStore(nil)
	w.partialManifests = manifests
	if err := manifests.Put(context.Background(), partialmanifeststore.Record{
		TenantID:             "acme",
		SessionID:            "sess-1",
		CheckpointID:         "chk-1",
		ChunkObjectKeyPrefix: "checkpoints/acme/sess-1/chk-1/",
		ReservedBytes:        512,
	}); err != nil {
		t.Fatalf("seed manifest row: %v", err)
	}

	w.buildRedisAndQuota()

	if w.storageLiveBytes == nil {
		t.Fatal("storageLiveBytes is nil after buildRedisAndQuota with a catalog and no Redis: the startup rebuild seam was skipped, so the storage counter would start at zero and fail the quota gate open after a restart")
	}
	if w.storageCounter == nil {
		t.Fatal("storageCounter is nil: the no-Redis path must leave the in-memory counter for the startup rehydrate to rebuild")
	}
	// The composed seam folds the outstanding reservation (stubCatalog reports
	// zero live bytes, so the total is the 512-byte reservation) so the restart
	// rebuild charges it against the tenant's quota.
	got, err := w.storageLiveBytes(context.Background(), "acme")
	if err != nil {
		t.Fatalf("storageLiveBytes seam: %v", err)
	}
	if got != 512 {
		t.Errorf("seam total = %d, want 512 (0 live + 512 outstanding reservation)", got)
	}
}
