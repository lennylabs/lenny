// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// cancelTrackingExecutor implements Executor and SessionReleaser so a test can
// assert the §6.2 disposition recorded at teardown keyed by session id. The
// cascade-drain (F-11.3.9) must release every cancelled descendant, not just
// the directly-terminated parent.
type cancelTrackingExecutor struct {
	mu       sync.Mutex
	released map[string]executor.Disposition
}

func newCancelTrackingExecutor() *cancelTrackingExecutor {
	return &cancelTrackingExecutor{released: map[string]executor.Disposition{}}
}

func (e *cancelTrackingExecutor) Send(context.Context, string, []executor.Message) (executor.Response, error) {
	return executor.Response{}, nil
}

func (e *cancelTrackingExecutor) Close(_ context.Context, id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.released[id] = ""
	return nil
}

func (e *cancelTrackingExecutor) Release(_ context.Context, id string, d executor.Disposition) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.released[id] = d
	return nil
}

func (e *cancelTrackingExecutor) dispositionOf(id string) (executor.Disposition, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	d, ok := e.released[id]
	return d, ok
}

func seedRunningChild(t *testing.T, store sessionstore.Store, id, parent string) {
	t.Helper()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: "acme", State: session.StateRunning,
		ParentSessionID: parent, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

// TestCascadeCancelDrainsDescendantRuntimes_spec_11_3_9 asserts that a cancel
// cascade (DELETE of a parent under the default cancel_all policy) drains every
// descendant's pod, not merely the directly-terminated parent's. Before
// F-11.3.9 the cascade flipped each descendant row to cancelled but never
// released the executor, so the descendant runtimes kept running until the
// watchdog's maxSessionAge clock fired hours later. spec: §11.3 line 236; §11.4
// line 258; §8.10 cascade.
func TestCascadeCancelDrainsDescendantRuntimes_spec_11_3_9(t *testing.T) {
	store := memstore.New()
	exec := newCancelTrackingExecutor()
	srv := sessionserver.New(store, sessionserver.Options{Executor: exec})

	// parent → child → grandchild, all running, default cancel_all cascade.
	seedRunning(t, store, "p")
	seedRunningChild(t, store, "c1", "p")
	seedRunningChild(t, store, "c2", "c1")

	req := httptest.NewRequest(http.MethodDelete, "/v1/sessions/p", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, want 200", rr.Code)
	}

	// The parent and both descendants must each be released with the §6.2
	// cancelled disposition (the parent via recordSessionCompleted, the
	// descendants via the cascade).
	for _, id := range []string{"p", "c1", "c2"} {
		d, ok := exec.dispositionOf(id)
		if !ok || d != executor.DispositionCancelled {
			t.Errorf("F-11.3.9: session %s released disposition = %q (present=%v), want cancelled", id, d, ok)
		}
		row, err := store.Get(context.Background(), "acme", id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		if row.State != session.StateCancelled {
			t.Errorf("F-11.3.9: session %s state = %q, want cancelled", id, row.State)
		}
	}
}

// TestCascadeCancelDetachLeavesGrandchildRuntimeRunning_spec_11_3_9 asserts the
// cascade-drain respects the per-node §8.10 policy: a detach child shields its
// own subtree, so the cascade neither cancels nor drains a grandchild under a
// detach node. Only the descendants the cascade actually cancels are drained.
func TestCascadeCancelDetachLeavesGrandchildRuntimeRunning_spec_11_3_9(t *testing.T) {
	store := memstore.New()
	exec := newCancelTrackingExecutor()
	srv := sessionserver.New(store, sessionserver.Options{Executor: exec})

	seedRunning(t, store, "p")
	// c1 detaches, so its child gc must stay running and undrained.
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "c1", TenantID: "acme", State: session.StateRunning,
		ParentSessionID: "p", CascadeOnFailure: session.CascadeDetach,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed c1: %v", err)
	}
	seedRunningChild(t, store, "gc", "c1")

	req := httptest.NewRequest(http.MethodDelete, "/v1/sessions/p", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, want 200", rr.Code)
	}

	// c1 is cancelled+drained; gc (under the detach node) is neither.
	if d, ok := exec.dispositionOf("c1"); !ok || d != executor.DispositionCancelled {
		t.Errorf("F-11.3.9: c1 released = %q (present=%v), want cancelled", d, ok)
	}
	if _, ok := exec.dispositionOf("gc"); ok {
		t.Errorf("F-11.3.9: detach grandchild gc must not be drained, but it was released")
	}
	gc, err := store.Get(context.Background(), "acme", "gc")
	if err != nil {
		t.Fatalf("get gc: %v", err)
	}
	if gc.State != session.StateRunning {
		t.Errorf("F-11.3.9: detach grandchild gc state = %q, want running", gc.State)
	}
}
