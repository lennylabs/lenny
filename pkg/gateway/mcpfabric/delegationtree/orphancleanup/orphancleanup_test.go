// SPDX-License-Identifier: MIT

package orphancleanup_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/orphancleanup"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treearchive"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// seed inserts a session row with the given state and parent under the
// `detach` cascade policy. A row with a parent is a delegated session one
// delegation level below its parent, and a terminal row records updatedAt as
// its TerminatedAt, the instant the cascade window counts from.
func seed(t *testing.T, store sessionstore.Store, id, parent string, state session.State, updatedAt time.Time) {
	t.Helper()
	row := sessionstore.Session{
		ID: id, TenantID: "acme", State: state, ParentSessionID: parent,
		CascadeOnFailure: session.CascadeDetach,
		CreatedAt:        updatedAt, UpdatedAt: updatedAt,
	}
	if parent != "" {
		p, err := store.Get(context.Background(), "acme", parent)
		if err != nil {
			t.Fatalf("seed %s: get parent %s: %v", id, parent, err)
		}
		row.DelegationDepth = p.DelegationDepth + 1
	}
	if session.IsTerminal(state) {
		row.TerminatedAt = updatedAt
	}
	seedRow(t, store, row)
}

// seedRow inserts row as given. The store's creation fence refuses a
// delegated row under a terminal parent, which lenny/delegate_task never
// creates, so a terminal parent is put back in a live state for the insert
// and then restored with its recorded termination. A test seeds the row
// that a parent's later end leaves behind.
func seedRow(t *testing.T, store sessionstore.Store, row sessionstore.Session) {
	t.Helper()
	ctx := context.Background()
	if parent, err := store.Get(ctx, row.TenantID, row.ParentSessionID); err == nil && session.IsTerminal(parent.State) {
		setState(t, store, parent.ID, session.StateRunning, time.Time{})
		defer setState(t, store, parent.ID, parent.State, parent.TerminatedAt)
	}
	if err := store.Create(ctx, row); err != nil {
		t.Fatalf("seed %s: %v", row.ID, err)
	}
}

// setState rewrites a seeded row's state and TerminatedAt with a raw store
// write, outside every production writer.
func setState(t *testing.T, store sessionstore.Store, id string, state session.State, terminatedAt time.Time) {
	t.Helper()
	if _, err := store.Update(context.Background(), "acme", id, func(r *sessionstore.Session) error {
		r.State, r.TerminatedAt = state, terminatedAt
		return nil
	}); err != nil {
		t.Fatalf("set %s to %s: %v", id, state, err)
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
// §8.8 expiry-reason prefix lands on the row when orphan
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
		t.Errorf("FailureReason = %q, want %q (§8.8)", row.FailureReason, session.FailureExpiredDeadline)
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
	sw.WaitDispatched()
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
type fakeTerminalHook struct {
	// mu guards calls, which dispatched hook goroutines append to.
	mu    sync.Mutex
	calls []sessionstore.Session
}

// BeginTerminal opens no claim; the fake records only the terminal hook.
func (*fakeTerminalHook) BeginTerminal(string) func() { return func() {} }

func (f *fakeTerminalHook) OnSessionTerminal(_ context.Context, _ session.State, sess sessionstore.Session) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, sess)
}

// spec: §5.2 + §8.10 — F-5.2.26: an orphan terminated by the
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
	sw.WaitDispatched()
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
	sw.WaitDispatched()
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
// §16.1 orphan-cleanup observability surface — one
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

// spec: §8.10 (Orphan cleanup)
//
// The cascade window counts from the root's TerminatedAt. A write that lands
// on the terminal root after its termination advances its UpdatedAt and does
// not move the window, so the orphan is expired once the window measured from
// the termination instant has elapsed.
func TestOrphanCascadeWindowCountsFromRootTerminatedAt_spec_8_10(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	storeNow := base
	store := memstore.NewWithClock(func() time.Time { return storeNow })
	seed(t, store, "sess_root", "", session.StateCompleted, base)
	seed(t, store, "sess_child", "sess_root", session.StateRunning, base)
	// Ninety minutes after termination a write rewrites the root's UpdatedAt.
	storeNow = base.Add(90 * time.Minute)
	rewritten, err := store.Update(context.Background(), "acme", "sess_root", func(r *sessionstore.Session) error {
		r.LastSeq++
		return nil
	})
	if err != nil {
		t.Fatalf("rewrite root: %v", err)
	}
	if !rewritten.UpdatedAt.After(base) || !rewritten.TerminatedAt.Equal(base) {
		t.Fatalf("root UpdatedAt %v TerminatedAt %v, want UpdatedAt past %v and TerminatedAt at it",
			rewritten.UpdatedAt, rewritten.TerminatedAt, base)
	}
	sw := orphancleanup.New(store, orphancleanup.StaticTenants{"acme"}, orphancleanup.Options{})

	// The default 3600s window has elapsed from TerminatedAt and has not
	// elapsed from the rewritten UpdatedAt.
	n, err := sw.Tick(context.Background(), base.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if n != 1 {
		t.Fatalf("terminated count = %d, want 1: the window counts from TerminatedAt", n)
	}
	if row, _ := store.Get(context.Background(), "acme", "sess_child"); row.State != session.StateExpired {
		t.Errorf("orphan state = %q, want expired", row.State)
	}
}

// spec: §8.10 (Orphan cleanup)
//
// A terminal root with no recorded TerminatedAt opens no cascade window, so
// its non-terminal child is left in place however old the root's UpdatedAt
// is. The child still counts in the active-orphan gauge.
func TestOrphanCleanupSkipsRootWithZeroTerminatedAt_spec_8_10(t *testing.T) {
	store := memstore.New()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedRow(t, store, sessionstore.Session{
		ID: "sess_root", TenantID: "acme", State: session.StateCompleted,
		CascadeOnFailure: session.CascadeDetach, CreatedAt: base, UpdatedAt: base,
	})
	seed(t, store, "sess_child", "sess_root", session.StateRunning, base)
	sink := &fakeMetricsSink{}
	sw := orphancleanup.New(store, orphancleanup.StaticTenants{"acme"}, orphancleanup.Options{Metrics: sink})

	n, err := sw.Tick(context.Background(), base.Add(10*time.Hour))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if n != 0 {
		t.Errorf("terminated count = %d, want 0: the root has no TerminatedAt", n)
	}
	if row, _ := store.Get(context.Background(), "acme", "sess_child"); row.State != session.StateRunning {
		t.Errorf("orphan state = %q, want running", row.State)
	}
	if got := sink.perTenant["acme"]; got != 1 {
		t.Errorf("SetOrphanTasksActivePerTenant[acme] = %d, want 1", got)
	}
}

// spec: §8.10 (Orphan cleanup)
//
// The cascade window is inclusive of its end: a pass at exactly
// cascadeTimeout after the root's TerminatedAt leaves the orphan, and a pass
// one nanosecond later expires it.
func TestOrphanCascadeWindowBoundary_spec_8_10(t *testing.T) {
	const window = 30 * time.Minute
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		at    time.Time
		state session.State
	}{
		{name: "exactly cascadeTimeout", at: base.Add(window), state: session.StateRunning},
		{name: "one nanosecond past cascadeTimeout", at: base.Add(window + time.Nanosecond), state: session.StateExpired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := memstore.New()
			seed(t, store, "sess_root", "", session.StateFailed, base)
			seed(t, store, "sess_child", "sess_root", session.StateRunning, base)
			sw := orphancleanup.New(store, orphancleanup.StaticTenants{"acme"},
				orphancleanup.Options{CascadeTimeout: window})
			if _, err := sw.Tick(context.Background(), tc.at); err != nil {
				t.Fatalf("Tick: %v", err)
			}
			if row, _ := store.Get(context.Background(), "acme", "sess_child"); row.State != tc.state {
				t.Errorf("orphan state = %q, want %q", row.State, tc.state)
			}
		})
	}
}
