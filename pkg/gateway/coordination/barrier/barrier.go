// SPDX-License-Identifier: MIT

// Package barrier implements the gateway side of the §10.1 lines
// 163-181 CheckpointBarrier protocol for rolling updates. The adapter
// side (generation validation, tool-call quiescence, best-effort
// checkpoint flush, and the CheckpointBarrierAck) lives in
// pkg/adapter; this package drives the protocol from the coordinating
// gateway replica:
//
//   - Dispatch enumerates the barrier-target set (the sessions this
//     replica coordinates), allocates a monotonically-increasing
//     barrier id per session, sends the CheckpointBarrier to every
//     target's pod under a single wall-clock deadline, and persists
//     the ack's barrier_id, checkpoint_ref, and
//     workspace_recovery_fraction into session_checkpoint_meta so a
//     later coordinator can source the `session.resumed`
//     workspaceRecoveryFraction (§10.1 lines 165-166, 393).
//
// The gateway is not in the per-tool-call dispatch path (§4.7) and a
// coordinator handoff re-adopts the still-running pod without a
// checkpoint restore (§4.2), so §10.1 performs no gateway-side
// resume-deduplication on handoff; the barrier flushes a checkpoint
// on graceful drain and is otherwise independently useful.
//
// The collaborators are injected as interfaces so the protocol logic
// is unit-testable without a cluster: production wires a per-pod
// adapter dialer behind Dispatcher and the coordination-lease query
// behind TargetLister. The cmd/lenny-gateway wiring that connects
// these to the live preStop drain is the remaining integration step.
//
// spec: §10.1 lines 163-181, 393.
package barrier

import (
	"context"
	"errors"
	"strconv"
	"sync"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessioncheckpointmeta"
)

// Source labels the provenance of a barrier-target-set read. They are
// the values stamped on the §10.1 counters.
const (
	// SourcePostgres is the steady-state healthy path: the target set
	// came from the coordination-lease table.
	SourcePostgres = "postgres"
	// SourceCacheFallback is the §10.1 line 165 degraded path: the
	// coordination-lease read failed or exceeded its 2s deadline and
	// the replica fell back to its in-memory lease cache for the
	// barrier-target set.
	SourceCacheFallback = "cache_fallback"
)

// Target is one session in the barrier-target set: a session this
// replica coordinates, addressed by its pod for the CheckpointBarrier
// RPC.
type Target struct {
	TenantID  string
	SessionID string
	// CoordinationGeneration is the replica's fenced generation for
	// the session. The adapter rejects a barrier whose generation does
	// not match its last fenced value (§10.1 line 165 false-positive
	// handling), so a stale value is safe.
	CoordinationGeneration int64
	// PodAddr is the adapter dial target the Dispatcher routes to.
	// Opaque to this package.
	PodAddr string
}

// Ack mirrors the §10.1 CheckpointBarrierAck the adapter returns: the
// best-effort checkpoint ref and the optional workspace-recovery
// fraction the coordinator-handoff `session.resumed` event reports
// (§10.1 line 393).
type Ack struct {
	CheckpointRef             string
	WorkspaceRecoveryFraction *float64
}

// Dispatcher sends one CheckpointBarrier to a target's pod and returns
// the adapter's ack. Production wires a per-pod adapter dialer behind
// it; a FailedPrecondition error from a generation-stale pod is
// surfaced as ErrGenerationStale so Dispatch can record it without
// aborting the drain.
type Dispatcher interface {
	Send(ctx context.Context, t Target, barrierID string) (Ack, error)
}

// TargetLister enumerates the barrier-target set for this replica and
// reports its source. Production queries the coordination-lease mirror
// under a 2s deadline and falls back to the in-memory lease cache; the
// returned Source drives the §10.1 line 165 counter.
type TargetLister interface {
	Targets(ctx context.Context) ([]Target, string, error)
}

// Metrics receives the §10.1 barrier counters. *gatewaymetrics.Metrics
// satisfies it; tests inject fakes.
type Metrics interface {
	IncPreStopBarrierTargetSource(source string)
}

// ErrGenerationStale is the sentinel a Dispatcher returns when the pod
// rejects the barrier as generation-stale (a §10.1 line 165
// false-positive that survived the cache fallback). It is recorded but
// never aborts the drain.
var ErrGenerationStale = errors.New("barrier: pod rejected barrier as generation-stale")

// Coordinator drives the gateway side of the CheckpointBarrier
// protocol. Construct with New.
type Coordinator struct {
	targets  TargetLister
	dispatch Dispatcher
	meta     sessioncheckpointmeta.Store
	metrics  Metrics
}

// New returns a Coordinator. targets, dispatch, and meta are required;
// metrics may be nil (the counters are disabled).
func New(targets TargetLister, dispatch Dispatcher, meta sessioncheckpointmeta.Store, metrics Metrics) *Coordinator {
	return &Coordinator{targets: targets, dispatch: dispatch, meta: meta, metrics: metrics}
}

// Outcome is the per-session result of a barrier dispatch.
type Outcome struct {
	Target Target
	// BarrierID is the id allocated for this barrier.
	BarrierID string
	// Acked reports whether the pod acknowledged within the deadline.
	Acked bool
	// Stale reports the pod rejected the barrier as generation-stale
	// (a §10.1 line 165 false-positive); not a failure.
	Stale bool
	// Err is any non-stale dispatch error (deadline, transport). The
	// drain continues past it.
	Err error
}

// DispatchSummary aggregates a Dispatch pass.
type DispatchSummary struct {
	Source   string
	Outcomes []Outcome
}

// Dispatch runs one §10.1 barrier pass. It enumerates the
// barrier-target set, then sends the CheckpointBarrier to every target
// concurrently under ctx (the caller bounds ctx by
// checkpointBarrierAckTimeoutSeconds — the single wall-clock deadline
// across all pods, §10.1 line 167). For each acked target it persists
// the barrier_id, checkpoint_ref, and workspace_recovery_fraction into
// session_checkpoint_meta so a later coordinator can source the
// `session.resumed` workspaceRecoveryFraction (§10.1 line 393). A
// per-target error (deadline, transport, generation-stale) is recorded
// in the Outcome and does not abort the pass — a degraded barrier is
// strictly better than none when the replica is already terminating.
//
// spec: §10.1 lines 165-166.
func (c *Coordinator) Dispatch(ctx context.Context) (DispatchSummary, error) {
	targets, source, err := c.targets.Targets(ctx)
	if err != nil {
		return DispatchSummary{}, err
	}
	if c.metrics != nil {
		c.metrics.IncPreStopBarrierTargetSource(source)
	}
	outcomes := make([]Outcome, len(targets))
	var wg sync.WaitGroup
	for i, t := range targets {
		i, t := i, t
		wg.Add(1)
		go func() {
			defer wg.Done()
			outcomes[i] = c.dispatchOne(ctx, t)
		}()
	}
	wg.Wait()
	return DispatchSummary{Source: source, Outcomes: outcomes}, nil
}

// dispatchOne allocates the next barrier id for the target, sends the
// barrier, and persists the ack. It never returns an error: the
// Outcome carries the disposition.
func (c *Coordinator) dispatchOne(ctx context.Context, t Target) Outcome {
	barrierID := c.nextBarrierID(ctx, t)
	out := Outcome{Target: t, BarrierID: barrierID}
	ack, err := c.dispatch.Send(ctx, t, barrierID)
	if err != nil {
		if errors.Is(err, ErrGenerationStale) {
			out.Stale = true
			return out
		}
		out.Err = err
		return out
	}
	out.Acked = true
	rec := sessioncheckpointmeta.Record{
		TenantID:                  t.TenantID,
		SessionID:                 t.SessionID,
		CoordinationGeneration:    t.CoordinationGeneration,
		BarrierID:                 barrierID,
		CheckpointRef:             ack.CheckpointRef,
		WorkspaceRecoveryFraction: ack.WorkspaceRecoveryFraction,
	}
	if err := c.meta.Upsert(ctx, rec); err != nil {
		// The persisted barrier_id, checkpoint_ref, and
		// workspace_recovery_fraction are a best-effort record: the
		// only current consumer is the coordinator-handoff
		// `session.resumed` workspaceRecoveryFraction (§10.1 line 393),
		// which tolerates an absent row. Record the write error but keep
		// Acked true.
		out.Err = err
	}
	return out
}

// nextBarrierID returns the next monotonically-increasing-per-session
// barrier id (§10.1 line 165). It reads the session's prior persisted
// barrier id and increments; an absent or unparseable prior value
// starts the sequence at 1. A store read error degrades to 1 rather
// than aborting the barrier — a non-monotonic id on the degraded path
// is harmless because the adapter validates the generation, not the
// barrier id.
func (c *Coordinator) nextBarrierID(ctx context.Context, t Target) string {
	prev := int64(0)
	if rec, err := c.meta.Get(ctx, t.TenantID, t.SessionID); err == nil {
		if n, perr := strconv.ParseInt(rec.BarrierID, 10, 64); perr == nil {
			prev = n
		}
	}
	return strconv.FormatInt(prev+1, 10)
}
