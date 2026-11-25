// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/billing/billingstore"
	"github.com/lennylabs/lenny/pkg/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/session/sessionstore/memstore"
)

// markProbe records, for each pipeline step it observes, whether the session
// row already carried the pipeline mark when the step ran.
type markProbe struct {
	store sessionstore.Store
	mu    sync.Mutex
	steps []string
	early []string
}

func (p *markProbe) observe(ctx context.Context, step, tenantID, sessionID string) {
	row, err := p.store.Get(ctx, tenantID, sessionID)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.steps = append(p.steps, step)
	if err == nil && !row.TerminalPipelineDoneAt.IsZero() {
		p.early = append(p.early, step)
	}
}

// probeBilling is a billing store whose Append reports the session.completed
// event to the probe before appending it.
type probeBilling struct {
	billingstore.Store
	probe *markProbe
}

func (b probeBilling) Append(ctx context.Context, e billingstore.Event) (billingstore.Event, error) {
	if e.EventType == billingstore.EventSessionCompleted {
		b.probe.observe(ctx, "billing", e.TenantID, e.SessionID)
	}
	return b.Store.Append(ctx, e)
}

// probeCheckpointer reports the final quota checkpoint to the probe.
type probeCheckpointer struct {
	probe     *markProbe
	sessionID string
}

func (c probeCheckpointer) CheckpointSubject(ctx context.Context, tenantID, _ string) error {
	c.probe.observe(ctx, "checkpoint", tenantID, c.sessionID)
	return nil
}

// probeLogHook reports the session-log close to the probe.
type probeLogHook struct{ probe *markProbe }

func (h probeLogHook) OnSessionTerminal(ctx context.Context, tenantID, sessionID string, _ []byte, _ bool) error {
	h.probe.observe(ctx, "session-log", tenantID, sessionID)
	return nil
}

// failingMarkStore is a session store whose Update fails with err.
type failingMarkStore struct {
	sessionstore.Store
	err error
}

func (f failingMarkStore) Update(context.Context, string, string, func(*sessionstore.Session) error) (sessionstore.Session, error) {
	return sessionstore.Session{}, f.err
}

// seedTerminal creates a terminal row for alice that ended at terminatedAt.
func seedTerminal(t *testing.T, store sessionstore.Store, id string, terminatedAt time.Time) sessionstore.Session {
	t.Helper()
	row := sessionstore.Session{
		ID: id, TenantID: "acme", UserID: "alice@acme.com", RuntimeRef: "claude-code",
		State: session.StateCompleted, TerminatedAt: terminatedAt,
		CreatedAt: terminatedAt.Add(-time.Minute), UpdatedAt: terminatedAt,
	}
	if err := store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
	return row
}

// markOf reads the pipeline mark of the row.
func markOf(t *testing.T, store sessionstore.Store, id string) time.Time {
	t.Helper()
	row, err := store.Get(context.Background(), "acme", id)
	if err != nil {
		t.Fatalf("read %s: %v", id, err)
	}
	return row.TerminalPipelineDoneAt
}

// TestTerminalPipelineMarkIsWrittenAfterTheLastStep pins the pipeline mark's
// place in the funnel: recordSessionCompleted writes it after the session-log
// close, the final quota checkpoint, and the session.completed billing event,
// and none of those steps sees a row that already carries it.
//
// spec: §7.1 (Terminal pipeline context)
func TestTerminalPipelineMarkIsWrittenAfterTheLastStep(t *testing.T) {
	store := memstore.New()
	probe := &markProbe{store: store}
	now := time.Now()
	row := seedTerminal(t, store, "sess_mark", now)
	srv := New(store, Options{
		DevTenantHeader:   true,
		Billing:           probeBilling{Store: billingstore.NewMemory(), probe: probe},
		QuotaCheckpointer: probeCheckpointer{probe: probe, sessionID: row.ID},
		SessionLogHook:    probeLogHook{probe: probe},
	})
	t.Cleanup(srv.Close)

	srv.recordSessionCompleted(context.Background(), session.StateRunning, row)

	if want := []string{"session-log", "checkpoint", "billing"}; len(probe.steps) != len(want) {
		t.Fatalf("observed steps = %v, want %v", probe.steps, want)
	}
	if len(probe.early) != 0 {
		t.Errorf("steps that saw the pipeline mark already written: %v, want none", probe.early)
	}
	if markOf(t, store, row.ID).IsZero() {
		t.Error("the pipeline mark is not written after the billing event")
	}
}

// TestTerminalPipelineMarkIsWrittenWithoutABillingStore pins the mark on the
// funnel path with no billing store, where afterTerminalRelease returns
// before the billing step, and on a session that never had a pod.
//
// spec: §7.1 (Terminal pipeline context)
func TestTerminalPipelineMarkIsWrittenWithoutABillingStore(t *testing.T) {
	store := memstore.New()
	row := seedTerminal(t, store, "sess_nobilling", time.Now())
	srv := New(store, Options{DevTenantHeader: true})
	t.Cleanup(srv.Close)

	srv.OnSessionTerminal(context.Background(), session.StateRunning, row)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.WaitTerminalWork(ctx); err != nil {
		t.Fatalf("wait for the terminal pipeline: %v", err)
	}
	if markOf(t, store, row.ID).IsZero() {
		t.Error("the pipeline mark is not written on the path with no billing store")
	}
}

// TestTerminalPipelineMarkIsWrittenByThePostTerminalHook pins the mark on the
// writers that call AfterTerminalWrite directly, for which the hook is the
// whole terminal pipeline.
//
// spec: §7.1 (Terminal pipeline context)
func TestTerminalPipelineMarkIsWrittenByThePostTerminalHook(t *testing.T) {
	store := memstore.New()
	row := seedTerminal(t, store, "sess_hook", time.Now())
	srv := New(store, Options{DevTenantHeader: true})
	t.Cleanup(srv.Close)

	srv.AfterTerminalWrite(context.Background(), session.StateRunning, row, nil)

	if markOf(t, store, row.ID).IsZero() {
		t.Error("AfterTerminalWrite did not write the pipeline mark")
	}
}

// TestTerminalPipelineMarkIsNotWrittenAfterTheBudget pins the budget arm: a
// pipeline whose context expired, because its session ended more than one
// terminal pipeline budget ago, writes no mark, on both entry points.
//
// spec: §7.1 (Terminal pipeline context)
func TestTerminalPipelineMarkIsNotWrittenAfterTheBudget(t *testing.T) {
	store := memstore.New()
	srv := New(store, Options{DevTenantHeader: true, Billing: billingstore.NewMemory()})
	t.Cleanup(srv.Close)
	past := time.Now().Add(-srv.terminalPipelineBudget() - time.Minute)

	funnel := seedTerminal(t, store, "sess_late_funnel", past)
	srv.recordSessionCompleted(context.Background(), session.StateRunning, funnel)
	if mark := markOf(t, store, funnel.ID); !mark.IsZero() {
		t.Errorf("recordSessionCompleted wrote the mark %v after the budget, want none", mark)
	}

	hook := seedTerminal(t, store, "sess_late_hook", past)
	srv.AfterTerminalWrite(context.Background(), session.StateRunning, hook, nil)
	if mark := markOf(t, store, hook.ID); !mark.IsZero() {
		t.Errorf("AfterTerminalWrite wrote the mark %v after the budget, want none", mark)
	}
}

// TestTerminalPipelineMarkWriteOutcomes pins the write's error handling: a
// row that already carries a mark keeps its first instant, a missing row and
// a failing store end the pipeline without a panic or a retry, and a failing
// store leaves the row without a mark.
//
// spec: §7.1 (Terminal pipeline context)
func TestTerminalPipelineMarkWriteOutcomes(t *testing.T) {
	store := memstore.New()
	row := seedTerminal(t, store, "sess_twice", time.Now())
	srv := New(store, Options{DevTenantHeader: true})
	t.Cleanup(srv.Close)
	ctx := context.Background()

	srv.recordTerminalPipelineDone(ctx, row)
	first := markOf(t, store, row.ID)
	if first.IsZero() {
		t.Fatal("first mark not written")
	}
	srv.recordTerminalPipelineDone(ctx, row)
	if again := markOf(t, store, row.ID); !again.Equal(first) {
		t.Errorf("second mark moved the instant from %v to %v", first, again)
	}
	srv.recordTerminalPipelineDone(ctx, sessionstore.Session{ID: "sess_gone", TenantID: "acme"})

	failing := failingMarkStore{Store: memstore.New(), err: errors.New("primary unreachable")}
	failed := seedTerminal(t, failing.Store, "sess_fail", time.Now())
	fsrv := New(failing, Options{DevTenantHeader: true})
	t.Cleanup(fsrv.Close)
	fsrv.recordTerminalPipelineDone(ctx, failed)
	if mark := markOf(t, failing.Store, failed.ID); !mark.IsZero() {
		t.Errorf("a failing store recorded the mark %v", mark)
	}
}
