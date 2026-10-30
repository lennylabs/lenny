// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// The terminal pipeline budget the fixture runs with: a 300 s seal window
// plus the 30 s release allowance.
const (
	tcSealWindow     = 300 * time.Second
	tcAllowance      = 30 * time.Second
	tcBudget         = tcSealWindow + tcAllowance
	tcDeadlineSlack  = 2 * time.Second
	tcCutRemaining   = 200 * time.Millisecond
	tcPipelineReturn = 10 * time.Second
)

// tcClock is a settable server clock.
type tcClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *tcClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *tcClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// tcStore wraps the session store. On the first terminal Update that
// commits, it runs onTerminal with the returned row, so a scenario can move
// the server clock or restamp the row the writer hands the hook, which sets
// where the hook entry falls relative to TerminatedAt.
type tcStore struct {
	sessionstore.Store
	mu         sync.Mutex
	fired      bool
	onTerminal func(*sessionstore.Session)
}

func (s *tcStore) Update(ctx context.Context, tenantID, id string, mutate func(*sessionstore.Session) error) (sessionstore.Session, error) {
	out, err := s.Store.Update(ctx, tenantID, id, mutate)
	if err != nil || !session.IsTerminal(out.State) {
		return out, err
	}
	s.mu.Lock()
	first := !s.fired
	s.fired = true
	s.mu.Unlock()
	if first && s.onTerminal != nil {
		s.onTerminal(&out)
	}
	return out, err
}

// tcSealer observes the context the seal step receives. On entry it cancels
// the writer's context, then records whether its own context is still live,
// so a seal context tied to the writer shows up as cancelled. With block
// set it waits until its context ends, as a seal that never returns.
type tcSealer struct {
	cancelWriter context.CancelFunc
	block        bool

	mu              sync.Mutex
	calls           int
	deadline        time.Time
	liveAfterCancel bool
}

func (s *tcSealer) Seal(ctx context.Context, _, _ string) error {
	if s.cancelWriter != nil {
		s.cancelWriter()
	}
	dl, _ := ctx.Deadline()
	s.mu.Lock()
	s.calls++
	if s.calls == 1 {
		s.deadline = dl
		s.liveAfterCancel = ctx.Err() == nil
	}
	s.mu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if s.block {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

// tcExecutor records each release and the deadline of its context.
type tcExecutor struct {
	mu       sync.Mutex
	releases int
	deadline time.Time
	expired  bool
}

func (e *tcExecutor) Send(context.Context, string, []executor.Message) (executor.Response, error) {
	return executor.Response{}, nil
}

func (e *tcExecutor) Close(ctx context.Context, sessionID string) error {
	return e.Release(ctx, sessionID, "")
}

func (e *tcExecutor) Release(ctx context.Context, _ string, _ executor.Disposition) error {
	dl, _ := ctx.Deadline()
	e.mu.Lock()
	defer e.mu.Unlock()
	e.releases++
	e.deadline = dl
	e.expired = ctx.Err() != nil
	return nil
}

// tcLogHook records the deadline of the session-log close's context.
type tcLogHook struct {
	mu       sync.Mutex
	called   bool
	deadline time.Time
}

func (h *tcLogHook) OnSessionTerminal(ctx context.Context, _, _ string, _ []byte, _ bool) error {
	dl, _ := ctx.Deadline()
	h.mu.Lock()
	defer h.mu.Unlock()
	h.called = true
	h.deadline = dl
	return nil
}

// tcOutcomes records the seal outcome labels.
type tcOutcomes struct {
	mu       sync.Mutex
	outcomes []string
}

func (o *tcOutcomes) observe(_, outcome string, _ float64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.outcomes = append(o.outcomes, outcome)
}

func (o *tcOutcomes) list() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.outcomes...)
}

// tcFixture is a Server wired with the recorders above and a settable clock.
type tcFixture struct {
	srv      *Server
	store    *tcStore
	clock    *tcClock
	sealer   *tcSealer
	exec     *tcExecutor
	logHook  *tcLogHook
	outcomes *tcOutcomes
}

func newTCFixture(t *testing.T, st session.State, cancelWriter context.CancelFunc, block bool) *tcFixture {
	t.Helper()
	f := &tcFixture{
		store:    &tcStore{Store: memstore.New()},
		clock:    &tcClock{now: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)},
		sealer:   &tcSealer{cancelWriter: cancelWriter, block: block},
		exec:     &tcExecutor{},
		logHook:  &tcLogHook{},
		outcomes: &tcOutcomes{},
	}
	f.srv = New(f.store, Options{
		DevTenantHeader:              true,
		Events:                       sessionevents.NewBus(64),
		Sealer:                       f.sealer,
		PodRegistry:                  sealBoundRegistry("s"),
		Executor:                     f.exec,
		SessionLogHook:               f.logHook,
		ObserveWorkspaceSealDuration: f.outcomes.observe,
		Clock:                        f.clock.Now,
		WorkspaceSealMaxDuration:     tcSealWindow,
		StreamFailureReportTimeout:   tcAllowance,
	})
	t.Cleanup(f.srv.Close)
	if err := f.store.Create(context.Background(), sessionstore.Session{
		ID: "s", TenantID: "acme", RuntimeRef: "echo", State: st,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return f
}

// tcDriver makes one terminal write on ctx, the writer's context, and runs
// the hook entry that writer reaches.
type tcDriver func(ctx context.Context, f *tcFixture)

// restDriver drives a REST terminal writer with ctx as the request context.
func restDriver(method, path string) tcDriver {
	return func(ctx context.Context, f *tcFixture) {
		req := httptest.NewRequest(method, path, nil).WithContext(ctx)
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		asOwner(f.srv.Handler()).ServeHTTP(httptest.NewRecorder(), req)
	}
}

// onSessionTerminalDriver stands in for a writer outside this package that
// runs the hook through OnSessionTerminal after its terminal Update.
func onSessionTerminalDriver(to session.State) tcDriver {
	return func(ctx context.Context, f *tcFixture) {
		from, updated, err := f.srv.writeTerminal(ctx, "acme", "s", to, nonTerminalGuard)
		if err != nil {
			return
		}
		f.srv.OnSessionTerminal(ctx, from, updated)
	}
}

// afterTerminalWriteDriver stands in for a writer outside this package that
// runs the hook through AfterTerminalWrite after its terminal Update.
func afterTerminalWriteDriver(ctx context.Context, f *tcFixture) {
	from, updated, err := f.srv.writeTerminal(ctx, "acme", "s", session.StateCancelled, nonTerminalGuard)
	if err != nil {
		return
	}
	f.srv.AfterTerminalWrite(ctx, from, updated, nil)
}

// tcWriter is one terminal writer of the inventory and the hook entry it
// reaches. A nil drive marks a writer that runs no post-terminal hook, and
// noHook states why. seed is the state the row starts in, for a writer whose
// guard admits only one state; empty seeds running.
type tcWriter struct {
	key    string
	drive  tcDriver
	noHook string
	seed   session.State
}

// terminalContextWriters covers every row of the terminal writer inventory.
// A writer in this package is driven through its own path; a writer outside
// it is driven through the hook entry it calls, which is where the terminal
// context is derived.
var terminalContextWriters = []tcWriter{
	{key: "pkg/gateway/sessionserver/session_transitions.go handleDelete", drive: restDriver(http.MethodDelete, "/v1/sessions/s")},
	{key: "pkg/gateway/sessionserver/session_transitions.go transitionTerminate", drive: restDriver(http.MethodPost, "/v1/sessions/s/terminate")},
	{key: "pkg/gateway/sessionserver/failure.go transitionToFailed", drive: func(ctx context.Context, f *tcFixture) {
		_, _ = f.srv.ReportSessionFailure(ctx, FailureReport{
			TenantID: "acme", SessionID: "s", Reason: string(session.FailureWorkspaceValidationFailed),
		})
	}},
	{key: "pkg/gateway/sessionserver/session_terminal.go writeTerminal", drive: func(ctx context.Context, f *tcFixture) {
		f.srv.failSession(ctx, "acme", "s")
	}, seed: session.StateResumePending},
	{key: "pkg/gateway/sessionserver/derive.go persistDeriveFailure", noHook: "a derive failure row is created terminal and never ran"},
	{key: "pkg/gateway/sessionserver/seal.go failWorkspaceSeal", noHook: "the relabel runs inside the hook"},
	{key: "pkg/gateway/runtime/watchdog/watchdog.go failPreRunning", drive: onSessionTerminalDriver(session.StateFailed)},
	{key: "pkg/gateway/runtime/watchdog/watchdog.go expireAwaiting", drive: onSessionTerminalDriver(session.StateExpired)},
	{key: "pkg/gateway/runtime/watchdog/watchdog.go expireIdle", drive: onSessionTerminalDriver(session.StateExpired)},
	{key: "pkg/gateway/runtime/watchdog/suspended.go expireSuspendedIfDue", drive: onSessionTerminalDriver(session.StateExpired)},
	{key: "pkg/gateway/runtime/watchdog/watchdog.go expireMaxAge", drive: onSessionTerminalDriver(session.StateExpired)},
	{key: "pkg/gateway/session/orphansession/orphansession.go failOrphan", drive: onSessionTerminalDriver(session.StateFailed)},
	{key: "pkg/gateway/mcpfabric/delegationtree/orphancleanup/orphancleanup.go endOrphan", drive: onSessionTerminalDriver(session.StateExpired)},
	{key: "pkg/gateway/mcpfabric/mcptools/mcptools.go cancelNode", drive: afterTerminalWriteDriver},
	{key: "pkg/gateway/mcpfabric/mcptools/mcptools_register.go cancelSession", drive: afterTerminalWriteDriver},
	{key: "pkg/gateway/externalapi/admin/users.go cancelForRevoke", noHook: "the full_revoke write runs AfterRevokeWrite and the revoked-binding release"},
	{key: "pkg/gateway/environment/translator/openai_chat.go handleCreateCompletion", noHook: "the translator writes on the none path"},
	{key: "pkg/gateway/environment/translator/open_responses.go serveCold", noHook: "the translator writes on the none path"},
	{key: "pkg/gateway/environment/translator/open_responses.go endHeldSession", drive: afterTerminalWriteDriver},
	{key: "cmd/lenny-gateway/session_admin.go ForceTerminate", drive: onSessionTerminalDriver(session.StateFailed)},
	{key: "cmd/lenny-gateway/session_budget.go terminate", drive: onSessionTerminalDriver(session.StateExpired)},
	{key: "cmd/lenny-gateway/reconciler_workers.go deadlockFail", drive: onSessionTerminalDriver(session.StateFailed)},
}

// TerminalContextWriterKeys returns the inventory keys the terminal context
// table covers, so the inventory test in the external test package asserts
// the table and the terminal writer inventory name the same writers.
func TerminalContextWriterKeys() []string {
	out := make([]string, 0, len(terminalContextWriters))
	for _, w := range terminalContextWriters {
		out = append(out, w.key)
	}
	sort.Strings(out)
	return out
}

// tcScenario places the hook entry relative to the row's TerminatedAt.
type tcScenario struct {
	name string
	// atTerminal runs on the first terminal Update: it moves the clock or
	// restamps the returned row.
	atTerminal func(c *tcClock, row *sessionstore.Session)
	// remaining is the budget left at the hook entry; negative means
	// already expired.
	remaining time.Duration
	block     bool
}

var tcScenarios = []tcScenario{
	{
		name:       "terminated 10s before the hook entry",
		atTerminal: func(c *tcClock, _ *sessionstore.Session) { c.advance(10 * time.Second) },
		remaining:  tcBudget - 10*time.Second,
	},
	{
		name:       "zero TerminatedAt",
		atTerminal: func(_ *tcClock, row *sessionstore.Session) { row.TerminatedAt = time.Time{} },
		remaining:  tcBudget,
	},
	{
		name: "TerminatedAt after the server clock",
		atTerminal: func(c *tcClock, row *sessionstore.Session) {
			row.TerminatedAt = c.Now().Add(time.Minute)
		},
		remaining: tcBudget,
	},
	{
		name:       "terminated more than the budget before the hook entry",
		atTerminal: func(c *tcClock, _ *sessionstore.Session) { c.advance(tcBudget + 5*time.Second) },
		remaining:  -5 * time.Second,
	},
	{
		name:       "seal that never returns is cut at the budget",
		atTerminal: func(c *tcClock, _ *sessionstore.Session) { c.advance(tcBudget - tcCutRemaining) },
		remaining:  tcCutRemaining,
		block:      true,
	},
}

// TestTerminalPipelineContextEveryWriter_spec_7_1 drives every hook-path
// terminal writer of the inventory under each placement of the hook entry
// relative to the row's TerminatedAt, and cancels the writer's context as
// the seal starts. It asserts that the seal, the release, and the
// session-log close run on one context the gateway owns: the context stays
// live after the writer's context is cancelled, its deadline is TerminatedAt
// plus the terminal pipeline budget on the server clock (the whole budget
// for a zero or future TerminatedAt), the release and the session-log close
// share it, a context already past the budget records a timeout outcome and
// still releases once, and a seal that never returns is cut at the budget
// with a timeout outcome and the release still runs.
//
// spec: 7.1 (Normal Flow), 7.1 (Terminal pipeline context), 7.2
// (Interactive Session Model)
func TestTerminalPipelineContextEveryWriter_spec_7_1(t *testing.T) {
	for _, w := range terminalContextWriters {
		t.Run(w.key, func(t *testing.T) {
			if w.drive == nil {
				t.Skipf("no post-terminal hook: %s", w.noHook)
			}
			for _, sc := range tcScenarios {
				t.Run(sc.name, func(t *testing.T) {
					runTerminalContextScenario(t, w.seedState(), w.drive, sc)
				})
			}
		})
	}
}

// seedState is the state the writer's row starts in.
func (w tcWriter) seedState() session.State {
	if w.seed == "" {
		return session.StateRunning
	}
	return w.seed
}

func runTerminalContextScenario(t *testing.T, seed session.State, drive tcDriver, sc tcScenario) {
	writerCtx, cancelWriter := context.WithCancel(context.Background())
	defer cancelWriter()
	f := newTCFixture(t, seed, cancelWriter, sc.block)
	f.store.onTerminal = func(row *sessionstore.Session) { sc.atTerminal(f.clock, row) }

	entry := time.Now()
	done := make(chan struct{})
	go func() {
		defer close(done)
		drive(writerCtx, f)
		// spec: §7.1 (Terminal pipeline context) — a detached writer's hook
		// runs on its own goroutine under its terminal claim.
		_ = f.srv.WaitTerminalWork(context.Background())
	}()
	select {
	case <-done:
	case <-time.After(tcPipelineReturn):
		t.Fatal("the terminal pipeline did not return; the budget did not bound it")
	}
	assertTerminalContext(t, f, entry, sc)
}

func assertTerminalContext(t *testing.T, f *tcFixture, entry time.Time, sc tcScenario) {
	t.Helper()
	f.exec.mu.Lock()
	releases, releaseDL, releaseExpired := f.exec.releases, f.exec.deadline, f.exec.expired
	f.exec.mu.Unlock()
	f.sealer.mu.Lock()
	sealCalls, sealDL, live := f.sealer.calls, f.sealer.deadline, f.sealer.liveAfterCancel
	f.sealer.mu.Unlock()

	if releases != 1 {
		t.Fatalf("releases = %d, want 1", releases)
	}
	if sealCalls == 0 {
		t.Fatal("the seal step did not run")
	}
	if releaseDL.IsZero() {
		t.Fatal("the release context carries no deadline; the terminal pipeline must be bounded")
	}
	wantDL := entry.Add(sc.remaining)
	if d := releaseDL.Sub(wantDL); d < -tcDeadlineSlack || d > tcDeadlineSlack {
		t.Errorf("release deadline = entry%+v, want entry%+v (TerminatedAt plus the budget on the server clock)",
			releaseDL.Sub(entry), sc.remaining)
	}
	if sc.remaining > 0 {
		if !live {
			t.Error("the seal context ended when the writer's context was cancelled; it must be detached")
		}
		wantSeal := entry.Add(min(sc.remaining, tcSealWindow))
		if d := sealDL.Sub(wantSeal); d < -tcDeadlineSlack || d > tcDeadlineSlack {
			t.Errorf("seal deadline = entry%+v, want entry%+v", sealDL.Sub(entry), min(sc.remaining, tcSealWindow))
		}
	}
	if sc.remaining <= tcCutRemaining {
		if got := f.outcomes.list(); len(got) != 1 || got[0] != sealOutcomeTimeout {
			t.Errorf("seal outcomes = %v, want [timeout]", got)
		}
		if !releaseExpired {
			t.Error("the release context is live past the budget")
		}
	}
	f.logHook.mu.Lock()
	logCalled, logDL := f.logHook.called, f.logHook.deadline
	f.logHook.mu.Unlock()
	if logCalled && !logDL.Equal(releaseDL) {
		t.Errorf("session-log close deadline %v differs from the release deadline %v; they must share one context", logDL, releaseDL)
	}
	// spec: 7.1 (Post-terminal window) — the binding was held, so the
	// release is recorded on the row, an already-expired context included.
	if row, err := f.store.Get(context.Background(), "acme", "s"); err != nil || row.PodReleasedAt.IsZero() {
		t.Errorf("PodReleasedAt = %v (%v), want the release recorded", row.PodReleasedAt, err)
	}
}

// TestTerminalPipelineBudgetFor_spec_7_1 pins the one computation of the
// terminal pipeline budget: the seal window plus the release allowance, with
// a non-positive allowance falling back to the stream-failure report
// default, and the server's budget taking the same value.
//
// spec: 7.1 (Terminal pipeline context)
func TestTerminalPipelineBudgetFor_spec_7_1(t *testing.T) {
	for _, tc := range []struct {
		seal, allowance, want time.Duration
	}{
		{300 * time.Second, 30 * time.Second, 330 * time.Second},
		{580 * time.Second, 31 * time.Second, 611 * time.Second},
		{300 * time.Second, 0, 300*time.Second + DefaultStreamFailureReportTimeout},
		{300 * time.Second, -time.Second, 300*time.Second + DefaultStreamFailureReportTimeout},
	} {
		if got := TerminalPipelineBudgetFor(tc.seal, tc.allowance); got != tc.want {
			t.Errorf("TerminalPipelineBudgetFor(%v, %v) = %v, want %v", tc.seal, tc.allowance, got, tc.want)
		}
	}
	srv := New(memstore.New(), Options{WorkspaceSealMaxDuration: 120 * time.Second, StreamFailureReportTimeout: 15 * time.Second})
	defer srv.Close()
	if got := srv.TerminalPipelineBudget(); got != 135*time.Second {
		t.Errorf("server budget = %v, want 135s", got)
	}
	defaults := New(memstore.New(), Options{})
	defer defaults.Close()
	if got, want := defaults.TerminalPipelineBudget(), DefaultWorkspaceSealMaxDuration+DefaultStreamFailureReportTimeout; got != want {
		t.Errorf("default server budget = %v, want %v", got, want)
	}
}

// ctxFailingStore fails an Update whose context has ended, as a Postgres
// transaction on a cancelled context does.
type ctxFailingStore struct {
	sessionstore.Store
}

func (c ctxFailingStore) Update(ctx context.Context, tenantID, id string, mutate func(*sessionstore.Session) error) (sessionstore.Session, error) {
	if err := ctx.Err(); err != nil {
		return sessionstore.Session{}, err
	}
	return c.Store.Update(ctx, tenantID, id, mutate)
}

// TestFailFinalizingCommitsOnCancelledRequest_spec_7_1 asserts that each of
// the finalize failure exits commits `failed` when the request context is
// cancelled before the write: the gateway makes the finalizing → failed
// write on a context it owns. Against a write on the request context, the
// Update fails and the row stays finalizing with no later writer.
//
// spec: 7.1 (Gateway-owned terminal writes), 15.1 (REST API), 6.2 (Pod State
// Machine)
func TestFailFinalizingCommitsOnCancelledRequest_spec_7_1(t *testing.T) {
	cause := errors.New("prepare failed")
	for _, tc := range []struct {
		name string
		exit func(srv *Server, w http.ResponseWriter, r *http.Request)
	}{
		{"plan parse failure", func(srv *Server, w http.ResponseWriter, r *http.Request) {
			srv.finalizePlanParseFailed(w, r, "acme", "s", "", "", cause)
		}},
		{"prepare failure", func(srv *Server, w http.ResponseWriter, r *http.Request) {
			srv.finalizePrepareFailed(w, r, "acme", "s", cause)
		}},
		{"ready write failure", func(srv *Server, w http.ResponseWriter, r *http.Request) {
			srv.finalizeReadyWriteFailed(w, r, "acme", "s", cause)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inner := memstore.New()
			if err := inner.Create(context.Background(), sessionstore.Session{
				ID: "s", TenantID: "acme", RuntimeRef: "echo", State: session.StateFinalizing,
			}); err != nil {
				t.Fatalf("seed: %v", err)
			}
			bus := sessionevents.NewBus(64)
			srv := New(ctxFailingStore{Store: inner}, Options{DevTenantHeader: true, Events: bus})
			defer srv.Close()
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			req := httptest.NewRequest(http.MethodPost, "/v1/sessions/s/finalize", nil).WithContext(ctx)
			tc.exit(srv, httptest.NewRecorder(), req)
			got, err := inner.Get(context.Background(), "acme", "s")
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if got.State != session.StateFailed {
				t.Fatalf("state = %q, want failed: the finalizing → failed write must not depend on the request", got.State)
			}
			if eventCount(bus, "s", "session_complete") != 1 {
				t.Errorf("session_complete events = %d, want 1", eventCount(bus, "s", "session_complete"))
			}
			if srv.terminalClaims.size() != 0 {
				t.Errorf("terminal claims left = %d, want 0", srv.terminalClaims.size())
			}
		})
	}
}
