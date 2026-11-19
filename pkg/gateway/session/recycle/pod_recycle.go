// SPDX-License-Identifier: MIT

package recycle

import (
	"context"

	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/sandbox/podscrub"
)

// RecycleCounterStore is the agent_pod_state recycle-counter surface: the
// sessions-served and scrub-failure counters of a pod.
type RecycleCounterStore interface {
	IncrementSessionsServed(ctx context.Context, podID string) (count int, found bool, err error)
	IncrementScrubFailureCount(ctx context.Context, podID string) (count int, found bool, err error)
	RecycleCounters(ctx context.Context, podID string) (sessionsServed, scrubFailureCount int, found bool, err error)
}

// DrainLedger records an adapter-reported leaked slot of a pod against the
// §5.2 unhealthy threshold and drains the pod when it crosses it.
type DrainLedger interface {
	RecordLeak(ctx context.Context, podID, slotID string) error
}

// SessionCountRetirer retires a concurrent pod on the release that brings
// its sessions-served count to maxSessionsPerPod.
type SessionCountRetirer interface {
	RetireOnSessionCount(ctx context.Context, podID string, count int) error
}

// PodInspector resolves a pod's recycle policy and the facts its recycle
// decision reads, or found false when the pod or its claim is gone.
type PodInspector interface {
	InspectForRecycle(ctx context.Context, podID string) (policy seam.PodRecyclePolicy, found bool, err error)
}

// PodLifetime is a pod's lifetime usage that a retirement records: the
// sessions it served and its wall-clock uptime.
type PodLifetime struct {
	SessionsServed int
	UptimeSeconds  int64
}

// ClaimDispositionDriver applies a recycle disposition to a pod's claim:
// the reuse of a pod after a successful scrub, or its retirement.
type ClaimDispositionDriver interface {
	Recycle(ctx context.Context, podID string, preConnect, scrubWarning bool) error
	Retire(ctx context.Context, podID string, failed, scrubWarning bool, reason podscrub.RetireReason, lifetime PodLifetime, detail string) error
}

// RetirementMetrics records the §16.1 scrub-failure and pod-retirement
// metrics with the typed retirement reason.
type RetirementMetrics interface {
	IncScrubFailureTotal(pool, runtimeClass string)
	SetScrubFailureCount(podID, pool, runtimeClass string, count int)
	IncRetirement(reason podscrub.RetireReason, pool, runtimeClass string)
}

// PodRecycleParts are the recycle seams the pod recycle operations compose.
// Counters, Ledger, SessionRetirer, Inspector, and Driver are required;
// a nil Metrics records nothing.
type PodRecycleParts struct {
	Counters       RecycleCounterStore
	Ledger         DrainLedger
	SessionRetirer SessionCountRetirer
	Inspector      PodInspector
	Driver         ClaimDispositionDriver
	Metrics        RetirementMetrics
}

// PodRecycle is the session core's side of the pod recycle operations the
// platform tools seam's scrub report service calls.
type PodRecycle struct {
	parts PodRecycleParts
}

// NewPodRecycle returns the pod recycle operations over parts.
func NewPodRecycle(parts PodRecycleParts) *PodRecycle {
	return &PodRecycle{parts: parts}
}

var _ seam.PodRecycle = (*PodRecycle)(nil)

// IncrementSessionsServed increments a pod's sessions-served counter.
//
// spec: GWSEAM-IncrementSessionsServed
func (p *PodRecycle) IncrementSessionsServed(ctx context.Context, podID string) (int, bool, error) {
	return p.parts.Counters.IncrementSessionsServed(ctx, podID)
}

// IncrementScrubFailureCount increments a pod's scrub-failure counter.
//
// spec: GWSEAM-IncrementScrubFailureCount
func (p *PodRecycle) IncrementScrubFailureCount(ctx context.Context, podID string) (int, bool, error) {
	return p.parts.Counters.IncrementScrubFailureCount(ctx, podID)
}

// RecycleCounters reads a pod's recycle counters.
//
// spec: GWSEAM-RecycleCounters
func (p *PodRecycle) RecycleCounters(ctx context.Context, podID string) (int, int, bool, error) {
	return p.parts.Counters.RecycleCounters(ctx, podID)
}

// RecordLeak records a leaked slot of a pod.
//
// spec: GWSEAM-RecordLeak
func (p *PodRecycle) RecordLeak(ctx context.Context, podID, slotID string) error {
	return p.parts.Ledger.RecordLeak(ctx, podID, slotID)
}

// RetireOnSessionCount retires a pod that reached its session count limit.
//
// spec: GWSEAM-RetireOnSessionCount
func (p *PodRecycle) RetireOnSessionCount(ctx context.Context, podID string, count int) error {
	return p.parts.SessionRetirer.RetireOnSessionCount(ctx, podID, count)
}

// InspectForRecycle resolves a pod's recycle policy.
//
// spec: GWSEAM-InspectForRecycle
func (p *PodRecycle) InspectForRecycle(ctx context.Context, podID string) (seam.PodRecyclePolicy, bool, error) {
	return p.parts.Inspector.InspectForRecycle(ctx, podID)
}

// RecyclePod returns a pod to its pool after a successful scrub.
//
// spec: GWSEAM-RecyclePod
func (p *PodRecycle) RecyclePod(ctx context.Context, podID string, preConnect, scrubWarning bool) error {
	return p.parts.Driver.Recycle(ctx, podID, preConnect, scrubWarning)
}

// RetirePod retires a pod with its reason, lifetime, and detail.
//
// spec: GWSEAM-RetirePod
func (p *PodRecycle) RetirePod(ctx context.Context, podID string, r seam.PodRetirement) error {
	return p.parts.Driver.Retire(ctx, podID, r.Failed, r.ScrubWarning, podscrub.RetireReason(r.Reason),
		PodLifetime{SessionsServed: r.SessionsServed, UptimeSeconds: r.UptimeSeconds}, r.Detail)
}

// RecordScrubFailure counts a failed whole-pod scrub in both scrub-failure
// series.
//
// spec: GWSEAM-RecordScrubFailure
func (p *PodRecycle) RecordScrubFailure(podID, pool, runtimeClass string, count int) {
	if p.parts.Metrics == nil {
		return
	}
	p.parts.Metrics.IncScrubFailureTotal(pool, runtimeClass)
	p.parts.Metrics.SetScrubFailureCount(podID, pool, runtimeClass, count)
}

// RecordPodRetirement counts a gateway-decided pod retirement by reason.
//
// spec: GWSEAM-RecordPodRetirement
func (p *PodRecycle) RecordPodRetirement(reason, pool, runtimeClass string) {
	if p.parts.Metrics == nil {
		return
	}
	p.parts.Metrics.IncRetirement(podscrub.RetireReason(reason), pool, runtimeClass)
}
