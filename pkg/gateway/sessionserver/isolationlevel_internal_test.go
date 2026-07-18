// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
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
			// §5.2 whole-pod scrub cleanup commands and their aggregate cap:
			// gateway-enforced, so the mirror must surface them for the
			// recycle-path Shutdown to deliver to the adapter.
			CleanupCommands:       []string{"rm -rf /workspace/*", "sync"},
			CleanupTimeoutSeconds: 20,
			Recycle: &runtimestore.RecyclePolicy{
				Enabled:                    true,
				AcknowledgeBestEffortScrub: true,
				MaxSessionsPerPod:          50,
				AllowCrossTenantReuse:      true,
				ScrubProfile:               runtimestore.MicrovmScrubVMRestart,
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
	// §5.2 / §4.6.1 onPoolExhausted: queue pool with a tuned wait bound, so
	// the mirror surfaces the queue disposition to the start path.
	if err := store.Create(ctx, poolstore.Pool{
		Name:          "queue-pool",
		RuntimeRef:    "queue-runtime",
		ExecutionMode: runtimestore.ExecutionModeSession,
		SessionPolicy: &runtimestore.SessionPolicy{
			OnPoolExhausted:     runtimestore.PoolExhaustedQueue,
			MaxQueueWaitSeconds: 45,
		},
	}); err != nil {
		t.Fatalf("create queue pool: %v", err)
	}
	// §5.2 sequential pod-reuse ("task mode") on the standard (non-microvm)
	// scrub profile: no scrubProfile and no cross-tenant reuse, the common
	// recycling-pool configuration. The CRD pair carries no signal at all
	// for this case (sessionPolicyToCRD returns nil without a scrubProfile
	// or the in-place acknowledgment), so the mirror is the only source
	// foldPoolPolicy has for it.
	if err := store.Create(ctx, poolstore.Pool{
		Name:          "standard-reuse-pool",
		RuntimeRef:    "standard-reuse-runtime",
		ExecutionMode: runtimestore.ExecutionModeSession,
		SessionPolicy: &runtimestore.SessionPolicy{
			Recycle: &runtimestore.RecyclePolicy{
				Enabled:                    true,
				AcknowledgeBestEffortScrub: true,
				MaxSessionsPerPod:          5,
			},
		},
	}); err != nil {
		t.Fatalf("create standard-reuse pool: %v", err)
	}

	// §10.1 line 131 / §5.2 per-pool checkpointGrantWindow override: the
	// gateway-enforced value the checkpoint driver prefers over the
	// deployment-wide default.
	grantWindow := 8
	if err := store.Create(ctx, poolstore.Pool{
		Name:                  "cp-pool",
		RuntimeRef:            "cp-runtime",
		ExecutionMode:         runtimestore.ExecutionModeSession,
		CheckpointGrantWindow: &grantWindow,
	}); err != nil {
		t.Fatalf("create checkpoint-grant-window pool: %v", err)
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
	if !got.Recycle || !got.AllowCrossTenantReuse || got.MaxPodUptimeSeconds != 86400 {
		t.Errorf("reuse-pool mirror = %+v, want recycle / allowCrossTenantReuse / 86400", got)
	}
	// §5.2 whole-pod scrub cleanup config surfaces on the mirror so the
	// recycle-path Shutdown delivers it to the adapter.
	if got.CleanupTimeoutSeconds != 20 || len(got.CleanupCommands) != 2 ||
		got.CleanupCommands[0] != "rm -rf /workspace/*" || got.CleanupCommands[1] != "sync" {
		t.Errorf("reuse-pool mirror cleanup = %v / %d, want [rm -rf /workspace/* sync] / 20", got.CleanupCommands, got.CleanupTimeoutSeconds)
	}

	got, found, err = m.PoolPolicy(ctx, "standard-reuse-pool")
	if err != nil || !found {
		t.Fatalf("PoolPolicy(standard-reuse-pool): found=%v err=%v", found, err)
	}
	if !got.Recycle {
		t.Errorf("standard-reuse-pool mirror.Recycle = false, want true (§5.2 recycle.enabled on the "+
			"standard scrub profile); got %+v", got)
	}
	if got.AllowCrossTenantReuse {
		t.Errorf("standard-reuse-pool mirror.AllowCrossTenantReuse = true, want false (not requested)")
	}

	got, found, err = m.PoolPolicy(ctx, "svc-pool")
	if err != nil || !found {
		t.Fatalf("PoolPolicy(svc-pool): found=%v err=%v", found, err)
	}
	if got.MaxConcurrent != 16 {
		t.Errorf("svc-pool MaxConcurrent = %d, want 16", got.MaxConcurrent)
	}

	got, found, err = m.PoolPolicy(ctx, "queue-pool")
	if err != nil || !found {
		t.Fatalf("PoolPolicy(queue-pool): found=%v err=%v", found, err)
	}
	if got.OnPoolExhausted != "queue" || got.MaxQueueWaitSeconds != 45 {
		t.Errorf("queue-pool mirror = %+v, want onPoolExhausted=queue / maxQueueWaitSeconds=45", got)
	}

	// §10.1 line 131 / §5.2 — the per-pool checkpointGrantWindow override
	// surfaces on the mirror so the checkpoint driver's per-pool lookup
	// reads the gateway-enforced value.
	got, found, err = m.PoolPolicy(ctx, "cp-pool")
	if err != nil || !found {
		t.Fatalf("PoolPolicy(cp-pool): found=%v err=%v", found, err)
	}
	if got.CheckpointGrantWindow == nil || *got.CheckpointGrantWindow != 8 {
		t.Errorf("cp-pool mirror.CheckpointGrantWindow = %v, want 8", got.CheckpointGrantWindow)
	}
	// A pool with no override leaves the mirror field nil so the driver
	// falls back to the deployment-wide default.
	got, found, err = m.PoolPolicy(ctx, "conc-pool")
	if err != nil || !found {
		t.Fatalf("PoolPolicy(conc-pool) recheck: found=%v err=%v", found, err)
	}
	if got.CheckpointGrantWindow != nil {
		t.Errorf("conc-pool mirror.CheckpointGrantWindow = %d, want nil (no override)", *got.CheckpointGrantWindow)
	}

	// A missing pool reports found=false with no error so ResolvePool
	// keeps the CRD-derived defaults rather than failing the resolve.
	if _, found, err = m.PoolPolicy(ctx, "absent"); err != nil || found {
		t.Errorf("PoolPolicy(absent): found=%v err=%v, want found=false / no error", found, err)
	}
}

// spec: §5.2 (sessionPolicy block, gateway-enforced subset); §5.2
// NewPoolPolicyReader shares one poolstore-backed reader between
// ResolvePool and the Checkpointer's per-pool checkpointGrantWindow
// lookup. It returns nil for a nil store so the caller keeps its defaults.
func TestNewPoolPolicyReader(t *testing.T) {
	if r := NewPoolPolicyReader(nil); r != nil {
		t.Errorf("NewPoolPolicyReader(nil) = %v, want nil", r)
	}
	store := poolstore.NewMemory()
	grantWindow := 6
	ctx := context.Background()
	if err := store.Create(ctx, poolstore.Pool{
		Name:                  "cp-pool",
		RuntimeRef:            "cp-runtime",
		ExecutionMode:         runtimestore.ExecutionModeSession,
		CheckpointGrantWindow: &grantWindow,
	}); err != nil {
		t.Fatalf("create pool: %v", err)
	}
	r := NewPoolPolicyReader(store)
	if r == nil {
		t.Fatal("NewPoolPolicyReader(store) = nil, want a reader")
	}
	got, found, err := r.PoolPolicy(ctx, "cp-pool")
	if err != nil || !found {
		t.Fatalf("PoolPolicy(cp-pool): found=%v err=%v", found, err)
	}
	if got.CheckpointGrantWindow == nil || *got.CheckpointGrantWindow != 6 {
		t.Errorf("mirror.CheckpointGrantWindow = %v, want 6", got.CheckpointGrantWindow)
	}
}

// spec: §5.2 / §7.1 lines 69-73 — sessionIsolationLevel maps the resolved
// §5.2 pool's execution mode and scrub policy to the client-visible fields.
// The former task and concurrent modes are session-mode presets: recycle
// reuse and maxConcurrentSessions > 1.
func TestIsolationLevelForPool_spec_7_1(t *testing.T) {
	cases := []struct {
		name     string
		match    podsession.PoolMatch
		req      isolation.Profile
		wantExe  string
		wantPro  string
		wantRe   bool
		wantWar  bool
		wantScr  string
		wantCont string
	}{
		{
			name:    "session mode: no reuse, no scrub, no warning",
			match:   podsession.PoolMatch{ExecutionMode: "session", IsolationProfile: "sandboxed"},
			req:     isolation.ProfileSandboxed,
			wantExe: "session", wantPro: "sandboxed", wantRe: false, wantWar: false, wantScr: "", wantCont: "platform",
		},
		{
			name:    "empty execution mode defaults to session",
			match:   podsession.PoolMatch{IsolationProfile: "sandboxed"},
			req:     isolation.ProfileSandboxed,
			wantExe: "session", wantPro: "sandboxed", wantRe: false, wantWar: false, wantScr: "", wantCont: "platform",
		},
		{
			name:    "service mode: reuse + warning + no scrub + no continuity",
			match:   podsession.PoolMatch{ExecutionMode: "service", IsolationProfile: "sandboxed"},
			req:     isolation.ProfileSandboxed,
			wantExe: "service", wantPro: "sandboxed", wantRe: true, wantWar: true, wantScr: "none", wantCont: "none",
		},
		{
			name:    "recycle standard: best-effort scrub",
			match:   podsession.PoolMatch{ExecutionMode: "session", Recycle: true, IsolationProfile: "sandboxed"},
			req:     isolation.ProfileSandboxed,
			wantExe: "session", wantPro: "sandboxed", wantRe: true, wantWar: true, wantScr: "best-effort", wantCont: "platform",
		},
		{
			name:    "recycle microvm cross-tenant vm-restart (explicit): vm-restart",
			match:   podsession.PoolMatch{ExecutionMode: "session", Recycle: true, IsolationProfile: "microvm", AllowCrossTenantReuse: true, MicrovmScrubMode: "vm-restart"},
			req:     isolation.ProfileMicrovm,
			wantExe: "session", wantPro: "microvm", wantRe: true, wantWar: true, wantScr: "vm-restart", wantCont: "platform",
		},
		{
			// The poolstore validator rejects an unset scrubProfile on a
			// cross-tenant-reuse pool, so this combination does not reach the
			// gateway in practice; the mapping defensively maps any non-in-place
			// value to vm-restart.
			name:    "recycle microvm cross-tenant unset scrub mode: defensively vm-restart",
			match:   podsession.PoolMatch{ExecutionMode: "session", Recycle: true, IsolationProfile: "microvm", AllowCrossTenantReuse: true},
			req:     isolation.ProfileMicrovm,
			wantExe: "session", wantPro: "microvm", wantRe: true, wantWar: true, wantScr: "vm-restart", wantCont: "platform",
		},
		{
			name:    "recycle microvm cross-tenant in-place: best-effort-in-place",
			match:   podsession.PoolMatch{ExecutionMode: "session", Recycle: true, IsolationProfile: "microvm", AllowCrossTenantReuse: true, MicrovmScrubMode: "in-place"},
			req:     isolation.ProfileMicrovm,
			wantExe: "session", wantPro: "microvm", wantRe: true, wantWar: true, wantScr: "best-effort-in-place", wantCont: "platform",
		},
		{
			name:    "recycle microvm same-tenant: best-effort (scrub mode irrelevant without cross-tenant)",
			match:   podsession.PoolMatch{ExecutionMode: "session", Recycle: true, IsolationProfile: "microvm", MicrovmScrubMode: "in-place"},
			req:     isolation.ProfileMicrovm,
			wantExe: "session", wantPro: "microvm", wantRe: true, wantWar: true, wantScr: "best-effort", wantCont: "platform",
		},
		{
			name:    "concurrent sessions: best-effort-per-slot",
			match:   podsession.PoolMatch{ExecutionMode: "session", MaxConcurrentSessions: 4, IsolationProfile: "sandboxed"},
			req:     isolation.ProfileSandboxed,
			wantExe: "session", wantPro: "sandboxed", wantRe: true, wantWar: true, wantScr: "best-effort-per-slot", wantCont: "platform",
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
			// spec: §7.1 line 74 — service mode reports "none", every other
			// mode "platform".
			if got.ConversationContinuity != tc.wantCont {
				t.Errorf("conversationContinuity = %q, want %q", got.ConversationContinuity, tc.wantCont)
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
	got := s.resolveIsolationLevel(t.Context(), "claude-code", isolation.ProfileSandboxed, "")
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
				IsolationProfile:       isolation.ProfileSandboxed,
				ExecutionMode:          "session",
				ConversationContinuity: "platform",
			},
			want: SessionIsolationLevel{
				ExecutionMode:          "session",
				IsolationProfile:       "sandboxed",
				ConversationContinuity: "platform",
			},
		},
		{
			name: "recycle mode: pod reuse + warning + scrub",
			row: sessionstore.Session{
				IsolationProfile:       isolation.ProfileMicrovm,
				ExecutionMode:          "session",
				ScrubPolicy:            "vm-restart",
				ConversationContinuity: "platform",
			},
			want: SessionIsolationLevel{
				ExecutionMode:          "session",
				IsolationProfile:       "microvm",
				PodReuse:               true,
				ResidualStateWarning:   true,
				ScrubPolicy:            "vm-restart",
				ConversationContinuity: "platform",
			},
		},
		{
			name: "service mode: pod reuse + warning + no scrub + no continuity",
			row: sessionstore.Session{
				IsolationProfile:       isolation.ProfileSandboxed,
				ExecutionMode:          "service",
				ScrubPolicy:            "none",
				ConversationContinuity: "none",
			},
			want: SessionIsolationLevel{
				ExecutionMode:          "service",
				IsolationProfile:       "sandboxed",
				PodReuse:               true,
				ResidualStateWarning:   true,
				ScrubPolicy:            "none",
				ConversationContinuity: "none",
			},
		},
		{
			name: "concurrent sessions: per-slot scrub recorded on the row",
			row: sessionstore.Session{
				IsolationProfile:       isolation.ProfileSandboxed,
				ExecutionMode:          "session",
				ScrubPolicy:            "best-effort-per-slot",
				ConversationContinuity: "platform",
			},
			want: SessionIsolationLevel{
				ExecutionMode:          "session",
				IsolationProfile:       "sandboxed",
				PodReuse:               true,
				ResidualStateWarning:   true,
				ScrubPolicy:            "best-effort-per-slot",
				ConversationContinuity: "platform",
			},
		},
		{
			// spec: §7.1 line 74 — a service-mode row whose stored
			// conversation_continuity column was never populated (a row
			// persisted before the gateway resolved a service pool) still
			// reports "none" through the mode-derived fallback, so the field
			// never reports "platform" for a service-mode session.
			name: "service mode with empty continuity column falls back to none",
			row: sessionstore.Session{
				IsolationProfile: isolation.ProfileSandboxed,
				ExecutionMode:    "service",
				ScrubPolicy:      "none",
			},
			want: SessionIsolationLevel{
				ExecutionMode:          "service",
				IsolationProfile:       "sandboxed",
				PodReuse:               true,
				ResidualStateWarning:   true,
				ScrubPolicy:            "none",
				ConversationContinuity: "none",
			},
		},
		{
			// spec: §7.1 line 74 — a session-mode row with an empty
			// continuity column falls back to "platform".
			name: "session mode with empty continuity column falls back to platform",
			row: sessionstore.Session{
				IsolationProfile: isolation.ProfileSandboxed,
				ExecutionMode:    "session",
			},
			want: SessionIsolationLevel{
				ExecutionMode:          "session",
				IsolationProfile:       "sandboxed",
				ConversationContinuity: "platform",
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

// spec: §7.1 line 74 — conversationContinuityFor maps the §5.2 execution
// mode to the contract value: "none" for service mode, "platform" for
// session mode and the empty (unresolved) default.
func TestConversationContinuityFor_spec_7_1_74(t *testing.T) {
	cases := []struct {
		mode string
		want string
	}{
		{mode: "service", want: "none"},
		{mode: "session", want: "platform"},
		{mode: "", want: "platform"},
	}
	for _, tc := range cases {
		if got := conversationContinuityFor(tc.mode); got != tc.want {
			t.Errorf("conversationContinuityFor(%q) = %q, want %q", tc.mode, got, tc.want)
		}
	}
}

// spec: §7.1 line 74 — persistedContinuity prefers the stored S25a
// conversation_continuity column when non-empty so a read returns the value
// frozen at create, and falls back to the mode-derived value for an empty
// column (a pre-migration or never-resolved row).
func TestPersistedContinuity_spec_7_1_74(t *testing.T) {
	cases := []struct {
		name   string
		stored string
		mode   string
		want   string
	}{
		{name: "stored value is authoritative", stored: "platform", mode: "service", want: "platform"},
		{name: "empty column falls back to service mode none", stored: "", mode: "service", want: "none"},
		{name: "empty column falls back to session mode platform", stored: "", mode: "session", want: "platform"},
		{name: "empty column and empty mode falls back to platform", stored: "", mode: "", want: "platform"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := persistedContinuity(tc.stored, tc.mode); got != tc.want {
				t.Errorf("persistedContinuity(%q, %q) = %q, want %q", tc.stored, tc.mode, got, tc.want)
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
