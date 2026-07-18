// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointer"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
)

// backstopCatalog is a minimal ChunkCatalogReleaser: it records the soft-deleted
// URIs and is idempotent on a replay.
type backstopCatalog struct {
	mu      sync.Mutex
	deleted map[string]bool
}

func newBackstopCatalog() *backstopCatalog { return &backstopCatalog{deleted: map[string]bool{}} }

func (c *backstopCatalog) SoftDeleteRow(_ context.Context, uri string, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deleted[uri] = true
	return nil
}

// backstopObjects is a minimal ChunkObjectStore: ListByPrefix returns the
// objects under the walked prefix and HardDeleteObject drops the object so the
// backstop's final empty-prefix check converges.
type backstopObjects struct {
	mu      sync.Mutex
	objects []blobstore.URI
}

func (o *backstopObjects) ListByPrefix(_ context.Context, u blobstore.URI) ([]blobstore.BlobInfo, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	var out []blobstore.BlobInfo
	for _, obj := range o.objects {
		if obj.TenantID == u.TenantID && obj.SessionID == u.SessionID && strings.HasPrefix(obj.PartID, u.PartID) {
			out = append(out, blobstore.BlobInfo{URI: obj})
		}
	}
	return out, nil
}

func (o *backstopObjects) HardDeleteObject(u blobstore.URI) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	kept := o.objects[:0]
	for _, obj := range o.objects {
		if obj.PartID != u.PartID {
			kept = append(kept, obj)
		}
	}
	o.objects = kept
	return nil
}

// backstopQuota records the signed relative Adjust calls.
type backstopQuota struct {
	mu    sync.Mutex
	total int64
}

func (q *backstopQuota) Adjust(_ context.Context, _ string, delta int64) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.total += delta
	return nil
}

func backstopChunkURI(session, checkpointID string, idx int) blobstore.URI {
	return blobstore.URI{
		TenantID:   "acme",
		ObjectType: blobstore.ObjectTypeCheckpoint,
		SessionID:  session,
		PartID:     checkpointID + "/chunk-0000" + string(rune('0'+idx)) + ".tar",
	}
}

// spec: §12.5 partial-manifest backstop and GC rule 6 — the GC-cycle worker
// helper selects abandoned active rows via ListReclaimable and runs the
// three-step release on each: it releases the chunk bytes, decrements the
// reservation headroom, and soft-deletes the manifest row. This pins the wiring
// the sweep runs on the GC cycle beside the tombstone hard-prune.
func TestReclaimAbandonedManifestsSweepsReclaimableRows(t *testing.T) {
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	nowVal := base
	store := partialmanifeststore.NewMemoryStore(func() time.Time { return nowVal })
	ctx := context.Background()

	if err := store.Put(ctx, partialmanifeststore.Record{
		TenantID:             "acme",
		CheckpointID:         "cp-x",
		SessionID:            "s1",
		ChunkObjectKeyPrefix: "/acme/checkpoints/s1/cp-x/",
		ChunkSizeBytes:       16 << 20,
		ChunkEncoding:        partialmanifeststore.ChunkEncodingTar,
		ReservedBytes:        1000,
		CheckpointStartedAt:  base,
		CheckpointTimeoutAt:  base.Add(90 * time.Second),
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := store.ConfirmChunk(ctx, "acme", "cp-x", 0, 100); err != nil {
		t.Fatalf("ConfirmChunk: %v", err)
	}

	catalog := newBackstopCatalog()
	objects := &backstopObjects{objects: []blobstore.URI{backstopChunkURI("s1", "cp-x", 0)}}
	quota := &backstopQuota{}
	release := checkpointer.BackstopReclaim{
		Manifests:          store,
		Catalog:            catalog,
		Objects:            objects,
		Quota:              quota,
		TombstoneRetention: time.Hour,
	}

	// Advance past the resume window so ListReclaimable selects the row (it is
	// past its checkpoint_timeout_at and older than the window).
	nowVal = base.Add(20 * time.Minute)
	reclaimed, err := reclaimAbandonedManifests(ctx, release, 15*time.Minute)
	if err != nil {
		t.Fatalf("reclaimAbandonedManifests: %v", err)
	}
	if reclaimed != 1 {
		t.Fatalf("reclaimed = %d, want 1", reclaimed)
	}
	row, _ := store.Get(ctx, "acme", "cp-x")
	if row.DeletedAt.IsZero() {
		t.Error("backstop did not soft-delete the abandoned manifest row")
	}
	if row.ReservationReleasedAt.IsZero() {
		t.Error("backstop did not release the row's reservation")
	}
	if quota.total != -900 {
		t.Errorf("reservation adjust total = %d, want -900 (reserved − confirmed)", quota.total)
	}
	if !catalog.deleted[backstopChunkURI("s1", "cp-x", 0).String()] {
		t.Error("backstop did not soft-delete the chunk's catalog row")
	}
}

// spec: §12.5 GC rule 6; §10.1 line 133 — a live seal-checkpoint attempt that
// holds an in_progress intent row inside its timeout is not swept out from
// under it. ListReclaimable excludes it, so the worker reclaims nothing. This
// pins the boundary the backstop selector must respect.
func TestReclaimAbandonedManifestsSkipsLiveInProgressRow(t *testing.T) {
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	nowVal := base
	store := partialmanifeststore.NewMemoryStore(func() time.Time { return nowVal })
	ctx := context.Background()

	if err := store.Put(ctx, partialmanifeststore.Record{
		TenantID:             "acme",
		CheckpointID:         "cp-live",
		SessionID:            "s1",
		ChunkObjectKeyPrefix: "/acme/checkpoints/s1/cp-live/",
		ChunkSizeBytes:       16 << 20,
		ChunkEncoding:        partialmanifeststore.ChunkEncodingTar,
		ReservedBytes:        1000,
		CheckpointStartedAt:  base,
		CheckpointTimeoutAt:  base.Add(90 * time.Second),
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	catalog := newBackstopCatalog()
	objects := &backstopObjects{objects: []blobstore.URI{backstopChunkURI("s1", "cp-live", 0)}}
	quota := &backstopQuota{}
	release := checkpointer.BackstopReclaim{
		Manifests: store, Catalog: catalog, Objects: objects, Quota: quota, TombstoneRetention: time.Hour,
	}

	// Still inside the row's checkpoint_timeout_at: the in_progress attempt is
	// live, so the selector must not return it.
	nowVal = base.Add(30 * time.Second)
	reclaimed, err := reclaimAbandonedManifests(ctx, release, 15*time.Minute)
	if err != nil {
		t.Fatalf("reclaimAbandonedManifests: %v", err)
	}
	if reclaimed != 0 {
		t.Fatalf("reclaimed = %d, want 0 (live in_progress attempt within timeout)", reclaimed)
	}
	row, _ := store.Get(ctx, "acme", "cp-live")
	if !row.DeletedAt.IsZero() {
		t.Error("backstop swept a live in_progress attempt row")
	}
	if quota.total != 0 {
		t.Errorf("reservation adjust = %d, want 0 (nothing reclaimed)", quota.total)
	}
}
