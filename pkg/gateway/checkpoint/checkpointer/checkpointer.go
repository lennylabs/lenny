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
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/checkpoint"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointretention"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
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

// DefaultGrantWindow is the §10.1 line 131 / §17.8.1 default for
// `checkpointGrantWindow`: the gateway keeps at most this many
// chunk-upload capabilities outstanding at once while draining a
// session's workspace checkpoint. It bounds both the pipelining depth
// and the worst-case unreconciled overage against a backend that does
// not enforce the signed Content-Length.
// spec: §17.8.1 — "Checkpoint grant window (checkpointGrantWindow) | 4
// chunks".
const DefaultGrantWindow = 4

// DefaultCapabilityTTLSeconds is the §13.2 / §17.8.1 default for
// `checkpointCapabilityTTLSeconds`: every gateway-minted presigned
// checkpoint upload or restore capability expires this many seconds
// after it is signed.
// spec: §17.8.1 — "Checkpoint capability TTL
// (checkpointCapabilityTTLSeconds) | 30 s".
const DefaultCapabilityTTLSeconds = 30

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
	// GrantWindow is the deployment-wide default number of chunk-upload
	// capabilities the checkpoint grant/confirm loop keeps outstanding at
	// once. It is the fallback the loop applies when the session's pool
	// declares no per-pool checkpointGrantWindow override. Zero selects
	// DefaultGrantWindow.
	// spec: §10.1 line 131 chunk-grant window; §17.8.1 checkpointGrantWindow
	// default 4; §5.2 (deployment-wide default with per-pool override).
	GrantWindow int
	// CapabilityTTL is the expiry the loop signs into every presigned
	// checkpoint upload and restore capability. Zero selects
	// DefaultCapabilityTTLSeconds.
	// spec: §13.2 checkpoint capability model; §17.8.1
	// checkpointCapabilityTTLSeconds default 30 s.
	CapabilityTTL time.Duration
	// PoolGrantWindow resolves a session's pool to its per-pool
	// checkpointGrantWindow override. The loop reads the override at
	// checkpoint time and falls back to GrantWindow when the reader is nil,
	// the pool has no mirror row, or the pool declares no override. It is
	// the §5.2 poolstore-backed sessionPolicy mirror in production.
	// spec: §5.2 (per-pool override of the deployment-wide default);
	// §5.2 (pool config).
	PoolGrantWindow podsession.PoolPolicyReader
	// Manifests persists the §10.1 checkpoint_manifest intent row, the
	// monotonic chunk-confirm counter, the terminal finalisation, and the
	// exactly-once reservation release. Nil disables the whole manifest
	// side of the driver (dev-mode / degenerate test path); the snapshot
	// still records the WorkspaceSnapshot from the adapter's Summary total.
	// spec: §10.1 lines 130-141.
	Manifests partialmanifeststore.Store
	// Quota reserves the probed workspace size against the tenant's §11.2
	// storage counter before any grant and reconciles it against the
	// confirmed total on every terminal arm. Nil (or a nil QuotaLimitFor)
	// disables reservation.
	// spec: §11.2 (checkpoint quota reservation and reconciliation).
	Quota StorageQuota
	// QuotaLimitFor resolves the tenant's storage limit at reservation
	// time. Nil disables reservation.
	QuotaLimitFor func(ctx context.Context, tenantID string) (int64, error)
	// Presigner mints one single-key presigned PUT capability per chunk in
	// response to a ChunkReady. Required to serve a chunked producer; nil
	// aborts a declared chunk.
	// spec: §13.2 (capability model).
	Presigner blobstore.Presigner
	// ObjectStore confirms each committed chunk with Stat (the bytes
	// actually written). Nil disables confirmation; the adapter's Summary
	// total is then authoritative.
	// spec: §11.2 (bytes-actually-written confirmation).
	ObjectStore ObjectStat
	// Cataloging inserts the §12.5 artifact_store catalog row for each
	// confirmed chunk from its Stat-confirmed size. Nil skips cataloging.
	// spec: §12.5 line 309.
	Cataloging ChunkRecorder
	// ChunkDeleter sweeps the chunk objects under a manifest's
	// chunk_object_key_prefix on the abort and supersede arms. Nil skips
	// the object-store sweep (dev-mode / test path).
	// spec: §4.4 line 236 / §10.1 line 157.
	ChunkDeleter PartialChunkDeleter
	// DriverMetrics receives the gateway-side §4.4 / §10.1 counter
	// increments the object-store call moving off the adapter transferred
	// to the gateway. Nil disables the emissions.
	// spec: §4.4 lines 254, 262; §10.1 supersede-on-write; §12.5 line 303.
	DriverMetrics DriverMetrics
	// SkippedEventFunc emits the §4.4 line 255 `checkpoint.skipped` session
	// event when the adapter rejects the run at its workspace-size probe.
	// Nil disables the emission.
	// spec: §4.4 line 255.
	SkippedEventFunc func(ctx context.Context, tenantID, sessionID, reason string)
	// ChunkSizeBytes is the gateway-chosen fixed chunk size the driver
	// signs into every grant and clamps every declared length against.
	// Zero selects DefaultChunkSizeBytes.
	// spec: §10.1 line 139 (fixed-size chunked-object model).
	ChunkSizeBytes int64
	// ChunkEncoding is the wire encoding the gateway declares in Start.
	// Empty selects tar.
	ChunkEncoding string

	// flights holds the per-(session, slot) single-flight lock the driver
	// acquires for the duration of one attempt. Lazily populated.
	flights sync.Map
}

// grantWindow returns the deployment-wide default checkpoint grant
// window, applying DefaultGrantWindow when GrantWindow is unset.
func (c *Checkpointer) grantWindow() int {
	if c.GrantWindow > 0 {
		return c.GrantWindow
	}
	return DefaultGrantWindow
}

// capabilityTTL returns the presigned-capability expiry the loop signs
// into each grant, applying DefaultCapabilityTTLSeconds when
// CapabilityTTL is unset.
func (c *Checkpointer) capabilityTTL() time.Duration {
	if c.CapabilityTTL > 0 {
		return c.CapabilityTTL
	}
	return DefaultCapabilityTTLSeconds * time.Second
}

// grantWindowForPool resolves the effective checkpoint grant window for a
// session bound to pool. It reads the pool's per-pool
// checkpointGrantWindow override through PoolGrantWindow and falls back to
// the deployment-wide default (grantWindow) when no reader is wired, the
// pool has no mirror row, or the pool declares no override. A reader error
// is treated as "no override" so a transient mirror-read failure degrades
// to the deployment-wide default rather than aborting the checkpoint.
//
// spec: §5.2 — the checkpoint driver resolves the pool's override at
// attempt time and falls back to the deployment-wide default when the pool
// sets none; §5.2 (pool config).
func (c *Checkpointer) grantWindowForPool(ctx context.Context, pool string) int {
	def := c.grantWindow()
	if c.PoolGrantWindow == nil || pool == "" {
		return def
	}
	mirror, found, err := c.PoolGrantWindow.PoolPolicy(ctx, pool)
	if err != nil || !found || mirror.CheckpointGrantWindow == nil {
		return def
	}
	if w := int(*mirror.CheckpointGrantWindow); w > 0 {
		return w
	}
	return def
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
	return c.snapshot(ctx, tenantID, sessionID, sessionstore.WorkspaceSnapshotCheckpoint, checkpoint.TriggerPeriodic)
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
//
// Seal stamps the §4.4 TriggerPeriodic label on the duration histogram:
// the §4.4 enum (pkg/checkpoint.Trigger) has no seal value, and the
// seal/periodic distinction is carried by the recorded
// WorkspaceSnapshotSource on the session row rather than by the trigger.
// spec: §4.4 line 259 (the trigger enum has no seal value).
func (c *Checkpointer) Seal(ctx context.Context, tenantID, sessionID string) error {
	err := c.snapshot(ctx, tenantID, sessionID, sessionstore.WorkspaceSnapshotSealed, checkpoint.TriggerPeriodic)
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
func (c *Checkpointer) snapshot(ctx context.Context, tenantID, sessionID string, source sessionstore.WorkspaceSnapshotSource, trigger checkpoint.Trigger) error {
	binding, ok := c.Registry.Get(sessionID)
	if !ok {
		return ErrNoBinding
	}
	startedAt := c.now()
	checkpointID, sizeBytes, err := c.driveCheckpoint(ctx, binding, tenantID, sessionID, trigger)
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
			// spec: §10.1 line 130 — the gateway mints the
			// checkpoint_id and records it as the snapshot ref; the
			// adapter never mints one.
			Ref:       checkpointID,
			Source:    source,
			Timestamp: now,
			// spec: §7.3 line 397 / §10.1 coordinator-handoff step 0 —
			// persist the adapter-reported compressed size as the
			// authoritative last_checkpoint_workspace_bytes value so the
			// §7.2 line 138 workspaceRecoveryFraction and the §10.1
			// preStop tiered-cap selection both have a non-NULL input.
			// A zero size is treated the same as NULL by the storage
			// layer and the preStop fallback. F-7.3.21.
			Bytes: sizeBytes,
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
	c.recordRetention(ctx, tenantID, sessionID, binding.SlotID, checkpointID, legalHold)
	return nil
}

// driveCheckpoint runs the gateway side of the §4.7 / §10.1
// bidirectional Checkpoint stream far enough to record a workspace
// snapshot. It mints the checkpoint_id (§10.1 line 130 — the gateway
// mints it, never the adapter), opens the stream against the bound pod,
// and sends CheckpointStart carrying the id and the typed trigger. It
// then consumes server frames until the adapter closes the attempt with
// CheckpointSummary (returning the gateway-minted id and the
// adapter-reported total byte count) or with CheckpointFailed or a
// stream error (returning the failure). The intermediate CheckpointProbe,
// ChunkReady, and ChunkCommitted frames advance the chunk grant/confirm
// loop; the record path here waits for the terminal frame.
//
// The per-(session, slot) single-flight lock serialises a periodic
// checkpoint against a concurrent seal of the same workspace so their
// supersede / finalise manifest writes cannot interleave, and the driver
// resolves the effective grant window from the bound pool at attempt time
// (§5.2 per-pool checkpointGrantWindow override).
//
// spec: §10.1 lines 130-132 — the gateway mints the checkpoint_id, opens
// the stream, and the adapter reports the confirmed byte total in
// CheckpointSummary.
func (c *Checkpointer) driveCheckpoint(ctx context.Context, binding *podsession.BindResult, tenantID, sessionID string, trigger checkpoint.Trigger) (string, int64, error) {
	slotID := binding.SlotID
	if slotID == "" {
		slotID = partialmanifeststore.SlotDefault
	}
	// Serialise every attempt for this (session, slot) so the supersede set
	// the intent-row transaction fences is stable for the duration of the
	// attempt.
	unlock := c.lockFlight(sessionID, slotID)
	defer unlock()

	// Cancel the stream on return so its RPC is torn down once the
	// terminal frame is read. c.Deadline additionally bounds the whole
	// attempt: a stalled stream cancels rather than blocking the sweep
	// indefinitely. Zero lets the adapter apply its own §4.4 default.
	var cancel context.CancelFunc
	if c.Deadline > 0 {
		ctx, cancel = context.WithTimeout(ctx, c.Deadline)
	} else {
		ctx, cancel = context.WithCancel(ctx)
	}
	defer cancel()
	stream, err := binding.Adapter.Checkpoint(ctx)
	if err != nil {
		return "", 0, fmt.Errorf("open checkpoint stream: %w", err)
	}
	checkpointID := uuid.NewString()
	start := &adapterv1.CheckpointClientMessage{
		Msg: &adapterv1.CheckpointClientMessage_Start{
			Start: &adapterv1.CheckpointStart{
				CheckpointId:   checkpointID,
				Trigger:        trigger.Proto(),
				ChunkSizeBytes: c.chunkSize(),
				ChunkEncoding:  string(c.chunkEncoding()),
				DeadlineMs:     c.Deadline.Milliseconds(),
			},
		},
	}
	if err := stream.Send(start); err != nil {
		return "", 0, fmt.Errorf("send checkpoint start: %w", err)
	}
	// Read the session row once for every value the attempt is scoped by: the
	// pool (for the §5.2 per-pool grant-window override) and the §10.1
	// coordinator/recovery generations and baseline the intent row is fenced
	// and sized on.
	sc := c.sessionCheckpointContext(ctx, tenantID, sessionID)
	d := &uploadDriver{
		c:            c,
		ctx:          ctx,
		cancel:       cancel,
		stream:       stream,
		tenantID:     tenantID,
		sessionID:    sessionID,
		slotID:       slotID,
		checkpointID: checkpointID,
		prefix:       chunkPrefix(tenantID, sessionID, checkpointID),
		trigger:      trigger,
		// Resolve the effective grant window from the bound pool's per-pool
		// override at attempt time (§5.2). It bounds the grants the driver
		// keeps outstanding ahead of confirmation. The pool is the session's
		// own scheduled pool, read from its session-store row, rather than the
		// static Checkpointer.Pool label, so the per-pool override applies per
		// session across the sweep.
		window:          c.grantWindowForPool(ctx, sc.pool),
		coordinationGen: sc.coordinationGen,
		recoveryGen:     sc.recoveryGen,
		baselineBytes:   sc.baselineBytes,
	}
	return d.run()
}

// sessionCheckpointContext is the slice of session-row state a checkpoint
// attempt is scoped by: the pool it is scheduled against and the §10.1
// generations and baseline the intent row is written with.
type sessionCheckpointContext struct {
	pool            string
	coordinationGen int64
	recoveryGen     int64
	baselineBytes   *int64
}

// sessionCheckpointContext reads the session row once so the driver resolves
// the bound pool's per-pool checkpointGrantWindow override (§5.2) and writes
// the §10.1 intent row with the session's coordinator generation, recovery
// counter, and last_checkpoint_workspace_bytes baseline. The supersede fence
// rejects a stale coordinator on coordination_generation and the resume path
// selects the highest-generation row, so pinning every intent row at
// generation 0 would let a stale coordinator orphan a live newer writer's
// active manifest. A nil session store or a read error yields the zero value
// (empty pool → deployment-wide window, generation 0, no baseline), so a
// transient read failure degrades to the deployment default rather than
// aborting the checkpoint.
//
// spec: §5.2 (per-pool override); §10.1 lines 137, 155 (generation fence,
// MAX(coordination_generation) resume selector, baseline denominator).
func (c *Checkpointer) sessionCheckpointContext(ctx context.Context, tenantID, sessionID string) sessionCheckpointContext {
	if c.Sessions == nil {
		return sessionCheckpointContext{}
	}
	row, err := c.Sessions.Get(ctx, tenantID, sessionID)
	if err != nil {
		return sessionCheckpointContext{}
	}
	sc := sessionCheckpointContext{
		pool:            row.PoolRef,
		coordinationGen: row.CoordinationGeneration,
		recoveryGen:     row.RecoveryGeneration,
	}
	// A prior successful checkpoint recorded its compressed size as
	// last_checkpoint_workspace_bytes; freeze it as this attempt's baseline.
	// Nil is load-bearing: it denotes a session with no prior successful full
	// checkpoint (§10.1 line 155).
	if row.WorkspaceSnapshot != nil && row.WorkspaceSnapshot.Bytes > 0 {
		b := row.WorkspaceSnapshot.Bytes
		sc.baselineBytes = &b
	}
	return sc
}

// chunkPrefix builds the §10.1 chunk_object_key_prefix under which an
// attempt's chunk objects live:
// /{tenant_id}/checkpoints/{session_id}/{checkpoint_id}/.
func chunkPrefix(tenantID, sessionID, checkpointID string) string {
	return fmt.Sprintf("/%s/%s/%s/%s/", tenantID, blobstore.ObjectTypeCheckpoint, sessionID, checkpointID)
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

// now returns the checkpoint timestamp.
func (c *Checkpointer) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now().UTC()
}
