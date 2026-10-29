// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/gatewaycontrol/inputwaitrecovery"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// updateFault selects how faultStore answers one Update; the zero value
// passes the Update through.
type updateFault int

const (
	// faultBeforeCommit returns an error without applying the write.
	faultBeforeCommit updateFault = iota + 1
	// faultAfterCommit applies the write and then returns an error.
	faultAfterCommit
)

// faultStore wraps a session store and fails chosen Update calls, counted
// from one.
type faultStore struct {
	sessionstore.Store
	mu     sync.Mutex
	n      int
	faults map[int]updateFault
}

var errInjected = errors.New("injected store failure")

func (f *faultStore) Update(ctx context.Context, tenantID, id string, mutate func(*sessionstore.Session) error) (sessionstore.Session, error) {
	f.mu.Lock()
	f.n++
	fault := f.faults[f.n]
	f.mu.Unlock()
	switch fault {
	case faultBeforeCommit:
		return sessionstore.Session{}, errInjected
	case faultAfterCommit:
		if _, err := f.Store.Update(ctx, tenantID, id, mutate); err != nil {
			return sessionstore.Session{}, err
		}
		return sessionstore.Session{}, errInjected
	default:
		return f.Store.Update(ctx, tenantID, id, mutate)
	}
}

// inputRig is one MCP server serving lenny/request_input over a store, a
// registry, and an event bus.
type inputRig struct {
	srv   *mcp.Server
	store *faultStore
	reg   *inputwait.Registry
	bus   *sessionevents.Bus
	gen   *inputwaitrecovery.Generation
	ids   atomic.Int64
}

const rowSessionID = "sess_row"

func newInputRig(t *testing.T, st session.State, timeout time.Duration) *inputRig {
	t.Helper()
	r := &inputRig{
		store: &faultStore{Store: memstore.New(), faults: map[int]updateFault{}},
		reg:   inputwait.NewRegistry(),
		bus:   sessionevents.NewBus(0),
		gen:   inputwaitrecovery.NewGeneration(),
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := r.store.Create(context.Background(), sessionstore.Session{
		ID: rowSessionID, TenantID: "acme", UserID: "alice@acme.com", RuntimeRef: "echo",
		State: st, PodAssignment: "pod-a", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	r.srv = mcp.NewServer()
	mcptools.Register(r.srv, mcptools.Deps{
		DevTenantFallback:   true,
		Store:               r.store,
		Executor:            executor.NewEchoExecutor(),
		Events:              r.bus,
		InputWaits:          r.reg,
		RequestInputTimeout: timeout,
		Clock:               func() time.Time { return now.Add(time.Hour) },
		IDFunc:              func() string { return fmt.Sprintf("id%d", r.ids.Add(1)) },
		TenantID:            "acme",
	})
	return r
}

// callResult is one finished lenny/request_input dispatch.
type callResult struct {
	res mcp.ToolResult
	err error
}

// start dispatches lenny/request_input with requestID on ctx and returns the
// channel its result arrives on.
func (r *inputRig) start(ctx context.Context, requestID string) <-chan callResult {
	out := make(chan callResult, 1)
	args := fmt.Sprintf(`{"sessionId":%q,"requestId":%q,"parts":[{"type":"text","inline":"ok?"}]}`, rowSessionID, requestID)
	go func() {
		res, _, err := r.srv.DispatchTool(ctx, "lenny/request_input", json.RawMessage(args))
		out <- callResult{res, err}
	}()
	return out
}

func (r *inputRig) connCtx() context.Context {
	return inputwaitrecovery.NewContext(context.Background(), r.gen)
}

func (r *inputRig) row(t *testing.T) sessionstore.Session {
	t.Helper()
	row, err := r.store.Get(context.Background(), "acme", rowSessionID)
	if err != nil {
		t.Fatalf("get row: %v", err)
	}
	return row
}

// eventTypes returns the session's event types in publish order, with the
// state each status_change carries appended after a colon.
func (r *inputRig) eventTypes() []string {
	var out []string
	for _, e := range r.bus.History(rowSessionID, 0) {
		if e.Type == "status_change" {
			var p struct {
				State string `json:"state"`
			}
			_ = json.Unmarshal([]byte(e.Data), &p)
			out = append(out, "status_change:"+p.State)
			continue
		}
		out = append(out, e.Type)
	}
	return out
}

func (r *inputRig) statusChanges() []string {
	var out []string
	for _, e := range r.eventTypes() {
		if len(e) > len("status_change:") && e[:len("status_change:")] == "status_change:" {
			out = append(out, e)
		}
	}
	return out
}

// assertTenantFrozen pins that the status_change was published under the
// row's tenant: the bus refuses a subscription for another tenant.
func (r *inputRig) assertTenantFrozen(t *testing.T) {
	t.Helper()
	if _, err := r.bus.SubscribeForTenant("globex", rowSessionID, 0, 1); !errors.Is(err, sessionevents.ErrTenantMismatch) {
		t.Errorf("subscribe as another tenant = %v, want ErrTenantMismatch (status_change published under the row tenant)", err)
	}
}

func waitResult(t *testing.T, ch <-chan callResult) callResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("lenny/request_input did not return")
		return callResult{}
	}
}

// TestRequestInputEntryWriteOnRunningRow_spec_7_2 pins the entry write on a
// running row: the row becomes input_required naming the call's wait, one
// status_change(input_required) precedes elicitation_request under the row's
// tenant, and a later idle sweep's state filter does not list the row as
// running.
//
// spec: §7.2 (Session state machine), §6.2 (Pod State Machine)
func TestRequestInputEntryWriteOnRunningRow_spec_7_2(t *testing.T) {
	r := newInputRig(t, session.StateRunning, time.Minute)
	ctx, cancel := context.WithCancel(r.connCtx())
	defer cancel()
	done := r.start(ctx, "req-1")
	waitPending(t, r.reg, rowSessionID, "req-1")
	row := r.row(t)
	pend := r.reg.PendingDetailsForSession(rowSessionID)
	if row.State != session.StateInputRequired || len(pend) != 1 || row.InputWaitID != pend[0].WaitID || row.InputWaitID == "" {
		t.Fatalf("row = %s/%q, pending %+v; want input_required naming the pending wait", row.State, row.InputWaitID, pend)
	}
	if row.InputWaitID == "req-1" {
		t.Error("the row names the caller's requestId; the wait identity must come from the gateway's ID source")
	}
	running, _ := r.store.List(context.Background(), "acme", sessionstore.ListFilter{State: session.StateRunning})
	if len(running) != 0 {
		t.Errorf("idle listing of running rows = %d, want 0 while the wait is live", len(running))
	}
	if got := r.eventTypes(); len(got) < 2 || got[0] != "status_change:input_required" || got[1] != "elicitation_request" {
		t.Errorf("events = %v, want status_change(input_required) then elicitation_request", got)
	}
	r.assertTenantFrozen(t)
	cancel()
	waitResult(t, done)
}

// TestRequestInputEntryWriteOnInputRequiredRow_spec_7_2 pins that the entry
// write on a row already input_required changes only InputWaitID and
// publishes no status_change.
//
// spec: §7.2 (Session state machine)
func TestRequestInputEntryWriteOnInputRequiredRow_spec_7_2(t *testing.T) {
	r := newInputRig(t, session.StateInputRequired, time.Minute)
	ctx, cancel := context.WithCancel(r.connCtx())
	defer cancel()
	done := r.start(ctx, "req-1")
	waitPending(t, r.reg, rowSessionID, "req-1")
	row := r.row(t)
	if row.State != session.StateInputRequired || row.InputWaitID != r.reg.PendingDetailsForSession(rowSessionID)[0].WaitID {
		t.Errorf("row = %s/%q, want input_required naming the new wait", row.State, row.InputWaitID)
	}
	if sc := r.statusChanges(); len(sc) != 0 {
		t.Errorf("status_change = %v, want none for a write that leaves the state", sc)
	}
	cancel()
	waitResult(t, done)
}

// TestRequestInputEntryWriteSupersededOnOtherStates_spec_7_2 pins that the
// entry write on a row in any state other than running or input_required
// leaves the row unchanged, publishes no status_change, and the wait still
// proceeds to its answer.
//
// spec: §7.2 (Session state machine)
func TestRequestInputEntryWriteSupersededOnOtherStates_spec_7_2(t *testing.T) {
	for _, st := range []session.State{
		session.StateCreated, session.StateFinalizing, session.StateReady, session.StateStarting,
		session.StateSuspended, session.StateResumePending, session.StateAwaitingClientAction, session.StateResuming,
	} {
		t.Run(string(st), func(t *testing.T) {
			r := newInputRig(t, st, time.Minute)
			done := r.start(r.connCtx(), "req-1")
			waitPending(t, r.reg, rowSessionID, "req-1")
			if err := r.reg.Resolve(rowSessionID, "req-1", "yes"); err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if res := waitResult(t, done); res.err != nil || res.res.IsError {
				t.Errorf("call = %+v, want an answered wait", res)
			}
			if row := r.row(t); row.State != st || row.InputWaitID != "" {
				t.Errorf("row = %s/%q, want %s unchanged", row.State, row.InputWaitID, st)
			}
			if sc := r.statusChanges(); len(sc) != 0 {
				t.Errorf("status_change = %v, want none", sc)
			}
		})
	}
}

// TestRequestInputWithoutConnectionRecordWritesNothing_spec_7_2 pins that a
// call carrying no LNK-GWCONTROL connection record, as a gateway-edge /mcp
// call, registers an unpersisted wait and leaves the row running.
//
// spec: §7.2 (Session state machine)
func TestRequestInputWithoutConnectionRecordWritesNothing_spec_7_2(t *testing.T) {
	r := newInputRig(t, session.StateRunning, time.Minute)
	done := r.start(context.Background(), "req-1")
	waitPending(t, r.reg, rowSessionID, "req-1")
	pend := r.reg.PendingDetailsForSession(rowSessionID)
	if len(pend) != 1 || pend[0].Persisted || pend[0].WaitID != "" {
		t.Errorf("pending = %+v, want one unpersisted wait", pend)
	}
	if row := r.row(t); row.State != session.StateRunning || row.InputWaitID != "" {
		t.Errorf("row = %s/%q, want running with no wait named", row.State, row.InputWaitID)
	}
	_ = r.reg.Resolve(rowSessionID, "req-1", "yes")
	waitResult(t, done)
	if sc := r.statusChanges(); len(sc) != 0 {
		t.Errorf("status_change = %v, want none", sc)
	}
}

// TestRequestInputExitWriteRestoresRunning_spec_7_2 pins the exit write for
// each way a wait leaves the registry: the row returns to running with
// LastAgentActivityAt set, and exactly one status_change(running) follows the
// entry's status_change(input_required).
//
// spec: §7.2 (Session state machine), §6.2 (Pod State Machine)
func TestRequestInputExitWriteRestoresRunning_spec_7_2(t *testing.T) {
	ends := map[string]func(r *inputRig, cancel context.CancelFunc){
		"reply":          func(r *inputRig, _ context.CancelFunc) { _ = r.reg.Resolve(rowSessionID, "req-1", "yes") },
		"peer cancel":    func(r *inputRig, _ context.CancelFunc) { r.reg.Cancel(rowSessionID, "req-1") },
		"context ending": func(_ *inputRig, cancel context.CancelFunc) { cancel() },
		"close":          func(r *inputRig, _ context.CancelFunc) { r.reg.Close() },
		"timeout":        func(*inputRig, context.CancelFunc) {},
	}
	for name, end := range ends {
		t.Run(name, func(t *testing.T) {
			timeout := time.Minute
			if name == "timeout" {
				timeout = 50 * time.Millisecond
			}
			r := newInputRig(t, session.StateRunning, timeout)
			ctx, cancel := context.WithCancel(r.connCtx())
			defer cancel()
			done := r.start(ctx, "req-1")
			waitPending(t, r.reg, rowSessionID, "req-1")
			end(r, cancel)
			waitResult(t, done)
			row := r.row(t)
			if row.State != session.StateRunning || row.LastAgentActivityAt.IsZero() {
				t.Errorf("row = %s, LastAgentActivityAt %v; want running with the idle clock restarted", row.State, row.LastAgentActivityAt)
			}
			if sc := r.statusChanges(); len(sc) != 2 || sc[0] != "status_change:input_required" || sc[1] != "status_change:running" {
				t.Errorf("status_change = %v, want input_required then one running", sc)
			}
			if r.gen.Load() != 1 {
				t.Errorf("generation = %d, want 1 (an applied exit write re-arms nothing)", r.gen.Load())
			}
		})
	}
}

// TestRequestInputExitWriteRepointsToAnotherPersistedWait_spec_7_2 pins that
// the exit write of one wait, while another persisted wait of the session is
// registered, re-points the row to that wait and leaves input_required with
// no status_change; the last exit restores running.
//
// spec: §7.2 (Session state machine)
func TestRequestInputExitWriteRepointsToAnotherPersistedWait_spec_7_2(t *testing.T) {
	r := newInputRig(t, session.StateRunning, time.Minute)
	first := r.start(r.connCtx(), "req-1")
	waitPending(t, r.reg, rowSessionID, "req-1")
	second := r.start(r.connCtx(), "req-2")
	waitPending(t, r.reg, rowSessionID, "req-2")
	var secondWait string
	for _, p := range r.reg.PendingDetailsForSession(rowSessionID) {
		if p.RequestID == "req-2" {
			secondWait = p.WaitID
		}
	}
	if row := r.row(t); row.InputWaitID != secondWait {
		t.Fatalf("row names %q, want the later wait %q", row.InputWaitID, secondWait)
	}
	_ = r.reg.Resolve(rowSessionID, "req-2", "b")
	waitResult(t, second)
	row := r.row(t)
	pend := r.reg.PendingDetailsForSession(rowSessionID)
	if row.State != session.StateInputRequired || len(pend) != 1 || row.InputWaitID != pend[0].WaitID {
		t.Errorf("row = %s/%q, pending %+v; want input_required re-pointed to the remaining wait", row.State, row.InputWaitID, pend)
	}
	if sc := r.statusChanges(); len(sc) != 1 {
		t.Errorf("status_change = %v, want only the entry's", sc)
	}
	_ = r.reg.Resolve(rowSessionID, "req-1", "a")
	waitResult(t, first)
	if row := r.row(t); row.State != session.StateRunning {
		t.Errorf("state after the last exit = %s, want running", row.State)
	}
}

// TestRequestInputExitWriteSuperseded_spec_7_2 pins that the exit write
// leaves a row that is no longer input_required, or that names another wait,
// unchanged and publishes no status_change.
//
// spec: §7.2 (Session state machine)
func TestRequestInputExitWriteSuperseded_spec_7_2(t *testing.T) {
	for name, mutate := range map[string]func(*sessionstore.Session) error{
		"row left input_required": func(s *sessionstore.Session) error { s.State = session.StateSuspended; return nil },
		"row names another wait":  func(s *sessionstore.Session) error { s.InputWaitID = "iw_other"; return nil },
	} {
		t.Run(name, func(t *testing.T) {
			r := newInputRig(t, session.StateRunning, time.Minute)
			done := r.start(r.connCtx(), "req-1")
			waitPending(t, r.reg, rowSessionID, "req-1")
			before, err := r.store.Update(context.Background(), "acme", rowSessionID, mutate)
			if err != nil {
				t.Fatal(err)
			}
			_ = r.reg.Resolve(rowSessionID, "req-1", "yes")
			waitResult(t, done)
			row := r.row(t)
			if row.State != before.State || row.InputWaitID != before.InputWaitID {
				t.Errorf("row = %s/%q, want unchanged %s/%q", row.State, row.InputWaitID, before.State, before.InputWaitID)
			}
			if sc := r.statusChanges(); len(sc) != 1 {
				t.Errorf("status_change = %v, want only the entry's", sc)
			}
		})
	}
}

// TestRequestInputRegistrationRefusedWritesNothing_spec_7_2 pins that a
// refused registration, a duplicate request or one after Close, writes
// nothing to the row.
//
// spec: §7.2 (Session state machine)
func TestRequestInputRegistrationRefusedWritesNothing_spec_7_2(t *testing.T) {
	t.Run("duplicate", func(t *testing.T) {
		r := newInputRig(t, session.StateRunning, time.Minute)
		if _, err := r.reg.Register(rowSessionID, "req-1", nil); err != nil {
			t.Fatal(err)
		}
		res := waitResult(t, r.start(r.connCtx(), "req-1"))
		if !res.res.IsError {
			t.Errorf("duplicate call = %+v, want an error result", res)
		}
		if row := r.row(t); row.State != session.StateRunning || row.InputWaitID != "" {
			t.Errorf("row = %s/%q, want running unchanged", row.State, row.InputWaitID)
		}
	})
	t.Run("closed", func(t *testing.T) {
		r := newInputRig(t, session.StateRunning, time.Minute)
		r.reg.Close()
		res := waitResult(t, r.start(r.connCtx(), "req-1"))
		if !res.res.IsError {
			t.Errorf("call after Close = %+v, want an error result", res)
		}
		if row := r.row(t); row.State != session.StateRunning || row.InputWaitID != "" {
			t.Errorf("row = %s/%q, want running unchanged", row.State, row.InputWaitID)
		}
		if sc := r.statusChanges(); len(sc) != 0 {
			t.Errorf("status_change = %v, want none", sc)
		}
	})
}

// TestRequestInputEntryWriteFailureCancelsAndRestores_spec_7_2 pins the
// failed entry write: against a store that errors before committing and one
// that commits and then errors, the handler removes the wait, its exit write
// leaves the row running, and the call returns the entry error.
//
// spec: §7.2 (Session state machine)
func TestRequestInputEntryWriteFailureCancelsAndRestores_spec_7_2(t *testing.T) {
	for name, fault := range map[string]updateFault{"before commit": faultBeforeCommit, "after commit": faultAfterCommit} {
		t.Run(name, func(t *testing.T) {
			r := newInputRig(t, session.StateRunning, time.Minute)
			r.store.faults[1] = fault
			res := waitResult(t, r.start(r.connCtx(), "req-1"))
			if !res.res.IsError {
				t.Errorf("call = %+v, want the entry error", res)
			}
			if r.reg.Pending(rowSessionID, "req-1") {
				t.Error("the wait is still registered after a failed entry write")
			}
			if row := r.row(t); row.State != session.StateRunning {
				t.Errorf("state = %s, want running", row.State)
			}
			for _, e := range r.eventTypes() {
				if e == "elicitation_request" {
					t.Error("elicitation_request published after a failed entry write")
				}
			}
		})
	}
}

// TestRequestInputExitWriteFailureRearmsReplica_spec_7_2 pins that an exit
// write failing with an error other than ErrSuperseded increments the
// replica's re-arm generation, leaving the row for the connection recovery,
// and a superseded exit write increments nothing.
//
// spec: §7.2 (Session state machine), §28.3 (Link register)
func TestRequestInputExitWriteFailureRearmsReplica_spec_7_2(t *testing.T) {
	r := newInputRig(t, session.StateRunning, time.Minute)
	r.store.faults[2] = faultBeforeCommit
	done := r.start(r.connCtx(), "req-1")
	waitPending(t, r.reg, rowSessionID, "req-1")
	_ = r.reg.Resolve(rowSessionID, "req-1", "yes")
	waitResult(t, done)
	if r.gen.Load() != 2 {
		t.Errorf("generation = %d, want 2 after a failed exit write", r.gen.Load())
	}
	if row := r.row(t); row.State != session.StateInputRequired {
		t.Errorf("state = %s, want input_required naming a wait no registry holds", row.State)
	}
	if sc := r.statusChanges(); len(sc) != 1 {
		t.Errorf("status_change = %v, want only the entry's (a failed write publishes none)", sc)
	}
}
