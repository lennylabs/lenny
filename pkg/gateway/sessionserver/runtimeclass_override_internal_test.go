// SPDX-License-Identifier: MIT

package sessionserver

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// TestServerLabelsRuntimeClassThroughTheOverride_spec_16_1 pins that the
// RuntimeClassNameOverrides option reaches every runtime_class label the
// session server emits: the §6.3 startup phase and duration histograms and
// lenny_session_time_to_first_token_seconds. Under an override each names the
// overridden RuntimeClass, as the controller does on the pod, and a profile
// the override leaves unset keeps its default name. Without the option the
// default names apply.
//
// spec: 16.1 (Metrics), 6.3 (Startup Latency Analysis), 17.5 (the per-profile
// RuntimeClass name override), 5.3 (Isolation Profiles).
func TestServerLabelsRuntimeClassThroughTheOverride_spec_16_1(t *testing.T) {
	cases := []struct {
		name      string
		overrides map[isolation.Profile]string
		profile   isolation.Profile
		want      string
	}{
		{name: "sandboxed overridden", overrides: isolation.RuntimeClassOverrides("", "runsc", "kata-qemu"), profile: isolation.ProfileSandboxed, want: "runsc"},
		{name: "microvm overridden", overrides: isolation.RuntimeClassOverrides("", "runsc", "kata-qemu"), profile: isolation.ProfileMicrovm, want: "kata-qemu"},
		{name: "standard left unset", overrides: isolation.RuntimeClassOverrides("", "runsc", "kata-qemu"), profile: isolation.ProfileStandard, want: "runc"},
		{name: "no override", overrides: nil, profile: isolation.ProfileSandboxed, want: "gvisor"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var phaseRC, durationRC, ttftRC []string
			now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
			s := New(memstore.New(), Options{
				RuntimeClassNameOverrides: tc.overrides,
				Clock:                     func() time.Time { return now },
				ObserveStartupPhase: func(_, rc string, _ float64) {
					phaseRC = append(phaseRC, rc)
				},
				ObserveStartupDuration: func(_, rc, _ string, _ float64) {
					durationRC = append(durationRC, rc)
				},
				ObserveTimeToFirstToken: func(_, rc, _ string, _ float64) {
					ttftRC = append(ttftRC, rc)
				},
			})
			match := podsession.PoolMatch{Pool: "pool-a", IsolationProfile: string(tc.profile)}
			timings := podsession.BindTimings{PodClaim: 80 * time.Millisecond, AgentSessionStart: time.Second}
			s.recordStartupPhases(match, timings)
			s.recordStartupDuration(match, timings)
			s.recordTTFTOnce(sessionstore.Session{
				ID: "sess-1", PoolRef: "pool-a", IsolationProfile: tc.profile, CreatedAt: now.Add(-time.Second),
			}, "response")

			for _, got := range [][]string{phaseRC, durationRC, ttftRC} {
				if len(got) == 0 {
					t.Fatalf("an observer saw no sample (phase %v, duration %v, ttft %v)", phaseRC, durationRC, ttftRC)
				}
				for _, rc := range got {
					if rc != tc.want {
						t.Errorf("runtime_class = %q, want %q (phase %v, duration %v, ttft %v)", rc, tc.want, phaseRC, durationRC, ttftRC)
					}
				}
			}
		})
	}
}
