// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"fmt"

	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// errRestoreUnwired is returned by a derived or replayed session's restore
// closure when no chunk resolver is wired, so the bind fails rather than
// promoting a workspace without the starting snapshot.
//
// spec: §7.1 (Derive restore semantics).
var errRestoreUnwired = errors.New("sessionserver: workspace restore: no chunk resolver is wired")

// errRestoreEmpty is returned by a derived or replayed session's restore
// closure when the starting snapshot resolves to no chunk. The derived
// manifest is the session's only manifest, so an empty restore would start
// the session with a different workspace.
//
// spec: §7.1 (Derive restore semantics).
var errRestoreEmpty = errors.New("sessionserver: workspace restore: starting snapshot resolves to no chunk")

// workspaceRestore returns the closure that mints the starting snapshot's
// GET capabilities for a derived or replayed session's first bind, or nil
// when the row carries no snapshot. The closure is keyed on the row's own
// tenant, its own session id, and the checkpoint id its snapshot ref names,
// so every capability it mints names a key under the derived session's own
// prefix in its own tenant. It has no fallback: an unwired resolver, any
// resolve error, and an empty chunk set each fail. The Binder invokes the
// closure immediately before each bind-time FinalizeWorkspace, so the
// capabilities are fresh on every attempt.
//
// The resume path's resolveResumeChunks is deliberately not used here: it
// degrades to no chunks with a log line, which would start the session
// silently without its workspace.
//
// spec: §7.1 (Derive restore semantics); §13.1; §28.5.5.
func (s *Server) workspaceRestore(row sessionstore.Session) func(context.Context) ([]adapterclient.ChunkGrant, error) {
	if row.WorkspaceSnapshot == nil || row.WorkspaceSnapshot.Ref == "" {
		return nil
	}
	tenant, id, cp := row.TenantID, row.ID, checkpointIDFromSnapshotRef(row.WorkspaceSnapshot.Ref)
	return func(ctx context.Context) ([]adapterclient.ChunkGrant, error) {
		if s.resumeChunkResolver == nil {
			return nil, errRestoreUnwired
		}
		res, err := s.resumeChunkResolver.Resolve(ctx, tenant, id, cp)
		if err != nil {
			return nil, fmt.Errorf("sessionserver: resolve starting snapshot %s: %w", cp, err)
		}
		if len(res.Grants) == 0 {
			return nil, errRestoreEmpty
		}
		return res.Grants, nil
	}
}
