// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// laggingReadStore answers Get with a running row for any session, whether
// or not the primary still holds it, standing in for a read pool that lags
// a terminal write or a hard delete. Update, and so the primary read, runs
// against the stored row. A non-nil updateErr makes every Update fail, which
// stands in for an unreachable primary.
type laggingReadStore struct {
	sessionstore.Store
	updateErr error
}

func (s laggingReadStore) Get(_ context.Context, tenant, id string) (sessionstore.Session, error) {
	now := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	return sessionstore.Session{
		UserID: testOwner,
		ID:     id, TenantID: tenant, RuntimeRef: "echo",
		State: session.StateRunning, PodAssignment: "pod-1",
		CreatedAt: now, UpdatedAt: now,
	}, nil
}

func (s laggingReadStore) Update(ctx context.Context, tenant, id string, mutate func(*sessionstore.Session) error) (sessionstore.Session, error) {
	if s.updateErr != nil {
		return sessionstore.Session{}, s.updateErr
	}
	return s.Store.Update(ctx, tenant, id, mutate)
}

// interruptPrimaryCase is one row of the primary-row sweep.
type interruptPrimaryCase struct {
	name string
	// state is the primary row's state; absent means no primary row.
	state  session.State
	absent bool
	// updateErr makes the primary read fail.
	updateErr error
	// adapterStatus is what the bound adapter answers.
	adapterStatus adapterv1.InterruptResponse_Status
	wantHTTP      int
	wantCode      string
	wantAdapter   int32
	wantState     session.State
}

// interruptPrimaryCases builds the sweep: every session state against an
// adapter that answers STATUS_BUSY (the defect's trigger) and one that
// acknowledges, plus the absent row and an unreadable primary.
func interruptPrimaryCases() []interruptPrimaryCase {
	var cases []interruptPrimaryCase
	for _, st := range session.AllStates() {
		for _, ad := range []adapterv1.InterruptResponse_Status{
			adapterv1.InterruptResponse_STATUS_BUSY,
			adapterv1.InterruptResponse_STATUS_ACKNOWLEDGED,
		} {
			c := interruptPrimaryCase{
				name: string(st) + "/" + ad.String(), state: st, adapterStatus: ad,
				wantHTTP: http.StatusConflict, wantCode: "INVALID_STATE_TRANSITION",
				wantAdapter: 0, wantState: st,
			}
			// spec: §15.1 — the interrupt row admits running alone; an
			// input_required session answers 409 with no adapter call.
			if st == session.StateRunning {
				c.wantAdapter = 1
				if ad == adapterv1.InterruptResponse_STATUS_BUSY {
					c.wantCode = "INTERRUPT_BUSY"
				} else {
					c.wantHTTP, c.wantCode, c.wantState = http.StatusOK, "", session.StateSuspended
				}
			}
			cases = append(cases, c)
		}
	}
	cases = append(
		cases,
		interruptPrimaryCase{
			name: "absent/STATUS_BUSY", absent: true, adapterStatus: adapterv1.InterruptResponse_STATUS_BUSY,
			wantHTTP: http.StatusNotFound, wantCode: "RESOURCE_NOT_FOUND",
		},
		interruptPrimaryCase{
			name: "primary_unreadable/STATUS_BUSY", state: session.StateRunning,
			updateErr:     errors.New("primary unreachable"),
			adapterStatus: adapterv1.InterruptResponse_STATUS_BUSY,
			wantHTTP:      http.StatusInternalServerError, wantCode: "INTERNAL_ERROR",
			wantState: session.StateRunning,
		},
	)
	return cases
}

// TestInterruptChecksThePrimaryRowBeforeDrivingTheAdapter_spec_15_1 pins that
// the interrupt applies its precondition to the primary row before it drives
// the bound adapter. The read pool shows every session running. A primary row
// in any terminal state, or in any other state the interrupt is not valid in,
// answers 409 INVALID_STATE_TRANSITION with the row's current state, never
// reaches the adapter, and is left unchanged, even when the adapter would
// answer STATUS_BUSY. A hard-deleted row answers 404 and an unreadable primary
// fails closed with 500, neither reaching the adapter. A running row still
// reaches the adapter exactly once, and BUSY there keeps its 409
// INTERRUPT_BUSY; an input_required row answers 409 INVALID_STATE_TRANSITION
// without reaching the adapter (§15.1 interrupt row).
//
// Before the fix the terminal rows answered 409 INTERRUPT_BUSY against a
// STATUS_BUSY adapter, which told the client to retry an interrupt no retry
// could complete.
//
// spec: 15.1 (REST API), 7.2 (Interactive Session Model), 4.7 (Runtime Adapter)
func TestInterruptChecksThePrimaryRowBeforeDrivingTheAdapter_spec_15_1(t *testing.T) {
	for _, tc := range interruptPrimaryCases() {
		t.Run(tc.name, func(t *testing.T) {
			base := memstore.New()
			now := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
			if !tc.absent {
				if err := base.Create(context.Background(), sessionstore.Session{
					UserID: testOwner,
					ID:     "s1", TenantID: "acme", RuntimeRef: "echo", State: tc.state,
					CreatedAt: now, UpdatedAt: now,
				}); err != nil {
					t.Fatalf("seed: %v", err)
				}
			}
			stub := &stubAdapterServer{status: tc.adapterStatus}
			reg := podsession.NewRegistry()
			reg.Put(&podsession.BindResult{SessionID: "s1", TenantID: "acme", Adapter: dialStubAdapter(t, stub)})
			srv := sessionserver.New(laggingReadStore{Store: base, updateErr: tc.updateErr},
				sessionserver.Options{DevTenantHeader: true, PodRegistry: reg})

			req := httptest.NewRequest(http.MethodPost, "/v1/sessions/s1/interrupt", nil)
			req.Header.Set("X-Lenny-Tenant-ID", "acme")
			rr := httptest.NewRecorder()
			asOwner(srv.Handler()).ServeHTTP(rr, req)

			if rr.Code != tc.wantHTTP {
				t.Fatalf("status = %d, want %d; body=%s", rr.Code, tc.wantHTTP, rr.Body.String())
			}
			if got := stub.called.Load(); got != tc.wantAdapter {
				t.Errorf("adapter Interrupt calls = %d, want %d", got, tc.wantAdapter)
			}
			if tc.wantCode != "" {
				var env struct {
					Error struct {
						Code    string         `json:"code"`
						Details map[string]any `json:"details"`
					} `json:"error"`
				}
				if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
					t.Fatalf("decode error envelope: %v; body=%s", err, rr.Body.String())
				}
				if env.Error.Code != tc.wantCode {
					t.Errorf("error code = %q, want %q; body=%s", env.Error.Code, tc.wantCode, rr.Body.String())
				}
				if tc.wantCode == "INVALID_STATE_TRANSITION" && env.Error.Details["currentState"] != string(tc.state) {
					t.Errorf("details.currentState = %v, want the primary row's %q", env.Error.Details["currentState"], tc.state)
				}
			}
			row, err := base.Get(context.Background(), "acme", "s1")
			if tc.absent {
				if !errors.Is(err, sessionstore.ErrNotFound) {
					t.Fatalf("absent row read = %v, want ErrNotFound", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("read row: %v", err)
			}
			if row.State != tc.wantState {
				t.Errorf("row state = %q, want %q", row.State, tc.wantState)
			}
		})
	}
}

// TestInterruptWithoutBindingSkipsThePrimaryRead_spec_15_1 pins that the
// row-only interrupt path, taken when the gateway holds no adapter binding,
// keeps deciding on the locked row: a terminal row answers 409
// INVALID_STATE_TRANSITION and is left unchanged, so the primary check the
// adapter path gained does not change the bindingless answer.
//
// spec: 15.1 (REST API), 7.2 (Interactive Session Model)
func TestInterruptWithoutBindingSkipsThePrimaryRead_spec_15_1(t *testing.T) {
	for _, st := range session.TerminalStates() {
		t.Run(string(st), func(t *testing.T) {
			base := memstore.New()
			now := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
			if err := base.Create(context.Background(), sessionstore.Session{
				UserID: testOwner,
				ID:     "s1", TenantID: "acme", RuntimeRef: "echo", State: st, CreatedAt: now, UpdatedAt: now,
			}); err != nil {
				t.Fatalf("seed: %v", err)
			}
			srv := sessionserver.New(laggingReadStore{Store: base}, sessionserver.Options{DevTenantHeader: true, PodRegistry: podsession.NewRegistry()})
			req := httptest.NewRequest(http.MethodPost, "/v1/sessions/s1/interrupt", nil)
			req.Header.Set("X-Lenny-Tenant-ID", "acme")
			rr := httptest.NewRecorder()
			asOwner(srv.Handler()).ServeHTTP(rr, req)
			if rr.Code != http.StatusConflict {
				t.Fatalf("status = %d, want 409; body=%s", rr.Code, rr.Body.String())
			}
			row, _ := base.Get(context.Background(), "acme", "s1")
			if row.State != st {
				t.Errorf("row state = %q, want %q unchanged", row.State, st)
			}
		})
	}
}
