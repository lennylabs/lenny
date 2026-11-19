// SPDX-License-Identifier: MIT

package recycle_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/core/session/recycle"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/sandbox/podscrub"
)

// recyclePartsFake records what each recycle seam receives.
type recyclePartsFake struct {
	calls       []string
	lifetime    recycle.PodLifetime
	reason      podscrub.RetireReason
	failed      bool
	scrubTotal  int
	scrubGauge  int
	retirements []podscrub.RetireReason
}

func (f *recyclePartsFake) IncrementSessionsServed(context.Context, string) (int, bool, error) {
	f.calls = append(f.calls, "served")
	return 2, true, nil
}

func (f *recyclePartsFake) IncrementScrubFailureCount(context.Context, string) (int, bool, error) {
	f.calls = append(f.calls, "scrub")
	return 1, true, nil
}

func (f *recyclePartsFake) RecycleCounters(context.Context, string) (int, int, bool, error) {
	f.calls = append(f.calls, "counters")
	return 2, 1, true, nil
}

func (f *recyclePartsFake) RecordLeak(context.Context, string, string) error {
	f.calls = append(f.calls, "leak")
	return nil
}

func (f *recyclePartsFake) RetireOnSessionCount(context.Context, string, int) error {
	f.calls = append(f.calls, "count")
	return nil
}

func (f *recyclePartsFake) InspectForRecycle(context.Context, string) (seam.PodRecyclePolicy, bool, error) {
	f.calls = append(f.calls, "inspect")
	return seam.PodRecyclePolicy{Pool: "pool-a", OnScrubFailure: "warn"}, true, nil
}

func (f *recyclePartsFake) Recycle(context.Context, string, bool, bool) error {
	f.calls = append(f.calls, "recycle")
	return nil
}

func (f *recyclePartsFake) Retire(_ context.Context, _ string, failed, _ bool, reason podscrub.RetireReason, lifetime recycle.PodLifetime, _ string) error {
	f.calls = append(f.calls, "retire")
	f.failed, f.reason, f.lifetime = failed, reason, lifetime
	return nil
}

func (f *recyclePartsFake) IncScrubFailureTotal(string, string) { f.scrubTotal++ }

func (f *recyclePartsFake) SetScrubFailureCount(_, _, _ string, count int) { f.scrubGauge = count }

func (f *recyclePartsFake) IncRetirement(r podscrub.RetireReason, _, _ string) {
	f.retirements = append(f.retirements, r)
}

// TestPodRecycleServesEachOperationThroughItsSeam checks that every pod
// recycle operation reaches the recycle seam that holds it, that a
// retirement carries its reason and lifetime, and that the recycle metrics
// count a failed scrub in both series and a retirement by reason.
//
// spec: GWSEAM-IncrementSessionsServed, GWSEAM-IncrementScrubFailureCount, GWSEAM-RecycleCounters, GWSEAM-RecordLeak, GWSEAM-RetireOnSessionCount, GWSEAM-InspectForRecycle, GWSEAM-RecyclePod, GWSEAM-RetirePod, GWSEAM-RecordScrubFailure, GWSEAM-RecordPodRetirement
func TestPodRecycleServesEachOperationThroughItsSeam(t *testing.T) {
	f := &recyclePartsFake{}
	p, err := recycle.NewPodRecycle(recycle.PodRecycleParts{Counters: f, Ledger: f, SessionRetirer: f, Inspector: f, Driver: f, Metrics: f})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if n, found, err := p.IncrementSessionsServed(ctx, "pod"); n != 2 || !found || err != nil {
		t.Errorf("served = %d %t %v", n, found, err)
	}
	_, _, _ = p.IncrementScrubFailureCount(ctx, "pod")
	if s, fl, found, _ := p.RecycleCounters(ctx, "pod"); s != 2 || fl != 1 || !found {
		t.Errorf("counters = %d %d %t", s, fl, found)
	}
	_ = p.RecordLeak(ctx, "pod", "slot")
	_ = p.RetireOnSessionCount(ctx, "pod", 3)
	if pol, found, _ := p.InspectForRecycle(ctx, "pod"); !found || pol.Pool != "pool-a" {
		t.Errorf("policy = %+v %t", pol, found)
	}
	_ = p.RecyclePod(ctx, "pod", true, false)
	_ = p.RetirePod(ctx, "pod", seam.PodRetirement{Failed: true, Reason: string(podscrub.ReasonCleanupFailPolicy), SessionsServed: 4, UptimeSeconds: 60})
	if !f.failed || f.reason != podscrub.ReasonCleanupFailPolicy || f.lifetime != (recycle.PodLifetime{SessionsServed: 4, UptimeSeconds: 60}) {
		t.Errorf("retire = failed %t reason %q lifetime %+v", f.failed, f.reason, f.lifetime)
	}
	p.RecordScrubFailure("pod", "pool-a", "gvisor", 3)
	p.RecordPodRetirement(string(podscrub.ReasonSessionCountLimit), "pool-a", "gvisor")
	if f.scrubTotal != 1 || f.scrubGauge != 3 || len(f.retirements) != 1 || f.retirements[0] != podscrub.ReasonSessionCountLimit {
		t.Errorf("metrics = total %d gauge %d retirements %v", f.scrubTotal, f.scrubGauge, f.retirements)
	}
	if got := len(f.calls); got != 8 {
		t.Errorf("calls = %v, want one per seam operation", f.calls)
	}
	// A pod recycle with no metrics records nothing and does not fail.
	bare, err := recycle.NewPodRecycle(recycle.PodRecycleParts{Counters: f, Ledger: f, SessionRetirer: f, Inspector: f, Driver: f})
	if err != nil {
		t.Fatal(err)
	}
	bare.RecordScrubFailure("pod", "pool", "rc", 1)
	bare.RecordPodRetirement("x", "pool", "rc")
}

// TestNewPodRecycleRefusesAMissingRequiredSeam checks that the pod recycle
// operations refuse parts without the recycle counters, the drain ledger,
// the per-release retirer, the pod inspector, or the claim disposition, so a
// missing seam fails at startup rather than on the first scrub report.
//
// spec: GWSEAM-RecyclePod
func TestNewPodRecycleRefusesAMissingRequiredSeam(t *testing.T) {
	f := &recyclePartsFake{}
	full := recycle.PodRecycleParts{Counters: f, Ledger: f, SessionRetirer: f, Inspector: f, Driver: f}
	for name, drop := range map[string]func(*recycle.PodRecycleParts){
		"counters":  func(p *recycle.PodRecycleParts) { p.Counters = nil },
		"ledger":    func(p *recycle.PodRecycleParts) { p.Ledger = nil },
		"retirer":   func(p *recycle.PodRecycleParts) { p.SessionRetirer = nil },
		"inspector": func(p *recycle.PodRecycleParts) { p.Inspector = nil },
		"driver":    func(p *recycle.PodRecycleParts) { p.Driver = nil },
	} {
		parts := full
		drop(&parts)
		if _, err := recycle.NewPodRecycle(parts); err == nil {
			t.Errorf("parts without the %s were accepted", name)
		}
	}
}
