// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/resultrollup"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treearchive"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treebudget"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionusage"
	"github.com/lennylabs/lenny/pkg/sessionrecord"
	"github.com/lennylabs/lenny/tests/testinfra/orphanfixture"
)

// rtTimeout is the usage quiescence timeout the return fixture runs with.
const (
	rtTimeout = 3 * time.Second
	rtSlice   = int64(1000)
	rtWait    = 5 * time.Second
)

// rtReturner records each budget return. With err set, Return fails with it.
type rtReturner struct {
	mu     sync.Mutex
	calls  []treebudget.Reservation
	err    error
	issued chan treebudget.Reservation
}

func newRTReturner() *rtReturner {
	return &rtReturner{issued: make(chan treebudget.Reservation, 64)}
}

func (r *rtReturner) Return(_ context.Context, res treebudget.Reservation) error {
	r.mu.Lock()
	r.calls = append(r.calls, res)
	err := r.err
	r.mu.Unlock()
	r.issued <- res
	return err
}

// from returns the recorded returns whose ParentSessionID is parent.
func (r *rtReturner) from(parent string) []treebudget.Reservation {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []treebudget.Reservation
	for _, c := range r.calls {
		if c.ParentSessionID == parent {
			out = append(out, c)
		}
	}
	return out
}

func (r *rtReturner) total() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// rtTimer is the usage quiescence timer seam. With auto set every request
// fires at once; otherwise a request waits until fire.
type rtTimer struct {
	auto bool

	mu        sync.Mutex
	waits     []time.Duration
	pending   []chan time.Time
	requested chan time.Duration
}

func newRTTimer(auto bool) *rtTimer {
	return &rtTimer{auto: auto, requested: make(chan time.Duration, 64)}
}

func (tm *rtTimer) after(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	tm.mu.Lock()
	tm.waits = append(tm.waits, d)
	if tm.auto {
		ch <- time.Time{}
	} else {
		tm.pending = append(tm.pending, ch)
	}
	tm.mu.Unlock()
	tm.requested <- d
	return ch
}

// fire delivers every pending request.
func (tm *rtTimer) fire() {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	for _, ch := range tm.pending {
		ch <- time.Time{}
	}
	tm.pending = nil
}

func (tm *rtTimer) requests() []time.Duration {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return append([]time.Duration(nil), tm.waits...)
}

// rtExecutor records releases. With gate set, Release signals started and
// blocks until the gate closes or its context ends.
type rtExecutor struct {
	gate    chan struct{}
	started chan string

	mu       sync.Mutex
	released []string
}

func newRTExecutor(block bool) *rtExecutor {
	e := &rtExecutor{started: make(chan string, 16)}
	if block {
		e.gate = make(chan struct{})
	}
	return e
}

func (e *rtExecutor) Send(context.Context, string, []executor.Message) (executor.Response, error) {
	return executor.Response{}, nil
}

func (e *rtExecutor) Close(ctx context.Context, id string) error { return e.Release(ctx, id, "") }

func (e *rtExecutor) Release(ctx context.Context, id string, _ executor.Disposition) error {
	e.started <- id
	if e.gate != nil {
		select {
		case <-e.gate:
		case <-ctx.Done():
		}
	}
	e.mu.Lock()
	e.released = append(e.released, id)
	e.mu.Unlock()
	return nil
}

func (e *rtExecutor) releases(id string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, r := range e.released {
		if r == id {
			n++
		}
	}
	return n
}

// rtSealer fails every seal with err, or blocks until gate closes.
type rtSealer struct {
	err     error
	gate    chan struct{}
	only    string
	entered chan string
}

func (s *rtSealer) Seal(ctx context.Context, _, id string) error {
	if s.entered != nil {
		s.entered <- id
	}
	if s.gate != nil && (s.only == "" || s.only == id) {
		select {
		case <-s.gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.err
}

// rtStore counts tenant-wide List calls and fails the Get of failGet.
type rtStore struct {
	sessionstore.Store
	mu      sync.Mutex
	lists   int
	failGet string
	failUpd string
	failLBR error
}

func (s *rtStore) List(ctx context.Context, tenantID string, f sessionstore.ListFilter) ([]sessionstore.Session, error) {
	s.mu.Lock()
	s.lists++
	s.mu.Unlock()
	return s.Store.List(ctx, tenantID, f)
}

func (s *rtStore) Get(ctx context.Context, tenantID, id string) (sessionstore.Session, error) {
	if id == s.failGet {
		return sessionstore.Session{}, errors.New("primary unavailable")
	}
	return s.Store.Get(ctx, tenantID, id)
}

func (s *rtStore) Update(ctx context.Context, tenantID, id string, fn func(*sessionstore.Session) error) (sessionstore.Session, error) {
	if id == s.failUpd {
		return sessionstore.Session{}, errors.New("store unavailable")
	}
	return s.Store.Update(ctx, tenantID, id, fn)
}

func (s *rtStore) ListByRoot(ctx context.Context, tenantID, rootID string) ([]sessionstore.Session, error) {
	if s.failLBR != nil {
		return nil, s.failLBR
	}
	return s.Store.ListByRoot(ctx, tenantID, rootID)
}

func (s *rtStore) listCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lists
}

// rtArchive fails Replay with err when set.
type rtArchive struct {
	treearchive.Store
	err error
}

func (a *rtArchive) Replay(ctx context.Context, tenantID, rootID string) ([]treearchive.ArchivedNode, error) {
	if a.err != nil {
		return nil, a.err
	}
	return a.Store.Replay(ctx, tenantID, rootID)
}

// rtTokens fails GetMany with err when set.
type rtTokens struct {
	sessionusage.Store
	err error
}

func (t *rtTokens) GetMany(ctx context.Context, tenantID string, ids []string) (map[string]sessionusage.Tokens, error) {
	if t.err != nil {
		return nil, t.err
	}
	return t.Store.GetMany(ctx, tenantID, ids)
}

// rtOpts configures the return fixture.
type rtOpts struct {
	manualTimer  bool
	blockRelease bool
	sealer       *rtSealer
	sealWindow   time.Duration
	allowance    time.Duration
	quiescence   time.Duration
	unsetTimeout bool
	childState   session.State
	interactions interactionstore.Store
}

// rtFixture is a session server over an in-memory tree root → parent →
// child, where parent and child are delegated, the child carries a granted
// token slice, and the server's budget returner, quiescence timer, clock,
// executor, and budget forget are recorders.
type rtFixture struct {
	store   *rtStore
	tokens  *rtTokens
	archive *rtArchive
	srv     *Server
	ret     *rtReturner
	timer   *rtTimer
	exec    *rtExecutor
	clock   *tcClock
	forgets *forgetRecorder
	metric  *keysExpiredCounter
}

func newRTFixture(t *testing.T, o rtOpts) *rtFixture {
	t.Helper()
	f := &rtFixture{
		store:   &rtStore{Store: memstore.New()},
		tokens:  &rtTokens{Store: sessionusage.NewMemory()},
		archive: &rtArchive{Store: treearchive.NewMemory()},
		ret:     newRTReturner(),
		timer:   newRTTimer(!o.manualTimer),
		exec:    newRTExecutor(o.blockRelease),
		clock:   &tcClock{now: time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)},
		forgets: &forgetRecorder{},
		metric:  newKeysExpiredCounter(),
	}
	quiescence := rtTimeout
	if o.quiescence != 0 {
		quiescence = o.quiescence
	}
	if o.unsetTimeout {
		quiescence = 0
	}
	opts := Options{
		DevTenantHeader:                true,
		Clock:                          f.clock.Now,
		TreeBudgetReturner:             f.ret,
		TreeArchive:                    f.archive,
		TaskUsage:                      resultrollup.New(f.store, f.tokens, f.archive, f.clock.Now),
		UsageQuiescenceTimeout:         quiescence,
		UsageQuiescenceTimer:           f.timer.after,
		Executor:                       f.exec,
		BudgetForget:                   func(id string) { f.forgets.add("forget:" + id) },
		IncDelegationBudgetKeysExpired: f.metric.inc,
		WorkspaceSealMaxDuration:       o.sealWindow,
		StreamFailureReportTimeout:     o.allowance,
	}
	if o.interactions != nil {
		opts.Interactions = o.interactions
	}
	if o.sealer != nil {
		opts.Sealer = o.sealer
		reg := podsession.NewRegistry()
		for _, id := range []string{"root", "parent", "child"} {
			reg.Put(&podsession.BindResult{SessionID: id, TenantID: "acme", CoordinationGeneration: 1 << 30})
		}
		opts.PodRegistry = reg
	}
	f.srv = New(f.store, opts)
	t.Cleanup(f.srv.Close)
	childState := o.childState
	if childState == "" {
		childState = session.StateRunning
	}
	f.seed(t, sessionstore.Session{ID: "root", State: session.StateRunning, RootSessionID: "root"})
	f.seed(t, sessionstore.Session{
		ID: "parent", State: session.StateRunning, ParentSessionID: "root", RootSessionID: "root", DelegationDepth: 1,
	})
	f.seed(t, sessionstore.Session{
		ID: "child", State: childState, ParentSessionID: "parent", RootSessionID: "root", DelegationDepth: 2,
		DelegationLease: &sessionstore.DelegationLease{MaxTokenBudget: rtSlice},
	})
	return f
}

func (f *rtFixture) seed(t *testing.T, row sessionstore.Session) {
	t.Helper()
	now := f.clock.Now()
	row.TenantID, row.RuntimeRef, row.CreatedAt, row.UpdatedAt = "acme", "echo", now, now
	if err := f.store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed %s: %v", row.ID, err)
	}
}

// cancel commits id cancelled with the Terminated fact at the server clock.
func (f *rtFixture) cancel(t *testing.T, id string) (session.State, sessionstore.Session) {
	t.Helper()
	from, row, err := orphanfixture.Cancel(context.Background(), f.store, "acme", id, f.clock.Now())
	if err != nil {
		t.Fatalf("cancel %s: %v", id, err)
	}
	return from, row
}

func (f *rtFixture) wait(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), rtWait)
	defer cancel()
	if err := f.srv.WaitTerminalWork(ctx); err != nil {
		t.Fatalf("WaitTerminalWork: %v", err)
	}
}

// awaitReturn waits for the next budget return.
func (f *rtFixture) awaitReturn(t *testing.T) treebudget.Reservation {
	t.Helper()
	select {
	case r := <-f.ret.issued:
		return r
	case <-time.After(rtWait):
		t.Fatal("no budget return was issued")
		return treebudget.Reservation{}
	}
}

func (f *rtFixture) awaitTimerRequest(t *testing.T) time.Duration {
	t.Helper()
	select {
	case d := <-f.timer.requested:
		return d
	case <-time.After(rtWait):
		t.Fatal("the return step requested no quiescence wait")
		return 0
	}
}

// childReturn is the return the child's terminal pipeline must issue when
// the subtree consumed used tokens.
func childReturn(used int64) treebudget.Reservation {
	return treebudget.Reservation{
		RootSessionID: "root", ParentSessionID: "parent",
		TreeSizeDelta: 1, TreeMemoryDelta: treebudget.PerNodeMemoryBytes, ParallelChildrenDelta: 1,
		TokenDelta: max(0, rtSlice-used),
	}
}

// assertOneChildReturn fails unless the child's pipeline issued exactly one
// return, equal to want.
func (f *rtFixture) assertOneChildReturn(t *testing.T, want treebudget.Reservation) {
	t.Helper()
	got := f.ret.from("parent")
	if len(got) != 1 {
		t.Fatalf("child budget returns = %d (%+v), want 1", len(got), got)
	}
	if got[0] != want {
		t.Errorf("child budget return = %+v, want %+v", got[0], want)
	}
}

// TestBudgetReturnOncePerTerminalWriter_spec_8_3 drives each terminal writer
// that runs the post-terminal hook on a delegated child and asserts, after
// WaitTerminalWork for the detached writers, exactly one budget return with
// the four deltas on the recorded root and exactly one forget.
//
// spec: §7.1 (Terminal pipeline context), §8.3 (Return on completion), §11.2
func TestBudgetReturnOncePerTerminalWriter_spec_8_3(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name  string
		state session.State
		drive func(t *testing.T, f *rtFixture)
	}{
		{"funnel through recordSessionCompleted", "", func(t *testing.T, f *rtFixture) {
			end := f.srv.BeginTerminal("child")
			defer end()
			from, row := f.cancel(t, "child")
			f.srv.recordSessionCompleted(ctx, from, row)
		}},
		{"cascade through cancelNodeGuarded", "", func(t *testing.T, f *rtFixture) {
			end := f.srv.BeginTerminal("parent")
			defer end()
			from, row := f.cancel(t, "parent")
			f.srv.recordSessionCompleted(ctx, from, row)
		}},
		{"cancel_session with nil signals", "", func(t *testing.T, f *rtFixture) {
			end := f.srv.BeginTerminal("child")
			defer end()
			from, row := f.cancel(t, "child")
			f.srv.AfterTerminalWrite(ctx, from, row, nil)
		}},
		{"cancel_child with the archive tail", "", func(t *testing.T, f *rtFixture) {
			end := f.srv.BeginTerminal("child")
			from, row := f.cancel(t, "child")
			go func() {
				defer end()
				f.srv.AfterTerminalWrite(context.WithoutCancel(ctx), from, row, f.srv.archiveSettledChild)
			}()
		}},
		{"OnSessionTerminal (watchdog, orphan, deadlock, admin, budget terminator)", "", func(t *testing.T, f *rtFixture) {
			end := f.srv.BeginTerminal("child")
			from, row := f.cancel(t, "child")
			f.srv.OnSessionTerminal(ctx, from, row)
			end()
		}},
		{"tree recovery failSession", session.StateResumePending, func(t *testing.T, f *rtFixture) {
			f.srv.failSession(ctx, "acme", "child")
		}},
		{"BUDGET_KEYS_EXPIRED sweep", "", func(t *testing.T, f *rtFixture) {
			f.srv.HandleBudgetKeysExpired(ctx, "acme", "root")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRTFixture(t, rtOpts{childState: tc.state})
			tc.drive(t, f)
			f.wait(t)
			f.assertOneChildReturn(t, childReturn(0))
			if n := f.forgets.count("forget:child"); n != 1 {
				t.Errorf("child forgets = %d, want 1", n)
			}
		})
	}
}

// TestBudgetReturnSkipsNonDelegatedRows_spec_8_3 asserts that the return
// step starts no wait and issues no return for a delegated child whose
// recorded root is its own id, for a depth-0 row that carries a parent, and
// for the root that the BUDGET_KEYS_EXPIRED root write fails, which still
// forgets once.
//
// spec: §8.3 (Return on completion), §8.3 (BUDGET_KEYS_EXPIRED detection and
// tree cleanup)
func TestBudgetReturnSkipsNonDelegatedRows_spec_8_3(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		row  sessionstore.Session
	}{
		{"delegated child recording itself as root", sessionstore.Session{
			ID: "self", State: session.StateRunning, ParentSessionID: "root", RootSessionID: "self", DelegationDepth: 1,
		}},
		{"depth-0 row with a parent", sessionstore.Session{
			ID: "derived", State: session.StateRunning, ParentSessionID: "root", RootSessionID: "root",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRTFixture(t, rtOpts{})
			f.seed(t, tc.row)
			end := f.srv.BeginTerminal(tc.row.ID)
			from, row := f.cancel(t, tc.row.ID)
			f.srv.AfterTerminalWrite(ctx, from, row, nil)
			end()
			f.wait(t)
			if n := f.ret.total(); n != 0 {
				t.Errorf("budget returns = %d, want 0", n)
			}
			if w := f.timer.requests(); len(w) != 0 {
				t.Errorf("quiescence waits = %v, want none: no goroutine starts", w)
			}
		})
	}
	t.Run("BUDGET_KEYS_EXPIRED root write", func(t *testing.T) {
		f := newRTFixture(t, rtOpts{})
		if !f.srv.failBudgetKeysExpired(ctx, "acme", "root") {
			t.Fatal("the root write did not apply")
		}
		f.wait(t)
		// The root's cascade cancels its delegated descendants, whose returns
		// carry their parent; the root itself has no parent to return to.
		if n := len(f.ret.from("")); n != 0 {
			t.Errorf("returns issued for the root = %d, want 0", n)
		}
		if n := f.forgets.count("forget:root"); n != 1 {
			t.Errorf("root forgets = %d, want 1", n)
		}
	})
}

// TestBudgetReturnSealRelabelRunsNoSecondReturn_spec_8_3 asserts that a seal
// failure, which relabels the row failed inside the hook, issues one return.
//
// spec: §8.3 (Return on completion), §7.1 (Seal-and-export invariant)
func TestBudgetReturnSealRelabelRunsNoSecondReturn_spec_8_3(t *testing.T) {
	f := newRTFixture(t, rtOpts{sealer: &rtSealer{err: errors.New("minio down")}, sealWindow: 2 * time.Second})
	end := f.srv.BeginTerminal("child")
	from, row := f.cancel(t, "child")
	f.srv.AfterTerminalWrite(context.Background(), from, row, nil)
	end()
	f.wait(t)
	got, _ := f.store.Get(context.Background(), "acme", "child")
	if got.State != session.StateFailed {
		t.Fatalf("child state = %q, want the seal relabel to failed", got.State)
	}
	f.assertOneChildReturn(t, childReturn(0))
}

// TestBudgetReturnTokenCredit_spec_8_3 asserts the token credit
// max(0, slice − subtree consumption): a partial spend is credited the
// remainder, a child that spent its whole slice receives zero, a zero slice
// gives zero, and an archived descendant whose live row is gone counts.
//
// spec: §8.3 (Return on completion)
func TestBudgetReturnTokenCredit_spec_8_3(t *testing.T) {
	ctx := context.Background()
	archived, _ := json.Marshal(sessionrecord.Result{Usage: &sessionrecord.Usage{InputTokens: 50, OutputTokens: 25}})
	for _, tc := range []struct {
		name    string
		prepare func(t *testing.T, f *rtFixture)
		want    int64
	}{
		{"partial spend", func(t *testing.T, f *rtFixture) {
			_ = f.tokens.Add(ctx, "acme", "child", 200, 100)
		}, 700},
		{"whole slice spent", func(t *testing.T, f *rtFixture) {
			_ = f.tokens.Add(ctx, "acme", "child", 900, 300)
		}, 0},
		{"zero slice", func(t *testing.T, f *rtFixture) {
			_, _ = f.store.Update(ctx, "acme", "child", func(r *sessionstore.Session) error {
				r.DelegationLease = nil
				return nil
			})
		}, 0},
		{"archived descendant counts", func(t *testing.T, f *rtFixture) {
			_ = f.tokens.Add(ctx, "acme", "child", 100, 0)
			_ = f.archive.Archive(ctx, treearchive.ArchivedNode{
				TenantID: "acme", RootSessionID: "root", NodeSessionID: "gone", ParentSessionID: "child",
				State: string(session.StateCompleted), Result: string(archived),
			})
		}, 825},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRTFixture(t, rtOpts{})
			tc.prepare(t, f)
			end := f.srv.BeginTerminal("child")
			from, row := f.cancel(t, "child")
			f.srv.AfterTerminalWrite(ctx, from, row, nil)
			end()
			want := childReturn(0)
			want.TokenDelta = tc.want
			f.assertOneChildReturn(t, want)
		})
	}
}

// TestBudgetReturnSubtreeReadErrorCreditsNoTokens_spec_8_3 asserts that a
// ListByRoot, Replay, or GetMany error in the subtree token read, a deadline
// error included, gives a zero TokenDelta while the other three deltas are
// still returned.
//
// spec: §8.3 (Return on completion), §8.3 (Usage quiescence before budget
// return)
func TestBudgetReturnSubtreeReadErrorCreditsNoTokens_spec_8_3(t *testing.T) {
	for _, tc := range []struct {
		name string
		arm  func(f *rtFixture)
	}{
		{"ListByRoot", func(f *rtFixture) { f.store.failLBR = errors.New("primary unavailable") }},
		{"ListByRoot deadline", func(f *rtFixture) { f.store.failLBR = context.DeadlineExceeded }},
		{"Replay", func(f *rtFixture) { f.archive.err = errors.New("archive unavailable") }},
		{"GetMany", func(f *rtFixture) { f.tokens.err = context.DeadlineExceeded }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRTFixture(t, rtOpts{})
			end := f.srv.BeginTerminal("child")
			from, row := f.cancel(t, "child")
			tc.arm(f)
			f.srv.AfterTerminalWrite(context.Background(), from, row, nil)
			end()
			want := childReturn(0)
			want.TokenDelta = 0
			f.assertOneChildReturn(t, want)
		})
	}
}

// TestBudgetReturnReleaseDoesNotWait_spec_8_3 asserts that with the return
// step's wait held on its timer seam the executor release starts, and that
// when the seam fires while that release still blocks, the return with the
// four deltas is issued before the release returns.
//
// spec: §7.1 (Terminal pipeline context), §8.3 (Usage quiescence before
// budget return)
func TestBudgetReturnReleaseDoesNotWait_spec_8_3(t *testing.T) {
	f := newRTFixture(t, rtOpts{manualTimer: true, blockRelease: true})
	end := f.srv.BeginTerminal("child")
	from, row := f.cancel(t, "child")
	f.srv.OnSessionTerminal(context.Background(), from, row)
	end()
	f.awaitTimerRequest(t)
	select {
	case <-f.exec.started:
	case <-time.After(rtWait):
		t.Fatal("the executor release did not start while the quiescence wait was held")
	}
	if n := f.ret.total(); n != 0 {
		t.Fatalf("returns before the seam fired = %d, want 0", n)
	}
	f.timer.fire()
	if got := f.awaitReturn(t); got != childReturn(0) {
		t.Errorf("return = %+v, want %+v", got, childReturn(0))
	}
	if n := f.exec.releases("child"); n != 0 {
		t.Errorf("the release returned before the gate opened (%d)", n)
	}
	close(f.exec.gate)
	f.wait(t)
	f.assertOneChildReturn(t, childReturn(0))
}

// TestBudgetReturnDoesNotWaitForTheSeal_spec_8_3 asserts that with a sealer
// that blocks until the test releases it, the seam firing issues the return
// while the seal is still blocked.
//
// spec: §8.3 (Usage quiescence before budget return), §7.1 (Terminal
// pipeline context)
func TestBudgetReturnDoesNotWaitForTheSeal_spec_8_3(t *testing.T) {
	sealer := &rtSealer{gate: make(chan struct{}), entered: make(chan string, 4)}
	f := newRTFixture(t, rtOpts{manualTimer: true, sealer: sealer})
	end := f.srv.BeginTerminal("child")
	from, row := f.cancel(t, "child")
	f.srv.OnSessionTerminal(context.Background(), from, row)
	end()
	f.awaitTimerRequest(t)
	select {
	case <-sealer.entered:
	case <-time.After(rtWait):
		t.Fatal("the seal did not start")
	}
	f.timer.fire()
	if got := f.awaitReturn(t); got != childReturn(0) {
		t.Errorf("return = %+v, want %+v", got, childReturn(0))
	}
	close(sealer.gate)
	f.wait(t)
}

// TestBudgetReturnWaitIsMeasuredFromTerminatedAt_spec_8_3 asserts the wait
// the return step requests: the whole timeout at the terminal instant, the
// remainder when part has elapsed, none when the hook starts after
// TerminatedAt plus the timeout, the whole timeout from the goroutine's start
// for a zero TerminatedAt, and 5 s for a zero Options.UsageQuiescenceTimeout.
//
// spec: §8.3 (Usage quiescence before budget return), §11.3
func TestBudgetReturnWaitIsMeasuredFromTerminatedAt_spec_8_3(t *testing.T) {
	for _, tc := range []struct {
		name    string
		unset   bool
		elapsed time.Duration
		zero    bool
		want    []time.Duration
	}{
		{"at the terminal instant", false, 0, false, []time.Duration{rtTimeout}},
		{"part elapsed", false, time.Second, false, []time.Duration{rtTimeout - time.Second}},
		{"after the deadline", false, rtTimeout + time.Second, false, nil},
		{"zero TerminatedAt", false, time.Hour, true, []time.Duration{rtTimeout}},
		{"zero timeout selects 5 s", true, 0, false, []time.Duration{DefaultUsageQuiescenceTimeout}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRTFixture(t, rtOpts{unsetTimeout: tc.unset})
			end := f.srv.BeginTerminal("child")
			from, row := f.cancel(t, "child")
			if tc.zero {
				row.TerminatedAt = time.Time{}
			}
			f.clock.advance(tc.elapsed)
			f.srv.AfterTerminalWrite(context.Background(), from, row, nil)
			end()
			f.assertOneChildReturn(t, childReturn(0))
			got := f.timer.requests()
			if len(got) != len(tc.want) || (len(got) == 1 && got[0] != tc.want[0]) {
				t.Errorf("quiescence waits = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestBudgetReturnContextEndDuringWait_spec_8_3 asserts that a terminal
// context that ends during the wait issues no return while the release and
// the forget still run, and that a new Server over the same stores issues
// none for that child, so its reservation stays counted.
//
// spec: §7.1 (Terminal pipeline context), §8.3 (Usage quiescence before
// budget return)
func TestBudgetReturnContextEndDuringWait_spec_8_3(t *testing.T) {
	f := newRTFixture(t, rtOpts{
		manualTimer: true, sealWindow: 100 * time.Millisecond, allowance: 100 * time.Millisecond,
	})
	end := f.srv.BeginTerminal("child")
	from, row := f.cancel(t, "child")
	f.srv.AfterTerminalWrite(context.Background(), from, row, nil)
	end()
	if n := f.ret.total(); n != 0 {
		t.Errorf("returns = %d after the terminal context ended, want 0", n)
	}
	if n := f.exec.releases("child"); n != 1 {
		t.Errorf("releases = %d, want 1", n)
	}
	if n := f.forgets.count("forget:child"); n != 1 {
		t.Errorf("forgets = %d, want 1", n)
	}
	next := New(f.store, Options{
		DevTenantHeader: true, TreeBudgetReturner: f.ret, TreeArchive: f.archive,
		UsageQuiescenceTimer: newRTTimer(true).after,
	})
	defer next.Close()
	if err := next.WaitTerminalWork(context.Background()); err != nil {
		t.Fatalf("WaitTerminalWork: %v", err)
	}
	if n := f.ret.total(); n != 0 {
		t.Errorf("a new server issued %d returns for the child, want 0", n)
	}
}

// TestDetachedWritersHoldTheHookClaim_spec_7_1 asserts the hook dispatch's
// claim hand-off: with the return step's wait held, OnSessionTerminal on a
// delegated child (whose caller ends its own claim when OnSessionTerminal
// returns) and failSession on a resume_pending delegated child each return,
// and WaitTerminalWork returns only after the hook, the joined return
// included, ends; a failSession on a row that is not resume_pending, and one
// whose store Update fails, each leave OpenTerminalWork at 0 when it returns.
//
// spec: §7.1 (Terminal pipeline context), §10.1.7 (Terminal pipelines at
// process exit.), §8.10 (Delegation Tree Recovery)
func TestDetachedWritersHoldTheHookClaim_spec_7_1(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name  string
		state session.State
		drive func(f *rtFixture)
	}{
		{"OnSessionTerminal", "", func(f *rtFixture) {
			end := f.srv.BeginTerminal("child")
			from, row := f.cancel(t, "child")
			f.srv.OnSessionTerminal(ctx, from, row)
			end()
		}},
		{"failSession", session.StateResumePending, func(f *rtFixture) {
			f.srv.failSession(ctx, "acme", "child")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRTFixture(t, rtOpts{manualTimer: true, childState: tc.state})
			tc.drive(f)
			f.awaitTimerRequest(t)
			if n := f.srv.OpenTerminalWork(); n != 1 {
				t.Errorf("OpenTerminalWork while the return waits = %d, want 1", n)
			}
			short, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
			defer cancel()
			if err := f.srv.WaitTerminalWork(short); !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("WaitTerminalWork while the return waits = %v, want DeadlineExceeded", err)
			}
			f.timer.fire()
			f.wait(t)
			f.assertOneChildReturn(t, childReturn(0))
		})
	}
	t.Run("failSession refused", func(t *testing.T) {
		f := newRTFixture(t, rtOpts{manualTimer: true})
		f.srv.failSession(ctx, "acme", "child")
		if n := f.srv.OpenTerminalWork(); n != 0 {
			t.Errorf("OpenTerminalWork = %d, want 0", n)
		}
	})
	t.Run("failSession store error", func(t *testing.T) {
		f := newRTFixture(t, rtOpts{manualTimer: true, childState: session.StateResumePending})
		f.store.failUpd = "child"
		f.srv.failSession(ctx, "acme", "child")
		if n := f.srv.OpenTerminalWork(); n != 0 {
			t.Errorf("OpenTerminalWork = %d, want 0", n)
		}
	})
}

// TestBudgetKeysExpiredReturnsWhileTheRootSeals_spec_8_3 asserts that with
// the root's seal held on its sealer, HandleBudgetKeysExpired returns with
// every descendant cancelled, and WaitTerminalWork returns only after the
// root's hook ends.
//
// spec: §8.3 (BUDGET_KEYS_EXPIRED detection and tree cleanup), §7.1
func TestBudgetKeysExpiredReturnsWhileTheRootSeals_spec_8_3(t *testing.T) {
	sealer := &rtSealer{gate: make(chan struct{}), only: "root"}
	f := newRTFixture(t, rtOpts{sealer: sealer})
	ctx := context.Background()
	f.srv.HandleBudgetKeysExpired(ctx, "acme", "root")
	for _, id := range []string{"parent", "child"} {
		if got, _ := f.store.Get(ctx, "acme", id); got.State != session.StateCancelled {
			t.Errorf("%s state = %q when the handler returned, want cancelled", id, got.State)
		}
	}
	short, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	if err := f.srv.WaitTerminalWork(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("WaitTerminalWork while the root seals = %v, want DeadlineExceeded", err)
	}
	close(sealer.gate)
	f.wait(t)
}

// TestBudgetReturnFailureStillForgetsAndExpires_spec_8_3 asserts that a
// return failing with ErrBudgetUnavailable still runs the forget and the
// elicitation expiry.
//
// spec: §8.3 (Return on completion), §9.2 (Elicitation Chain), §11.2
func TestBudgetReturnFailureStillForgetsAndExpires_spec_8_3(t *testing.T) {
	inter := &failingExpiry{Store: interactionstore.NewMemory()}
	f := newRTFixture(t, rtOpts{interactions: inter})
	f.ret.err = treebudget.ErrBudgetUnavailable
	end := f.srv.BeginTerminal("child")
	from, row := f.cancel(t, "child")
	f.srv.AfterTerminalWrite(context.Background(), from, row, nil)
	end()
	f.assertOneChildReturn(t, childReturn(0))
	if n := f.forgets.count("forget:child"); n != 1 {
		t.Errorf("forgets = %d, want 1", n)
	}
	if inter.calls != 1 {
		t.Errorf("ExpireRaisedBy calls = %d, want 1", inter.calls)
	}
}

// TestBudgetReturnListsNoTenant_spec_8_3 asserts that the return issues no
// tenant-wide List: the subtree read lists only the recorded root's tree.
//
// spec: §8.3 (Return on completion)
func TestBudgetReturnListsNoTenant_spec_8_3(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	end := f.srv.BeginTerminal("child")
	from, row := f.cancel(t, "child")
	before := f.store.listCalls()
	f.srv.AfterTerminalWrite(context.Background(), from, row, nil)
	end()
	f.assertOneChildReturn(t, childReturn(0))
	if n := f.store.listCalls() - before; n != 0 {
		t.Errorf("tenant-wide List calls = %d, want 0", n)
	}
}

// TestBudgetReturnKeysExpiredIsNotADetection_spec_8_3 asserts that a return
// meeting ErrBudgetKeysExpired, under a terminal root after cleanup and under
// a live root before Redis recovery restores the tree, calls no cleanup,
// leaves the detection metric unchanged, and fails no session.
//
// spec: §8.3 (BUDGET_KEYS_EXPIRED detection and tree cleanup), §16.1
func TestBudgetReturnKeysExpiredIsNotADetection_spec_8_3(t *testing.T) {
	ctx := context.Background()
	for _, rootState := range []session.State{session.StateCompleted, session.StateRunning} {
		t.Run(string(rootState), func(t *testing.T) {
			f := newRTFixture(t, rtOpts{})
			_, _ = f.store.Update(ctx, "acme", "root", func(r *sessionstore.Session) error {
				r.State = rootState
				return nil
			})
			f.ret.err = treebudget.ErrBudgetKeysExpired
			end := f.srv.BeginTerminal("child")
			from, row := f.cancel(t, "child")
			f.srv.AfterTerminalWrite(ctx, from, row, nil)
			end()
			f.wait(t)
			f.assertOneChildReturn(t, childReturn(0))
			if n := f.metric.total(); n != 0 {
				t.Errorf("detections counted = %d, want 0", n)
			}
			for id, want := range map[string]session.State{"root": rootState, "parent": session.StateRunning} {
				if got, _ := f.store.Get(ctx, "acme", id); got.State != want {
					t.Errorf("%s state = %q, want %q", id, got.State, want)
				}
			}
		})
	}
}

// TestBudgetReturnAddressesTheRecordedRoot_spec_8_3 asserts that in a tree
// where the child's parent is not the root and the parent row cannot be
// read, the return addresses the recorded root's keys and the archive row is
// written under the recorded root. Walking the parent chain stops at the
// child when the parent read fails, so an archive keyed by that walk records
// the child as its own root.
//
// spec: §8.3 (Return on completion), §8.10
func TestBudgetReturnAddressesTheRecordedRoot_spec_8_3(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	f.store.failGet = "parent"
	end := f.srv.BeginTerminal("child")
	from, row := f.cancel(t, "child")
	f.srv.AfterTerminalWrite(context.Background(), from, row, f.srv.archiveSettledChild)
	end()
	f.assertOneChildReturn(t, childReturn(0))
	node, err := f.archive.GetByNode(context.Background(), "acme", "child")
	if err != nil {
		t.Fatalf("archive GetByNode: %v", err)
	}
	if node.RootSessionID != "root" {
		t.Errorf("archive RootSessionID = %q, want root", node.RootSessionID)
	}
}

// TestUsageQuiescenceTimeoutFor_spec_11_3 asserts the resolution of the
// configured timeout: a positive value is kept and a non-positive value
// selects the §11.3 default of 5 s.
//
// spec: §11.3, §8.3 (Usage quiescence before budget return)
func TestUsageQuiescenceTimeoutFor_spec_11_3(t *testing.T) {
	for in, want := range map[time.Duration]time.Duration{
		0: 5 * time.Second, -time.Second: 5 * time.Second, 2 * time.Second: 2 * time.Second,
	} {
		if got := UsageQuiescenceTimeoutFor(in); got != want {
			t.Errorf("UsageQuiescenceTimeoutFor(%s) = %s, want %s", in, got, want)
		}
	}
}
