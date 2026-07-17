// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration coverage for the §10.1 line 155 reassembly-on-resume
// path, re-expressed against the gateway-minted presigned-GET restore that
// replaced the deleted in-adapter CheckpointSource.
//
// The adapter Resume RPC no longer restores from an in-pod pull: the
// gateway resolves the checkpoint's chunk set from the manifest row it
// owns, verifies contiguity, and mints one presigned single-key GET
// capability per chunk. The adapter fetches them in ascending index order
// and concatenates the bodies into one decompress→untar pipeline. This
// file drives the real gateway resolver against a live MinIO container,
// fetches the minted capabilities the way the adapter's restoreChunks does,
// and pins that the reassembled byte stream reproduces the source archive.
//
// spec: §4.2 line 156 (recovery_generation), §10.1 line 155 (reassembly on
// resume from presigned chunk GET capabilities).

package tier4_integration_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// spec: §10.1 line 155 — the gateway mints one GET capability per chunk in
// [0, chunk_count); fetching them in index order and concatenating the
// bodies reproduces the checkpoint archive byte-for-byte. A completed
// multi-chunk resume leaves the objects and the manifest row intact, a
// second resume succeeds, and the workspace download still serves the
// archive.
//
// diagnosis: a mismatch here means the resolver minted the wrong keys,
// wrong order, or wrong count, so the adapter concatenates a corrupted
// stream and the resumed workspace is unrecoverable.
func TestResumeReassemblesMultiChunkCheckpointFromPresignedGET(t *testing.T) {
	const (
		tenant     = "acme"
		sessID     = "sess_resume"
		checkpoint = "ck_resume"
	)
	store := newLiveChunkStore(t)
	mstore := partialmanifeststore.NewMemoryStore(nil)
	archive := seedCheckpoint(t, store, mstore, tenant, sessID, checkpoint,
		[]int{4096, 4096, 1500}, partialmanifeststore.ChunkEncodingTarGz)

	resolver := chunkResolver(store, mstore)
	ctx := context.Background()

	// A resume mints one GET capability per chunk; fetching them in index
	// order reproduces the archive.
	fetchAll := func() []byte {
		grants, err := resolver.Resolve(ctx, tenant, sessID, checkpoint)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if len(grants) != 3 {
			t.Fatalf("grants = %d, want 3", len(grants))
		}
		var got []byte
		for i, g := range grants {
			if g.Index != uint32(i) {
				t.Fatalf("grant[%d].Index = %d, want %d (ascending order)", i, g.Index, i)
			}
			got = append(got, httpGet(t, g.URL, g.Headers)...)
		}
		return got
	}

	if got := fetchAll(); string(got) != string(archive) {
		t.Fatalf("reassembled stream (%d bytes) does not match source archive (%d bytes)", len(got), len(archive))
	}

	// The completed resume leaves the chunk objects and the manifest row
	// intact: a partial = false checkpoint survives its own resume.
	objs, err := store.ListByPrefix(ctx, resumechunkPrefixURI(tenant, sessID, checkpoint))
	if err != nil {
		t.Fatalf("ListByPrefix: %v", err)
	}
	if len(objs) != 3 {
		t.Fatalf("chunk objects after resume = %d, want 3 (retained)", len(objs))
	}
	rec, err := mstore.Get(ctx, tenant, checkpoint)
	if err != nil {
		t.Fatalf("manifest Get after resume: %v", err)
	}
	if rec.Partial || rec.ManifestReason != partialmanifeststore.ReasonComplete {
		t.Fatalf("manifest after resume: partial=%v reason=%q, want partial=false complete", rec.Partial, rec.ManifestReason)
	}

	// A second resume succeeds and reproduces the same archive.
	if got := fetchAll(); string(got) != string(archive) {
		t.Fatalf("second resume reassembled stream does not match source archive")
	}

	// The workspace download still serves the concatenated archive from the
	// gateway (the gateway reads the chunk bodies itself).
	handler, _ := newChunkGateway(t, store, mstore, func() string { return "sess_derived" },
		sessionstore.Session{
			ID: sessID, TenantID: tenant, State: session.StateCompleted,
			WorkspaceSnapshot: &sessionstore.WorkspaceSnapshot{Ref: checkpoint},
		})
	rr := getWorkspace(t, handler, tenant, sessID)
	if rr.Code != 200 {
		t.Fatalf("workspace download status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != string(archive) {
		t.Fatalf("workspace download body (%d bytes) does not match archive (%d bytes)", rr.Body.Len(), len(archive))
	}
}

// spec: §10.1 line 155 — a partial checkpoint with a non-NULL
// baseline_full_checkpoint_bytes is reassembled only when its confirmed
// workspace bytes clear baseline * partialRecoveryThresholdFraction; below
// the threshold the resume falls back to the last full checkpoint.
//
// diagnosis: a failure here means the recovery threshold is not enforced,
// so a barely-started drain partial is spliced over a healthy full
// checkpoint (or a recoverable partial is discarded).
func TestResumePartialRecoveryHonoursThreshold(t *testing.T) {
	const (
		tenant  = "acme"
		sessID  = "sess_partial"
		belowCk = "ck_below"
		aboveCk = "ck_above"
	)
	store := newLiveChunkStore(t)
	mstore := partialmanifeststore.NewMemoryStore(nil)
	resolver := chunkResolver(store, mstore) // fraction 0.5
	ctx := context.Background()
	baseline := int64(4096)

	// A partial checkpoint that uploaded 1024 bytes (< 0.5 * 4096 = 2048):
	// below threshold, the resolver refuses to reassemble it.
	putChunk(t, store, tenant, sessID, belowCk, 0, partialmanifeststore.ChunkEncodingTarGz, chunkBody(belowCk, 0, 1024))
	seedPartialManifest(t, mstore, tenant, sessID, belowCk, 1, 1024, baseline, partialmanifeststore.ChunkEncodingTarGz)
	if _, err := resolver.Resolve(ctx, tenant, sessID, belowCk); err == nil {
		t.Fatal("below-threshold partial resolved chunks, want a recovery-threshold fallback error")
	}

	// A partial that uploaded 3000 bytes (>= 2048): above threshold, the
	// resolver reassembles it.
	putChunk(t, store, tenant, sessID, aboveCk, 0, partialmanifeststore.ChunkEncodingTarGz, chunkBody(aboveCk, 0, 3000))
	seedPartialManifest(t, mstore, tenant, sessID, aboveCk, 1, 3000, baseline, partialmanifeststore.ChunkEncodingTarGz)
	grants, err := resolver.Resolve(ctx, tenant, sessID, aboveCk)
	if err != nil {
		t.Fatalf("above-threshold Resolve: %v", err)
	}
	if len(grants) != 1 {
		t.Fatalf("above-threshold grants = %d, want 1", len(grants))
	}
	if got := httpGet(t, grants[0].URL, grants[0].Headers); len(got) != 3000 {
		t.Fatalf("fetched chunk = %d bytes, want 3000", len(got))
	}
}
