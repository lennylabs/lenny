// SPDX-License-Identifier: MIT

package sessionserver

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/podsession"
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

// spec: §6.3 lines 348, 372 — recordStartupMetrics emits one per-phase
// observation per instrumented hot-path phase and one end-to-end
// observation whose value is the platform-controlled envelope (pod
// claim + credential assignment + agent session start), excluding
// workspace materialization and deployer setup commands.
func TestRecordStartupMetrics_spec_6_3(t *testing.T) {
	timings := podsession.BindTimings{
		PodClaim:                 80 * time.Millisecond,
		WorkspaceMaterialization: 2 * time.Second,
		SetupCommands:            3 * time.Second,
		CredentialAssignment:     40 * time.Millisecond,
		AgentSessionStart:        1200 * time.Millisecond,
	}

	var phases []phaseObs
	var totals []totalObs
	s := &Server{
		observeStartupPhase: func(phase, rc string, sec float64) {
			phases = append(phases, phaseObs{phase, rc, sec})
		},
		observeStartupDuration: func(pool, rc, iso string, sec float64) {
			totals = append(totals, totalObs{pool, rc, iso, sec})
		},
	}

	s.recordStartupMetrics(podsession.PoolMatch{Pool: "pool-a", IsolationProfile: "sandboxed"}, timings)

	// All five §6.3-line-372 phases are observed, labeled with the
	// runtime class mapped from the pool's isolation profile.
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

	// The end-to-end SLO metric excludes materialization (2s) and setup
	// (3s): total = 0.08 + 0.04 + 1.2 = 1.32s, well within the 5s gVisor
	// budget even though the wall clock from claim to ready was 6.32s.
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
// standard→runc, sandboxed→gvisor, microvm→kata.
func TestRecordStartupMetricsRuntimeClassMapping_spec_5_3(t *testing.T) {
	cases := map[string]string{
		"standard":  "runc",
		"sandboxed": "gvisor",
		"microvm":   "kata",
	}
	for profile, wantRC := range cases {
		var gotRC string
		s := &Server{observeStartupDuration: func(_, rc, _ string, _ float64) { gotRC = rc }}
		s.recordStartupMetrics(podsession.PoolMatch{Pool: "p", IsolationProfile: profile}, podsession.BindTimings{})
		if gotRC != wantRC {
			t.Errorf("profile %q -> runtime_class %q, want %q", profile, gotRC, wantRC)
		}
	}
}

// An unrecognized isolation profile would mislabel the series with an
// empty runtime_class, so recordStartupMetrics emits nothing.
func TestRecordStartupMetricsSkipsUnknownProfile_spec_6_3(t *testing.T) {
	called := false
	s := &Server{
		observeStartupPhase:    func(string, string, float64) { called = true },
		observeStartupDuration: func(string, string, string, float64) { called = true },
	}
	s.recordStartupMetrics(podsession.PoolMatch{Pool: "p", IsolationProfile: "nonsense"}, podsession.BindTimings{})
	if called {
		t.Error("recordStartupMetrics emitted an observation for an unrecognized isolation profile")
	}
}

// Nil callbacks (metrics not wired) must not panic.
func TestRecordStartupMetricsNilCallbacks(t *testing.T) {
	s := &Server{}
	s.recordStartupMetrics(podsession.PoolMatch{Pool: "p", IsolationProfile: "standard"}, podsession.BindTimings{})
}

func approxEq(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-6
}
