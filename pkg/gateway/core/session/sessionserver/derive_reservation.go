// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// sessionObjectDeleter is the blob-store capability the derive undo needs:
// deleting every object written under one session ID. The MinIO, memory,
// filesystem, and cataloging blob stores implement it.
type sessionObjectDeleter interface {
	DeleteBySession(ctx context.Context, tenantID, sessionID string) (int, error)
}

// reserveDerivedSession reserves derivedID for its owner before the derive
// copies any object under it, so the owner's erasure reaches what the copy
// writes even when the derive never inserts its row. A fence refusal
// answers 403 with its code, and any other error answers 500
// INTERNAL_ERROR. It returns true when the derive may copy.
// spec: §12.8 (Lifecycle fence and drain).
func (s *Server) reserveDerivedSession(w http.ResponseWriter, r *http.Request, tenantID, ownerUserID, derivedID string) bool {
	err := s.store.Reserve(r.Context(), tenantID, ownerUserID, derivedID, false)
	if err == nil {
		return true
	}
	var ref *sessionstore.CreationRefusal
	if errors.As(err, &ref) {
		s.writeCreationRefusal(w, ref)
		return false
	}
	s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
		"session ID reservation failed: "+err.Error(), nil)
	return false
}

// undoDerivedWrites deletes what a derive wrote under derivedID before its
// insert (the snapshot or chunk objects and the derived checkpoint
// manifest row) and then releases the reservation. It runs on a context
// detached from the request's cancellation, so a client disconnect cannot
// strand the objects. A deletion error, or a blob store that copied but
// cannot delete by session, keeps the reservation, so the owner's erasure
// still reaches the objects through the reserved ID; the error is logged
// and the caller still answers with its own response.
// spec: §12.8 (Lifecycle fence and drain).
func (s *Server) undoDerivedWrites(ctx context.Context, tenantID, derivedID string) {
	ctx = context.WithoutCancel(ctx)
	if !s.deleteDerivedObjects(ctx, tenantID, derivedID) {
		return
	}
	if s.checkpointManifestWriter != nil {
		if err := s.checkpointManifestWriter.DeleteByUser(ctx, tenantID, "", []string{derivedID}); err != nil {
			slog.Warn("sessionserver: delete the derived checkpoint manifest; keeping the reservation",
				"session_id", derivedID, "error", err)
			return
		}
	}
	if err := s.store.ReleaseReservation(ctx, tenantID, derivedID); err != nil {
		slog.Warn("sessionserver: release the derived session reservation",
			"session_id", derivedID, "error", err)
	}
}

// deleteDerivedObjects deletes the objects a derive copied under
// derivedID. A blob store that cannot copy wrote nothing, so there is
// nothing to delete. It reports whether the reservation may be released.
func (s *Server) deleteDerivedObjects(ctx context.Context, tenantID, derivedID string) bool {
	if _, copies := s.blobs.(blobstore.Copier); !copies {
		return true
	}
	deleter, ok := s.blobs.(sessionObjectDeleter)
	if !ok {
		slog.Warn("sessionserver: the blob store cannot delete by session; keeping the derived session reservation",
			"session_id", derivedID)
		return false
	}
	if _, err := deleter.DeleteBySession(ctx, tenantID, derivedID); err != nil {
		slog.Warn("sessionserver: delete the derived session objects; keeping the reservation",
			"session_id", derivedID, "error", err)
		return false
	}
	return true
}
