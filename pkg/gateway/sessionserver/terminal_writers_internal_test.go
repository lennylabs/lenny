// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
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
// that became resuming answers the precondition error with no hook; a
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
			session.StateRunning, session.StateResuming, http.StatusConflict, session.StateResuming, false,
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
	type writeCase struct {
		name  string
		write func(ctx context.Context, srv *Server)
		want  session.State
	}
	cases := []writeCase{
		{"DELETE", func(ctx context.Context, srv *Server) {
			req := httptest.NewRequest(http.MethodDelete, "/v1/sessions/s", nil).WithContext(ctx)
			req.Header.Set("X-Lenny-Tenant-ID", "acme")
			srv.Handler().ServeHTTP(httptest.NewRecorder(), req)
		}, session.StateCancelled},
		{"failure funnel", func(ctx context.Context, srv *Server) {
			_, _ = srv.ReportSessionFailure(ctx, FailureReport{
				TenantID: "acme", SessionID: "s", Reason: string(session.FailureWorkspaceValidationFailed),
			})
		}, session.StateFailed},
		{"failSession", func(ctx context.Context, srv *Server) { srv.failSession(ctx, "acme", "s") }, session.StateFailed},
		{"direct emitTerminalLifecycle", func(ctx context.Context, srv *Server) {
			row, _ := srv.store.Get(ctx, "acme", "s")
			row.State = session.StateCompleted
			srv.emitTerminalLifecycle(ctx, row)
		}, ""},
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
				if err := store.Create(context.Background(), sessionstore.Session{
					ID: "s", TenantID: "acme", RuntimeRef: "echo", State: session.StateRunning,
				}); err != nil {
					t.Fatalf("seed: %v", err)
				}
				bus := sessionevents.NewBus(64)
				srv := New(store, Options{DevTenantHeader: true, Events: bus})
				defer srv.Close()
				tc.write(ctx, srv)
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
