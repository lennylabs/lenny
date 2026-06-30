// SPDX-License-Identifier: MIT

package prestop

import (
	"context"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// RegistryEnumerator adapts the §4.6.1 podsession.Registry to the
// SessionEnumerator interface. Snapshot walks every binding the
// registry currently holds and returns the synthesized SessionInfo
// slice. When Sessions is wired, the enumerator reads each row's
// §7.3 `last_checkpoint_workspace_bytes` from the store and supplies
// it to the tiered-cap selection; the postgres_null fallback fires
// only for sessions that have not yet recorded a workspace checkpoint
// (or whose store read failed). F-7.3.21.
//
// spec: §10.1 — preStop Stage 2 session enumeration.
type RegistryEnumerator struct {
	// Registry is the per-replica pod binding registry.
	Registry *podsession.Registry
	// Sessions, when set, supplies the §7.3 line 397
	// last_checkpoint_workspace_bytes value for each enumerated
	// session. Nil keeps the legacy postgres_null fallback for every
	// session. F-7.3.21.
	Sessions sessionstore.Store
	// DefaultPool is the pool-label value stamped onto every
	// SessionInfo. The registry does not currently carry the pool
	// label so the v1 implementation uses a single default; the
	// alert ratio operates on this label too. Operators can replace
	// the field with a per-session lookup in a follow-on phase.
	DefaultPool string
}

// Snapshot satisfies the SessionEnumerator interface.
func (e *RegistryEnumerator) Snapshot(ctx context.Context) ([]SessionInfo, error) {
	if e == nil || e.Registry == nil {
		return nil, nil
	}
	bindings := e.Registry.Snapshot()
	out := make([]SessionInfo, 0, len(bindings))
	for _, b := range bindings {
		info := SessionInfo{
			TenantID:  b.TenantID,
			SessionID: b.SessionID,
			Pool:      e.DefaultPool,
		}
		bytes, ok := e.lookupCheckpointBytes(ctx, b.TenantID, b.SessionID)
		switch {
		case ok && bytes > 0:
			// spec: §7.3 line 397 — the persisted size feeds the
			// §10.1 tiered-cap selection directly; the postgres_null
			// fallback no longer fires for this session. F-7.3.21.
			info.LastCheckpointWorkspaceBytes = bytes
		default:
			// spec: §10.1 — postgres_null path. A session that has
			// not yet recorded a checkpoint (or whose lookup failed)
			// falls back to the 90s conservative tier.
			info.IsPostgresNull = true
		}
		out = append(out, info)
	}
	return out, nil
}

// lookupCheckpointBytes returns the §7.3 line 397
// last_checkpoint_workspace_bytes value for the (tenant, session) when
// the store is wired and the row carries a non-zero size. A nil store,
// a missing row, or a store error all degrade to (0, false) so the
// caller falls back to the postgres_null path; the preStop budget is
// observability, not a §4.4 correctness gate.
func (e *RegistryEnumerator) lookupCheckpointBytes(ctx context.Context, tenantID, sessionID string) (int64, bool) {
	if e.Sessions == nil {
		return 0, false
	}
	row, err := e.Sessions.Get(ctx, tenantID, sessionID)
	if err != nil || row.WorkspaceSnapshot == nil || row.WorkspaceSnapshot.Bytes <= 0 {
		return 0, false
	}
	return row.WorkspaceSnapshot.Bytes, true
}
