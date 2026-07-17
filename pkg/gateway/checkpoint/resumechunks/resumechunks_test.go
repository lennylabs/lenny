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
// can construct an out-of-order or gapped chunk set. A non-nil err models a
// store-side list failure (MinIO unreachable).
type fakeLister struct {
	objects []blobstore.BlobInfo
	err     error
}

func (f fakeLister) ListByPrefix(_ context.Context, _ blobstore.URI) ([]blobstore.BlobInfo, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.objects, nil
}

// countingPresigner mints a deterministic URL per key and counts the mints
// so a test can assert that a contiguity failure mints nothing (no body is
// fetched before the check passes). A non-nil err models a signer failure on
// the first mint.
type countingPresigner struct {
	mints []blobstore.URI
	err   error
}

func (p *countingPresigner) PresignGet(u blobstore.URI, _ time.Duration) (blobstore.Grant, error) {
	p.mints = append(p.mints, u)
	if p.err != nil {
		return blobstore.Grant{}, p.err
	}
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

	res, err := r.Resolve(context.Background(), rcTenant, rcSession, rcCheckpoint)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	grants := res.Grants
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

	res, err := r.Resolve(context.Background(), rcTenant, rcSession, rcCheckpoint)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	grants := res.Grants
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
				ManifestReason:              partialmanifeststore.ReasonTimeout,
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
	res, err := build(60).Resolve(context.Background(), rcTenant, rcSession, rcCheckpoint)
	if err != nil {
		t.Fatalf("at-threshold Resolve: %v", err)
	}
	if len(res.Grants) != 2 {
		t.Fatalf("grants = %d, want 2 (partial above threshold reassembles)", len(res.Grants))
	}
	// spec: §16.1 line 195 — an above-threshold partial reassembly is a
	// recovered partial checkpoint, so Resolve surfaces Recovered = true and
	// the recovered row's manifest_reason for the recovered=true counter.
	if !res.Recovered {
		t.Errorf("Recovered = false, want true for an above-threshold partial reassembly")
	}
	if res.ManifestReason != partialmanifeststore.ReasonTimeout {
		t.Errorf("ManifestReason = %q, want %q (the recovered row's reason)", res.ManifestReason, partialmanifeststore.ReasonTimeout)
	}
}

// spec: §10.1 line 155 — a partial checkpoint that never confirmed a chunk
// (chunk_count == 0, workspace_bytes_uploaded == 0) is below any recovery
// threshold, so it returns ErrBelowRecoveryThreshold rather than resolving
// to an empty restore. Without this the zero-chunk short-circuit would run
// first and the caller would silently skip the last-full-checkpoint fallback,
// resuming the session with an empty workspace.
func TestResolveZeroChunkPartialFallsBackNotEmptyRestore(t *testing.T) {
	baseline := int64(100)
	r := &Resolver{
		Manifests: fakeManifests{rec: partialmanifeststore.Record{
			CheckpointID:                rcCheckpoint,
			ChunkCount:                  0,
			ChunkEncoding:               partialmanifeststore.ChunkEncodingTar,
			Partial:                     true,
			WorkspaceBytesUploaded:      0,
			BaselineFullCheckpointBytes: &baseline,
		}},
		Presigner:                        &countingPresigner{},
		Lister:                           fakeLister{},
		TTL:                              30 * time.Second,
		PartialRecoveryThresholdFraction: 0.5,
	}
	if _, err := r.Resolve(context.Background(), rcTenant, rcSession, rcCheckpoint); !errors.Is(err, ErrBelowRecoveryThreshold) {
		t.Fatalf("zero-chunk partial Resolve err = %v, want ErrBelowRecoveryThreshold", err)
	}
}

// spec: §10.1 line 155 — a manifest read failure aborts the resolve and
// surfaces the wrapped store error so the caller falls back to the last full
// checkpoint rather than resuming from an unresolvable manifest.
func TestResolveSurfacesManifestGetError(t *testing.T) {
	sentinel := errors.New("postgres unreachable")
	r := &Resolver{
		Manifests: fakeManifests{err: sentinel},
		Presigner: &countingPresigner{},
		Lister:    fakeLister{},
		TTL:       30 * time.Second,
	}
	_, err := r.Resolve(context.Background(), rcTenant, rcSession, rcCheckpoint)
	if !errors.Is(err, sentinel) {
		t.Fatalf("Resolve err = %v, want it to wrap the manifest read error", err)
	}
}

// spec: §10.1 line 155 — when the manifest's chunk_encoding column is empty
// the resolver falls back to the tar encoding for the object key, so a
// legacy manifest with no recorded encoding still resolves to the canonical
// tar chunk keys.
func TestResolveDefaultsChunkEncodingWhenColumnEmpty(t *testing.T) {
	objs := []blobstore.BlobInfo{
		chunkObject(0, 16, partialmanifeststore.ChunkEncodingTar, false),
		chunkObject(1, 16, partialmanifeststore.ChunkEncodingTar, false),
	}
	r, _ := newResolver(2, "", objs)

	res, err := r.Resolve(context.Background(), rcTenant, rcSession, rcCheckpoint)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(res.Grants) != 2 {
		t.Fatalf("grants = %d, want 2", len(res.Grants))
	}
	wantKey := fmt.Sprintf("%s/chunk-%05d.tar", rcCheckpoint, 0)
	if want := "https://obj.test/" + wantKey; res.Grants[0].URL != want {
		t.Errorf("grant[0].URL = %q, want %q (default tar encoding)", res.Grants[0].URL, want)
	}
}

// spec: §10.1 line 155 — a store-side list failure aborts the resolve and
// surfaces the wrapped error before any capability is minted.
func TestResolveSurfacesListError(t *testing.T) {
	sentinel := errors.New("minio unreachable")
	p := &countingPresigner{}
	r := &Resolver{
		Manifests: fakeManifests{rec: partialmanifeststore.Record{CheckpointID: rcCheckpoint, ChunkCount: 2, ChunkEncoding: partialmanifeststore.ChunkEncodingTar}},
		Presigner: p,
		Lister:    fakeLister{err: sentinel},
		TTL:       30 * time.Second,
	}
	_, err := r.Resolve(context.Background(), rcTenant, rcSession, rcCheckpoint)
	if !errors.Is(err, sentinel) {
		t.Fatalf("Resolve err = %v, want it to wrap the list error", err)
	}
	if len(p.mints) != 0 {
		t.Errorf("presign mints = %d, want 0 (list fails before any capability is minted)", len(p.mints))
	}
}

// spec: §10.1 line 155 — a signer failure aborts the resolve and surfaces the
// wrapped error so the caller falls back rather than resuming from a partial
// grant set.
func TestResolveSurfacesPresignError(t *testing.T) {
	sentinel := errors.New("signer key rotated")
	objs := []blobstore.BlobInfo{
		chunkObject(0, 16, partialmanifeststore.ChunkEncodingTar, false),
		chunkObject(1, 16, partialmanifeststore.ChunkEncodingTar, false),
	}
	r := &Resolver{
		Manifests: fakeManifests{rec: partialmanifeststore.Record{CheckpointID: rcCheckpoint, ChunkCount: 2, ChunkEncoding: partialmanifeststore.ChunkEncodingTar}},
		Presigner: &countingPresigner{err: sentinel},
		Lister:    fakeLister{objects: objs},
		TTL:       30 * time.Second,
	}
	_, err := r.Resolve(context.Background(), rcTenant, rcSession, rcCheckpoint)
	if !errors.Is(err, sentinel) {
		t.Fatalf("Resolve err = %v, want it to wrap the presign error", err)
	}
}

// spec: §10.1 line 155 — parseChunkIndex accepts only a chunk key under the
// resolving checkpoint of the form "{checkpoint_id}/chunk-{n}.{enc}". A key
// under a different checkpoint, one that is not a chunk object, one with no
// encoding suffix, and one with a non-numeric index are all rejected so an
// unrelated object under the prefix is treated as residue rather than parsed
// into a bogus chunk index.
func TestParseChunkIndexRejectsNonChunkKeys(t *testing.T) {
	cases := []struct {
		name    string
		partID  string
		wantIdx uint32
		wantOK  bool
	}{
		{"canonical padded key", rcCheckpoint + "/chunk-00003.tar", 3, true},
		{"non-padded stray key", rcCheckpoint + "/chunk-3.tar", 3, true},
		{"different checkpoint", "other-cp/chunk-00003.tar", 0, false},
		{"not a chunk object", rcCheckpoint + "/manifest.json", 0, false},
		{"no encoding suffix", rcCheckpoint + "/chunk-00003", 0, false},
		{"non-numeric index", rcCheckpoint + "/chunk-abc.tar", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			idx, ok := parseChunkIndex(tc.partID, rcCheckpoint)
			if ok != tc.wantOK || idx != tc.wantIdx {
				t.Fatalf("parseChunkIndex(%q) = (%d, %v), want (%d, %v)", tc.partID, idx, ok, tc.wantIdx, tc.wantOK)
			}
		})
	}
}

// A zero-chunk complete (partial = false) manifest resolves to no chunks
// (restores nothing) without consulting the lister or presigner.
func TestResolveZeroChunkManifestRestoresNothing(t *testing.T) {
	r, p := newResolver(0, partialmanifeststore.ChunkEncodingTar, nil)
	res, err := r.Resolve(context.Background(), rcTenant, rcSession, rcCheckpoint)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(res.Grants) != 0 {
		t.Errorf("grants = %d, want 0", len(res.Grants))
	}
	// spec: §16.1 line 195 — a complete (partial = false) restore is not a
	// recovery, so Recovered stays false and the resume emits nothing.
	if res.Recovered {
		t.Errorf("Recovered = true, want false for a complete restore")
	}
	if len(p.mints) != 0 {
		t.Errorf("presign mints = %d, want 0", len(p.mints))
	}
}
