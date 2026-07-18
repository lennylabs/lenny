// SPDX-License-Identifier: MIT

package checkpointer

import (
	"context"

	"github.com/lennylabs/lenny/pkg/checkpoint"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// Snapshot exposes the unexported snapshot driver to the external test
// package so a test can exercise a trigger the exported Checkpoint and
// Seal entry points do not stamp (for example checkpoint.TriggerEviction,
// which the gateway's preStop drain path threads through). It carries no
// production callers.
func (c *Checkpointer) Snapshot(ctx context.Context, tenantID, sessionID string, source sessionstore.WorkspaceSnapshotSource, trigger checkpoint.Trigger) error {
	return c.snapshot(ctx, tenantID, sessionID, source, trigger)
}
