// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration coverage for the §10.1 line 155 reassembly contiguity
// check, driven against a live MinIO container. Before minting any GET
// capability, the gateway lists the objects under a checkpoint's chunk
// prefix and verifies the prefix [0, chunk_count) is contiguous: every
// index below chunk_count must be present exactly once and in ascending
// order. An index at or beyond chunk_count is expected residue and is
// ignored; a gap or an out-of-order index below chunk_count fails
// reassembly atomically before any chunk body is fetched, and the resume
// falls back to the last successful full checkpoint.
//
// spec: §10.1 line 155.

package tier4_integration_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/resumechunks"
)

// spec: §10.1 line 155 — an extra object at index N with chunk_count = N is
// expected residue; reassembly consumes exactly the contiguous prefix
// [0, N) and ignores the residue.
// diagnosis: reassembly consumed residue beyond chunk_count or rejected a
// valid checkpoint, so an expected extra object corrupts or blocks a
// resume.
func TestReassemblyToleratesResidueBeyondChunkCount(t *testing.T) {
	const (
		tenant = "acme"
		sessID = "sess_residue"
		ck     = "ck_residue"
	)
	store := newLiveChunkStore(t)
	mstore := partialmanifeststore.NewMemoryStore(nil)
	enc := partialmanifeststore.ChunkEncodingTar
	ctx := context.Background()

	// Write four chunk objects [0..3] but record chunk_count = 3, so index 3
	// is residue from a grant that outlived the finalising deadline.
	var want []byte
	for i := 0; i < 4; i++ {
		body := chunkBody(ck, i, 512)
		putChunk(t, store, tenant, sessID, ck, uint32(i), enc, body)
		if i < 3 {
			want = append(want, body...)
		}
	}
	seedCompleteManifest(t, mstore, tenant, sessID, ck, 3, 1536, enc)

	res, err := chunkResolver(store, mstore).Resolve(ctx, tenant, sessID, ck)
	grants := res.Grants
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(grants) != 3 {
		t.Fatalf("grants = %d, want 3 (residue index 3 ignored)", len(grants))
	}
	var got []byte
	for _, g := range grants {
		got = append(got, httpGet(t, g.URL, g.Headers)...)
	}
	if string(got) != string(want) {
		t.Fatalf("reassembled [0,3) does not match; consumed the wrong object set")
	}
}

// spec: §10.1 line 155 — a missing intermediate index below chunk_count
// fails reassembly atomically before any chunk body is fetched; the resume
// falls back to the last successful full checkpoint.
//
// diagnosis: a failure here means a gapped chunk set is spliced into the
// pipeline, corrupting the stream, instead of failing over to the full
// checkpoint.
func TestReassemblyFailsOnGapAndFallsBackToFullCheckpoint(t *testing.T) {
	const (
		tenant  = "acme"
		sessID  = "sess_gap"
		partial = "ck_gap"
		fullCk  = "ck_full"
	)
	store := newLiveChunkStore(t)
	mstore := partialmanifeststore.NewMemoryStore(nil)
	enc := partialmanifeststore.ChunkEncodingTar
	ctx := context.Background()

	// A gapped drain attempt: chunk_count = 3 but only indices 0 and 2 are
	// present (index 1 missing). It carries a baseline above threshold so the
	// failure is the contiguity check, not the recovery-threshold gate.
	putChunk(t, store, tenant, sessID, partial, 0, enc, chunkBody(partial, 0, 4096))
	putChunk(t, store, tenant, sessID, partial, 2, enc, chunkBody(partial, 2, 4096))
	seedPartialManifest(t, mstore, tenant, sessID, partial, 3, 8192, 4096, enc)

	_, err := chunkResolver(store, mstore).Resolve(ctx, tenant, sessID, partial)
	if !errors.Is(err, resumechunks.ErrReassemblyContiguity) {
		t.Fatalf("Resolve on a gapped set = %v, want ErrReassemblyContiguity", err)
	}

	// The fallback: the session's last successful full checkpoint reassembles
	// cleanly. LatestFull selects it, and it resolves without error.
	fullArchive := seedCheckpoint(t, store, mstore, tenant, sessID, fullCk,
		[]int{2048, 2048}, enc)
	full, ferr := mstore.LatestFull(ctx, tenant, sessID)
	if ferr != nil {
		t.Fatalf("LatestFull: %v", ferr)
	}
	if full.CheckpointID != fullCk {
		t.Fatalf("LatestFull = %q, want %q", full.CheckpointID, fullCk)
	}
	res, gerr := chunkResolver(store, mstore).Resolve(ctx, tenant, sessID, full.CheckpointID)
	grants := res.Grants
	if gerr != nil {
		t.Fatalf("fallback Resolve: %v", gerr)
	}
	var got []byte
	for _, g := range grants {
		got = append(got, httpGet(t, g.URL, g.Headers)...)
	}
	if string(got) != string(fullArchive) {
		t.Fatalf("fallback full checkpoint did not reassemble to the source archive")
	}
}

// spec: §10.1 line 155 — an out-of-order index below chunk_count fails the
// same way as a gap, before any chunk body is fetched. A stray
// non-zero-padded chunk-1 sorts after chunk-00002 in lexicographic list
// order, reintroducing index 1 out of ascending order below chunk_count.
// diagnosis: an out-of-order index below chunk_count was reassembled
// instead of failing closed to a full checkpoint, so a resume could splice
// chunks in the wrong order.
func TestReassemblyFailsOnOutOfOrderIndex(t *testing.T) {
	const (
		tenant = "acme"
		sessID = "sess_ooo"
		ck     = "ck_ooo"
	)
	store := newLiveChunkStore(t)
	mstore := partialmanifeststore.NewMemoryStore(nil)
	enc := partialmanifeststore.ChunkEncodingTar
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		putChunk(t, store, tenant, sessID, ck, uint32(i), enc, chunkBody(ck, i, 4096))
	}
	// The stray key parses to index 1 but lists after chunk-00002.
	putRawChunk(t, store, tenant, sessID, ck, 1, enc, chunkBody(ck, 1, 4096))
	seedPartialManifest(t, mstore, tenant, sessID, ck, 3, 12288, 4096, enc)

	if _, err := chunkResolver(store, mstore).Resolve(ctx, tenant, sessID, ck); !errors.Is(err, resumechunks.ErrReassemblyContiguity) {
		t.Fatalf("Resolve on an out-of-order set = %v, want ErrReassemblyContiguity", err)
	}
}
