// SPDX-License-Identifier: MIT

package leasecontrol

import (
	"context"

	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/sandbox/podscrub"
)

// NewScrubReporterForRecycle builds the ScrubReporter over the session
// core's pod recycle operations: the recycle counters, the drain ledger,
// the per-release retirer, the pod inspector, the claim disposition, and
// the recycle metrics all reach the core through rec.
func NewScrubReporterForRecycle(rec seam.PodRecycle) (*ScrubReporter, error) {
	if rec == nil {
		return NewScrubReporter(ScrubReporterOptions{})
	}
	return NewScrubReporter(ScrubReporterOptions{
		Counters:       rec,
		Ledger:         rec,
		SessionRetirer: rec,
		Inspector:      recycleInspector{rec},
		Driver:         recycleDriver{rec},
		Metrics:        recycleMetrics{rec},
	})
}

// recycleInspector maps the core's recycle policy onto the disposition's.
type recycleInspector struct{ rec seam.PodRecycle }

// InspectForRecycle resolves a pod's recycle policy through the core.
//
// spec: GWSEAM-InspectForRecycle
func (i recycleInspector) InspectForRecycle(ctx context.Context, podID string) (PodRecyclePolicy, bool, error) {
	p, found, err := i.rec.InspectForRecycle(ctx, podID)
	if err != nil || !found {
		return PodRecyclePolicy{}, found, err
	}
	return PodRecyclePolicy{
		PreConnect:            p.PreConnect,
		OnScrubFailure:        podscrub.CleanupFailurePolicy(p.OnScrubFailure),
		MaxScrubFailures:      p.MaxScrubFailures,
		MaxSessionsPerPod:     p.MaxSessionsPerPod,
		MaxConcurrentSessions: p.MaxConcurrentSessions,
		MaxPodUptimeSeconds:   p.MaxPodUptimeSeconds,
		VMRestart:             p.VMRestart,
		HostSchedulable:       p.HostSchedulable,
		PodUptimeSeconds:      p.PodUptimeSeconds,
		Pool:                  p.Pool,
		RuntimeClass:          p.RuntimeClass,
	}, true, nil
}

// recycleDriver drives the claim disposition through the core.
type recycleDriver struct{ rec seam.PodRecycle }

// Recycle returns the pod to its pool through the core.
//
// spec: GWSEAM-RecyclePod
func (d recycleDriver) Recycle(ctx context.Context, podID string, preConnect, scrubWarning bool) error {
	return d.rec.RecyclePod(ctx, podID, preConnect, scrubWarning)
}

// Retire retires the pod through the core.
//
// spec: GWSEAM-RetirePod
func (d recycleDriver) Retire(ctx context.Context, podID string, failed, scrubWarning bool, reason podscrub.RetireReason, lifetime PodLifetime, detail string) error {
	return d.rec.RetirePod(ctx, podID, seam.PodRetirement{
		Failed:         failed,
		ScrubWarning:   scrubWarning,
		Reason:         string(reason),
		SessionsServed: lifetime.SessionsServed,
		UptimeSeconds:  lifetime.UptimeSeconds,
		Detail:         detail,
	})
}

// recycleMetrics records the recycle metrics through the core, which owns
// them because its own per-release retirement counts retirements too.
type recycleMetrics struct{ rec seam.PodRecycle }

// RecordScrubFailure counts the failed scrub in both scrub-failure series.
//
// spec: GWSEAM-RecordScrubFailure
func (m recycleMetrics) RecordScrubFailure(podID, pool, runtimeClass string, count int) {
	m.rec.RecordScrubFailure(podID, pool, runtimeClass, count)
}

// IncRetirement counts a gateway-decided retirement.
//
// spec: GWSEAM-RecordPodRetirement
func (m recycleMetrics) IncRetirement(reason podscrub.RetireReason, pool, runtimeClass string) {
	m.rec.RecordPodRetirement(string(reason), pool, runtimeClass)
}
