// SPDX-License-Identifier: MIT

package leasecontrol

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/sandbox/podscrub"
)

// seamRecycle records the pod recycle operations the adapters call.
type seamRecycle struct {
	retirement  seam.PodRetirement
	recycled    bool
	scrubCount  int
	retirements []string
}

func (s *seamRecycle) IncrementSessionsServed(context.Context, string) (int, bool, error) {
	return 1, true, nil
}

func (s *seamRecycle) IncrementScrubFailureCount(context.Context, string) (int, bool, error) {
	return 1, true, nil
}

func (s *seamRecycle) RecycleCounters(context.Context, string) (int, int, bool, error) {
	return 1, 0, true, nil
}
func (s *seamRecycle) RecordLeak(context.Context, string, string) error        { return nil }
func (s *seamRecycle) RetireOnSessionCount(context.Context, string, int) error { return nil }
func (s *seamRecycle) RecyclePod(context.Context, string, bool, bool) error {
	s.recycled = true
	return nil
}
func (s *seamRecycle) RetirePod(_ context.Context, _ string, r seam.PodRetirement) error {
	s.retirement = r
	return nil
}
func (s *seamRecycle) RecordScrubFailure(_, _, _ string, count int) { s.scrubCount = count }
func (s *seamRecycle) RecordPodRetirement(reason, _, _ string) {
	s.retirements = append(s.retirements, reason)
}

func (s *seamRecycle) InspectForRecycle(context.Context, string) (seam.PodRecyclePolicy, bool, error) {
	return seam.PodRecyclePolicy{
		PreConnect: true, OnScrubFailure: "fail", MaxScrubFailures: 3, MaxSessionsPerPod: 5,
		MaxConcurrentSessions: 2, MaxPodUptimeSeconds: 60, VMRestart: true, HostSchedulable: true,
		PodUptimeSeconds: 30, Pool: "pool-a", RuntimeClass: "gvisor",
	}, true, nil
}

// TestScrubReporterAdaptersMapThePodRecycleOperations checks that the scrub
// report service reads the core's recycle policy with every field, retires
// and recycles a pod through the core with the retirement's reason and
// lifetime, and records the recycle metrics through the core.
//
// spec: GWSEAM-InspectForRecycle, GWSEAM-RecyclePod, GWSEAM-RetirePod, GWSEAM-RecordScrubFailure, GWSEAM-RecordPodRetirement
func TestScrubReporterAdaptersMapThePodRecycleOperations(t *testing.T) {
	rec := &seamRecycle{}
	pol, found, err := recycleInspector{rec}.InspectForRecycle(context.Background(), "pod")
	if err != nil || !found {
		t.Fatal(found, err)
	}
	want := PodRecyclePolicy{
		PreConnect: true, OnScrubFailure: podscrub.OnCleanupFail, MaxScrubFailures: 3, MaxSessionsPerPod: 5,
		MaxConcurrentSessions: 2, MaxPodUptimeSeconds: 60, VMRestart: true, HostSchedulable: true,
		PodUptimeSeconds: 30, Pool: "pool-a", RuntimeClass: "gvisor",
	}
	if pol != want {
		t.Errorf("policy = %+v, want %+v", pol, want)
	}
	d := recycleDriver{rec}
	if err := d.Retire(context.Background(), "pod", true, true, podscrub.ReasonCleanupFailPolicy, PodLifetime{SessionsServed: 4, UptimeSeconds: 9}, "shred"); err != nil {
		t.Fatal(err)
	}
	if rec.retirement != (seam.PodRetirement{Failed: true, ScrubWarning: true, Reason: string(podscrub.ReasonCleanupFailPolicy), SessionsServed: 4, UptimeSeconds: 9, Detail: "shred"}) {
		t.Errorf("retirement = %+v", rec.retirement)
	}
	if err := d.Recycle(context.Background(), "pod", true, false); err != nil || !rec.recycled {
		t.Errorf("recycle: %v %t", err, rec.recycled)
	}
	m := recycleMetrics{rec}
	m.RecordScrubFailure("pod", "pool-a", "gvisor", 2)
	m.IncRetirement(podscrub.ReasonSessionCountLimit, "pool-a", "gvisor")
	if rec.scrubCount != 2 || len(rec.retirements) != 1 || rec.retirements[0] != string(podscrub.ReasonSessionCountLimit) {
		t.Errorf("metrics = %d %v", rec.scrubCount, rec.retirements)
	}
	if _, err := NewScrubReporterForRecycle(rec); err != nil {
		t.Errorf("NewScrubReporterForRecycle: %v", err)
	}
	if _, err := NewScrubReporterForRecycle(nil); err == nil {
		t.Error("a reporter with no pod recycle operations built")
	}
}

// TestBudgetExtensionsMapTheExtensionOutcome checks that the lease control
// service's side of GWSEAM-ExtendForBudget refuses a request with no
// session or tenant as terminal with an error.
//
// spec: GWSEAM-ExtendForBudget
func TestBudgetExtensionsMapTheExtensionOutcome(t *testing.T) {
	ext := (&Service{}).BudgetExtensions()
	for _, tc := range []struct{ tenant, session string }{{"acme", ""}, {"", "s1"}} {
		got, err := ext.ExtendForBudget(context.Background(), context.Background(), tc.tenant, tc.session)
		if err == nil || got != seam.BudgetTerminal {
			t.Errorf("tenant %q session %q = %v %v, want terminal with an error", tc.tenant, tc.session, got, err)
		}
	}
}
