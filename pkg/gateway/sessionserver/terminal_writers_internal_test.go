// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// movingStore moves the row with move after the handler's entry read, so the
// locked row differs from the row the handler validated.
type movingStore struct {
	sessionstore.Store
	once sync.Once
	move func(*sessionstore.Session)
}

func (m *movingStore) Get(ctx context.Context, tenantID, id string) (sessionstore.Session, error) {
	row, err := m.Store.Get(ctx, tenantID, id)
	m.once.Do(func() {
		_, _ = m.Update(ctx, tenantID, id, func(r *sessionstore.Session) error {
			m.move(r)
			return nil
		})
	})
	return row, err
}

// GetPrimary is the entry read of a handler that reads the row from the
// primary, such as POST /start. It moves the row after the read, as Get does,
// so the handler's later locked write finds the moved row.
func (m *movingStore) GetPrimary(ctx context.Context, tenantID, id string) (sessionstore.Session, error) {
	var row sessionstore.Session
	var err error
	if pr, ok := m.Store.(sessionstore.PrimaryReader); ok {
		row, err = pr.GetPrimary(ctx, tenantID, id)
	} else {
		row, err = m.Store.Get(ctx, tenantID, id)
	}
	m.once.Do(func() {
		_, _ = m.Update(ctx, tenantID, id, func(r *sessionstore.Session) error {
			m.move(r)
			return nil
		})
	})
	return row, err
}

func terminalRequest(t *testing.T, srv *Server, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	return rr
}

// TestTerminateAndDeleteRevalidateOnTheLockedRow_spec_15_1 asserts that POST
// /terminate and DELETE re-validate on the locked row: a terminate on a row
// that became resuming writes completed with a generation bump; a
// terminate on a row that became resume_pending completes; a delete on a row
// that became resuming writes cancelled with a generation bump; a delete on a
// row that became expired answers the precondition error with no hook; and a
// delete whose snapshot reads resume_pending while the stored row is resuming
// makes one Update that writes cancelled at the next generation.
//
// spec: §7.2 (Interactive Session Model), §15.1 (REST API)
func TestTerminateAndDeleteRevalidateOnTheLockedRow_spec_15_1(t *testing.T) {
	for _, tc := range []struct {
		name      string
		method    string
		path      string
		from      session.State
		becomes   session.State
		wantCode  int
		wantState session.State
		wantBump  bool
	}{
		{
			"terminate on a row that became resuming", http.MethodPost, "/v1/sessions/s/terminate",
			session.StateRunning, session.StateResuming, http.StatusOK, session.StateCompleted, true,
		},
		{
			"terminate on a row that became resume_pending", http.MethodPost, "/v1/sessions/s/terminate",
			session.StateRunning, session.StateResumePending, http.StatusOK, session.StateCompleted, false,
		},
		{
			"delete on a row that became resuming", http.MethodDelete, "/v1/sessions/s",
			session.StateResumePending, session.StateResuming, http.StatusOK, session.StateCancelled, true,
		},
		{
			"delete on a row that became expired", http.MethodDelete, "/v1/sessions/s",
			session.StateRunning, session.StateExpired, http.StatusConflict, session.StateExpired, false,
		},
		{
			"delete on a running row", http.MethodDelete, "/v1/sessions/s",
			session.StateRunning, session.StateRunning, http.StatusOK, session.StateCancelled, false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inner := memstore.New()
			if err := inner.Create(context.Background(), sessionstore.Session{
				ID: "s", TenantID: "acme", RuntimeRef: "echo", State: tc.from, CoordinationGeneration: 4,
			}); err != nil {
				t.Fatalf("seed: %v", err)
			}
			store := &movingStore{Store: inner, move: func(r *sessionstore.Session) {
				r.State = tc.becomes
				if tc.becomes == session.StateExpired {
					sessionstore.MarkTerminated(r, tc.from, r.UpdatedAt)
				}
			}}
			bus := sessionevents.NewBus(64)
			srv := New(store, Options{DevTenantHeader: true, Events: bus})
			defer srv.Close()
			rr := terminalRequest(t, srv, tc.method, tc.path)
			if rr.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d; body %s", rr.Code, tc.wantCode, rr.Body.String())
			}
			got, _ := inner.Get(context.Background(), "acme", "s")
			if got.State != tc.wantState {
				t.Errorf("state = %q, want %q", got.State, tc.wantState)
			}
			wantGen := int64(4)
			if tc.wantBump {
				wantGen = 5
			}
			if got.CoordinationGeneration != wantGen {
				t.Errorf("coordination_generation = %d, want %d", got.CoordinationGeneration, wantGen)
			}
			completes := eventCount(bus, "s", "session_complete")
			if session.IsTerminal(tc.wantState) && tc.wantCode == http.StatusOK {
				if completes != 1 {
					t.Errorf("session_complete events = %d, want 1", completes)
				}
			} else if completes != 0 {
				t.Errorf("session_complete events = %d, want 0: a refused write runs no hook", completes)
			}
			if srv.terminalClaims.size() != 0 {
				t.Errorf("terminal claims left = %d, want 0", srv.terminalClaims.size())
			}
		})
	}
}

// TestFunnelAndHookWritersEmitTheTerminalEventsOnce_spec_7_2 asserts that a
// terminal write through DELETE, through the failure funnel's failed edge,
// and through failSession emits exactly one status_change with the terminal
// state and one session_complete, and that a direct emitTerminalLifecycle
// call emits neither. Each writer also runs with its context cancelled as
// its terminal write commits, and the hook, which runs on the gateway-owned
// terminal context, still emits the events once.
//
// spec: §7.2 (Interactive Session Model), §7.1 (Terminal pipeline context)
func TestFunnelAndHookWritersEmitTheTerminalEventsOnce_spec_7_2(t *testing.T) {
	statusOf := func(bus *sessionevents.Bus, id string, st session.State) int {
		n := 0
		for _, e := range bus.History(id, 0) {
			if e.Type == "status_change" && containsState(e.Data, st) {
				n++
			}
		}
		return n
	}
	// seed is the row's starting state; empty seeds running.
	type writeCase struct {
		name  string
		write func(ctx context.Context, srv *Server)
		want  session.State
		seed  session.State
	}
	cases := []writeCase{
		{"DELETE", func(ctx context.Context, srv *Server) {
			req := httptest.NewRequest(http.MethodDelete, "/v1/sessions/s", nil).WithContext(ctx)
			req.Header.Set("X-Lenny-Tenant-ID", "acme")
			srv.Handler().ServeHTTP(httptest.NewRecorder(), req)
		}, session.StateCancelled, ""},
		{"failure funnel", func(ctx context.Context, srv *Server) {
			_, _ = srv.ReportSessionFailure(ctx, FailureReport{
				TenantID: "acme", SessionID: "s", Reason: string(session.FailureWorkspaceValidationFailed),
			})
		}, session.StateFailed, ""},
		{"failSession", func(ctx context.Context, srv *Server) { srv.failSession(ctx, "acme", "s") }, session.StateFailed, session.StateResumePending},
		{"direct emitTerminalLifecycle", func(ctx context.Context, srv *Server) {
			row, _ := srv.store.Get(ctx, "acme", "s")
			row.State = session.StateCompleted
			srv.emitTerminalLifecycle(ctx, row)
		}, "", ""},
	}
	for _, tc := range cases {
		for _, cancelAtCommit := range []bool{false, true} {
			name := tc.name
			if cancelAtCommit {
				if tc.want == "" {
					continue
				}
				name += "/writer context cancelled at commit"
			}
			t.Run(name, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				store := &tcStore{Store: memstore.New()}
				if cancelAtCommit {
					store.onTerminal = func(*sessionstore.Session) { cancel() }
				}
				seed := tc.seed
				if seed == "" {
					seed = session.StateRunning
				}
				if err := store.Create(context.Background(), sessionstore.Session{
					ID: "s", TenantID: "acme", RuntimeRef: "echo", State: seed,
				}); err != nil {
					t.Fatalf("seed: %v", err)
				}
				bus := sessionevents.NewBus(64)
				srv := New(store, Options{DevTenantHeader: true, Events: bus})
				defer srv.Close()
				tc.write(ctx, srv)
				if err := srv.WaitTerminalWork(context.Background()); err != nil {
					t.Fatalf("WaitTerminalWork: %v", err)
				}
				completes := eventCount(bus, "s", "session_complete")
				if tc.want == "" {
					if completes != 0 || eventCount(bus, "s", "status_change") != 0 {
						t.Errorf("a direct emitTerminalLifecycle emitted terminal events")
					}
					return
				}
				if completes != 1 || statusOf(bus, "s", tc.want) != 1 {
					t.Errorf("session_complete %d, status_change(%s) %d; want 1 and 1", completes, tc.want, statusOf(bus, "s", tc.want))
				}
			})
		}
	}
}

func containsState(data string, st session.State) bool {
	return strings.Contains(data, `"state":"`+string(st)+`"`)
}

// waitTerminalWorkAsync runs WaitTerminalWork on a context bounded by bound
// and delivers its result on the returned channel.
func waitTerminalWorkAsync(srv *Server, bound time.Duration) <-chan error {
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), bound)
		defer cancel()
		done <- srv.WaitTerminalWork(ctx)
	}()
	return done
}

// requireStillWaiting fails the test when the wait has already returned.
func requireStillWaiting(t *testing.T, done <-chan error, why string) {
	t.Helper()
	select {
	case err := <-done:
		t.Fatalf("WaitTerminalWork returned %v while %s", err, why)
	default:
	}
}

// receiveWait returns the wait's result, failing after limit.
func receiveWait(t *testing.T, done <-chan error, limit time.Duration) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(limit):
		t.Fatalf("WaitTerminalWork did not return within %v", limit)
		return nil
	}
}

// TestWaitTerminalWorkWaitsOnTheTerminalClaimRecord_spec_10_1_7 asserts the
// shutdown wait on the terminal claim record: it returns nil at once when no
// claim is open, returns ctx.Err() at its bound while a claim stays open,
// and treats a claim opened during the wait as extending it until that
// claim closes or the bound ends. Background loops open claims during the
// wait, so a claim opened mid-wait is part of the terminal work the wait
// covers rather than a hang.
//
// spec: 10.1.7 (preStop Hook Drain), 10.1.7 (Terminal pipelines at process
// exit.)
func TestWaitTerminalWorkWaitsOnTheTerminalClaimRecord_spec_10_1_7(t *testing.T) {
	t.Run("empty record returns nil at once", func(t *testing.T) {
		srv := New(memstore.New(), Options{})
		defer srv.Close()
		start := time.Now()
		if err := receiveWait(t, waitTerminalWorkAsync(srv, 5*time.Second), 5*time.Second); err != nil {
			t.Fatalf("WaitTerminalWork = %v, want nil", err)
		}
		if waited := time.Since(start); waited >= terminalWorkPollInterval {
			t.Errorf("waited %v on an empty record, want no poll interval", waited)
		}
	})
	t.Run("open claim at the bound returns ctx.Err", func(t *testing.T) {
		srv := New(memstore.New(), Options{})
		defer srv.Close()
		end := srv.BeginTerminal("s")
		defer end()
		err := receiveWait(t, waitTerminalWorkAsync(srv, 250*time.Millisecond), 5*time.Second)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("WaitTerminalWork = %v, want context.DeadlineExceeded", err)
		}
		if got := srv.OpenTerminalWork(); got != 1 {
			t.Errorf("OpenTerminalWork = %d, want 1", got)
		}
	})
	t.Run("cancelled context returns context.Canceled", func(t *testing.T) {
		srv := New(memstore.New(), Options{})
		defer srv.Close()
		end := srv.BeginTerminal("s")
		defer end()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := srv.WaitTerminalWork(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("WaitTerminalWork = %v, want context.Canceled", err)
		}
	})
	t.Run("claim opened mid-wait extends the wait until it closes", func(t *testing.T) {
		srv := New(memstore.New(), Options{})
		defer srv.Close()
		endFirst := srv.BeginTerminal("first")
		done := waitTerminalWorkAsync(srv, 10*time.Second)
		time.Sleep(50 * time.Millisecond)
		endSecond := srv.BeginTerminal("second")
		endFirst()
		time.Sleep(3 * terminalWorkPollInterval)
		requireStillWaiting(t, done, "a claim opened during the wait is still open")
		endSecond()
		if err := receiveWait(t, done, 5*time.Second); err != nil {
			t.Fatalf("WaitTerminalWork = %v, want nil once every claim closed", err)
		}
	})
	t.Run("claim opened mid-wait that stays open ends at the bound", func(t *testing.T) {
		srv := New(memstore.New(), Options{})
		defer srv.Close()
		endFirst := srv.BeginTerminal("first")
		done := waitTerminalWorkAsync(srv, 400*time.Millisecond)
		time.Sleep(50 * time.Millisecond)
		endSecond := srv.BeginTerminal("second")
		defer endSecond()
		endFirst()
		if err := receiveWait(t, done, 5*time.Second); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("WaitTerminalWork = %v, want context.DeadlineExceeded", err)
		}
	})
	t.Run("terminal binding release in progress is waited on", func(t *testing.T) {
		f := newFunnelFixture(t, funnelOpts{})
		row, _ := bindAt(t, f, "s", session.StateCancelled, 3, 3)
		unlock, err := f.srv.slotAccountLocks.Lock(context.Background(), "s")
		if err != nil {
			t.Fatalf("lock: %v", err)
		}
		f.srv.ReleaseTerminalBinding(row)
		done := waitTerminalWorkAsync(f.srv, 10*time.Second)
		time.Sleep(2 * terminalWorkPollInterval)
		requireStillWaiting(t, done, "a terminal binding release waits on the slot-accounting lock")
		unlock()
		if err := receiveWait(t, done, 5*time.Second); err != nil {
			t.Fatalf("WaitTerminalWork = %v, want nil once the release ended", err)
		}
		f.srv.Close()
		if calls := f.exec.callList(); len(calls) != 1 || calls[0] != executor.DispositionCancelled {
			t.Errorf("releases = %v, want one cancelled release before Close", calls)
		}
	})
}

// TestShutdownCloseBeforeTheWaitSkipsAPendingRelease_spec_10_1_7 pins why
// the shutdown wait precedes Close: a terminal binding release waiting on
// the slot-accounting lock acquires it on the server-lifetime context, so a
// Close that runs first ends the wait for the lock and the release is
// skipped, leaving the binding held. Run in the stated order, the same
// release completes its lock acquisition and releases the binding.
//
// spec: 10.1.7 (Terminal pipelines at process exit.), 7.2 (Interactive
// Session Model)
func TestShutdownCloseBeforeTheWaitSkipsAPendingRelease_spec_10_1_7(t *testing.T) {
	f := newFunnelFixture(t, funnelOpts{})
	row, _ := bindAt(t, f, "s", session.StateCancelled, 3, 3)
	unlock, err := f.srv.slotAccountLocks.Lock(context.Background(), "s")
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	f.srv.ReleaseTerminalBinding(row)
	time.Sleep(20 * time.Millisecond)
	f.srv.Close()
	if err := receiveWait(t, waitTerminalWorkAsync(f.srv, 5*time.Second), 6*time.Second); err != nil {
		t.Fatalf("WaitTerminalWork = %v, want nil once the cancelled release ended", err)
	}
	unlock()
	if calls := f.exec.callList(); len(calls) != 0 {
		t.Errorf("releases = %v, want none: Close ended the release's lock wait", calls)
	}
	if _, ok := f.registry.Get("s"); !ok {
		t.Error("the binding was released although Close preceded the lock acquisition")
	}
}

// TestRESTMessageToAClosingBindingTakesTheTerminalReroute_spec_7_1 pins the
// REST end of the closing gate. The message handler classifies the session
// running on its entry read, the terminal write commits after that read,
// and the seal has marked the binding closing. With no held entries, the
// pod executor's closing refusal surfaces as a *SealedError, the handler
// re-reads the row on the primary, and the terminal state answers
// TARGET_TERMINAL. Returning the bare sentinel from Send would reach the
// handler's fallthrough and answer EXECUTOR_FAILURE.
//
// spec: §7.1 (Delivery and checkpoints during the seal), §7.2 (Message
// delivery routing — seven paths, path 6), §28.5.1 (Gateway-to-pod)
func TestRESTMessageToAClosingBindingTakesTheTerminalReroute_spec_7_1(t *testing.T) {
	inner := memstore.New()
	store := &movingStore{Store: inner, move: func(r *sessionstore.Session) {
		from := r.State
		r.State = session.StateCompleted
		sessionstore.MarkTerminated(r, from, time.Now().UTC())
	}}
	if err := inner.Create(context.Background(), sessionstore.Session{
		ID: "s", TenantID: "acme", RuntimeRef: "echo", State: session.StateRunning,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	reg := sealBoundRegistry("s")
	bind, _ := reg.Get("s")
	bind.MarkClosing()
	srv := New(store, Options{DevTenantHeader: true, PodRegistry: reg, Executor: executor.NewPodExecutor(reg, nil)})
	t.Cleanup(srv.Close)

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/s/messages",
		strings.NewReader(`{"messages":[{"role":"user","content":"x"}]}`))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if strings.Contains(rr.Body.String(), "EXECUTOR_FAILURE") {
		t.Fatalf("POST messages to a closing binding answered EXECUTOR_FAILURE: %d %s", rr.Code, rr.Body.String())
	}
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "TARGET_TERMINAL") {
		t.Fatalf("POST messages to a closing binding = %d %s, want 409 TARGET_TERMINAL", rr.Code, rr.Body.String())
	}
}
