// SPDX-License-Identifier: MIT

package orphancleanup_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/orphancleanup"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/treearchive"
)

// seed inserts a session row with the given state and parent.
func seed(t *testing.T, store sessionstore.Store, id, parent string, state session.State, updatedAt time.Time) {
	t.Helper()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: "acme", State: state, ParentSessionID: parent,
		CreatedAt: updatedAt, UpdatedAt: updatedAt,
	}); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

func TestTickTerminatesOrphanPastCascadeTimeout(t *testing.T) {
	store := memstore.New()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// The root terminated at base; the child is still running.
	seed(t, store, "sess_root", "", session.StateCompleted, base)
	seed(t, store, "sess_child", "sess_root", session.StateRunning, base)
	sw := orphancleanup.New(store, orphancleanup.StaticTenants{"acme"}, orphancleanup.Options{})

	// Sweep well past the default 3600s cascade timeout.
	n, err := sw.Tick(context.Background(), base.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if n != 1 {
		t.Errorf("terminated count = %d, want 1", n)
	}
	row, _ := store.Get(context.Background(), "acme", "sess_child")
	if row.State != session.StateExpired {
		t.Errorf("orphan state = %q, want expired", row.State)
	}
}

// TestTickStampsExpiredDeadlineReason_spec_8_8_867 verifies the
// §8.8 line 867 expiry-reason prefix lands on the row when orphan
// cleanup drives a child to `expired`. The MCP boundary's taskError
// fallback then surfaces `expired:deadline` to clients. F-8.8.8.
func TestTickStampsExpiredDeadlineReason_spec_8_8_867(t *testing.T) {
	store := memstore.New()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seed(t, store, "sess_root_or", "", session.StateCompleted, base)
	seed(t, store, "sess_child_or", "sess_root_or", session.StateRunning, base)
	sw := orphancleanup.New(store, orphancleanup.StaticTenants{"acme"}, orphancleanup.Options{})
	if _, err := sw.Tick(context.Background(), base.Add(2*time.Hour)); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	row, err := store.Get(context.Background(), "acme", "sess_child_or")
	if err != nil {
		t.Fatalf("Get sess_child_or: %v", err)
	}
	if row.State != session.StateExpired {
		t.Fatalf("State = %q, want expired", row.State)
	}
	if row.FailureReason != string(session.FailureExpiredDeadline) {
		t.Errorf("FailureReason = %q, want %q (§8.8 line 867)", row.FailureReason, session.FailureExpiredDeadline)
	}
}

func TestTickLeavesOrphanWithinCascadeWindow(t *testing.T) {
	store := memstore.New()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seed(t, store, "sess_root", "", session.StateCompleted, base)
	seed(t, store, "sess_child", "sess_root", session.StateRunning, base)
	sw := orphancleanup.New(store, orphancleanup.StaticTenants{"acme"}, orphancleanup.Options{})

	// Ten minutes after the root terminated — well inside the 3600s window.
	n, err := sw.Tick(context.Background(), base.Add(10*time.Minute))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if n != 0 {
		t.Errorf("terminated count = %d, want 0 — still inside the cascade window", n)
	}
	row, _ := store.Get(context.Background(), "acme", "sess_child")
	if row.State != session.StateRunning {
		t.Errorf("orphan state = %q, want running", row.State)
	}
}

func TestTickLeavesChildOfActiveRoot(t *testing.T) {
	store := memstore.New()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// The root is still running — its children are not orphans.
	seed(t, store, "sess_root", "", session.StateRunning, base)
	seed(t, store, "sess_child", "sess_root", session.StateRunning, base)
	sw := orphancleanup.New(store, orphancleanup.StaticTenants{"acme"}, orphancleanup.Options{})

	n, err := sw.Tick(context.Background(), base.Add(10*time.Hour))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if n != 0 {
		t.Errorf("terminated count = %d, want 0 — the root is still active", n)
	}
}

func TestTickTerminatesDeepOrphanSubtree(t *testing.T) {
	store := memstore.New()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// root (terminal) → mid (running) → leaf (running): both mid and
	// leaf are orphans of the terminated root.
	seed(t, store, "sess_root", "", session.StateFailed, base)
	seed(t, store, "sess_mid", "sess_root", session.StateRunning, base)
	seed(t, store, "sess_leaf", "sess_mid", session.StateRunning, base)
	sw := orphancleanup.New(store, orphancleanup.StaticTenants{"acme"}, orphancleanup.Options{})

	n, err := sw.Tick(context.Background(), base.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if n != 2 {
		t.Errorf("terminated count = %d, want 2 (mid + leaf)", n)
	}
	for _, id := range []string{"sess_mid", "sess_leaf"} {
		row, _ := store.Get(context.Background(), "acme", id)
		if row.State != session.StateExpired {
			t.Errorf("%s state = %q, want expired", id, row.State)
		}
	}
}

func TestTickArchivesTerminatedOrphan(t *testing.T) {
	store := memstore.New()
	archive := treearchive.NewMemory()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seed(t, store, "sess_root", "", session.StateCompleted, base)
	seed(t, store, "sess_child", "sess_root", session.StateRunning, base)
	sw := orphancleanup.New(store, orphancleanup.StaticTenants{"acme"},
		orphancleanup.Options{Archive: archive})

	if _, err := sw.Tick(context.Background(), base.Add(2*time.Hour)); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	got, err := archive.GetByNode(context.Background(), "acme", "sess_child")
	if err != nil {
		t.Fatalf("terminated orphan was not archived: %v", err)
	}
	if got.State != string(session.StateExpired) {
		t.Errorf("archived state = %q, want expired", got.State)
	}
	if got.RootSessionID != "sess_root" {
		t.Errorf("archived RootSessionID = %q, want sess_root", got.RootSessionID)
	}
}

// fakeTerminalHook captures terminal-pipeline invocations.
type fakeTerminalHook struct{ calls []sessionstore.Session }

func (f *fakeTerminalHook) OnSessionTerminal(_ context.Context, _ session.State, sess sessionstore.Session) {
	f.calls = append(f.calls, sess)
}

// spec: §5.2 line 519 + §8.10 — F-5.2.26: an orphan terminated by the
// cascade sweep must run the gateway-side terminal pipeline so its
// executor (concurrent-mode slot release + pod drain) is released.
func TestTickInvokesTerminalHook_spec_5_2_519(t *testing.T) {
	store := memstore.New()
	hook := &fakeTerminalHook{}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seed(t, store, "sess_root", "", session.StateCompleted, base)
	seed(t, store, "sess_child", "sess_root", session.StateRunning, base)
	sw := orphancleanup.New(store, orphancleanup.StaticTenants{"acme"},
		orphancleanup.Options{Terminal: hook})

	if _, err := sw.Tick(context.Background(), base.Add(2*time.Hour)); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(hook.calls) != 1 || hook.calls[0].ID != "sess_child" {
		t.Fatalf("OnSessionTerminal invocations: %+v", hook.calls)
	}
	if hook.calls[0].State != session.StateExpired {
		t.Errorf("hook state = %q, want expired", hook.calls[0].State)
	}
}

// With a TerminalHook the in-package archive emission is skipped (the
// hook drives the §8.10 archive write via the session-server pipeline),
// so the orphan is archived exactly once.
func TestTickWithTerminalHookSkipsInPackageArchive(t *testing.T) {
	store := memstore.New()
	archive := treearchive.NewMemory()
	hook := &fakeTerminalHook{}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seed(t, store, "sess_root", "", session.StateCompleted, base)
	seed(t, store, "sess_child", "sess_root", session.StateRunning, base)
	sw := orphancleanup.New(store, orphancleanup.StaticTenants{"acme"},
		orphancleanup.Options{Archive: archive, Terminal: hook})

	if _, err := sw.Tick(context.Background(), base.Add(2*time.Hour)); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if _, err := archive.GetByNode(context.Background(), "acme", "sess_child"); err == nil {
		t.Fatal("in-package archive must not fire when TerminalHook is wired")
	}
	if len(hook.calls) != 1 {
		t.Fatalf("OnSessionTerminal calls: got %d, want 1", len(hook.calls))
	}
}

// fakeMetricsSink captures §8.10 / §16.1 sweeper observations.
type fakeMetricsSink struct {
	runs       int
	terminated int
	active     int
	perTenant  map[string]int
}

func (m *fakeMetricsSink) IncOrphanCleanupRun()           { m.runs++ }
func (m *fakeMetricsSink) AddOrphanTasksTerminated(n int) { m.terminated += n }
func (m *fakeMetricsSink) SetOrphanTasksActive(value int) { m.active = value }
func (m *fakeMetricsSink) SetOrphanTasksActivePerTenant(tenantID string, value int) {
	if m.perTenant == nil {
		m.perTenant = map[string]int{}
	}
	m.perTenant[tenantID] = value
}

// TestTickEmitsOrphanCleanupMetrics_spec_8_10_1091 covers the §8.10 /
// §16.1 lines 146-149 orphan-cleanup observability surface — one
// IncOrphanCleanupRun per Tick, terminated counter += per-sweep return,
// fleet-wide active gauge sums the per-tenant remaining counts, and the
// per-tenant gauge re-publishes a value for every tenant. F-8.10.7.
func TestTickEmitsOrphanCleanupMetrics_spec_8_10_1091(t *testing.T) {
	store := memstore.New()
	base := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)
	seed(t, store, "sess_root", "", session.StateCompleted, base)
	seed(t, store, "sess_child", "sess_root", session.StateRunning, base)
	sink := &fakeMetricsSink{}
	sw := orphancleanup.New(store, orphancleanup.StaticTenants{"acme"}, orphancleanup.Options{
		Metrics: sink,
	})

	if _, err := sw.Tick(context.Background(), base.Add(2*time.Hour)); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if sink.runs != 1 {
		t.Errorf("IncOrphanCleanupRun calls = %d, want 1", sink.runs)
	}
	if sink.terminated != 1 {
		t.Errorf("AddOrphanTasksTerminated total = %d, want 1", sink.terminated)
	}
	if sink.active != 0 {
		t.Errorf("SetOrphanTasksActive = %d, want 0 (the child was terminated this Tick)", sink.active)
	}
	if got := sink.perTenant["acme"]; got != 0 {
		t.Errorf("SetOrphanTasksActivePerTenant[acme] = %d, want 0", got)
	}
}

// TestTickPublishesActiveOrphansInsideCascadeWindow_spec_8_10_1103
// verifies the per-tenant gauge counts orphans that are still inside
// the cascade window — the alert source must observe a non-zero
// population during the deferral. F-8.10.7.
func TestTickPublishesActiveOrphansInsideCascadeWindow_spec_8_10_1103(t *testing.T) {
	store := memstore.New()
	base := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)
	seed(t, store, "sess_root", "", session.StateCompleted, base)
	seed(t, store, "sess_child", "sess_root", session.StateRunning, base)
	sink := &fakeMetricsSink{}
	sw := orphancleanup.New(store, orphancleanup.StaticTenants{"acme"}, orphancleanup.Options{
		Metrics: sink,
	})

	// 10m after root termination — still inside the default 3600s window.
	if _, err := sw.Tick(context.Background(), base.Add(10*time.Minute)); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if sink.terminated != 0 {
		t.Errorf("AddOrphanTasksTerminated = %d, want 0 — sweep deferred", sink.terminated)
	}
	if sink.active != 1 {
		t.Errorf("SetOrphanTasksActive = %d, want 1", sink.active)
	}
	if got := sink.perTenant["acme"]; got != 1 {
		t.Errorf("SetOrphanTasksActivePerTenant[acme] = %d, want 1", got)
	}
}

func TestTickIsIdempotent(t *testing.T) {
	store := memstore.New()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seed(t, store, "sess_root", "", session.StateCompleted, base)
	seed(t, store, "sess_child", "sess_root", session.StateRunning, base)
	sw := orphancleanup.New(store, orphancleanup.StaticTenants{"acme"}, orphancleanup.Options{})

	if _, err := sw.Tick(context.Background(), base.Add(2*time.Hour)); err != nil {
		t.Fatalf("first Tick: %v", err)
	}
	// A second sweep finds nothing new — the orphan is already terminal.
	n, err := sw.Tick(context.Background(), base.Add(3*time.Hour))
	if err != nil {
		t.Fatalf("second Tick: %v", err)
	}
	if n != 0 {
		t.Errorf("second sweep terminated %d, want 0", n)
	}
}
