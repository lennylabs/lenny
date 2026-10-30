// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// errSnapshotCopierUnwired reports a gateway that cannot copy or restore the
// source workspace snapshot: no checkpoint manifest reader, no manifest
// writer, a blob store without blobstore.Copier, or no chunk resolver. Derive
// and replay both start the new session from a chunked copy of the source's
// snapshot that its first bind restores through the chunk resolver, so a
// gateway missing any of these serves neither, and the request fails before
// any chunk is copied rather than storing a ref the session cannot restore.
// spec: §7.1 (Derive copy semantics); §7.1 (Derive restore semantics); §15.1
// (Session Replay Semantics).
var errSnapshotCopierUnwired = errors.New("the gateway cannot copy or restore the source workspace snapshot")

// errSnapshotRefUnresolvable reports a source snapshot ref that names no
// manifest row with at least one chunk in the caller's tenant. The copy
// cannot read the source, which the caller answers as a missing snapshot.
var errSnapshotRefUnresolvable = errors.New("the source workspace snapshot ref names no chunked checkpoint in the tenant")

// copyWorkspaceSnapshot copies the source session's §10.1 chunked checkpoint
// chunk by chunk under newSessionID, writes the derived manifest row
// (deriveChunkedCheckpoint), and returns the WorkspaceSnapshot.Ref of the
// copy, which names the derived checkpoint.
//
// The returned error maps as follows. errSnapshotCopierUnwired, checked
// before any chunk is copied, when the manifest reader, the manifest writer,
// the blob store's Copier, or the chunk resolver is unwired.
// errSnapshotRefUnresolvable when the source ref names no manifest row
// (partialmanifeststore.ErrNotFound) or a row with no chunk.
// blobstore.ErrNotFound when a source chunk object is absent. The store's own
// error otherwise, including a transient manifest Get error. The caller
// answers the first and the last with 500 and the two missing-snapshot errors
// with 503 DERIVE_SNAPSHOT_UNAVAILABLE, and undoes what the copy wrote.
//
// spec: §7.1 (Derive copy semantics); §7.1 (Derive restore semantics);
// §7.1 (Derive session semantics) derive rule 2; §15.1 (Session Replay
// Semantics); §4.5 (Artifact Store); §10.1.
func (s *Server) copyWorkspaceSnapshot(ctx context.Context, source sessionstore.Session, newSessionID string, now time.Time) (string, error) {
	if !s.snapshotCopyWired() {
		return "", errSnapshotCopierUnwired
	}
	newRef, err := s.deriveChunkedCheckpoint(ctx, source, newSessionID, now)
	if err != nil {
		return "", fmt.Errorf("copy checkpoint chunks of %s: %w", source.ID, err)
	}
	return newRef, nil
}

// snapshotCopyWired reports whether every dependency the chunked copy and the
// new session's first-bind restore need is wired.
// spec: §7.1 (Derive copy semantics); §7.1 (Derive restore semantics).
func (s *Server) snapshotCopyWired() bool {
	if s.checkpointManifests == nil || s.checkpointManifestWriter == nil || s.resumeChunkResolver == nil {
		return false
	}
	_, ok := s.blobs.(blobstore.Copier)
	return ok
}

// snapshotCopyFailureReason is the derive_failure row's failure reason for
// a copyWorkspaceSnapshot error. spec: §7.1 (Derive session semantics)
// derive rule 2.
func snapshotCopyFailureReason(err error) string {
	if snapshotMissing(err) {
		return "derive_snapshot_unavailable"
	}
	return "workspace_copy_failed"
}

// snapshotMissing reports whether a copyWorkspaceSnapshot error means the
// source snapshot cannot be found.
func snapshotMissing(err error) bool {
	return errors.Is(err, blobstore.ErrNotFound) || errors.Is(err, errSnapshotRefUnresolvable)
}

// writeSnapshotCopyError answers a failed snapshot copy. A source ref that
// names no chunked manifest, or a source chunk object that is absent, answers
// 503 DERIVE_SNAPSHOT_UNAVAILABLE, the code §7.1 derive rule 2 names for a
// snapshot the copy cannot find; replay in either mode starts from the same
// copy and answers the same code. Every other failure, including an unwired
// dependency and a transient manifest read error, answers 500
// INTERNAL_ERROR.
// spec: §7.1 (Derive session semantics) derive rule 2; §15.1
// (Session Replay Semantics).
func (s *Server) writeSnapshotCopyError(w http.ResponseWriter, err error, sourceRef string) {
	if snapshotMissing(err) {
		s.writeError(w, http.StatusServiceUnavailable, "DERIVE_SNAPSHOT_UNAVAILABLE",
			"source workspace snapshot is absent in storage",
			map[string]any{"snapshotRef": sourceRef})
		return
	}
	s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
		"workspace snapshot copy failed: "+err.Error(), nil)
}
