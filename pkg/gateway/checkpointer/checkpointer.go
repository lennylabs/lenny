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
	"log"
	"time"

	"github.com/lennylabs/lenny/pkg/checkpoint"
	"github.com/lennylabs/lenny/pkg/gateway/checkpointretention"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
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

// DurationObserver receives the §4.4 line 254
// `lenny_checkpoint_duration_seconds` observation for a single
// checkpoint snapshot. The production wiring is
// gatewaymetrics.Metrics; a nil observer skips the emission.
//
// spec: §4.4 line 254.
type DurationObserver interface {
	// ObserveCheckpointDuration records one observation for the
	// supplied (pool, level, trigger) histogram label triple.
	ObserveCheckpointDuration(pool, level, trigger string, seconds float64)
}

// Retention persists the §4.4 line 234 / §12.5 latest-2 rotation
// catalog. The checkpointer records a row for every successful
// snapshot and runs Rotate immediately after so the table never
// holds more than RetainedCount active rows per (session, slot).
// Best-effort: a failure logs and discards rather than fail the
// snapshot — the catalog is observability for the §12.5 GC sweep,
// not a §4.4 correctness gate.
//
// spec: §4.4 line 234; §12.5 lines 313, 326.
type Retention interface {
	Insert(ctx context.Context, r checkpointretention.Record) error
	Rotate(ctx context.Context, tenantID, sessionID, slotID string) ([]checkpointretention.Record, error)
}

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
	// Metrics receives the §4.4 line 254 duration histogram
	// observation at the end of every checkpoint snapshot. Nil
	// disables the emission.
	Metrics DurationObserver
	// Retention persists the §4.4 line 234 / §12.5 latest-2
	// rotation catalog. Nil disables both the Insert and the Rotate
	// path; the snapshot still completes.
	Retention Retention
	// Pool is the pool-label value stamped onto the duration
	// histogram. Empty omits the label.
	Pool string
	// Level is the level-label value stamped onto the duration
	// histogram. Empty falls back to checkpoint.LevelBasic.
	Level checkpoint.Level
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
//
// A session this replica holds no binding for has no live workspace to
// seal — it either never ran on a pod or is coordinated elsewhere — so
// Seal returns nil rather than ErrNoBinding, satisfying the Sealer
// contract's "no-op for a session that never ran on a pod". The
// session's final snapshot then falls back to the latest checkpoint
// (§7.1 line 89). The §7.1 line 112 retry path therefore retries only
// real export failures, never an inapplicable seal.
func (c *Checkpointer) Seal(ctx context.Context, tenantID, sessionID string) error {
	err := c.snapshot(ctx, tenantID, sessionID, sessionstore.WorkspaceSnapshotSealed)
	if errors.Is(err, ErrNoBinding) {
		return nil
	}
	return err
}

// snapshot drives the session's bound pod adapter to checkpoint its
// workspace and records the result on the session row with the given
// §7.1 snapshot source. Every successful checkpoint also bumps the
// §4.4 line 258 `last_successful_checkpoint_at` freshness timestamp
// on the session row regardless of the trigger that produced it
// (periodic, eviction, pre-drain, or seal); the §4.4 freshness gauge
// keys off this field.
//
// Two side-effects are layered on top of the snapshot:
//
//   - The §4.4 line 254 `lenny_checkpoint_duration_seconds` histogram
//     is observed at the end of the snapshot regardless of outcome
//     (success or failure); the §16.5 CheckpointDurationHigh alert
//     reads the P95 of this histogram.
//   - The §4.4 line 234 / §12.5 latest-2 rotation catalog records a
//     new row for the checkpoint and runs Rotate to soft-delete any
//     row past the RetainedCount cap. Best-effort: catalog failures
//     log and discard.
func (c *Checkpointer) snapshot(ctx context.Context, tenantID, sessionID string, source sessionstore.WorkspaceSnapshotSource) error {
	binding, ok := c.Registry.Get(sessionID)
	if !ok {
		return ErrNoBinding
	}
	trigger := triggerForSource(source)
	startedAt := c.now()
	result, err := binding.Adapter.Checkpoint(ctx, sessionID, c.Deadline)
	elapsed := c.now().Sub(startedAt).Seconds()
	// spec: §4.4 line 254 — observe the duration histogram regardless
	// of outcome so operators see slow checkpoints that also failed
	// (the most common cause of a stuck quiescence handshake).
	c.observeDuration(trigger, elapsed)
	if err != nil {
		return fmt.Errorf("checkpointer: checkpoint session %s: %w", sessionID, err)
	}
	now := c.now()
	var legalHold bool
	if _, err := c.Sessions.Update(ctx, tenantID, sessionID, func(row *sessionstore.Session) error {
		// §12.5 line 313 — a session under legal hold is exempt from
		// the latest-2 rotation; capture the flag inside the update so
		// the post-snapshot rotation observes the committed state.
		legalHold = row.LegalHold
		row.WorkspaceSnapshot = &sessionstore.WorkspaceSnapshot{
			Ref:       result.CheckpointID,
			Source:    source,
			Timestamp: now,
			// spec: §7.3 line 397 / §10.1 coordinator-handoff step 0 —
			// persist the adapter-reported compressed size as the
			// authoritative last_checkpoint_workspace_bytes value so the
			// §7.2 line 138 workspaceRecoveryFraction and the §10.1
			// preStop tiered-cap selection both have a non-NULL input.
			// A zero size is treated the same as NULL by the storage
			// layer and the preStop fallback. F-7.3.21.
			Bytes: result.SizeBytes,
		}
		// spec: §4.4 line 258 — "The gateway tracks
		// last_successful_checkpoint_at on the session record in
		// Postgres, updated on every successful checkpoint regardless
		// of trigger".
		row.LastSuccessfulCheckpointAt = now
		return nil
	}); err != nil {
		return fmt.Errorf("checkpointer: record snapshot for session %s: %w", sessionID, err)
	}
	// §4.4 line 234 / §12.5 latest-2 rotation. Best-effort: a catalog
	// or rotation failure does not unwind the successful snapshot. The
	// rotation is per (session, slot) — binding.SlotID is the empty
	// string for the single-workspace path and the bound slot id for
	// concurrent-workspace pods.
	c.recordRetention(ctx, tenantID, sessionID, binding.SlotID, result.CheckpointID, legalHold)
	return nil
}

// observeDuration emits the §4.4 line 254 histogram observation.
// spec: §4.4 line 254.
func (c *Checkpointer) observeDuration(trigger checkpoint.Trigger, seconds float64) {
	if c.Metrics == nil {
		return
	}
	level := c.Level
	if level == "" {
		level = checkpoint.LevelBasic
	}
	c.Metrics.ObserveCheckpointDuration(c.Pool, string(level), string(trigger), seconds)
}

// recordRetention inserts the catalog row for the new checkpoint and
// runs Rotate to soft-delete any row past the §12.5 latest-2 cap for
// the (session, slot) pair. A session under legal hold is exempt from
// rotation (§12.5 line 313): the row is still catalogued so the
// §12.8 reconciler can observe it, but Rotate is skipped so every
// checkpoint is retained until the hold is lifted. Best-effort: a
// failure logs and discards rather than fail the snapshot.
// spec: §4.4 line 234 / §12.5 lines 313, 326.
func (c *Checkpointer) recordRetention(ctx context.Context, tenantID, sessionID, slotID, ref string, legalHold bool) {
	if c.Retention == nil || ref == "" {
		return
	}
	if err := c.Retention.Insert(ctx, checkpointretention.Record{
		TenantID:  tenantID,
		SessionID: sessionID,
		SlotID:    slotID,
		Ref:       ref,
	}); err != nil && !errors.Is(err, checkpointretention.ErrDuplicate) {
		log.Printf("checkpointer: retention insert tenant=%s session=%s slot=%s ref=%s: %v",
			tenantID, sessionID, slotID, ref, err)
		// Skip Rotate so the cap is not enforced against an unwritten
		// row; the next successful checkpoint will rotate the catalog.
		return
	}
	if legalHold {
		// §12.5 line 313 — held sessions retain all checkpoints.
		return
	}
	if _, err := c.Retention.Rotate(ctx, tenantID, sessionID, slotID); err != nil {
		log.Printf("checkpointer: retention rotate tenant=%s session=%s slot=%s: %v",
			tenantID, sessionID, slotID, err)
	}
}

// triggerForSource maps a §7.1 WorkspaceSnapshotSource to the
// §4.4 trigger label stamped onto the duration histogram. Sealed
// snapshots map to TriggerPeriodic because the seal-and-export path
// reuses the periodic checkpoint contract; eviction and pre-scale-
// down callers invoke the dedicated trigger directly via
// snapshotWithTrigger.
func triggerForSource(source sessionstore.WorkspaceSnapshotSource) checkpoint.Trigger {
	switch source {
	case sessionstore.WorkspaceSnapshotCheckpoint:
		return checkpoint.TriggerPeriodic
	case sessionstore.WorkspaceSnapshotSealed:
		return checkpoint.TriggerPeriodic
	default:
		return checkpoint.TriggerPeriodic
	}
}

// now returns the checkpoint timestamp.
func (c *Checkpointer) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now().UTC()
}
