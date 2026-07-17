// SPDX-License-Identifier: MIT

// Package resumechunks resolves the §10.1 line 155 reassembly-on-resume
// chunk set for a checkpoint the gateway is restoring onto a replacement
// pod. Given a checkpoint's manifest row, it lists the committed chunk
// objects under the row's chunk_object_key_prefix, verifies contiguity of
// the prefix [0, chunk_count), and mints one presigned single-key GET
// capability per index in [0, chunk_count). The adapter fetches the
// capabilities in ascending index order and concatenates the chunk bodies
// into one decompress→untar pipeline.
//
// The gateway is the sole authority that resolves, lists, validates, and
// signs the keys; the pod holds no object-store credential, no LIST, and
// no DELETE. A GET capability names one key the pod's own session
// authored, so it grants no read the pod did not already have.
//
// spec: §10.1 line 155 (reassembly on resume from presigned chunk GET
// capabilities), §13.2 (capability model).
package resumechunks

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
)

// ErrReassemblyContiguity is returned when the objects under a
// checkpoint's chunk prefix do not form a contiguous [0, chunk_count)
// sequence: a gap (a missing intermediate index) or an out-of-order index
// below chunk_count. Splicing non-adjacent regions would corrupt both the
// gzip deflate stream and the tar framing, so reassembly fails atomically
// before any chunk body is fetched. The caller falls back to the last
// successful full checkpoint.
//
// spec: §10.1 line 155 — a gap or an out-of-order index below chunk_count
// fails reassembly atomically before any chunk body is fetched.
var ErrReassemblyContiguity = errors.New("resumechunks: chunk set is not a contiguous [0, chunk_count) sequence")

// ErrBelowRecoveryThreshold is returned when a partial checkpoint carries
// fewer confirmed workspace bytes than the §10.1 line 155 recovery
// threshold (`baseline_full_checkpoint_bytes * partialRecoveryThresholdFraction`),
// or has no confirmed bytes or chunks. The reassembly is not worth the
// splice; the caller falls back to the last successful full checkpoint.
//
// spec: §10.1 line 155 — reassembly is attempted only when
// workspace_bytes_uploaded >= threshold AND workspace_bytes_uploaded > 0
// AND chunk_count > 0.
var ErrBelowRecoveryThreshold = errors.New("resumechunks: partial checkpoint is below the recovery threshold")

// ManifestReader resolves a checkpoint's manifest row by checkpoint_id.
type ManifestReader interface {
	Get(ctx context.Context, tenantID, checkpointID string) (partialmanifeststore.Record, error)
}

// Presigner mints a single-key read-only GET capability for one chunk key.
type Presigner interface {
	PresignGet(u blobstore.URI, ttl time.Duration) (blobstore.Grant, error)
}

// ChunkLister lists every object under a checkpoint's chunk prefix. The
// prefix is the object key of u (its PartID names the checkpoint segment
// with a trailing slash). The returned BlobInfos carry the full object URI
// (with PartID = "{checkpoint_id}/chunk-{n}.{enc}") and Size, in the
// store's native lexicographic list order. miniostore.Store satisfies it.
type ChunkLister interface {
	ListByPrefix(ctx context.Context, u blobstore.URI) ([]blobstore.BlobInfo, error)
}

// Resolver resolves the resume chunk set for a checkpoint.
type Resolver struct {
	Manifests ManifestReader
	Presigner Presigner
	Lister    ChunkLister
	// TTL is the checkpointCapabilityTTLSeconds window each minted GET
	// capability carries; a fetch that outlives it is re-driven by
	// re-calling Resume, which re-mints.
	TTL time.Duration
	// PartialRecoveryThresholdFraction is the §10.1 line 155
	// partialRecoveryThresholdFraction (Helm default 0.5): a partial
	// checkpoint is reassembled only when its confirmed workspace bytes are
	// at least this fraction of the frozen baseline_full_checkpoint_bytes.
	// It applies only to partial manifests with a non-NULL baseline; a full
	// checkpoint restores whole regardless. A zero value leaves the gate at
	// threshold 0 (any positive confirmed byte count passes).
	PartialRecoveryThresholdFraction float64
}

// Resolve resolves the chunk set for the checkpoint named by checkpointID
// on session (tenantID, sessionID). It returns one ChunkGrant per index in
// [0, chunk_count) in ascending order, or ErrReassemblyContiguity when the
// committed objects do not form a contiguous prefix. A checkpoint with
// chunk_count == 0 (an empty manifest) resolves to no chunks, which
// restores nothing.
//
// spec: §10.1 line 155.
func (r *Resolver) Resolve(ctx context.Context, tenantID, sessionID, checkpointID string) ([]adapterclient.ChunkGrant, error) {
	rec, err := r.Manifests.Get(ctx, tenantID, checkpointID)
	if err != nil {
		return nil, fmt.Errorf("resumechunks: resolve manifest %s/%s: %w", tenantID, checkpointID, err)
	}
	if rec.ChunkCount <= 0 {
		return nil, nil
	}
	// spec: §10.1 line 155 — a partial checkpoint is reconstructed only
	// when it clears the recovery threshold. A full checkpoint (partial =
	// false) restores whole and skips the gate. The threshold is
	// baseline_full_checkpoint_bytes * partialRecoveryThresholdFraction when
	// the baseline is non-NULL, else 0.
	if rec.Partial {
		var threshold int64
		if rec.BaselineFullCheckpointBytes != nil {
			threshold = int64(float64(*rec.BaselineFullCheckpointBytes) * r.PartialRecoveryThresholdFraction)
		}
		if rec.WorkspaceBytesUploaded <= 0 || rec.WorkspaceBytesUploaded < threshold {
			return nil, fmt.Errorf("%w: %d confirmed bytes below threshold %d for %s",
				ErrBelowRecoveryThreshold, rec.WorkspaceBytesUploaded, threshold, checkpointID)
		}
	}
	encoding := rec.ChunkEncoding
	if encoding == "" {
		encoding = partialmanifeststore.ChunkEncodingTar
	}

	// (1) List objects under the chunk prefix and verify contiguity of the
	// prefix [0, chunk_count) before minting any capability. An index at or
	// beyond chunk_count is expected residue (a grant that outlived the
	// finalising deadline landing a PUT the gateway never counted) and is
	// ignored; a gap or an out-of-order index below chunk_count fails
	// atomically before any body is fetched.
	objects, err := r.Lister.ListByPrefix(ctx, prefixURI(tenantID, sessionID, checkpointID))
	if err != nil {
		return nil, fmt.Errorf("resumechunks: list chunk objects for %s: %w", checkpointID, err)
	}
	sizes, err := contiguousChunkSizes(objects, checkpointID, rec.ChunkCount)
	if err != nil {
		return nil, err
	}

	// (2) Mint one presigned single-key GET capability per index in
	// [0, chunk_count). The key is the canonical zero-padded 5-digit form
	// with the encoding drawn strictly from the manifest column; the
	// object-key suffix exists purely for operator legibility.
	grants := make([]adapterclient.ChunkGrant, 0, rec.ChunkCount)
	for n := 0; n < rec.ChunkCount; n++ {
		uri := ChunkObjectURI(tenantID, sessionID, checkpointID, uint32(n), encoding)
		uri.TTL = r.TTL
		grant, gerr := r.Presigner.PresignGet(uri, r.TTL)
		if gerr != nil {
			return nil, fmt.Errorf("resumechunks: mint GET capability for chunk %d of %s: %w", n, checkpointID, gerr)
		}
		grants = append(grants, adapterclient.ChunkGrant{
			Index:     uint32(n),
			Length:    sizes[n],
			URL:       grant.URL,
			Headers:   grant.Headers,
			ExpiresAt: grant.ExpiresAt,
		})
	}
	return grants, nil
}

// contiguousChunkSizes parses the listed objects into per-index sizes and
// verifies that every index in [0, chunkCount) is present exactly once and
// in ascending list order. Indices at or beyond chunkCount are tolerated
// residue. A gap or an out-of-order (or duplicate) index below chunkCount
// returns ErrReassemblyContiguity.
func contiguousChunkSizes(objects []blobstore.BlobInfo, checkpointID string, chunkCount int) ([]int64, error) {
	sizes := make([]int64, chunkCount)
	seen := make([]bool, chunkCount)
	prevBelow := -1
	for _, obj := range objects {
		idx, ok := parseChunkIndex(obj.URI.PartID, checkpointID)
		if !ok {
			// A key that is not a chunk object under this checkpoint is
			// operator residue; ignore it.
			continue
		}
		if idx >= uint32(chunkCount) {
			// Expected residue: a PUT for an index the gateway never
			// confirmed or counted. Ignore it.
			continue
		}
		// An index below chunk_count must appear exactly once and in
		// ascending list order; a duplicate or an out-of-order key would
		// splice non-adjacent regions and corrupt the stream.
		if int(idx) <= prevBelow {
			return nil, fmt.Errorf("%w: index %d follows %d out of order under %s",
				ErrReassemblyContiguity, idx, prevBelow, checkpointID)
		}
		prevBelow = int(idx)
		seen[idx] = true
		sizes[idx] = obj.Size
	}
	for n := 0; n < chunkCount; n++ {
		if !seen[n] {
			return nil, fmt.Errorf("%w: missing chunk index %d under %s (chunk_count=%d)",
				ErrReassemblyContiguity, n, checkpointID, chunkCount)
		}
	}
	return sizes, nil
}

// parseChunkIndex extracts the chunk index from a manifest part id of the
// form "{checkpoint_id}/chunk-{n}.{enc}". It accepts any number of index
// digits (a zero-padded canonical key and a non-padded stray both parse to
// the same index) so an out-of-order stray is detected rather than
// silently skipped. Returns ok=false for a part id that is not a chunk
// object under checkpointID.
func parseChunkIndex(partID, checkpointID string) (uint32, bool) {
	rest, ok := strings.CutPrefix(partID, checkpointID+"/")
	if !ok {
		return 0, false
	}
	rest, ok = strings.CutPrefix(rest, "chunk-")
	if !ok {
		return 0, false
	}
	dot := strings.IndexByte(rest, '.')
	if dot <= 0 {
		return 0, false
	}
	n, err := strconv.ParseUint(rest[:dot], 10, 32)
	if err != nil {
		return 0, false
	}
	return uint32(n), true
}

// prefixURI is the object-key prefix under which a checkpoint's chunks
// live: /{tenant_id}/checkpoints/{session_id}/{checkpoint_id}/.
func prefixURI(tenantID, sessionID, checkpointID string) blobstore.URI {
	return blobstore.URI{
		TenantID:   tenantID,
		ObjectType: blobstore.ObjectTypeCheckpoint,
		SessionID:  sessionID,
		PartID:     checkpointID + "/",
	}
}

// ChunkObjectURI builds the object URI for a single chunk. The basename is
// the canonical zero-padded 5-digit form the upload path writes, so a GET
// capability (resume) or a direct store read (workspace download) names
// exactly the key the checkpoint authored. The encoding is drawn strictly
// from the manifest's chunk_encoding column, not from any object-key
// suffix. The caller sets TTL when the URI is signed into a capability.
//
// spec: §10.1 line 139 (zero-padded 5-digit chunk index), line 155.
func ChunkObjectURI(tenantID, sessionID, checkpointID string, index uint32, encoding partialmanifeststore.ChunkEncoding) blobstore.URI {
	return blobstore.URI{
		TenantID:   tenantID,
		ObjectType: blobstore.ObjectTypeCheckpoint,
		SessionID:  sessionID,
		PartID:     fmt.Sprintf("%s/chunk-%05d.%s", checkpointID, index, encoding),
	}
}
