// SPDX-License-Identifier: MIT

package watchdog_test

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/watchdog"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

var (
	errInjectedList  = errors.New("injected list failure")
	errInjectedWrite = errors.New("injected write failure")
)

// passFaultStore fails List for one tenant, or for one state filter of every
// tenant, and fails Update for chosen sessions.
type passFaultStore struct {
	sessionstore.Store
	mu         sync.Mutex
	listTenant string
	listState  *session.State
	updateErr  map[string]error
}

func (s *passFaultStore) List(ctx context.Context, tenantID string, f sessionstore.ListFilter) ([]sessionstore.Session, error) {
	s.mu.Lock()
	failTenant := s.listTenant != "" && tenantID == s.listTenant
	failState := s.listState != nil && f.State == *s.listState
	s.mu.Unlock()
	if failTenant || failState {
		return nil, errInjectedList
	}
	rows, err := s.Store.List(ctx, tenantID, f)
	// Rows come back in ID order, so the failing row is written before the
	// rows after it and a pass that stops at the failure leaves them.
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows, err
}

func (s *passFaultStore) Update(ctx context.Context, tenantID, id string, mut func(*sessionstore.Session) error) (sessionstore.Session, error) {
	s.mu.Lock()
	err := s.updateErr[id]
	s.mu.Unlock()
	if err != nil {
		return sessionstore.Session{}, err
	}
	return s.Store.Update(ctx, tenantID, id, mut)
}

// passHook counts terminal hooks and open claims, and implements the expiry
// warning notifier so the warning sweep lists rows.
type passHook struct {
	mu    sync.Mutex
	hooks map[string]int
	open  map[string]int
}

func newPassHook() *passHook { return &passHook{hooks: map[string]int{}, open: map[string]int{}} }

func (h *passHook) BeginTerminal(id string) func() {
	h.mu.Lock()
	h.open[id]++
	h.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			h.open[id]--
			h.mu.Unlock()
		})
	}
}

func (h *passHook) OnSessionTerminal(_ context.Context, _ session.State, sess sessionstore.Session) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.hooks[sess.ID]++
}

func (*passHook) OnSessionExpiringSoon(context.Context, sessionstore.Session, int, int) {}

func (h *passHook) counts(id string) (hooks, open int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.hooks[id], h.open[id]
}

// passSubject is one sub-sweep with a write: rows are seeded in state with
// row, the pass runs at the subject's at, and an applied write leaves the row
// in want.
type passSubject struct {
	name  string
	cfg   watchdog.Config
	state session.State
	row   func(id string, born time.Time) sessionstore.Session
	at    time.Duration
	want  session.State
}

// passSubjects covers every sub-sweep that writes a row.
func passSubjects() []passSubject {
	plain := func(st session.State) func(string, time.Time) sessionstore.Session {
		return func(id string, born time.Time) sessionstore.Session {
			return sessionstore.Session{ID: id, TenantID: "acme", State: st, CreatedAt: born, UpdatedAt: born, LastAgentActivityAt: born}
		}
	}
	return []passSubject{
		{"pre-running budget", watchdog.Config{}, session.StateCreated, plain(session.StateCreated), time.Hour, session.StateFailed},
		{"resuming budget", dlqCfgWith(func(c *watchdog.Config) { c.MaxResumingSeconds = 300 }), session.StateResuming, plain(session.StateResuming), time.Hour, session.StateResumePending},
		{"resume_pending window", dlqCfgWith(func(c *watchdog.Config) { c.MaxResumePendingSeconds = 300 }), session.StateResumePending, plain(session.StateResumePending), time.Hour, session.StateAwaitingClientAction},
		{"awaiting_client_action deadline", watchdog.Config{}, session.StateAwaitingClientAction, plain(session.StateAwaitingClientAction), time.Hour, session.StateExpired},
		{"idle expiry", idleCfg(600), session.StateRunning, plain(session.StateRunning), time.Hour, session.StateExpired},
		{"maxSessionAge", ageCfg(600), session.StateRunning, func(id string, born time.Time) sessionstore.Session {
			r := plain(session.StateRunning)(id, born)
			r.LastAgentActivityAt = born.Add(time.Hour)
			return r
		}, time.Hour, session.StateExpired},
		{"suspended lifetime", suspendedCfg(), session.StateSuspended, func(id string, born time.Time) sessionstore.Session {
			r := plain(session.StateSuspended)(id, born)
			r.SuspendedAt = born
			return r
		}, testLifetime * time.Second, session.StateExpired},
	}
}

// dlqCfgWith returns dlqCfg with one budget changed.
func dlqCfgWith(set func(*watchdog.Config)) watchdog.Config {
	c := dlqCfg()
	c.MaxIdleSeconds = idleCapDisabled
	c.MaxSessionAgeSeconds = idleCapDisabled
	set(&c)
	return c
}

// spec: §10.1.1 (Primary:)
//
// In every sub-sweep that writes a row, a failed write of one row skips that
// row: the pass writes the next row, returns the failure in its joined error,
// ends the failed row's claim at once, and runs no hook for it. A missing row
// is superseded rather than a failure. The next pass writes the failed row.
// Before the change, the first failed write ended the pass and left the next
// row unwritten until a later pass.
func TestEverySubSweepContinuesPastAFailedWrite_spec_10_1_1(t *testing.T) {
	born := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	for _, sub := range passSubjects() {
		t.Run(sub.name, func(t *testing.T) {
			mem := memstore.New()
			for _, id := range []string{"a-bad", "b-gone", "c-good"} {
				mustCreate(t, mem, sub.row(id, born))
			}
			store := &passFaultStore{Store: mem, updateErr: map[string]error{
				"a-bad": errInjectedWrite, "b-gone": sessionstore.ErrNotFound,
			}}
			hook := newPassHook()
			w := watchdog.New(store, watchdog.StaticTenants{"acme"}, sub.cfg, nil).WithTerminalHook(hook)
			_, err := w.Tick(context.Background(), born.Add(sub.at))
			if !errors.Is(err, errInjectedWrite) || errors.Is(err, sessionstore.ErrNotFound) {
				t.Fatalf("pass error = %v, want the write failure and no ErrNotFound", err)
			}
			for _, id := range []string{"a-bad", "b-gone"} {
				if hooks, open := hook.counts(id); hooks != 0 || open != 0 {
					t.Errorf("%s: %d hooks and %d open claims when the pass returned, want none", id, hooks, open)
				}
			}
			w.WaitDispatched()
			if got := mustGet(t, mem, "c-good").State; got != sub.want {
				t.Errorf("row after the failed one = %q, want %q in the same pass", got, sub.want)
			}
			if got := mustGet(t, mem, "a-bad").State; got != sub.state {
				t.Errorf("failed row = %q, want it left %q", got, sub.state)
			}

			store.mu.Lock()
			delete(store.updateErr, "a-bad")
			store.mu.Unlock()
			if _, err := w.Tick(context.Background(), born.Add(sub.at+time.Second)); err != nil {
				t.Fatalf("second pass error = %v", err)
			}
			w.WaitDispatched()
			if got := mustGet(t, mem, "a-bad").State; got != sub.want {
				t.Errorf("failed row on the next pass = %q, want %q", got, sub.want)
			}
		})
	}
}

// spec: §10.1.1 (Primary:)
//
// A failed listing skips only that listing. When one state's listing fails
// for every tenant, the pass returns the failure and still runs the other
// sub-sweeps; when one tenant's listings fail, the pass still sweeps the next
// tenant. Before the change, the first listing error ended the whole pass.
func TestAFailedListingSkipsOnlyThatListing_spec_10_1_1(t *testing.T) {
	born := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	failStates := []session.State{
		session.StateCreated, session.StateResuming, session.StateResumePending,
		session.StateAwaitingClientAction, session.StateRunning, session.StateSuspended, "",
	}
	for _, st := range failStates {
		t.Run("state "+string(st), func(t *testing.T) {
			mem := memstore.New()
			mustCreate(t, mem, sessionstore.Session{ID: "created", TenantID: "acme", State: session.StateCreated, CreatedAt: born, UpdatedAt: born})
			mustCreate(t, mem, sessionstore.Session{ID: "awaiting", TenantID: "acme", State: session.StateAwaitingClientAction, CreatedAt: born, UpdatedAt: born})
			failed := st
			store := &passFaultStore{Store: mem, listState: &failed}
			w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil).
				WithTerminalHook(newPassHook()).
				WithMessaging(&fakeDLQSweeper{})
			_, err := w.Tick(context.Background(), born.Add(time.Hour))
			if !errors.Is(err, errInjectedList) {
				t.Fatalf("pass error = %v, want the listing failure", err)
			}
			w.WaitDispatched()
			for id, listed := range map[string]session.State{"created": session.StateCreated, "awaiting": session.StateAwaitingClientAction} {
				ended := session.IsTerminal(mustGet(t, mem, id).State)
				if listed != st && !ended {
					t.Errorf("%s was not ended although only the %q listing failed", id, st)
				}
			}
		})
	}

	t.Run("one tenant", func(t *testing.T) {
		mem := memstore.New()
		for _, tenant := range []string{"acme", "globex"} {
			mustCreate(t, mem, sessionstore.Session{ID: tenant + "-s", TenantID: tenant, State: session.StateCreated, CreatedAt: born, UpdatedAt: born})
		}
		store := &passFaultStore{Store: mem, listTenant: "acme"}
		w := watchdog.New(store, watchdog.StaticTenants{"acme", "globex"}, watchdog.Config{}, nil)
		res, err := w.Tick(context.Background(), born.Add(time.Hour))
		if !errors.Is(err, errInjectedList) {
			t.Fatalf("pass error = %v, want the listing failure", err)
		}
		w.WaitDispatched()
		if res.ForcedFailures != 1 || mustGetTenant(t, mem, "globex", "globex-s").State != session.StateFailed {
			t.Errorf("forced failures %d, globex row %q; want the next tenant swept", res.ForcedFailures, mustGetTenant(t, mem, "globex", "globex-s").State)
		}
	})

	t.Run("tenant listing", func(t *testing.T) {
		w := watchdog.New(memstore.New(), failingTenants{}, watchdog.Config{}, nil)
		if _, err := w.Tick(context.Background(), born); !errors.Is(err, errInjectedList) {
			t.Fatalf("pass error = %v, want the tenant listing failure", err)
		}
	})
}

type failingTenants struct{}

func (failingTenants) ListTenants(context.Context) ([]string, error) { return nil, errInjectedList }

// spec: §7.1 (Terminal pipeline context:), §10.1.1 (Primary:)
//
// An applied terminal write's hooks run on a goroutine that holds the claim
// until they return, on a context the pass's cancellation does not reach.
func TestDispatchedHookOutlivesThePassContext_spec_7_1(t *testing.T) {
	born := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	mem := memstore.New()
	mustCreate(t, mem, sessionstore.Session{ID: "s", TenantID: "acme", State: session.StateCreated, CreatedAt: born, UpdatedAt: born})
	hook := &ctxHook{release: make(chan struct{}), entered: make(chan context.Context, 1)}
	w := watchdog.New(mem, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil).WithTerminalHook(hook)
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := w.Tick(ctx, born.Add(time.Hour)); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	hctx := <-hook.entered
	cancel()
	if hctx.Err() != nil {
		t.Errorf("hook context error after the pass was cancelled = %v, want none", hctx.Err())
	}
	if hook.ended() {
		t.Error("the claim ended while the hook was still running")
	}
	close(hook.release)
	w.WaitDispatched()
	if !hook.ended() {
		t.Error("the claim did not end after the hook returned")
	}
}

// ctxHook blocks its terminal hook until released and records the context.
type ctxHook struct {
	mu      sync.Mutex
	end     bool
	release chan struct{}
	entered chan context.Context
}

func (h *ctxHook) BeginTerminal(string) func() {
	return func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.end = true
	}
}

func (h *ctxHook) OnSessionTerminal(ctx context.Context, _ session.State, _ sessionstore.Session) {
	h.entered <- ctx
	<-h.release
}

func (h *ctxHook) ended() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.end
}

func mustGetTenant(t *testing.T, store sessionstore.Store, tenant, id string) sessionstore.Session {
	t.Helper()
	row, err := store.Get(context.Background(), tenant, id)
	if err != nil {
		t.Fatalf("get %s/%s: %v", tenant, id, err)
	}
	return row
}
