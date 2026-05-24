// SPDX-License-Identifier: MIT

package prestop

import (
	"context"

	"github.com/lennylabs/lenny/pkg/gateway/podsession"
)

// RegistryEnumerator adapts the §4.6.1 podsession.Registry to the
// SessionEnumerator interface. Snapshot walks every binding the
// registry currently holds and returns the synthesized SessionInfo
// slice. The §10.1 `last_checkpoint_workspace_bytes` field is not
// yet plumbed onto the sessions row (the §10.1 chunk-upload pipeline
// is a follow-on); until it lands, every session falls through to
// the postgres_null path so the hook selects the §10.1 conservative
// 90s tier per spec.
//
// spec: §10.1 — preStop Stage 2 session enumeration.
type RegistryEnumerator struct {
	// Registry is the per-replica pod binding registry.
	Registry *podsession.Registry
	// DefaultPool is the pool-label value stamped onto every
	// SessionInfo. The registry does not currently carry the pool
	// label so the v1 implementation uses a single default; the
	// alert ratio operates on this label too. Operators can replace
	// the field with a per-session lookup in a follow-on phase.
	DefaultPool string
}

// Snapshot satisfies the SessionEnumerator interface.
func (e *RegistryEnumerator) Snapshot(_ context.Context) ([]SessionInfo, error) {
	if e == nil || e.Registry == nil {
		return nil, nil
	}
	bindings := e.Registry.Snapshot()
	out := make([]SessionInfo, 0, len(bindings))
	for _, b := range bindings {
		out = append(out, SessionInfo{
			TenantID:  b.TenantID,
			SessionID: b.SessionID,
			Pool:      e.DefaultPool,
			// spec: §10.1 — postgres_null path. The
			// last_checkpoint_workspace_bytes field is not yet
			// implemented; every session falls back to the 90s
			// conservative tier until the §10.1 chunk pipeline lands
			// it. This is the spec-mandated fallback for sessions
			// without authoritative workspace-size evidence.
			IsPostgresNull: true,
		})
	}
	return out, nil
}
