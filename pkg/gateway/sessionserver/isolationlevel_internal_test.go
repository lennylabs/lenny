// SPDX-License-Identifier: MIT

package sessionserver

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// spec: §7.1 lines 69-73 — sessionIsolationLevel maps the resolved §5.2
// pool's execution mode and scrub policy to the client-visible fields.
func TestIsolationLevelForPool_spec_7_1(t *testing.T) {
	cases := []struct {
		name    string
		match   podsession.PoolMatch
		req     isolation.Profile
		wantExe string
		wantPro string
		wantRe  bool
		wantWar bool
		wantScr string
	}{
		{
			name:    "session mode: no reuse, no scrub, no warning",
			match:   podsession.PoolMatch{ExecutionMode: "session", IsolationProfile: "sandboxed"},
			req:     isolation.ProfileSandboxed,
			wantExe: "session", wantPro: "sandboxed", wantRe: false, wantWar: false, wantScr: "",
		},
		{
			name:    "empty execution mode defaults to session",
			match:   podsession.PoolMatch{IsolationProfile: "sandboxed"},
			req:     isolation.ProfileSandboxed,
			wantExe: "session", wantPro: "sandboxed", wantRe: false, wantWar: false, wantScr: "",
		},
		{
			name:    "task standard: best-effort scrub",
			match:   podsession.PoolMatch{ExecutionMode: "task", IsolationProfile: "sandboxed"},
			req:     isolation.ProfileSandboxed,
			wantExe: "task", wantPro: "sandboxed", wantRe: true, wantWar: true, wantScr: "best-effort",
		},
		{
			name:    "task microvm cross-tenant restart (explicit): vm-restart",
			match:   podsession.PoolMatch{ExecutionMode: "task", IsolationProfile: "microvm", AllowCrossTenantReuse: true, MicrovmScrubMode: "restart"},
			req:     isolation.ProfileMicrovm,
			wantExe: "task", wantPro: "microvm", wantRe: true, wantWar: true, wantScr: "vm-restart",
		},
		{
			name:    "task microvm cross-tenant default scrub mode: vm-restart",
			match:   podsession.PoolMatch{ExecutionMode: "task", IsolationProfile: "microvm", AllowCrossTenantReuse: true},
			req:     isolation.ProfileMicrovm,
			wantExe: "task", wantPro: "microvm", wantRe: true, wantWar: true, wantScr: "vm-restart",
		},
		{
			name:    "task microvm cross-tenant in-place: best-effort-in-place",
			match:   podsession.PoolMatch{ExecutionMode: "task", IsolationProfile: "microvm", AllowCrossTenantReuse: true, MicrovmScrubMode: "in-place"},
			req:     isolation.ProfileMicrovm,
			wantExe: "task", wantPro: "microvm", wantRe: true, wantWar: true, wantScr: "best-effort-in-place",
		},
		{
			name:    "task microvm same-tenant: best-effort (scrub mode irrelevant without cross-tenant)",
			match:   podsession.PoolMatch{ExecutionMode: "task", IsolationProfile: "microvm", MicrovmScrubMode: "in-place"},
			req:     isolation.ProfileMicrovm,
			wantExe: "task", wantPro: "microvm", wantRe: true, wantWar: true, wantScr: "best-effort",
		},
		{
			name:    "concurrent workspace: best-effort-per-slot",
			match:   podsession.PoolMatch{ExecutionMode: "concurrent", ConcurrencyStyle: "workspace", IsolationProfile: "sandboxed"},
			req:     isolation.ProfileSandboxed,
			wantExe: "concurrent", wantPro: "sandboxed", wantRe: true, wantWar: true, wantScr: "best-effort-per-slot",
		},
		{
			name:    "concurrent stateless: none",
			match:   podsession.PoolMatch{ExecutionMode: "concurrent", ConcurrencyStyle: "stateless", IsolationProfile: "sandboxed"},
			req:     isolation.ProfileSandboxed,
			wantExe: "concurrent", wantPro: "sandboxed", wantRe: true, wantWar: true, wantScr: "none",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isolationLevelForPool(tc.match, tc.req)
			if got.ExecutionMode != tc.wantExe {
				t.Errorf("executionMode = %q, want %q", got.ExecutionMode, tc.wantExe)
			}
			if got.IsolationProfile != tc.wantPro {
				t.Errorf("isolationProfile = %q, want %q", got.IsolationProfile, tc.wantPro)
			}
			wantReuse := tc.wantExe != "session"
			if got.PodReuse != wantReuse {
				t.Errorf("podReuse = %v, want %v", got.PodReuse, wantReuse)
			}
			if got.ResidualStateWarning != tc.wantWar {
				t.Errorf("residualStateWarning = %v, want %v", got.ResidualStateWarning, tc.wantWar)
			}
			if got.ScrubPolicy != tc.wantScr {
				t.Errorf("scrubPolicy = %q, want %q", got.ScrubPolicy, tc.wantScr)
			}
		})
	}
}

// When the pool's profile is empty the level reports the requested
// profile rather than an empty string, so the client always sees a
// concrete §5.3 profile.
func TestIsolationLevelForPool_FallsBackToRequestedProfile(t *testing.T) {
	got := isolationLevelForPool(podsession.PoolMatch{ExecutionMode: "session"}, isolation.ProfileMicrovm)
	if got.IsolationProfile != "microvm" {
		t.Errorf("isolationProfile = %q, want microvm (the requested profile)", got.IsolationProfile)
	}
}

// resolveIsolationLevel with no pool resolver wired (the Postgres-only
// posture) falls back to the conservative session-mode level rather
// than failing the create. spec: §7.1 line 75.
func TestResolveIsolationLevel_NoResolverFallsBackToSession(t *testing.T) {
	s := &Server{} // podBinder nil
	got := s.resolveIsolationLevel(t.Context(), "claude-code", isolation.ProfileSandboxed)
	want := defaultIsolationLevel(isolation.ProfileSandboxed)
	if got != want {
		t.Errorf("resolveIsolationLevel without resolver = %+v, want session-mode default %+v", got, want)
	}
}

// spec: §7.1 line 75 — persistedIsolationLevel surfaces the
// executionMode + scrubPolicy halves stamped on the row at create time,
// so GET / List return the same rich envelope a client received from
// the create response. Empty ExecutionMode (legacy rows or no-pool-
// resolved posture) falls back to the conservative session-mode level
// so the field never understates the isolation posture.
func TestPersistedIsolationLevel_spec_7_1_75(t *testing.T) {
	cases := []struct {
		name string
		row  sessionstore.Session
		want SessionIsolationLevel
	}{
		{
			name: "empty execution mode falls back to session-mode default",
			row:  sessionstore.Session{IsolationProfile: isolation.ProfileSandboxed},
			want: defaultIsolationLevel(isolation.ProfileSandboxed),
		},
		{
			name: "session mode: no reuse, no scrub, no warning",
			row: sessionstore.Session{
				IsolationProfile: isolation.ProfileSandboxed,
				ExecutionMode:    "session",
			},
			want: SessionIsolationLevel{
				ExecutionMode:    "session",
				IsolationProfile: "sandboxed",
			},
		},
		{
			name: "task mode: pod reuse + warning + scrub",
			row: sessionstore.Session{
				IsolationProfile: isolation.ProfileMicrovm,
				ExecutionMode:    "task",
				ScrubPolicy:      "vm-restart",
			},
			want: SessionIsolationLevel{
				ExecutionMode:        "task",
				IsolationProfile:     "microvm",
				PodReuse:             true,
				ResidualStateWarning: true,
				ScrubPolicy:          "vm-restart",
			},
		},
		{
			name: "concurrent stateless: pod reuse + warning + no scrub",
			row: sessionstore.Session{
				IsolationProfile: isolation.ProfileSandboxed,
				ExecutionMode:    "concurrent",
				ScrubPolicy:      "none",
			},
			want: SessionIsolationLevel{
				ExecutionMode:        "concurrent",
				IsolationProfile:     "sandboxed",
				PodReuse:             true,
				ResidualStateWarning: true,
				ScrubPolicy:          "none",
			},
		},
		{
			name: "concurrent workspace: per-slot scrub",
			row: sessionstore.Session{
				IsolationProfile: isolation.ProfileSandboxed,
				ExecutionMode:    "concurrent",
				ScrubPolicy:      "best-effort-per-slot",
			},
			want: SessionIsolationLevel{
				ExecutionMode:        "concurrent",
				IsolationProfile:     "sandboxed",
				PodReuse:             true,
				ResidualStateWarning: true,
				ScrubPolicy:          "best-effort-per-slot",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := persistedIsolationLevel(tc.row)
			if got != tc.want {
				t.Errorf("persistedIsolationLevel = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// spec: §7.1 line 75 — toResponse returns the persisted
// executionMode + scrubPolicy halves so a GET surfaces the rich
// envelope frozen at session creation, including after a coordinator
// handoff.
func TestToResponse_SurfacesPersistedIsolationLevel_spec_7_1_75(t *testing.T) {
	row := sessionstore.Session{
		ID:               "sess-1",
		TenantID:         "tenant-1",
		IsolationProfile: isolation.ProfileSandboxed,
		ExecutionMode:    "concurrent",
		ScrubPolicy:      "best-effort-per-slot",
	}
	out := toResponse(row)
	if out.SessionIsolationLevel.ExecutionMode != "concurrent" {
		t.Errorf("executionMode = %q, want concurrent", out.SessionIsolationLevel.ExecutionMode)
	}
	if out.SessionIsolationLevel.ScrubPolicy != "best-effort-per-slot" {
		t.Errorf("scrubPolicy = %q, want best-effort-per-slot", out.SessionIsolationLevel.ScrubPolicy)
	}
	if !out.SessionIsolationLevel.PodReuse {
		t.Errorf("podReuse = false, want true (derived from executionMode=concurrent)")
	}
	if !out.SessionIsolationLevel.ResidualStateWarning {
		t.Errorf("residualStateWarning = false, want true")
	}
}
