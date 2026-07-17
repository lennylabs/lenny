// SPDX-License-Identifier: MIT

package resumechunks

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
)

const (
	rcTenant     = "acme"
	rcSession    = "s1"
	rcCheckpoint = "ck1"
)

// fakeManifests returns a fixed record for the resolver under test.
type fakeManifests struct {
	rec partialmanifeststore.Record
	err error
}

func (f fakeManifests) Get(_ context.Context, _, _ string) (partialmanifeststore.Record, error) {
	if f.err != nil {
		return partialmanifeststore.Record{}, f.err
	}
	return f.rec, nil
}

// fakeLister returns a fixed object list in the order supplied, so a test
// can construct an out-of-order or gapped chunk set.
type fakeLister struct {
	objects []blobstore.BlobInfo
}

func (f fakeLister) ListByPrefix(_ context.Context, _ blobstore.URI) ([]blobstore.BlobInfo, error) {
	return f.objects, nil
}

// countingPresigner mints a deterministic URL per key and counts the mints
// so a test can assert that a contiguity failure mints nothing (no body is
// fetched before the check passes).
type countingPresigner struct {
	mints []blobstore.URI
}

func (p *countingPresigner) PresignGet(u blobstore.URI, _ time.Duration) (blobstore.Grant, error) {
	p.mints = append(p.mints, u)
	return blobstore.Grant{URL: "https://obj.test/" + u.PartID, ExpiresAt: time.Unix(1, 0)}, nil
}

// chunkObject builds a listed object with the given index and size,
// zero-padded to the canonical 5-digit key unless raw is set (a stray
// non-padded key used to exercise out-of-order detection).
func chunkObject(idx int, size int64, encoding partialmanifeststore.ChunkEncoding, raw bool) blobstore.BlobInfo {
	part := fmt.Sprintf("%s/chunk-%05d.%s", rcCheckpoint, idx, encoding)
	if raw {
		part = fmt.Sprintf("%s/chunk-%d.%s", rcCheckpoint, idx, encoding)
	}
	return blobstore.BlobInfo{
		URI:  blobstore.URI{TenantID: rcTenant, ObjectType: blobstore.ObjectTypeCheckpoint, SessionID: rcSession, PartID: part},
		Size: size,
	}
}

func newResolver(chunkCount int, encoding partialmanifeststore.ChunkEncoding, objects []blobstore.BlobInfo) (*Resolver, *countingPresigner) {
	p := &countingPresigner{}
	return &Resolver{
		Manifests: fakeManifests{rec: partialmanifeststore.Record{
			CheckpointID:  rcCheckpoint,
			ChunkCount:    chunkCount,
			ChunkEncoding: encoding,
		}},
		Presigner: p,
		Lister:    fakeLister{objects: objects},
		TTL:       30 * time.Second,
	}, p
}

// spec: §10.1 line 155 — a contiguous [0, chunk_count) set mints one GET
// capability per index in ascending order at the canonical zero-padded key.
func TestResolveMintsOneCapabilityPerChunkInOrder(t *testing.T) {
	objs := []blobstore.BlobInfo{
		chunkObject(0, 16, partialmanifeststore.ChunkEncodingTarGz, false),
		chunkObject(1, 16, partialmanifeststore.ChunkEncodingTarGz, false),
		chunkObject(2, 7, partialmanifeststore.ChunkEncodingTarGz, false),
	}
	r, _ := newResolver(3, partialmanifeststore.ChunkEncodingTarGz, objs)

	grants, err := r.Resolve(context.Background(), rcTenant, rcSession, rcCheckpoint)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(grants) != 3 {
		t.Fatalf("grants = %d, want 3", len(grants))
	}
	for i, g := range grants {
		if g.Index != uint32(i) {
			t.Errorf("grant[%d].Index = %d, want %d", i, g.Index, i)
		}
		wantKey := fmt.Sprintf("%s/chunk-%05d.tar.gz", rcCheckpoint, i)
		if want := "https://obj.test/" + wantKey; g.URL != want {
			t.Errorf("grant[%d].URL = %q, want %q (decoder from manifest encoding)", i, g.URL, want)
		}
	}
	if grants[2].Length != 7 {
		t.Errorf("grant[2].Length = %d, want 7 (per-object size)", grants[2].Length)
	}
}

// spec: §10.1 line 155 — an index at or beyond chunk_count is expected
// residue; reassembly consumes exactly the contiguous prefix [0, chunk_count).
func TestResolveToleratesResidueBeyondChunkCount(t *testing.T) {
	objs := []blobstore.BlobInfo{
		chunkObject(0, 16, partialmanifeststore.ChunkEncodingTar, false),
		chunkObject(1, 16, partialmanifeststore.ChunkEncodingTar, false),
		chunkObject(2, 16, partialmanifeststore.ChunkEncodingTar, false), // index 2 == chunk_count: residue
	}
	r, _ := newResolver(2, partialmanifeststore.ChunkEncodingTar, objs)

	grants, err := r.Resolve(context.Background(), rcTenant, rcSession, rcCheckpoint)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(grants) != 2 {
		t.Fatalf("grants = %d, want 2 (residue index 2 ignored)", len(grants))
	}
}

// spec: §10.1 line 155 — a gap (a missing intermediate index below
// chunk_count) fails reassembly atomically before any chunk body is fetched.
// diagnosis: a failure here means a gapped chunk set is spliced into the
// decompress→untar pipeline, corrupting the gzip deflate stream and tar
// framing instead of failing over to the last full checkpoint.
func TestResolveFailsOnGapBeforeMintingAnyCapability(t *testing.T) {
	objs := []blobstore.BlobInfo{
		chunkObject(0, 16, partialmanifeststore.ChunkEncodingTar, false),
		chunkObject(2, 16, partialmanifeststore.ChunkEncodingTar, false), // index 1 missing
	}
	r, p := newResolver(3, partialmanifeststore.ChunkEncodingTar, objs)

	_, err := r.Resolve(context.Background(), rcTenant, rcSession, rcCheckpoint)
	if !errors.Is(err, ErrReassemblyContiguity) {
		t.Fatalf("err = %v, want ErrReassemblyContiguity", err)
	}
	if len(p.mints) != 0 {
		t.Errorf("presign mints = %d, want 0 (contiguity fails before any capability is minted)", len(p.mints))
	}
}

// spec: §10.1 line 155 — an out-of-order index below chunk_count fails the
// same way as a gap, before any chunk body is fetched.
func TestResolveFailsOnOutOfOrderBeforeMintingAnyCapability(t *testing.T) {
	// A stray non-zero-padded chunk-1 sorts after chunk-00002 (lexicographic
	// list order), so index 1 reappears out of ascending order below
	// chunk_count.
	objs := []blobstore.BlobInfo{
		chunkObject(0, 16, partialmanifeststore.ChunkEncodingTar, false),
		chunkObject(1, 16, partialmanifeststore.ChunkEncodingTar, false),
		chunkObject(2, 16, partialmanifeststore.ChunkEncodingTar, false),
		chunkObject(1, 16, partialmanifeststore.ChunkEncodingTar, true), // stray chunk-1.tar
	}
	r, p := newResolver(3, partialmanifeststore.ChunkEncodingTar, objs)

	_, err := r.Resolve(context.Background(), rcTenant, rcSession, rcCheckpoint)
	if !errors.Is(err, ErrReassemblyContiguity) {
		t.Fatalf("err = %v, want ErrReassemblyContiguity", err)
	}
	if len(p.mints) != 0 {
		t.Errorf("presign mints = %d, want 0", len(p.mints))
	}
}

// spec: §10.1 line 155 — a partial checkpoint below the recovery threshold
// (baseline * partialRecoveryThresholdFraction) is not reassembled; the
// resolver returns ErrBelowRecoveryThreshold so the caller falls back to
// the last full checkpoint. A partial at or above threshold reassembles.
func TestResolveHonoursPartialRecoveryThreshold(t *testing.T) {
	baseline := int64(100)
	objs := []blobstore.BlobInfo{
		chunkObject(0, 16, partialmanifeststore.ChunkEncodingTar, false),
		chunkObject(1, 16, partialmanifeststore.ChunkEncodingTar, false),
	}
	build := func(uploaded int64) *Resolver {
		p := &countingPresigner{}
		return &Resolver{
			Manifests: fakeManifests{rec: partialmanifeststore.Record{
				CheckpointID:                rcCheckpoint,
				ChunkCount:                  2,
				ChunkEncoding:               partialmanifeststore.ChunkEncodingTar,
				Partial:                     true,
				WorkspaceBytesUploaded:      uploaded,
				BaselineFullCheckpointBytes: &baseline,
			}},
			Presigner:                        p,
			Lister:                           fakeLister{objects: objs},
			TTL:                              30 * time.Second,
			PartialRecoveryThresholdFraction: 0.5, // threshold = 50 bytes
		}
	}

	// 40 < 50: below threshold, do not reassemble.
	if _, err := build(40).Resolve(context.Background(), rcTenant, rcSession, rcCheckpoint); !errors.Is(err, ErrBelowRecoveryThreshold) {
		t.Fatalf("below-threshold Resolve err = %v, want ErrBelowRecoveryThreshold", err)
	}
	// 60 >= 50: reassemble.
	grants, err := build(60).Resolve(context.Background(), rcTenant, rcSession, rcCheckpoint)
	if err != nil {
		t.Fatalf("at-threshold Resolve: %v", err)
	}
	if len(grants) != 2 {
		t.Fatalf("grants = %d, want 2 (partial above threshold reassembles)", len(grants))
	}
}

// A zero-chunk manifest resolves to no chunks (restores nothing) without
// consulting the lister or presigner.
func TestResolveZeroChunkManifestRestoresNothing(t *testing.T) {
	r, p := newResolver(0, partialmanifeststore.ChunkEncodingTar, nil)
	grants, err := r.Resolve(context.Background(), rcTenant, rcSession, rcCheckpoint)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(grants) != 0 {
		t.Errorf("grants = %d, want 0", len(grants))
	}
	if len(p.mints) != 0 {
		t.Errorf("presign mints = %d, want 0", len(p.mints))
	}
}
