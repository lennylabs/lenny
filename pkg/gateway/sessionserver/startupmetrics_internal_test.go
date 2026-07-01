// SPDX-License-Identifier: MIT

package sessionserver

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
)

type phaseObs struct {
	phase        string
	runtimeClass string
	seconds      float64
}

type totalObs struct {
	pool             string
	runtimeClass     string
	isolationProfile string
	seconds          float64
}

// spec: §6.3 line 372 — recordStartupPhases emits one per-phase
// observation for each phase the caller measured, labeled with the
// runtime class mapped from the pool's isolation profile. A whole-sequence
// Bind measured every phase, so all five §6.3 phases are observed.
func TestRecordStartupPhases_spec_6_3(t *testing.T) {
	timings := podsession.BindTimings{
		PodClaim:                 80 * time.Millisecond,
		WorkspaceMaterialization: 2 * time.Second,
		SetupCommands:            3 * time.Second,
		CredentialAssignment:     40 * time.Millisecond,
		AgentSessionStart:        1200 * time.Millisecond,
	}

	var phases []phaseObs
	s := &Server{
		observeStartupPhase: func(phase, rc string, sec float64) {
			phases = append(phases, phaseObs{phase, rc, sec})
		},
	}

	s.recordStartupPhases(podsession.PoolMatch{Pool: "pool-a", IsolationProfile: "sandboxed"}, timings)

	want := map[string]float64{
		"pod_claim":                 0.08,
		"workspace_materialization": 2,
		"setup_commands":            3,
		"credential_assignment":     0.04,
		"agent_session_start":       1.2,
	}
	if len(phases) != len(want) {
		t.Fatalf("got %d phase observations, want %d: %+v", len(phases), len(want), phases)
	}
	for _, p := range phases {
		if p.runtimeClass != "gvisor" {
			t.Errorf("phase %q runtime_class = %q, want gvisor", p.phase, p.runtimeClass)
		}
		w, ok := want[p.phase]
		if !ok {
			t.Errorf("unexpected phase %q", p.phase)
			continue
		}
		if !approxEq(p.seconds, w) {
			t.Errorf("phase %q seconds = %v, want %v", p.phase, p.seconds, w)
		}
	}
}

// spec: §6.3 line 372 / §5 (0007 proposal) — the decomposed lifecycle
// records each §6.3 phase at the boundary where it runs. /create records
// only pod_claim; recordStartupPhases must not emit a spurious 0s sample
// for the four phases that have not run yet, which would pollute their
// histograms and skew the §6.3 distributions.
func TestRecordStartupPhasesSkipsZeroValuedPhases_spec_6_3(t *testing.T) {
	var phases []phaseObs
	s := &Server{
		observeStartupPhase: func(phase, rc string, sec float64) {
			phases = append(phases, phaseObs{phase, rc, sec})
		},
	}

	// The /create boundary records only the pod_claim phase.
	s.recordStartupPhases(podsession.PoolMatch{Pool: "pool-a", IsolationProfile: "sandboxed"},
		podsession.BindTimings{PodClaim: 80 * time.Millisecond})

	if len(phases) != 1 {
		t.Fatalf("got %d phase observations, want 1 (pod_claim only): %+v", len(phases), phases)
	}
	if phases[0].phase != "pod_claim" {
		t.Errorf("phase = %q, want pod_claim", phases[0].phase)
	}
	if !approxEq(phases[0].seconds, 0.08) {
		t.Errorf("pod_claim seconds = %v, want 0.08", phases[0].seconds)
	}
}

// spec: §6.3 line 372 / §5 (0007 proposal) — the launch boundary records
// the prepare/launch phases it measured but leaves pod_claim zero (it was
// recorded at /create). recordStartupPhases must skip the zero pod_claim so
// the start-time call does not re-emit a 0s pod_claim sample.
func TestRecordStartupPhasesSkipsPodClaimAtLaunch_spec_6_3(t *testing.T) {
	var phases []phaseObs
	s := &Server{
		observeStartupPhase: func(phase, rc string, sec float64) {
			phases = append(phases, phaseObs{phase, rc, sec})
		},
	}

	// The launch boundary leaves PodClaim zero (recorded at /create).
	s.recordStartupPhases(podsession.PoolMatch{Pool: "pool-a", IsolationProfile: "sandboxed"},
		podsession.BindTimings{
			WorkspaceMaterialization: 2 * time.Second,
			SetupCommands:            3 * time.Second,
			CredentialAssignment:     40 * time.Millisecond,
			AgentSessionStart:        1200 * time.Millisecond,
		})

	for _, p := range phases {
		if p.phase == "pod_claim" {
			t.Fatalf("recordStartupPhases emitted a pod_claim sample at the launch boundary (seconds=%v); pod_claim is recorded once at /create", p.seconds)
		}
	}
	want := map[string]float64{
		"workspace_materialization": 2,
		"setup_commands":            3,
		"credential_assignment":     0.04,
		"agent_session_start":       1.2,
	}
	if len(phases) != len(want) {
		t.Fatalf("got %d phase observations, want %d (no pod_claim): %+v", len(phases), len(want), phases)
	}
}

// spec: §6.3 line 348 — recordStartupDuration emits the end-to-end
// pod-warm envelope: total = pod claim + credential assignment + agent
// session start, excluding workspace materialization and deployer setup
// commands.
func TestRecordStartupDuration_spec_6_3(t *testing.T) {
	timings := podsession.BindTimings{
		PodClaim:                 80 * time.Millisecond,
		WorkspaceMaterialization: 2 * time.Second,
		SetupCommands:            3 * time.Second,
		CredentialAssignment:     40 * time.Millisecond,
		AgentSessionStart:        1200 * time.Millisecond,
	}

	var totals []totalObs
	s := &Server{
		observeStartupDuration: func(pool, rc, iso string, sec float64) {
			totals = append(totals, totalObs{pool, rc, iso, sec})
		},
	}

	s.recordStartupDuration(podsession.PoolMatch{Pool: "pool-a", IsolationProfile: "sandboxed"}, timings)

	// total = 0.08 + 0.04 + 1.2 = 1.32s, well within the 5s gVisor budget
	// even though the wall clock from claim to ready was 6.32s.
	if len(totals) != 1 {
		t.Fatalf("got %d total observations, want 1", len(totals))
	}
	tot := totals[0]
	if tot.pool != "pool-a" || tot.runtimeClass != "gvisor" || tot.isolationProfile != "sandboxed" {
		t.Errorf("total labels = %+v, want pool-a/gvisor/sandboxed", tot)
	}
	if !approxEq(tot.seconds, 1.32) {
		t.Errorf("total seconds = %v, want 1.32 (claim+credential+agent, excluding materialization+setup)", tot.seconds)
	}
}

// spec: §5.3 — the runtime class is mapped from the isolation profile;
// standard→runc, sandboxed→gvisor, microvm→kata. Verified through the
// end-to-end duration emitter, which always fires once.
func TestRecordStartupDurationRuntimeClassMapping_spec_5_3(t *testing.T) {
	cases := map[string]string{
		"standard":  "runc",
		"sandboxed": "gvisor",
		"microvm":   "kata",
	}
	for profile, wantRC := range cases {
		var gotRC string
		s := &Server{observeStartupDuration: func(_, rc, _ string, _ float64) { gotRC = rc }}
		s.recordStartupDuration(podsession.PoolMatch{Pool: "p", IsolationProfile: profile},
			podsession.BindTimings{PodClaim: time.Millisecond})
		if gotRC != wantRC {
			t.Errorf("profile %q -> runtime_class %q, want %q", profile, gotRC, wantRC)
		}
	}
}

// An unrecognized isolation profile would mislabel the series with an
// empty runtime_class, so both emitters skip it.
func TestStartupMetricsSkipUnknownProfile_spec_6_3(t *testing.T) {
	called := false
	s := &Server{
		observeStartupPhase:    func(string, string, float64) { called = true },
		observeStartupDuration: func(string, string, string, float64) { called = true },
	}
	match := podsession.PoolMatch{Pool: "p", IsolationProfile: "nonsense"}
	s.recordStartupPhases(match, podsession.BindTimings{PodClaim: time.Second})
	s.recordStartupDuration(match, podsession.BindTimings{PodClaim: time.Second})
	if called {
		t.Error("startup metrics emitted an observation for an unrecognized isolation profile")
	}
}

// Nil callbacks (metrics not wired) must not panic.
func TestStartupMetricsNilCallbacks(t *testing.T) {
	s := &Server{}
	match := podsession.PoolMatch{Pool: "p", IsolationProfile: "standard"}
	s.recordStartupPhases(match, podsession.BindTimings{PodClaim: time.Second})
	s.recordStartupDuration(match, podsession.BindTimings{PodClaim: time.Second})
}

func approxEq(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-6
}
