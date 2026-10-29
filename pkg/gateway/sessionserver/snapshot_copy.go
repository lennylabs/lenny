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

// errSnapshotCopierUnwired reports a blob store that cannot copy objects.
// Derive and replay both start the new session from a copy of the source's
// workspace snapshot, so a store without blobstore.Copier cannot serve
// either, and the request fails rather than storing a reference to an
// object that was never written. spec: §7.1 (Derive copy semantics); §15.1
// (Session Replay Semantics).
var errSnapshotCopierUnwired = errors.New("the blob store cannot copy the source workspace snapshot")

// errSnapshotRefUnresolvable reports a source snapshot ref that names no
// object the gateway can address in the caller's tenant. The copy cannot
// read the source, which the caller answers as a missing snapshot object.
var errSnapshotRefUnresolvable = errors.New("the source workspace snapshot ref names no object in the tenant")

// copyWorkspaceSnapshot copies the source session's workspace snapshot
// under newSessionID and returns the WorkspaceSnapshot.Ref of the copy. The
// returned ref always names an object the copy wrote.
//
// A §10.1 chunked checkpoint is copied chunk by chunk with a derived
// manifest row (deriveChunkedCheckpoint). A snapshot that predates the
// chunked model is copied as one object through the tenant-scoped boundary,
// and the returned ref is the destination URI, which parseSnapshotRef
// resolves for the workspace download.
//
// The error is blobstore.ErrNotFound when the source object is absent,
// errSnapshotRefUnresolvable when the source ref names no object in the
// tenant, errSnapshotCopierUnwired when the blob store cannot copy, and the
// store's own error otherwise. The caller undoes what the copy wrote.
//
// spec: §7.1 (Derive copy semantics); §15.1 (Session Replay Semantics);
// §4.5 (Artifact Store); §10.1.
func (s *Server) copyWorkspaceSnapshot(ctx context.Context, source sessionstore.Session, newSessionID string, now time.Time) (string, error) {
	if newRef, handled, err := s.deriveChunkedCheckpoint(ctx, source, newSessionID, now); handled {
		if err != nil {
			return "", fmt.Errorf("copy checkpoint chunks of %s: %w", source.ID, err)
		}
		return newRef, nil
	}
	if _, ok := s.blobs.(blobstore.Copier); !ok {
		return "", errSnapshotCopierUnwired
	}
	srcURI, dstURI, ok := parseDeriveURIs(source.WorkspaceSnapshot.Ref, source.TenantID, source.ID, newSessionID)
	if !ok {
		return "", errSnapshotRefUnresolvable
	}
	// spec: §12.5 — the copy runs through the tenant-scoped boundary, so
	// both URIs are validated against the caller tenant at the store
	// interface rather than trusted from the constructed URI. A copy of an
	// already-copied object (an idempotent retry) is tolerated via
	// ErrConflict.
	scoped := blobstore.NewTenantScoped(source.TenantID, s.blobs)
	if err := scoped.Copy(srcURI, dstURI); err != nil && !errors.Is(err, blobstore.ErrConflict) {
		return "", fmt.Errorf("copy workspace snapshot of %s: %w", source.ID, err)
	}
	return dstURI.String(), nil
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
// source snapshot object cannot be read.
func snapshotMissing(err error) bool {
	return errors.Is(err, blobstore.ErrNotFound) || errors.Is(err, errSnapshotRefUnresolvable)
}

// writeSnapshotCopyError answers a failed snapshot copy. A source object
// that is absent or unaddressable answers 503 DERIVE_SNAPSHOT_UNAVAILABLE,
// the code §7.1 derive rule 2 names for a snapshot object the copy cannot
// find; replay in either mode starts from the same copy and answers the
// same code. Every other failure, including a blob store with no Copier,
// answers 500 INTERNAL_ERROR, as derive answers any other copy failure.
// spec: §7.1 (Derive session semantics) derive rule 2; §15.1
// (Session Replay Semantics).
func (s *Server) writeSnapshotCopyError(w http.ResponseWriter, err error, sourceRef string) {
	if snapshotMissing(err) {
		s.writeError(w, http.StatusServiceUnavailable, "DERIVE_SNAPSHOT_UNAVAILABLE",
			"source workspace snapshot object is absent in storage",
			map[string]any{"snapshotRef": sourceRef})
		return
	}
	s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
		"workspace snapshot copy failed: "+err.Error(), nil)
}
