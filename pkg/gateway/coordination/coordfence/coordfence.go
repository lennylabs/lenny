// SPDX-License-Identifier: MIT

// Package coordfence drives the gateway side of the §10.1 / §4.2
// CoordinatorFence handshake. When a gateway replica (re-)establishes
// coordination of a session on a pod — the coordinator-handoff / resume
// path — it announces the session's current coordination_generation to
// the pod so the pod rejects any straggler RPC from a prior coordinator
// (§10.1 lines 33-37, §4.7 line 632).
//
// The Fencer wraps the §11.3 line 209 retry/relinquish policy around the
// adapterclient.Client.CoordinatorFence wrapper (which carries the 5s
// hard-coded per-call timeout):
//
//   - A generation-stale rejection (FailedPrecondition) means the pod has
//     already been fenced to an equal-or-higher generation by another
//     coordinator. The Fencer re-reads the authoritative generation; if
//     it advanced (a handoff bump landed mid-flight) it retries with the
//     new value, otherwise it relinquishes leadership.
//   - A transient transport fault is retried up to maxAttempts.
//   - When the attempt budget is exhausted, the Fencer relinquishes:
//     it releases the coordination lease so another replica can take the
//     session over and reports ErrRelinquished to the caller, which must
//     abort the resume.
//
// Each stale rejection increments `lenny_coordinator_handoff_stale_total`,
// each retry `lenny_coordinator_fence_retry_total`, and each relinquish
// `lenny_coordinator_fence_relinquished_total` (§10.1 line 61, §11.3
// line 209).
//
// The collaborators are injected as interfaces so the policy is
// unit-testable without a cluster: production wires the live pod adapter
// behind FenceClient, the SessionStore generation read behind
// GenerationReader, and the LeaseStore release behind LeaseReleaser.
//
// spec: §10.1 lines 33-37, 61; §4.2 line 158; §11.3 line 209.
package coordfence

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
)

// DefaultMaxAttempts is the §11.3 line 209 fence attempt budget before
// the coordinator relinquishes leadership. One initial attempt plus two
// retries balances tolerating a transient fault against not stalling the
// resume path; the 5s per-attempt timeout bounds the total wall-clock.
const DefaultMaxAttempts = 3

// ErrRelinquished reports that the coordinator gave up leadership of the
// session after exhausting its fence retries (or because the pod is
// fenced to a higher generation): the coordination lease was released
// and the caller must abort the resume so another replica takes over.
var ErrRelinquished = errors.New("coordfence: coordinator relinquished session after fence retries")

// FenceClient issues one CoordinatorFence to a pod's adapter.
// *adapterclient.Client satisfies it.
type FenceClient interface {
	CoordinatorFence(ctx context.Context, sessionID string, coordinationGeneration int64) (adapterclient.CoordinatorFenceResult, error)
}

// GenerationReader returns the session's current §4.2
// coordination_generation. Production wires the SessionStore; the Fencer
// re-reads it after a stale rejection so a generation bump that landed
// mid-handoff is picked up on retry.
type GenerationReader interface {
	CoordinationGeneration(ctx context.Context, tenantID, sessionID string) (int64, error)
}

// LeaseReleaser releases the session's coordination lease for the
// relinquish path. *leasestore.Store / leasestore.LeaseStore satisfies
// it via the Release method.
type LeaseReleaser interface {
	Release(ctx context.Context, tenantID, sessionID, holder string) error
}

// Metrics receives the §10.1 / §11.3 fence counters. *gatewaymetrics.Metrics
// satisfies it; tests inject fakes.
type Metrics interface {
	IncCoordinatorHandoffStale()
	IncCoordinatorFenceRetry()
	IncCoordinatorFenceRelinquished()
}

// Fencer issues the §10.1 CoordinatorFence with the §11.3 retry/relinquish
// policy. Construct with New.
type Fencer struct {
	generations GenerationReader
	leases      LeaseReleaser
	replicaID   string
	metrics     Metrics
	maxAttempts int
	logf        func(format string, args ...any)
}

// Options tunes a Fencer.
type Options struct {
	// MaxAttempts overrides DefaultMaxAttempts. A value < 1 selects the
	// default.
	MaxAttempts int
	// Logf, when set, receives a one-line diagnostic on each retry and
	// relinquish so operators can correlate the counters with the log.
	Logf func(format string, args ...any)
}

// New returns a Fencer. generations, leases, and replicaID are required;
// metrics may be nil (the counters are then disabled).
func New(generations GenerationReader, leases LeaseReleaser, replicaID string, metrics Metrics, opts Options) *Fencer {
	maxAttempts := opts.MaxAttempts
	if maxAttempts < 1 {
		maxAttempts = DefaultMaxAttempts
	}
	return &Fencer{
		generations: generations,
		leases:      leases,
		replicaID:   replicaID,
		metrics:     metrics,
		maxAttempts: maxAttempts,
		logf:        opts.Logf,
	}
}

// Fence announces the session's coordination_generation to the pod's
// adapter. It returns relinquished=true (and ErrRelinquished) when the
// coordinator gave up leadership after exhausting its retries, in which
// case the lease has been released and the caller must abort the resume.
// A non-relinquish error is a best-effort fence failure (the generation
// could not be read); the caller may log it and proceed, because the
// coordination lease still guards exclusive ownership.
//
// spec: §10.1 lines 33-37, §11.3 line 209.
func (f *Fencer) Fence(ctx context.Context, adapter *adapterclient.Client, tenantID, sessionID string) (relinquished bool, err error) {
	return f.fence(ctx, adapter, tenantID, sessionID)
}

// fence is the policy loop, parameterized over the FenceClient interface
// so tests can drive the stale/transient/accept paths without a real pod.
func (f *Fencer) fence(ctx context.Context, fc FenceClient, tenantID, sessionID string) (bool, error) {
	gen, err := f.generations.CoordinationGeneration(ctx, tenantID, sessionID)
	if err != nil {
		return false, fmt.Errorf("coordfence: read coordination_generation for %s/%s: %w", tenantID, sessionID, err)
	}
	if gen <= 0 {
		// The adapter requires a positive generation; the first
		// coordination_generation a session row carries is 1. A
		// zero/negative value is a fresh row that has not yet been
		// stamped, so fence at the baseline.
		gen = 1
	}

	for attempt := 1; attempt <= f.maxAttempts; attempt++ {
		if cerr := ctx.Err(); cerr != nil {
			return false, cerr
		}
		res, ferr := fc.CoordinatorFence(ctx, sessionID, gen)
		switch {
		case ferr == nil && res.Accepted:
			// spec: §10.1 lines 33-37 — the pod recorded the generation.
			return false, nil
		case ferr != nil && status.Code(ferr) == codes.FailedPrecondition,
			ferr == nil && !res.Accepted:
			// spec: §10.1 line 165 — generation-stale rejection. Re-read
			// the authoritative generation; if a handoff bump advanced it
			// mid-flight, retry with the new value, otherwise the pod is
			// genuinely ahead and this replica must relinquish.
			f.incStale()
			newGen, rerr := f.generations.CoordinationGeneration(ctx, tenantID, sessionID)
			if rerr == nil && newGen > gen {
				gen = newGen
				f.incRetry()
				f.log("coordfence: %s/%s stale fence, retrying at generation %d", tenantID, sessionID, gen)
				continue
			}
			f.log("coordfence: %s/%s stale fence with no generation advance; relinquishing", tenantID, sessionID)
			return f.relinquish(ctx, tenantID, sessionID)
		default:
			// Transient transport / deadline fault. Retry within budget.
			f.incRetry()
			f.log("coordfence: %s/%s transient fence fault (attempt %d/%d): %v", tenantID, sessionID, attempt, f.maxAttempts, ferr)
		}
	}
	// spec: §11.3 line 209 — attempt budget exhausted; relinquish.
	f.log("coordfence: %s/%s fence attempt budget exhausted; relinquishing", tenantID, sessionID)
	return f.relinquish(ctx, tenantID, sessionID)
}

// relinquish releases the coordination lease and records the §11.3
// relinquish counter, returning ErrRelinquished. A lease-release fault is
// logged but does not change the outcome: the caller must still abort the
// resume because the fence could not be established.
func (f *Fencer) relinquish(ctx context.Context, tenantID, sessionID string) (bool, error) {
	f.incRelinquished()
	if err := f.leases.Release(ctx, tenantID, sessionID, f.replicaID); err != nil {
		f.log("coordfence: release lease for %s/%s after relinquish: %v", tenantID, sessionID, err)
	}
	return true, ErrRelinquished
}

func (f *Fencer) incStale() {
	if f.metrics != nil {
		f.metrics.IncCoordinatorHandoffStale()
	}
}

func (f *Fencer) incRetry() {
	if f.metrics != nil {
		f.metrics.IncCoordinatorFenceRetry()
	}
}

func (f *Fencer) incRelinquished() {
	if f.metrics != nil {
		f.metrics.IncCoordinatorFenceRelinquished()
	}
}

func (f *Fencer) log(format string, args ...any) {
	if f.logf != nil {
		f.logf(format, args...)
	}
}
