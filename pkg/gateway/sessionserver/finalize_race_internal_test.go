// SPDX-License-Identifier: MIT

package sessionserver

// spec: 15.1 (finalize precondition), 7.1 (steps 11-13, step 23), 6.2 (finalize timeout), 7.2 (terminal states)

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/credrouter"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// seedState creates one row in the given state for the helper tests.
func seedState(t *testing.T, store sessionstore.Store, id string, st session.State) {
	t.Helper()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: "acme", RuntimeRef: "echo", State: st,
	}); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

func rowState(t *testing.T, store sessionstore.Store, id string) session.State {
	t.Helper()
	row, err := store.Get(context.Background(), "acme", id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	return row.State
}

// failSession's non-finalize callers (the /start path and tree recovery) keep
// its unconditional write: a failSession on a terminal row still writes
// `failed`. Only the finalize handler's failFinalizing is guarded.
// spec: 7.2 (terminal states), 15.1 (finalize row)
func TestFailSessionStaysUnconditional_spec_7_2(t *testing.T) {
	store := memstore.New()
	seedState(t, store, "s", session.StateCancelled)
	srv := New(store, Options{})

	srv.failSession(context.Background(), "acme", "s")

	if got := rowState(t, store, "s"); got != session.StateFailed {
		t.Errorf("state after failSession on a cancelled row = %q, want failed", got)
	}
}

// finalizingPrecondition admits only a `finalizing` row; a refusal aborts the
// store write, leaves the locked row unchanged, and reports the §15.1
// precondition error carrying the locked state.
// spec: 15.1 (finalize row), 6.2 (finalize timeout), 7.2 (terminal states)
func TestFinalizingPreconditionRefusesNonFinalizing_spec_15_1(t *testing.T) {
	for _, st := range []session.State{
		session.StateCancelled, session.StateCompleted, session.StateFailed, session.StateReady, session.StateCreated,
	} {
		t.Run(string(st), func(t *testing.T) {
			store := memstore.New()
			seedState(t, store, "s", st)
			before, _ := store.Get(context.Background(), "acme", "s")
			_, err := store.Update(context.Background(), "acme", "s", func(row *sessionstore.Session) error {
				if err := finalizingPrecondition(row); err != nil {
					return err
				}
				row.State = session.StateFailed
				return nil
			})
			var pe *session.PreconditionError
			if !errors.As(err, &pe) {
				t.Fatalf("Update error = %v, want *session.PreconditionError", err)
			}
			if pe.CurrentState != st {
				t.Errorf("CurrentState = %q, want %q", pe.CurrentState, st)
			}
			if want := []session.State{session.StateCreated}; !reflect.DeepEqual(pe.AllowedStates, want) {
				t.Errorf("AllowedStates = %v, want %v", pe.AllowedStates, want)
			}
			after, _ := store.Get(context.Background(), "acme", "s")
			if after.State != before.State || !after.UpdatedAt.Equal(before.UpdatedAt) {
				t.Errorf("row changed by a refused write: before %+v, after %+v", before, after)
			}
		})
	}
	if err := finalizingPrecondition(&sessionstore.Session{State: session.StateFinalizing}); err != nil {
		t.Errorf("finalizingPrecondition(finalizing) = %v, want nil", err)
	}
}

// failFinalizing on a `finalizing` row writes `failed` and runs the terminal
// tail once: one status_change, one session_complete, and one session.failed
// audit event.
// spec: 7.2 (terminal states), 11.7
func TestFailFinalizingCommitsAndEmitsTerminalTail_spec_7_2(t *testing.T) {
	store := memstore.New()
	seedState(t, store, "s", session.StateFinalizing)
	bus := sessionevents.NewBus(64)
	sink := &captureLifecycleAudit{}
	srv := New(store, Options{Events: bus, LifecycleAuditSink: sink})

	if err := srv.failFinalizing(context.Background(), "acme", "s"); err != nil {
		t.Fatalf("failFinalizing = %v, want nil", err)
	}
	if got := rowState(t, store, "s"); got != session.StateFailed {
		t.Errorf("state = %q, want failed", got)
	}
	if got := sseEventsOfType(bus, "s", "status_change"); len(got) != 1 {
		t.Errorf("status_change count = %d, want 1", len(got))
	}
	if got := sseEventsOfType(bus, "s", "session_complete"); len(got) != 1 {
		t.Errorf("session_complete count = %d, want 1", len(got))
	}
	if len(sink.events) != 1 || sink.events[0].EventType != auditSessionFailed {
		t.Errorf("audit = %+v, want one session.failed", sink.events)
	}
}

// failFinalizing on a row another writer already moved to a terminal state
// loses: it returns the precondition error, keeps the terminal state, and
// emits no second terminal lifecycle.
// spec: 15.1 (finalize row), 7.2 (terminal states)
func TestFailFinalizingLosesToTerminalWriter_spec_15_1(t *testing.T) {
	store := memstore.New()
	seedState(t, store, "s", session.StateCancelled)
	bus := sessionevents.NewBus(64)
	sink := &captureLifecycleAudit{}
	srv := New(store, Options{Events: bus, LifecycleAuditSink: sink})

	err := srv.failFinalizing(context.Background(), "acme", "s")
	var pe *session.PreconditionError
	if !errors.As(err, &pe) || pe.CurrentState != session.StateCancelled {
		t.Fatalf("failFinalizing = %v, want *session.PreconditionError with currentState cancelled", err)
	}
	if got := rowState(t, store, "s"); got != session.StateCancelled {
		t.Errorf("state = %q, want cancelled kept", got)
	}
	if n := len(bus.History("s", 0)); n != 0 {
		t.Errorf("SSE events = %d, want 0", n)
	}
	if len(sink.events) != 0 {
		t.Errorf("audit = %+v, want none", sink.events)
	}
}

// recordPodClaimFailure records exactly what writePodClaimError records for
// the same error, under the same arm precedence: a lease-assignment failure
// counts a mismatch, a lease-assignment failure answered by the Token Service
// arm counts none, a slot-bind refusal wrapped in a setup failure files no
// setup_command_failed row, and a genuine setup failure files one.
// spec: 4.9 (Credential Leasing Service), 7.5 (Setup Commands), 16.1 (Metrics)
func TestRecordPodClaimFailureKeepsArmPrecedence_spec_4_9(t *testing.T) {
	cases := []struct {
		name         string
		err          error
		wantMismatch int
		wantSetupRow int
	}{
		{
			name:         "finalize credential mismatch",
			err:          mapFinalizeCredentialMismatch(credrouter.ErrUserCredentialNotFound),
			wantMismatch: 1,
		},
		{
			name: "assignment error wrapping token service outage",
			err:  &podsession.CredentialAssignmentError{Err: credassign.ErrTokenServiceUnavailable},
		},
		{
			name: "setup failure wrapping a slot-bind refusal",
			err:  &podsession.SetupCommandFailure{Cause: supersededRefusal()},
		},
		{
			name:         "genuine setup failure",
			err:          &podsession.SetupCommandFailure{Cause: errors.New("exit 1")},
			wantSetupRow: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mismatches := 0
			sink := &captureLifecycleAudit{}
			srv := New(memstore.New(), Options{
				PreclaimMismatch:   func(string, string) { mismatches++ },
				LifecycleAuditSink: sink,
			})
			srv.recordPodClaimFailure(tc.err)
			if mismatches != tc.wantMismatch {
				t.Errorf("mismatch count = %d, want %d", mismatches, tc.wantMismatch)
			}
			setupRows := 0
			for _, ev := range sink.events {
				if ev.EventType == auditSessionSetupCommandFailed {
					setupRows++
				}
			}
			if setupRows != tc.wantSetupRow {
				t.Errorf("setup_command_failed rows = %d, want %d", setupRows, tc.wantSetupRow)
			}
		})
	}
}
