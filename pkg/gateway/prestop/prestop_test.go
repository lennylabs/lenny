// SPDX-License-Identifier: MIT

package prestop

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// spec: §10.1 — SelectTier picks the smallest tier whose upper bound
// >= workspace size.
func TestSelectTier(t *testing.T) {
	cases := []struct {
		name           string
		workspaceBytes int64
		wantBudget     time.Duration
	}{
		{"empty", 0, 30 * time.Second},
		{"small", 1, 30 * time.Second},
		{"just_under_100mb", 100 * 1024 * 1024, 30 * time.Second},
		{"between_100_300", 200 * 1024 * 1024, 60 * time.Second},
		{"at_300mb", 300 * 1024 * 1024, 60 * time.Second},
		{"at_512mb", 512 * 1024 * 1024, 90 * time.Second},
		{"oversize", 1024 * 1024 * 1024, 90 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SelectTier(tc.workspaceBytes, StandardTiers)
			if got.Budget != tc.wantBudget {
				t.Fatalf("workspaceBytes=%d: got Budget=%v, want %v",
					tc.workspaceBytes, got.Budget, tc.wantBudget)
			}
		})
	}
}

// spec: §10.1 — empty tier table returns the MaxTierBudget so the
// chooser never returns a zero budget.
func TestSelectTier_EmptyTable(t *testing.T) {
	got := SelectTier(123, nil)
	if got.Budget != MaxTierBudget {
		t.Fatalf("empty tiers: got Budget=%v, want MaxTierBudget=%v", got.Budget, MaxTierBudget)
	}
}

// spec: §10.1 — the tiered cap must leave at least 30 seconds for
// stream drain (stage 3); otherwise it is clamped.
func TestClampForStreamDrain(t *testing.T) {
	cases := []struct {
		name        string
		raw         time.Duration
		grace       time.Duration
		wantClamped time.Duration
	}{
		{"raw_fits_in_grace", 30 * time.Second, 240 * time.Second, 30 * time.Second},
		{"raw_clamped_to_grace_minus_30", 250 * time.Second, 240 * time.Second, 210 * time.Second},
		{"raw_equals_grace", 240 * time.Second, 240 * time.Second, 210 * time.Second},
		{"grace_below_30_zeroes_out", 30 * time.Second, 20 * time.Second, 0},
		{"zero_grace_returns_zero", 30 * time.Second, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClampForStreamDrain(tc.raw, tc.grace)
			if got != tc.wantClamped {
				t.Fatalf("raw=%v grace=%v: got %v, want %v",
					tc.raw, tc.grace, got, tc.wantClamped)
			}
		})
	}
}

// fakeEnumerator captures the registry snapshot the hook iterates.
type fakeEnumerator struct {
	sessions []SessionInfo
	err      error
}

func (f *fakeEnumerator) Snapshot(_ context.Context) ([]SessionInfo, error) {
	return f.sessions, f.err
}

// fakeMetrics records every IncPreStopCapSelection call.
type fakeMetrics struct {
	calls []capCall
}

type capCall struct {
	pool   string
	siid   string
	source string
}

func (f *fakeMetrics) IncPreStopCapSelection(pool, siid, source string) {
	f.calls = append(f.calls, capCall{pool, siid, source})
}

// spec: §10.1 — the hook drains every coordinated session and bumps
// lenny_prestop_cap_selection_total once per session.
func TestHook_DrainsSessionsAndEmitsCapSelection(t *testing.T) {
	enum := &fakeEnumerator{sessions: []SessionInfo{
		{TenantID: "acme", SessionID: "s1", Pool: "default", LastCheckpointWorkspaceBytes: 5 * 1024 * 1024},
		{TenantID: "acme", SessionID: "s2", Pool: "default", LastCheckpointWorkspaceBytes: 250 * 1024 * 1024},
	}}
	metrics := &fakeMetrics{}
	var checkpointed []string
	hook := &Hook{
		Sessions:          enum,
		Metrics:           metrics,
		ServiceInstanceID: "gateway-replica-1",
		Checkpoint: func(_ context.Context, _, sessionID string, budget time.Duration) error {
			checkpointed = append(checkpointed, sessionID+"|"+budget.String())
			return nil
		},
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/internal/prestop", nil)
	hook.ServeHTTP(w, r)

	if w.Result().StatusCode != 200 {
		t.Fatalf("status: %d", w.Result().StatusCode)
	}
	var summary Summary
	if err := json.NewDecoder(w.Result().Body).Decode(&summary); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if summary.AttemptedSessions != 2 {
		t.Fatalf("attempted: %d", summary.AttemptedSessions)
	}
	if summary.CompletedSessions != 2 {
		t.Fatalf("completed: %d", summary.CompletedSessions)
	}
	if len(metrics.calls) != 2 {
		t.Fatalf("cap selection emissions: %d", len(metrics.calls))
	}
	for _, c := range metrics.calls {
		if c.siid != "gateway-replica-1" {
			t.Fatalf("service_instance_id: %q", c.siid)
		}
		if c.source != string(CapSourcePostgres) {
			t.Fatalf("source: %q", c.source)
		}
	}
	if len(checkpointed) != 2 {
		t.Fatalf("checkpoint calls: %d", len(checkpointed))
	}
	// First session: 5 MB workspace -> 30s tier.
	if !strings.HasPrefix(checkpointed[0], "s1|30s") {
		t.Fatalf("s1 budget: %q", checkpointed[0])
	}
	// Second session: 250 MB workspace -> 60s tier.
	if !strings.HasPrefix(checkpointed[1], "s2|1m0s") {
		t.Fatalf("s2 budget: %q", checkpointed[1])
	}
}

// spec: §10.1 — a postgres_null session falls back to the 90s
// conservative tier and stamps the postgres_null source label.
func TestHook_PostgresNullSelectsMaxTier(t *testing.T) {
	enum := &fakeEnumerator{sessions: []SessionInfo{
		{TenantID: "acme", SessionID: "s1", Pool: "default", IsPostgresNull: true},
	}}
	metrics := &fakeMetrics{}
	var caps []time.Duration
	hook := &Hook{
		Sessions: enum,
		Metrics:  metrics,
		Checkpoint: func(_ context.Context, _, _ string, budget time.Duration) error {
			caps = append(caps, budget)
			return nil
		},
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/internal/prestop", nil)
	hook.ServeHTTP(w, r)

	if len(caps) != 1 {
		t.Fatalf("checkpoint calls: %d", len(caps))
	}
	if caps[0] != MaxTierBudget {
		t.Fatalf("budget: got %v want %v", caps[0], MaxTierBudget)
	}
	if len(metrics.calls) != 1 {
		t.Fatalf("metrics emissions: %d", len(metrics.calls))
	}
	if metrics.calls[0].source != string(CapSourcePostgresNull) {
		t.Fatalf("source: %q", metrics.calls[0].source)
	}
}

// spec: §10.1 — cache_hit and cache_miss_max_tier explicit overrides
// stamp the corresponding source label.
func TestHook_CacheSourceOverrides(t *testing.T) {
	cases := []struct {
		name   string
		source CapSelectionSource
	}{
		{"cache_hit", CapSourceCacheHit},
		{"cache_miss_max_tier", CapSourceCacheMissMaxTier},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enum := &fakeEnumerator{sessions: []SessionInfo{
				{TenantID: "acme", SessionID: "s1", SourceLabel: tc.source},
			}}
			metrics := &fakeMetrics{}
			hook := &Hook{
				Sessions: enum,
				Metrics:  metrics,
				Checkpoint: func(_ context.Context, _, _ string, _ time.Duration) error { return nil },
			}
			w := httptest.NewRecorder()
			r := httptest.NewRequest("POST", "/internal/prestop", nil)
			hook.ServeHTTP(w, r)
			if len(metrics.calls) != 1 {
				t.Fatalf("metrics emissions: %d", len(metrics.calls))
			}
			if metrics.calls[0].source != string(tc.source) {
				t.Fatalf("source: %q", metrics.calls[0].source)
			}
		})
	}
}

// spec: §10.1 — per-session checkpoint failure does not abort the
// drain; the summary records the failure and the hook continues.
func TestHook_PerSessionFailureDoesNotAbort(t *testing.T) {
	enum := &fakeEnumerator{sessions: []SessionInfo{
		{TenantID: "acme", SessionID: "ok"},
		{TenantID: "acme", SessionID: "bad"},
		{TenantID: "acme", SessionID: "ok2"},
	}}
	hook := &Hook{
		Sessions: enum,
		Checkpoint: func(_ context.Context, _, sessionID string, _ time.Duration) error {
			if sessionID == "bad" {
				return errors.New("simulated failure")
			}
			return nil
		},
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/internal/prestop", nil)
	hook.ServeHTTP(w, r)

	var summary Summary
	_ = json.NewDecoder(w.Result().Body).Decode(&summary)
	if summary.AttemptedSessions != 3 {
		t.Fatalf("attempted: %d", summary.AttemptedSessions)
	}
	if summary.CompletedSessions != 2 {
		t.Fatalf("completed: %d", summary.CompletedSessions)
	}
	if summary.FailedSessions != 1 {
		t.Fatalf("failed: %d", summary.FailedSessions)
	}
	if len(summary.Errors) != 1 || !strings.Contains(summary.Errors[0], "bad:") {
		t.Fatalf("errors: %v", summary.Errors)
	}
}

// spec: §10.1 — a second invocation returns already_fired=true so
// the drain does not run twice on the same pod.
func TestHook_IdempotentOnSecondInvocation(t *testing.T) {
	enum := &fakeEnumerator{sessions: []SessionInfo{{TenantID: "acme", SessionID: "s"}}}
	hook := &Hook{
		Sessions:   enum,
		Checkpoint: func(_ context.Context, _, _ string, _ time.Duration) error { return nil },
	}
	w1 := httptest.NewRecorder()
	hook.ServeHTTP(w1, httptest.NewRequest("POST", "/internal/prestop", nil))
	w2 := httptest.NewRecorder()
	hook.ServeHTTP(w2, httptest.NewRequest("POST", "/internal/prestop", nil))

	var s1, s2 Summary
	_ = json.NewDecoder(w1.Result().Body).Decode(&s1)
	_ = json.NewDecoder(w2.Result().Body).Decode(&s2)
	if s1.AlreadyFired {
		t.Fatal("first invocation should not report already_fired")
	}
	if !s2.AlreadyFired {
		t.Fatal("second invocation should report already_fired")
	}
}

// spec: §10.1 — a snapshot failure returns 200 with the error in the
// summary; Kubernetes never gets a non-200 response so the drain
// continues into the SIGTERM path.
func TestHook_SnapshotFailureSurfacesInSummary(t *testing.T) {
	enum := &fakeEnumerator{err: errors.New("registry down")}
	hook := &Hook{
		Sessions: enum,
	}
	w := httptest.NewRecorder()
	hook.ServeHTTP(w, httptest.NewRequest("POST", "/internal/prestop", nil))

	if w.Result().StatusCode != 200 {
		t.Fatalf("status: %d", w.Result().StatusCode)
	}
	var s Summary
	_ = json.NewDecoder(w.Result().Body).Decode(&s)
	if len(s.Errors) != 1 || !strings.Contains(s.Errors[0], "snapshot sessions") {
		t.Fatalf("errors: %v", s.Errors)
	}
}

// spec: §10.1 — a nil Checkpoint function records every session as
// skipped so the hook is observable in dev mode without the
// in-process checkpointer.
func TestHook_NilCheckpointSkipsSessions(t *testing.T) {
	enum := &fakeEnumerator{sessions: []SessionInfo{
		{TenantID: "acme", SessionID: "s1"},
		{TenantID: "acme", SessionID: "s2"},
	}}
	hook := &Hook{
		Sessions: enum,
		// Checkpoint left nil.
	}
	w := httptest.NewRecorder()
	hook.ServeHTTP(w, httptest.NewRequest("POST", "/internal/prestop", nil))

	var s Summary
	_ = json.NewDecoder(w.Result().Body).Decode(&s)
	if s.SkippedSessions != 2 {
		t.Fatalf("skipped: %d", s.SkippedSessions)
	}
	if s.CompletedSessions != 0 {
		t.Fatalf("completed: %d", s.CompletedSessions)
	}
}

// spec: §10.1 — the grace period defaults to DefaultTerminationGraceSeconds.
func TestHook_GracePeriodDefault(t *testing.T) {
	enum := &fakeEnumerator{}
	hook := &Hook{Sessions: enum}
	w := httptest.NewRecorder()
	hook.ServeHTTP(w, httptest.NewRequest("POST", "/internal/prestop", nil))

	var s Summary
	_ = json.NewDecoder(w.Result().Body).Decode(&s)
	if s.GracePeriodSeconds != DefaultTerminationGraceSeconds {
		t.Fatalf("grace: got %d, want %d", s.GracePeriodSeconds, DefaultTerminationGraceSeconds)
	}
}

// spec: §10.1 — an explicit override on the Hook honors the supplied
// grace value.
func TestHook_GracePeriodOverride(t *testing.T) {
	enum := &fakeEnumerator{}
	hook := &Hook{Sessions: enum, GracePeriod: 600 * time.Second}
	w := httptest.NewRecorder()
	hook.ServeHTTP(w, httptest.NewRequest("POST", "/internal/prestop", nil))

	var s Summary
	_ = json.NewDecoder(w.Result().Body).Decode(&s)
	if s.GracePeriodSeconds != 600 {
		t.Fatalf("grace: got %d, want 600", s.GracePeriodSeconds)
	}
}

// spec: §10.1 — a nil hook returns 200 with an empty summary so a
// missing wiring does not 500 the kubelet's preStop probe.
func TestHook_NilHookReturns200(t *testing.T) {
	var hook *Hook
	w := httptest.NewRecorder()
	hook.ServeHTTP(w, httptest.NewRequest("POST", "/internal/prestop", nil))
	if w.Result().StatusCode != 200 {
		t.Fatalf("status: %d", w.Result().StatusCode)
	}
}

// spec: §10.1 — when budget would force the tier above the
// grace-minus-30 floor, the per-session checkpoint runs against the
// clamped value rather than the raw tier.
func TestHook_ClampsBudgetForShortGrace(t *testing.T) {
	enum := &fakeEnumerator{sessions: []SessionInfo{
		{TenantID: "acme", SessionID: "s1", LastCheckpointWorkspaceBytes: 400 * 1024 * 1024},
	}}
	var seen time.Duration
	hook := &Hook{
		Sessions:    enum,
		GracePeriod: 60 * time.Second, // tight grace
		Checkpoint: func(_ context.Context, _, _ string, budget time.Duration) error {
			seen = budget
			return nil
		},
	}
	w := httptest.NewRecorder()
	hook.ServeHTTP(w, httptest.NewRequest("POST", "/internal/prestop", nil))
	// 400 MB selects 90s; grace 60s clamps to 30s (grace - 30s floor).
	if seen != 30*time.Second {
		t.Fatalf("clamped budget: got %v, want 30s", seen)
	}
}
