// SPDX-License-Identifier: MIT

//go:build load_local

// Package vm_restart_recycle_disposition exercises the §5.2 step 7
// recycle boundary under concurrent scrub reports to confirm the
// vm-restart retire decision is race-clean.
//
// The recycle boundary drives podscrub.Decide (a pure disposition
// function) from shared per-pod occupancy state that concurrent scrub
// reports for the same pod mutate (the served-session count and the
// cumulative scrub-failure count). Proposal 0034 (F-5.2.32) inverts the
// prior withhold-and-timeout fail-closed stopgap into an explicit
// retire branch keyed on the vm-restart scrub profile: a clean scrub on
// a vm-restart pool must retire (draining, ReasonVMRestartReprovision)
// rather than reuse the pod, because a reuse would return the pod to
// cross-tenant service without a fresh guest (fail-open). This scenario
// races many reports for the same vm-restart pool and asserts the retire
// disposition holds on every iteration regardless of the concurrently
// advancing session count, and that a standard pool reuses so the branch
// is genuinely keyed on the profile.
//
// TESTING.md §12.7.a regression scenarios.
//
// spec: 5.2 step 7.
package vm_restart_recycle_disposition

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/sandbox/podscrub"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "vm_restart_recycle_disposition"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

// Scenario races the recycle-boundary disposition for a small set of
// pods. Each pod is either a vm-restart pool or a standard pool. Many
// goroutines drive the same pod's recycle boundary concurrently: each
// advances the pod's shared occupancy state (served-session count) under
// its lock, reads that state out under the same lock, and then evaluates
// podscrub.Decide on the snapshot. The lock scopes the shared-state
// mutation; Decide itself is pure and evaluated on a local copy, so a
// data race on the shared counters (a torn read feeding the disposition)
// would surface under -race.
type Scenario struct {
	counters *scenkit.Counters

	mu   sync.Mutex
	pods map[string]*podState
}

// podState is the per-pod shared occupancy state the recycle boundary
// reads back. maxSessionsPerPod: 1 is the boundary case the proposal
// pins (a sequential vm-restart pod's read-back served count equals
// maxSessionsPerPod when the whole-pod scrub reports), where a
// misordered vm-restart branch would emit the counting
// ReasonSessionCountLimit instead of the non-counting reprovision.
type podState struct {
	vmRestart      bool
	sessionsServed int
}

func (s *Scenario) Name() string { return name }

func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 32, Duration: 2 * time.Second}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.pods = make(map[string]*podState, 8)
	return nil
}

func (s *Scenario) Teardown(ctx context.Context) error { return nil }

// Run models one recycle boundary for a pod. Even VUs drive vm-restart
// pods, odd VUs drive standard pools; VUs are partitioned across a small
// pod set so many goroutines contend on the same pod's shared state. The
// disposition must be the profile's disposition on every iteration: a
// vm-restart pool retires with ReasonVMRestartReprovision, a standard
// pool reuses. A vm-restart pod reused (or retired under any other
// reason) is the fail-open this branch closes; a standard pod retired
// under the vm-restart reason is the branch firing on the wrong profile.
//
// spec: 5.2 step 7.
// diagnosis: a failure means concurrent recycle reports raced the vm-restart retire decision.
func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	vmRestart := vu%2 == 0
	podID := fmt.Sprintf("std-%d", vu%3)
	if vmRestart {
		podID = fmt.Sprintf("vmr-%d", vu%3)
	}

	// Advance and read back the shared occupancy state under the lock,
	// modelling RecordPodScrub reading the already-advanced served-session
	// count at the occupancy-zero boundary. The read-back snapshot feeds
	// Decide; concurrent reports for the same pod race here.
	// A vm-restart pool uses maxSessionsPerPod: 1, the boundary case the
	// proposal pins: the read-back served count equals the cap when the
	// scrub reports, so a misordered vm-restart branch would emit the
	// counting ReasonSessionCountLimit rather than the reprovision. A
	// standard pool uses a high cap so a clean scrub genuinely reuses
	// (the session-count retire never fires), keeping it as the reuse
	// control the vm-restart branch is contrasted against.
	maxSessions := 1_000_000
	if vmRestart {
		maxSessions = 1
	}

	s.mu.Lock()
	p, ok := s.pods[podID]
	if !ok {
		p = &podState{vmRestart: vmRestart}
		s.pods[podID] = p
	}
	p.sessionsServed++
	// The read-back models the served-session count for the pod's current
	// occupancy cycle, which a real recycle boundary retires or reuses at
	// each pass, so it stays bounded by the pool cap. Clamp the shared
	// counter so the load-gen iteration count (millions per pod) does not
	// push the standard control's read-back to or past its high cap and fire
	// the legitimate ReasonSessionCountLimit retire, which is a real retire
	// under production logic rather than a scenario invariant break. A
	// vm-restart pod clamps to exactly maxSessionsPerPod (1) so the read-back
	// hits the boundary case the proposal pins (SessionsServed ==
	// MaxSessionsPerPod, where a misordered vm-restart branch would emit the
	// counting ReasonSessionCountLimit). The standard control clamps one
	// below its cap so the session-count branch never fires and a clean scrub
	// genuinely reuses. The increment-then-read race on the shared counter
	// under the lock is the concurrency this scenario exercises; clamping the
	// value fed to Decide does not weaken it.
	readBackCap := maxSessions
	if !vmRestart {
		readBackCap = maxSessions - 1
	}
	sessionsServed := p.sessionsServed
	if sessionsServed > readBackCap {
		sessionsServed = readBackCap
	}
	in := podscrub.Inputs{
		VMRestart:         p.vmRestart,
		Scrub:             podscrub.ScrubSucceeded,
		OnCleanupFailure:  podscrub.OnCleanupWarn,
		SessionsServed:    sessionsServed,
		MaxSessionsPerPod: maxSessions,
		HostSchedulable:   true,
		PreConnect:        vu%4 == 0, // exercise both reuse legs on standard pools.
	}
	s.mu.Unlock()

	d := podscrub.Decide(in)

	if vmRestart {
		if !d.Retire || d.NextPhase != state.Draining || d.Reason != podscrub.ReasonVMRestartReprovision {
			s.counters.Inc("vmrestart_not_reprovision_retire")
			return fmt.Errorf("§5.2 step 7 violated: vm-restart pod %s disposition retire=%v phase=%q reason=%q; want draining vm_restart_reprovision",
				podID, d.Retire, d.NextPhase, d.Reason)
		}
		if d.Reason.CountsOnRetirementTotal() {
			s.counters.Inc("vmrestart_counted_on_retirement_total")
			return fmt.Errorf("§16.1 violated: vm-restart reprovision for %s counted on lenny_gateway_pod_retirement_total", podID)
		}
		s.counters.Inc("vmrestart_retired")
		return nil
	}

	// Standard pool: the clean scrub reuses (reserved or sdk_connecting),
	// never the vm-restart retire. A standard pod retiring with the
	// vm-restart reason would mean the branch fired on the wrong profile.
	if d.Reason == podscrub.ReasonVMRestartReprovision {
		s.counters.Inc("standard_vmrestart_reason")
		return fmt.Errorf("§5.2 step 7 violated: standard pod %s got vm_restart_reprovision reason", podID)
	}
	if d.NextPhase != state.Reserved && d.NextPhase != state.SDKConnecting {
		s.counters.Inc("standard_not_reused")
		return fmt.Errorf("§5.2 recycle disposition violated: standard pod %s phase=%q; want reserved or sdk_connecting", podID, d.NextPhase)
	}
	s.counters.Inc("standard_reused")
	return nil
}

// Assert validates the §5.2 step 7 invariant under load: every
// vm-restart recycle boundary retired with the non-counting reprovision
// reason and no vm-restart pod was reused; every standard pod reused. A
// pre-0034 build (no vm-restart branch, reuse on a clean scrub) would
// drive vmrestart_not_reprovision_retire > 0 on the first iteration.
func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	if v := s.counters.Get("vmrestart_not_reprovision_retire"); v > 0 {
		return fmt.Errorf("§5.2 step 7 violated: %d vm-restart boundaries did not retire-and-reprovision (reused or wrong reason)", v)
	}
	if v := s.counters.Get("vmrestart_counted_on_retirement_total"); v > 0 {
		return fmt.Errorf("§16.1 violated: %d vm-restart reprovisions counted on lenny_gateway_pod_retirement_total", v)
	}
	if v := s.counters.Get("standard_vmrestart_reason"); v > 0 {
		return fmt.Errorf("§5.2 step 7 violated: %d standard boundaries got the vm-restart reason", v)
	}
	if v := s.counters.Get("standard_not_reused"); v > 0 {
		return fmt.Errorf("§5.2 recycle disposition violated: %d standard boundaries did not reuse", v)
	}
	if s.counters.Get("vmrestart_retired") == 0 || s.counters.Get("standard_reused") == 0 {
		return fmt.Errorf("scenario must exercise both the vm-restart retire and the standard reuse paths")
	}
	return nil
}
