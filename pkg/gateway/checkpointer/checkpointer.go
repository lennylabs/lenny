// SPDX-License-Identifier: MIT

// Package checkpointer drives §4.4 workspace checkpoints for the
// running sessions a gateway replica coordinates. It asks a session's
// bound pod adapter to snapshot the workspace and records the resulting
// §7.1 WorkspaceSnapshot on the session row, so a resume or a §7.1
// derive can recover the workspace from the latest checkpoint.
package checkpointer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
)

// ErrNoBinding reports that the checkpointer holds no pod binding for
// the session: it is not coordinated by this gateway replica.
var ErrNoBinding = errors.New("checkpointer: no pod binding for the session")

// Checkpointer takes §4.4 checkpoints of running sessions and records
// the resulting snapshot on the session row.
type Checkpointer struct {
	// Sessions is the session store the WorkspaceSnapshot is recorded
	// on.
	Sessions sessionstore.Store
	// Registry resolves a session to its bound pod adapter.
	Registry *podsession.Registry
	// Deadline bounds one checkpoint RPC. Zero lets the adapter apply
	// its own §4.4 default.
	Deadline time.Duration
	// Now returns the checkpoint timestamp. Nil selects time.Now.
	Now func() time.Time
}

// Checkpoint takes one §4.4 checkpoint of the session: it drives the
// session's bound pod adapter to snapshot the workspace, then records
// the checkpoint as the session's §7.1 WorkspaceSnapshot with source
// `checkpoint`. It returns ErrNoBinding when this replica does not
// coordinate the session.
func (c *Checkpointer) Checkpoint(ctx context.Context, tenantID, sessionID string) error {
	binding, ok := c.Registry.Get(sessionID)
	if !ok {
		return ErrNoBinding
	}
	result, err := binding.Adapter.Checkpoint(ctx, sessionID, c.Deadline)
	if err != nil {
		return fmt.Errorf("checkpointer: checkpoint session %s: %w", sessionID, err)
	}
	if _, err := c.Sessions.Update(ctx, tenantID, sessionID, func(row *sessionstore.Session) error {
		row.WorkspaceSnapshot = &sessionstore.WorkspaceSnapshot{
			Ref:       result.CheckpointID,
			Source:    sessionstore.WorkspaceSnapshotCheckpoint,
			Timestamp: c.now(),
		}
		return nil
	}); err != nil {
		return fmt.Errorf("checkpointer: record snapshot for session %s: %w", sessionID, err)
	}
	return nil
}

// now returns the checkpoint timestamp.
func (c *Checkpointer) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now().UTC()
}
