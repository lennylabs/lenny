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

// defaultInterval is the periodic-checkpoint cadence used when Interval
// is zero.
const defaultInterval = 5 * time.Minute

// Checkpointer takes §4.4 checkpoints of running sessions and records
// the resulting snapshot on the session row.
type Checkpointer struct {
	// Sessions is the session store the WorkspaceSnapshot is recorded
	// on.
	Sessions sessionstore.Store
	// Registry resolves a session to its bound pod adapter and
	// enumerates the sessions this replica coordinates.
	Registry *podsession.Registry
	// Deadline bounds one checkpoint RPC. Zero lets the adapter apply
	// its own §4.4 default.
	Deadline time.Duration
	// Interval is the periodic-checkpoint cadence Run ticks on. Zero
	// selects defaultInterval.
	Interval time.Duration
	// Now returns the checkpoint timestamp. Nil selects time.Now.
	Now func() time.Time
	// OnError, when set, receives a per-session checkpoint failure
	// during a sweep so the gateway can log it. A sweep continues
	// past a failed session regardless.
	OnError func(sessionID string, err error)
}

// Run drives the periodic-checkpoint loop: every Interval it sweeps a
// §4.4 checkpoint of every running session this gateway replica
// coordinates, keeping each session's WorkspaceSnapshot fresh against
// the §16.5 checkpoint-freshness SLO. Run blocks until ctx is
// cancelled.
func (c *Checkpointer) Run(ctx context.Context) {
	ticker := time.NewTicker(c.interval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.Sweep(ctx)
		}
	}
}

// Sweep takes one §4.4 checkpoint of every session this replica
// coordinates. A per-session failure is reported through OnError and
// does not stop the sweep.
func (c *Checkpointer) Sweep(ctx context.Context) {
	for _, b := range c.Registry.Snapshot() {
		if err := c.Checkpoint(ctx, b.TenantID, b.SessionID); err != nil && c.OnError != nil {
			c.OnError(b.SessionID, err)
		}
	}
}

// interval returns the configured periodic-checkpoint cadence.
func (c *Checkpointer) interval() time.Duration {
	if c.Interval > 0 {
		return c.Interval
	}
	return defaultInterval
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
