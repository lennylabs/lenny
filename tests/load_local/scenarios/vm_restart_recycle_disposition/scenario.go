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
// The scenario also covers the runtime-not-live retire. A standard pool
// whose adapter reports that its runtime cannot serve the next session
// retires with the non-counting runtime_not_live reason, and a vm-restart
// pool whose runtime is not live still retires with the reprovision
// reason, because the vm-restart branch runs before the runtime-not-live
// branch.
//
// TESTING.md §12.7.a regression scenarios.
//
// spec: 5.2 (Kata/microvm scrub variant step 7), 5.2 (Pod retirement policy).
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

// podClass is the recycle-boundary class a VU drives. Each class pairs a
// pool profile with the runtime-liveness sample the adapter's ReportPodScrub
// carries, so the scenario covers the order between the vm-restart retire
// branch, the runtime-not-live retire branch, and the reuse it replaces.
type podClass int

const (
	// classVMRestart is a vm-restart pool whose runtime is live: it retires
	// with the non-counting reprovision reason.
	classVMRestart podClass = iota
	// classStandard is the reuse control: a standard pool whose runtime is
	// live reuses the pod.
	classStandard
	// classRuntimeNotLive is a standard pool whose adapter reported that its
	// runtime cannot serve the next session: it retires with the
	// non-counting runtime_not_live reason.
	classRuntimeNotLive
	// classVMRestartRuntimeNotLive is a vm-restart pool whose runtime is not
	// live: the vm-restart branch runs first, so it retires with the
	// reprovision reason rather than runtime_not_live.
	classVMRestartRuntimeNotLive
	numPodClasses
)

// vmRestart reports whether the class is a vm-restart pool.
func (c podClass) vmRestart() bool {
	return c == classVMRestart || c == classVMRestartRuntimeNotLive
}

// runtimeNotLive reports whether the class's ReportPodScrub states that the
// runtime cannot serve the next session.
func (c podClass) runtimeNotLive() bool {
	return c == classRuntimeNotLive || c == classVMRestartRuntimeNotLive
}

// prefix names the class's pods, so VUs of one class contend on the same
// pod's shared state and never on another class's.
func (c podClass) prefix() string {
	return [...]string{"vmr", "std", "rnl", "vmr-rnl"}[c]
}

// wantRetireReason is the retire reason the class must produce on every
// iteration, or the empty reason for the reuse control.
func (c podClass) wantRetireReason() podscrub.RetireReason {
	switch c {
	case classVMRestart, classVMRestartRuntimeNotLive:
		return podscrub.ReasonVMRestartReprovision
	case classRuntimeNotLive:
		return podscrub.ReasonRuntimeNotLive
	case classStandard:
		return ""
	default:
		panic(fmt.Sprintf("unknown pod class %d", c))
	}
}

// Scenario races the recycle-boundary disposition for a small set of
// pods per class. Many goroutines drive the same pod's recycle boundary
// concurrently: each advances the pod's shared occupancy state
// (served-session count) under its lock, reads that state out under the
// same lock, and then evaluates podscrub.Decide on the snapshot. The lock
// scopes the shared-state mutation; Decide itself is pure and evaluated on a
// local copy, so a data race on the shared counters (a torn read feeding
// the disposition) would surface under -race.
type Scenario struct {
	counters *scenkit.Counters

	// mu guards pods and every podState it holds.
	mu   sync.Mutex
	pods map[string]*podState
}

// podState is the per-pod shared occupancy state the recycle boundary
// reads back. maxSessionsPerPod: 1 is the boundary case for a vm-restart
// pool (a sequential vm-restart pod's read-back served count equals
// maxSessionsPerPod when the whole-pod scrub reports), where a misordered
// vm-restart branch would emit the counting ReasonSessionCountLimit instead
// of the non-counting reprovision.
type podState struct {
	class          podClass
	sessionsServed int
}

func (s *Scenario) Name() string { return name }

func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 32, Duration: 2 * time.Second}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.pods = make(map[string]*podState, 16)
	return nil
}

func (s *Scenario) Teardown(ctx context.Context) error { return nil }

// Run models one recycle boundary for a pod. VUs are partitioned across the
// classes by vu modulo the class count, and within a class across a small
// pod set, so many goroutines contend on the same pod's shared state. The
// disposition must be the class's disposition on every iteration: a
// vm-restart pool retires with ReasonVMRestartReprovision whether or not
// its runtime is live, a standard pool whose runtime is not live retires
// with ReasonRuntimeNotLive, and a standard pool whose runtime is live
// reuses. No retire in this scenario counts on
// lenny_gateway_pod_retirement_total.
//
// spec: 5.2 (Kata/microvm scrub variant step 7), 5.2 (Pod retirement policy).
// diagnosis: concurrent recycle reports raced the boundary-retire decision, or the order between the retire branches changed.
func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	class := podClass(vu % int(numPodClasses))
	podID := fmt.Sprintf("%s-%d", class.prefix(), (vu/int(numPodClasses))%3)
	// Alternate preConnect within a class so the reuse control exercises
	// both reuse legs (reserved and sdk_connecting).
	preConnect := (vu/int(numPodClasses))%2 == 0

	d := podscrub.Decide(s.readBack(podID, class, preConnect))

	if want := class.wantRetireReason(); want != "" {
		return s.checkRetire(podID, class, want, d)
	}
	return s.checkReuse(podID, d)
}

// readBack advances and reads back the pod's shared occupancy state under
// the lock, modelling RecordPodScrub reading the already-advanced
// served-session count at the occupancy-zero boundary. Concurrent reports
// for the same pod race here.
//
// A vm-restart pool uses maxSessionsPerPod: 1, the boundary case where a
// misordered vm-restart branch would emit the counting
// ReasonSessionCountLimit rather than the reprovision. A standard pool uses
// a high cap so the session-count retire never fires: the reuse control
// genuinely reuses, and the runtime-not-live class reaches its own branch
// rather than the session-count branch that precedes it.
//
// The read-back is clamped because it models the served-session count for
// the pod's current occupancy cycle, which a real recycle boundary retires
// or reuses at each pass. Without the clamp the load-gen iteration count
// (millions per pod) would push a standard pool's read-back to its cap and
// fire the legitimate ReasonSessionCountLimit retire. A vm-restart pod
// clamps to exactly its cap of 1; a standard pod clamps one below its cap.
// The increment-then-read race under the lock is the concurrency this
// scenario exercises, and clamping the value fed to Decide does not weaken
// it.
func (s *Scenario) readBack(podID string, class podClass, preConnect bool) podscrub.Inputs {
	maxSessions := 1_000_000
	readBackCap := maxSessions - 1
	if class.vmRestart() {
		maxSessions = 1
		readBackCap = 1
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pods[podID]
	if !ok {
		p = &podState{class: class}
		s.pods[podID] = p
	}
	p.sessionsServed++
	sessionsServed := min(p.sessionsServed, readBackCap)
	return podscrub.Inputs{
		VMRestart:         p.class.vmRestart(),
		RuntimeNotLive:    p.class.runtimeNotLive(),
		Scrub:             podscrub.ScrubSucceeded,
		OnCleanupFailure:  podscrub.OnCleanupWarn,
		SessionsServed:    sessionsServed,
		MaxSessionsPerPod: maxSessions,
		HostSchedulable:   true,
		PreConnect:        preConnect,
	}
}

// checkRetire asserts that a retiring class drained the pod with its
// expected reason, and that the reason does not count on
// lenny_gateway_pod_retirement_total. A vm-restart pod reused, or retired
// under runtime_not_live, means the vm-restart branch no longer runs first;
// a runtime-not-live pod reused is the fail-open the runtime-not-live
// branch closes.
func (s *Scenario) checkRetire(podID string, class podClass, want podscrub.RetireReason, d podscrub.Disposition) error {
	if !d.Retire || d.NextPhase != state.Draining || d.Reason != want {
		s.counters.Inc(class.prefix() + "_wrong_retire")
		return fmt.Errorf("§5.2 recycle disposition violated: %s pod %s disposition retire=%v phase=%q reason=%q; want draining %s",
			class.prefix(), podID, d.Retire, d.NextPhase, d.Reason, want)
	}
	if d.Reason.CountsOnRetirementTotal() {
		s.counters.Inc(class.prefix() + "_counted_on_retirement_total")
		return fmt.Errorf("§16.1 violated: %s retire for %s counted on lenny_gateway_pod_retirement_total", d.Reason, podID)
	}
	s.counters.Inc(class.prefix() + "_retired")
	return nil
}

// checkReuse asserts that the reuse control returned the pod to service
// (reserved or sdk_connecting) and never took a retire branch. A standard
// pod retiring with the vm-restart or runtime-not-live reason would mean a
// branch fired on the wrong input.
func (s *Scenario) checkReuse(podID string, d podscrub.Disposition) error {
	if d.Reason == podscrub.ReasonVMRestartReprovision || d.Reason == podscrub.ReasonRuntimeNotLive {
		s.counters.Inc("standard_wrong_retire_reason")
		return fmt.Errorf("§5.2 recycle disposition violated: standard pod %s got reason %q", podID, d.Reason)
	}
	if d.NextPhase != state.Reserved && d.NextPhase != state.SDKConnecting {
		s.counters.Inc("standard_not_reused")
		return fmt.Errorf("§5.2 recycle disposition violated: standard pod %s phase=%q; want reserved or sdk_connecting", podID, d.NextPhase)
	}
	s.counters.Inc("standard_reused")
	return nil
}

// Assert validates the §5.2 invariants under load: every vm-restart
// boundary retired with the non-counting reprovision reason whether or not
// its runtime was live, every runtime-not-live boundary on a standard pool
// retired with the non-counting runtime_not_live reason, and every standard
// boundary with a live runtime reused. It also requires that every class
// was exercised.
func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	for c := podClass(0); c < numPodClasses; c++ {
		if c.wantRetireReason() == "" {
			continue
		}
		if v := s.counters.Get(c.prefix() + "_wrong_retire"); v > 0 {
			return fmt.Errorf("§5.2 recycle disposition violated: %d %s boundaries did not retire with %s", v, c.prefix(), c.wantRetireReason())
		}
		if v := s.counters.Get(c.prefix() + "_counted_on_retirement_total"); v > 0 {
			return fmt.Errorf("§16.1 violated: %d %s retires counted on lenny_gateway_pod_retirement_total", v, c.prefix())
		}
		if s.counters.Get(c.prefix()+"_retired") == 0 {
			return fmt.Errorf("scenario must exercise the %s retire path", c.prefix())
		}
	}
	if v := s.counters.Get("standard_wrong_retire_reason"); v > 0 {
		return fmt.Errorf("§5.2 recycle disposition violated: %d standard boundaries got a retire reason reserved for another class", v)
	}
	if v := s.counters.Get("standard_not_reused"); v > 0 {
		return fmt.Errorf("§5.2 recycle disposition violated: %d standard boundaries did not reuse", v)
	}
	if s.counters.Get("standard_reused") == 0 {
		return fmt.Errorf("scenario must exercise the standard reuse path")
	}
	return nil
}
