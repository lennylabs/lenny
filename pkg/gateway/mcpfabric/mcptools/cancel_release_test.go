// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// releaseRecordingExecutor records the §6.2 disposition the gateway records per
// session id at teardown so the cancel_child drain test (F-11.3.1) can assert
// each cancelled descendant's pod was released, not merely its row flipped.
type releaseRecordingExecutor struct {
	mu       sync.Mutex
	released map[string]executor.Disposition
}

func (e *releaseRecordingExecutor) Send(context.Context, string, []executor.Message) (executor.Response, error) {
	return executor.Response{}, nil
}

func (e *releaseRecordingExecutor) Close(_ context.Context, id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.released[id] = ""
	return nil
}

func (e *releaseRecordingExecutor) Release(_ context.Context, id string, d executor.Disposition) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.released[id] = d
	return nil
}

func (e *releaseRecordingExecutor) dispositionOf(id string) (executor.Disposition, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	d, ok := e.released[id]
	return d, ok
}

// TestCancelChildDrainsCancelledRuntimes_spec_11_3_1 asserts that
// lenny/cancel_child drains every cancelled session's pod via the executor,
// recording the §6.2 cancelled disposition. Before F-11.3.1 the handler only
// flipped each descendant row to cancelled and left the child agents running,
// holding tokens and charging credential leases until the watchdog's
// maxSessionAge clock fired. spec: §8.5; §11.3; §11.4.
func TestCancelChildDrainsCancelledRuntimes_spec_11_3_1(t *testing.T) {
	store := memstore.New()
	exec := &releaseRecordingExecutor{released: map[string]executor.Disposition{}}
	hook := newHookTerminal(exec)
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		DevTenantFallback: true,
		Store:             store,
		Executor:          exec,
		Terminal:          hook,
		Clock:             func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:            func() string { return "sess_mcp" },
		TenantID:          "acme",
	})

	// sess_root → sess_a (target) → sess_a1 (running), all cancel_all.
	mkSession(t, store, "sess_root", session.StateRunning, "")
	mkSession(t, store, "sess_a", session.StateRunning, "sess_root")
	mkSession(t, store, "sess_a1", session.StateRunning, "sess_a")

	resp := call(t, srv.Handler(), "lenny/cancel_child",
		`{"parentSessionId":"sess_root","childSessionId":"sess_a"}`)
	_ = resultText(t, resp)
	calls := hook.waitSettled(t, 2)
	for _, c := range calls {
		if c.from != session.StateRunning || c.to != session.StateCancelled {
			t.Errorf("hook call %+v, want running -> cancelled with the locked row's fromState", c)
		}
	}

	// Both cancelled sessions must be drained with the cancelled disposition.
	for _, id := range []string{"sess_a", "sess_a1"} {
		d, ok := exec.dispositionOf(id)
		if !ok || d != executor.DispositionCancelled {
			t.Errorf("F-11.3.1: session %s released disposition = %q (present=%v), want cancelled", id, d, ok)
		}
	}
	// The calling parent is not part of the cancelled set, so it is not drained.
	if _, ok := exec.dispositionOf("sess_root"); ok {
		t.Errorf("F-11.3.1: calling parent sess_root must not be drained by cancel_child")
	}
}

// primaryListingStore records the List calls that read the primary and runs
// afterFirstPrimary once, after the first such listing returns.
type primaryListingStore struct {
	*memstore.Store
	mu                sync.Mutex
	primaryListings   int
	afterFirstPrimary func()
	primaryErr        error
}

func (s *primaryListingStore) List(ctx context.Context, tenantID string, f sessionstore.ListFilter) ([]sessionstore.Session, error) {
	if f.Primary && s.primaryErr != nil {
		return nil, s.primaryErr
	}
	rows, err := s.Store.List(ctx, tenantID, f)
	var hook func()
	s.mu.Lock()
	if f.Primary {
		s.primaryListings++
		if s.primaryListings == 1 {
			hook = s.afterFirstPrimary
		}
	}
	s.mu.Unlock()
	if hook != nil {
		hook()
	}
	return rows, err
}

func (s *primaryListingStore) primary() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.primaryListings
}

// TestCancelChildListsPrimaryPerLevel_spec_12_8 asserts that the
// lenny/cancel_child walk lists the tenant from the primary after each
// level's cancel writes, and that a grandchild inserted under the target's
// running child after the first listing returned, as a child the creation
// fence admitted before its parent's terminal write is, ends cancelled. The
// handler's pre-write tree listing serves only authorization, so a walk that
// queued children from it would leave the grandchild running.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 8.10 (Delegation Tree Recovery), 8.5
func TestCancelChildListsPrimaryPerLevel_spec_12_8(t *testing.T) {
	store := &primaryListingStore{Store: memstore.New()}
	exec := &releaseRecordingExecutor{released: map[string]executor.Disposition{}}
	hook := newHookTerminal(exec)
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		DevTenantFallback: true,
		Store:             store,
		Executor:          exec,
		Terminal:          hook,
		Clock:             func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:            func() string { return "sess_mcp" },
		TenantID:          "acme",
	})
	mkSession(t, store, "sess_root", session.StateRunning, "")
	mkSession(t, store, "sess_a", session.StateRunning, "sess_root")
	mkSession(t, store, "sess_a1", session.StateRunning, "sess_a")
	store.afterFirstPrimary = func() {
		if err := store.Create(context.Background(), sessionstore.Session{
			ID: "sess_a1_late", TenantID: "acme", State: session.StateRunning,
			ParentSessionID: "sess_a1", RootSessionID: "sess_root", DelegationDepth: 3,
		}); err != nil {
			t.Errorf("insert the late grandchild: %v", err)
		}
	}

	_ = resultText(t, call(t, srv.Handler(), "lenny/cancel_child",
		`{"parentSessionId":"sess_root","childSessionId":"sess_a"}`))
	hook.waitSettled(t, 3)

	for _, id := range []string{"sess_a", "sess_a1", "sess_a1_late"} {
		row, err := store.Get(context.Background(), "acme", id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		if row.State != session.StateCancelled {
			t.Errorf("%s state = %q after cancel_child, want cancelled", id, row.State)
		}
	}
	// Levels after the target: the target's children, the child's children,
	// and the late grandchild's (empty) children.
	if got := store.primary(); got != 3 {
		t.Errorf("primary listings = %d, want 3 (one per tree level below the target)", got)
	}
}

// TestCancelChildPrimaryListingErrorFailsTheCall_spec_12_8 asserts that a
// failed primary listing after the target's cancel write fails the
// lenny/cancel_child call: the target stays cancelled and its running child,
// which the walk could not list, is not cancelled.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 8.5
func TestCancelChildPrimaryListingErrorFailsTheCall_spec_12_8(t *testing.T) {
	store := &primaryListingStore{Store: memstore.New(), primaryErr: errors.New("primary unreachable")}
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		DevTenantFallback: true,
		Store:             store,
		Clock:             func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:            func() string { return "sess_mcp" },
		TenantID:          "acme",
	})
	mkSession(t, store, "sess_root", session.StateRunning, "")
	mkSession(t, store, "sess_a", session.StateRunning, "sess_root")
	mkSession(t, store, "sess_a1", session.StateRunning, "sess_a")

	resp := call(t, srv.Handler(), "lenny/cancel_child",
		`{"parentSessionId":"sess_root","childSessionId":"sess_a"}`)
	if result, ok := resp["result"].(map[string]any); ok && result["isError"] != true {
		t.Fatalf("cancel_child succeeded with a failed primary listing: %+v", resp)
	}
	for id, want := range map[string]session.State{"sess_a": session.StateCancelled, "sess_a1": session.StateRunning} {
		row, err := store.Get(context.Background(), "acme", id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		if row.State != want {
			t.Errorf("%s state = %q, want %q", id, row.State, want)
		}
	}
}
