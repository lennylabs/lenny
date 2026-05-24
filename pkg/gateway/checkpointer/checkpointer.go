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
	"hash/fnv"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
)

// ErrNoBinding reports that the checkpointer holds no pod binding for
// the session: it is not coordinated by this gateway replica.
var ErrNoBinding = errors.New("checkpointer: no pod binding for the session")

// defaultInterval is the periodic-checkpoint cadence used when Interval
// is zero. The §4.4 line 256/258 mandated default is 600 s / 10 minutes
// (`periodicCheckpointIntervalSeconds`).
// spec: §4.4 line 256 — "every active session MUST have a successful
// checkpoint recorded within the last periodicCheckpointIntervalSeconds
// (default: 600s / 10 minutes)".
const defaultInterval = 10 * time.Minute

// DefaultJitterFraction is the §4.4 line 258 default for
// `periodicCheckpointJitterFraction`: 0.2 spreads the first periodic
// checkpoint uniformly across a 120-second window at the default
// 600-second interval, preventing correlated burst patterns.
// spec: §4.4 line 258 — "periodicCheckpointJitterFraction (default: 0.2,
// range: 0.0–1.0)".
const DefaultJitterFraction = 0.2

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
	// JitterFraction spreads each session's first periodic checkpoint
	// across `Interval + random(0, Interval × JitterFraction)` per
	// §4.4 line 258. Range is [0.0, 1.0]; a negative value or a value
	// > 1.0 is clamped to the [0.0, 1.0] range. Zero selects no jitter
	// (every session ticks together at the same wall-clock second),
	// matching the test-only no-jitter path.
	// spec: §4.4 line 258 — "each session's first periodic checkpoint
	// is scheduled at periodicCheckpointIntervalSeconds + random(0,
	// periodicCheckpointIntervalSeconds × periodicCheckpointJitterFraction)".
	JitterFraction float64
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

// FirstCheckpointDelay computes the per-session jitter for the session's
// first periodic checkpoint per §4.4 line 258: the delay is
// `interval + random(0, interval × jitterFraction)` where the random
// component is seeded by a stable hash of sessionID so the same session
// receives the same jitter across gateway restarts and coordinator
// handoffs. Subsequent checkpoints fire at the fixed interval from the
// previous checkpoint (no additional jitter); after the first cycle
// sessions naturally desynchronise.
// spec: §4.4 line 258 — "Subsequent checkpoints are scheduled at the
// fixed interval from the previous checkpoint (no additional jitter),
// so sessions naturally desynchronize after the first cycle."
func FirstCheckpointDelay(sessionID string, interval time.Duration, jitterFraction float64) time.Duration {
	if interval <= 0 {
		return 0
	}
	if jitterFraction < 0 {
		jitterFraction = 0
	}
	if jitterFraction > 1 {
		jitterFraction = 1
	}
	if jitterFraction == 0 || sessionID == "" {
		return interval
	}
	// hash/fnv is stable across restarts so the same session always
	// receives the same jitter — avoiding the thundering-herd retry
	// pattern that would emerge if a restarted gateway re-rolled the
	// jitter on every reboot.
	h := fnv.New64a()
	_, _ = h.Write([]byte(sessionID))
	frac := float64(h.Sum64()%1_000_000) / 1_000_000.0 // uniform [0, 1)
	addition := time.Duration(float64(interval) * jitterFraction * frac)
	return interval + addition
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

// Checkpoint takes one §4.4 periodic checkpoint of the session: it
// drives the session's bound pod adapter to snapshot the workspace,
// then records the result as the session's §7.1 WorkspaceSnapshot with
// source `checkpoint`. It returns ErrNoBinding when this replica does
// not coordinate the session.
func (c *Checkpointer) Checkpoint(ctx context.Context, tenantID, sessionID string) error {
	return c.snapshot(ctx, tenantID, sessionID, sessionstore.WorkspaceSnapshotCheckpoint)
}

// Seal takes the §7.1 final workspace snapshot of a completing session
// and records it with source `sealed`. It is the seal-and-export run
// on session completion, distinguished from a periodic checkpoint only
// by the recorded snapshot source.
func (c *Checkpointer) Seal(ctx context.Context, tenantID, sessionID string) error {
	return c.snapshot(ctx, tenantID, sessionID, sessionstore.WorkspaceSnapshotSealed)
}

// snapshot drives the session's bound pod adapter to checkpoint its
// workspace and records the result on the session row with the given
// §7.1 snapshot source.
func (c *Checkpointer) snapshot(ctx context.Context, tenantID, sessionID string, source sessionstore.WorkspaceSnapshotSource) error {
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
			Source:    source,
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
