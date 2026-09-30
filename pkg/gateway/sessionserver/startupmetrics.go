// SPDX-License-Identifier: MIT

package sessionserver

import (
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// recordStartupPhases observes the §6.3 per-phase
// lenny_session_startup_phase_duration_seconds histogram for the phases
// the caller actually measured. The decomposed create → finalize →
// start lifecycle records each §6.3 phase at the boundary where it runs:
// pod_claim at /create, workspace_materialization / setup_commands /
// credential_assignment at the /finalize prepare barrier, and
// agent_session_start at /start (§5 of the 0007 proposal). A phase whose
// duration is zero was not measured at this boundary, so it is skipped
// rather than recorded as a spurious 0s sample that would skew that
// phase's distribution; this keeps each phase a single observation per
// logical start without changing the histogram structure.
// The first-prompt/TTFT phase is tracked separately (F-6.3.3,
// lenny_session_time_to_first_token_seconds) because it needs runtime
// streaming feedback the start path does not see.
// spec: §6.3.
func (s *Server) recordStartupPhases(match podsession.PoolMatch, t podsession.BindTimings) {
	runtimeClass, ok := isolation.RuntimeClassName(isolation.Profile(match.IsolationProfile))
	if !ok {
		// An unrecognized profile would mislabel the series; skip rather
		// than emit an empty runtime_class.
		return
	}
	if s.observeStartupPhase == nil {
		return
	}
	phases := []struct {
		name string
		d    time.Duration
	}{
		{"pod_claim", t.PodClaim},
		{"workspace_materialization", t.WorkspaceMaterialization},
		{"setup_commands", t.SetupCommands},
		{"credential_assignment", t.CredentialAssignment},
		{"agent_session_start", t.AgentSessionStart},
	}
	for _, p := range phases {
		if p.d <= 0 {
			// A zero duration means the phase did not run at this boundary
			// (for example pod_claim is recorded at /create, not re-attributed
			// at the launch boundary). Skip it so the phase histogram is not
			// polluted with a spurious 0s sample.
			continue
		}
		s.observeStartupPhase(p.name, runtimeClass, p.d.Seconds())
	}
}

// recordStartupDuration observes the §6.3 end-to-end pod-warm
// envelope on lenny_session_startup_duration_seconds exactly once per
// logical start, at the launch boundary, with the full assembled
// timings. Per §6.3 the metric is pod claim through agent
// session ready excluding workspace materialization; setup commands are
// also excluded because they are deployer-controlled (§6.3) and
// the 2s runc / 5s gVisor SLO budgets only the platform phases (claim
// ≤100ms + credential ≤100ms + agent start ≤1.5s/4.5s). The end-to-end
// total is therefore PodClaim + CredentialAssignment + AgentSessionStart.
// The /create boundary records only the pod_claim phase via
// recordStartupPhases and does not emit this end-to-end metric, so a
// single logical start observes lenny_session_startup_duration_seconds
// once rather than once at create and again at launch.
// spec: §6.3.
func (s *Server) recordStartupDuration(match podsession.PoolMatch, t podsession.BindTimings) {
	runtimeClass, ok := isolation.RuntimeClassName(isolation.Profile(match.IsolationProfile))
	if !ok {
		return
	}
	if s.observeStartupDuration == nil {
		return
	}
	total := t.PodClaim + t.CredentialAssignment + t.AgentSessionStart
	s.observeStartupDuration(match.Pool, runtimeClass, match.IsolationProfile, total.Seconds())
}
