// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// spec: §5.2 (sessionPolicy block, gateway-enforced subset)
// poolPolicyReader returns nil when no pool store is wired, so
// ResolvePool keeps its CRD-derived dispatch defaults under the
// Postgres-only / CRD-only posture.
func TestPoolPolicyReader_NilWithoutStore(t *testing.T) {
	s := &Server{}
	if r := s.poolPolicyReader(); r != nil {
		t.Errorf("poolPolicyReader without a pool store = %v, want nil", r)
	}
}

// spec: §5.2 (sessionPolicy block, gateway-enforced subset)
// poolPolicyMirror maps the §5.2 poolstore sessionPolicy mirror onto the
// PoolPolicyMirror ResolvePool folds in: maxConcurrentSessions, the
// service-mode maxConcurrent, recycle.allowCrossTenantReuse, and
// recycle.maxPodUptimeSeconds. The three pools exercise the
// concurrent-session, cross-tenant sequential-reuse, and service-mode
// sourcing in one store.
func TestPoolPolicyMirror_PoolPolicy(t *testing.T) {
	store := poolstore.NewMemory()
	ctx := context.Background()
	// Concurrent-session pool: maxConcurrentSessions > 1 requires the
	// process-level acknowledgment and forbids cross-tenant reuse (§5.2).
	if err := store.Create(ctx, poolstore.Pool{
		Name:          "conc-pool",
		RuntimeRef:    "conc-runtime",
		ExecutionMode: runtimestore.ExecutionModeSession,
		SessionPolicy: &runtimestore.SessionPolicy{
			MaxConcurrentSessions:            4,
			AcknowledgeProcessLevelIsolation: true,
		},
	}); err != nil {
		t.Fatalf("create concurrent pool: %v", err)
	}
	// Sequential cross-tenant reuse pool: microvm-gated, single session
	// per pod, with the recycle acknowledgments and a uptime cap (§5.2).
	if err := store.Create(ctx, poolstore.Pool{
		Name:             "reuse-pool",
		RuntimeRef:       "reuse-runtime",
		ExecutionMode:    runtimestore.ExecutionModeSession,
		IsolationProfile: isolation.ProfileMicrovm,
		SessionPolicy: &runtimestore.SessionPolicy{
			MaxConcurrentSessions: 1,
			Recycle: &runtimestore.RecyclePolicy{
				Enabled:                    true,
				AcknowledgeBestEffortScrub: true,
				MaxSessionsPerPod:          50,
				AllowCrossTenantReuse:      true,
				MaxPodUptimeSeconds:        86400,
			},
		},
	}); err != nil {
		t.Fatalf("create reuse pool: %v", err)
	}
	if err := store.Create(ctx, poolstore.Pool{
		Name:          "svc-pool",
		RuntimeRef:    "svc-runtime",
		ExecutionMode: runtimestore.ExecutionModeService,
		MaxConcurrent: 16,
	}); err != nil {
		t.Fatalf("create service pool: %v", err)
	}

	m := poolPolicyMirror{pools: store}

	got, found, err := m.PoolPolicy(ctx, "conc-pool")
	if err != nil || !found {
		t.Fatalf("PoolPolicy(conc-pool): found=%v err=%v", found, err)
	}
	if got.MaxConcurrentSessions != 4 {
		t.Errorf("conc-pool MaxConcurrentSessions = %d, want 4", got.MaxConcurrentSessions)
	}

	got, found, err = m.PoolPolicy(ctx, "reuse-pool")
	if err != nil || !found {
		t.Fatalf("PoolPolicy(reuse-pool): found=%v err=%v", found, err)
	}
	if !got.AllowCrossTenantReuse || got.MaxPodUptimeSeconds != 86400 {
		t.Errorf("reuse-pool mirror = %+v, want allowCrossTenantReuse / 86400", got)
	}

	got, found, err = m.PoolPolicy(ctx, "svc-pool")
	if err != nil || !found {
		t.Fatalf("PoolPolicy(svc-pool): found=%v err=%v", found, err)
	}
	if got.MaxConcurrent != 16 {
		t.Errorf("svc-pool MaxConcurrent = %d, want 16", got.MaxConcurrent)
	}

	// A missing pool reports found=false with no error so ResolvePool
	// keeps the CRD-derived defaults rather than failing the resolve.
	if _, found, err = m.PoolPolicy(ctx, "absent"); err != nil || found {
		t.Errorf("PoolPolicy(absent): found=%v err=%v, want found=false / no error", found, err)
	}
}

// spec: §5.2 / §7.1 lines 69-73 — sessionIsolationLevel maps the resolved
// §5.2 pool's execution mode and scrub policy to the client-visible fields.
// The former task and concurrent modes are session-mode presets: recycle
// reuse and maxConcurrentSessions > 1.
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
			name:    "service mode: reuse + warning + no scrub",
			match:   podsession.PoolMatch{ExecutionMode: "service", IsolationProfile: "sandboxed"},
			req:     isolation.ProfileSandboxed,
			wantExe: "service", wantPro: "sandboxed", wantRe: true, wantWar: true, wantScr: "none",
		},
		{
			name:    "recycle standard: best-effort scrub",
			match:   podsession.PoolMatch{ExecutionMode: "session", Recycle: true, IsolationProfile: "sandboxed"},
			req:     isolation.ProfileSandboxed,
			wantExe: "session", wantPro: "sandboxed", wantRe: true, wantWar: true, wantScr: "best-effort",
		},
		{
			name:    "recycle microvm cross-tenant restart (explicit): vm-restart",
			match:   podsession.PoolMatch{ExecutionMode: "session", Recycle: true, IsolationProfile: "microvm", AllowCrossTenantReuse: true, MicrovmScrubMode: "restart"},
			req:     isolation.ProfileMicrovm,
			wantExe: "session", wantPro: "microvm", wantRe: true, wantWar: true, wantScr: "vm-restart",
		},
		{
			name:    "recycle microvm cross-tenant default scrub mode: vm-restart",
			match:   podsession.PoolMatch{ExecutionMode: "session", Recycle: true, IsolationProfile: "microvm", AllowCrossTenantReuse: true},
			req:     isolation.ProfileMicrovm,
			wantExe: "session", wantPro: "microvm", wantRe: true, wantWar: true, wantScr: "vm-restart",
		},
		{
			name:    "recycle microvm cross-tenant in-place: best-effort-in-place",
			match:   podsession.PoolMatch{ExecutionMode: "session", Recycle: true, IsolationProfile: "microvm", AllowCrossTenantReuse: true, MicrovmScrubMode: "in-place"},
			req:     isolation.ProfileMicrovm,
			wantExe: "session", wantPro: "microvm", wantRe: true, wantWar: true, wantScr: "best-effort-in-place",
		},
		{
			name:    "recycle microvm same-tenant: best-effort (scrub mode irrelevant without cross-tenant)",
			match:   podsession.PoolMatch{ExecutionMode: "session", Recycle: true, IsolationProfile: "microvm", MicrovmScrubMode: "in-place"},
			req:     isolation.ProfileMicrovm,
			wantExe: "session", wantPro: "microvm", wantRe: true, wantWar: true, wantScr: "best-effort",
		},
		{
			name:    "concurrent sessions: best-effort-per-slot",
			match:   podsession.PoolMatch{ExecutionMode: "session", MaxConcurrentSessions: 4, IsolationProfile: "sandboxed"},
			req:     isolation.ProfileSandboxed,
			wantExe: "session", wantPro: "sandboxed", wantRe: true, wantWar: true, wantScr: "best-effort-per-slot",
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
			if got.PodReuse != tc.wantRe {
				t.Errorf("podReuse = %v, want %v", got.PodReuse, tc.wantRe)
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
			name: "recycle mode: pod reuse + warning + scrub",
			row: sessionstore.Session{
				IsolationProfile: isolation.ProfileMicrovm,
				ExecutionMode:    "session",
				ScrubPolicy:      "vm-restart",
			},
			want: SessionIsolationLevel{
				ExecutionMode:        "session",
				IsolationProfile:     "microvm",
				PodReuse:             true,
				ResidualStateWarning: true,
				ScrubPolicy:          "vm-restart",
			},
		},
		{
			name: "service mode: pod reuse + warning + no scrub",
			row: sessionstore.Session{
				IsolationProfile: isolation.ProfileSandboxed,
				ExecutionMode:    "service",
				ScrubPolicy:      "none",
			},
			want: SessionIsolationLevel{
				ExecutionMode:        "service",
				IsolationProfile:     "sandboxed",
				PodReuse:             true,
				ResidualStateWarning: true,
				ScrubPolicy:          "none",
			},
		},
		{
			name: "concurrent sessions: per-slot scrub recorded on the row",
			row: sessionstore.Session{
				IsolationProfile: isolation.ProfileSandboxed,
				ExecutionMode:    "session",
				ScrubPolicy:      "best-effort-per-slot",
			},
			want: SessionIsolationLevel{
				ExecutionMode:        "session",
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
		ExecutionMode:    "session",
		ScrubPolicy:      "best-effort-per-slot",
	}
	out := toResponse(row)
	if out.SessionIsolationLevel.ExecutionMode != "session" {
		t.Errorf("executionMode = %q, want session", out.SessionIsolationLevel.ExecutionMode)
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
