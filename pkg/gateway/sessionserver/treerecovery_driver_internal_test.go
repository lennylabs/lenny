// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// flakyPrimary is a session store whose primary read fails the first fails
// calls, so a test can pin how a reader handles a primary-read error.
type flakyPrimary struct {
	sessionstore.Store
	fails atomic.Int32
	calls atomic.Int32
}

func (f *flakyPrimary) GetPrimary(ctx context.Context, tenantID, id string) (sessionstore.Session, error) {
	f.calls.Add(1)
	if f.fails.Add(-1) >= 0 {
		return sessionstore.Session{}, errors.New("primary unreachable")
	}
	return f.Get(ctx, tenantID, id)
}

// nonTerminalStates is every non-terminal state of the session state
// machine.
func nonTerminalStates() []session.State {
	var out []session.State
	for _, st := range session.AllStates() {
		if !session.IsTerminal(st) {
			out = append(out, st)
		}
	}
	return out
}

// spec: §8.10 (Delegation Tree Recovery)
// diagnosis: tree recovery selects a node by its row state, exactly the
// resume_pending nodes, whatever this replica's pod registry holds and
// whichever node's commit started the traversal; it drives each selected node
// through the resume driver and fails or expires no node it did not select.
// A failure means a node bound on a peer, or a running node absent from this
// replica's registry, was driven, failed, or expired.
func TestTreeRecoverySelectsExactlyResumePendingNodes_spec_8_10(t *testing.T) {
	for _, st := range nonTerminalStates() {
		for _, inRegistry := range []bool{false, true} {
			for _, trigger := range []string{"root", "sibling", "node"} {
				name := string(st) + "/registry=" + map[bool]string{false: "absent", true: "present"}[inRegistry] + "/" + trigger
				t.Run(name, func(t *testing.T) {
					store := memstore.New()
					reg := podsession.NewRegistry()
					srv := New(store, Options{DevTenantHeader: true, PodRegistry: reg})
					srv.treePollInterval = 2 * time.Millisecond
					ctx := context.Background()
					rows := []sessionstore.Session{
						{ID: "root", TenantID: "acme", RootSessionID: "root", State: session.StateRunning},
						{ID: "node", TenantID: "acme", ParentSessionID: "root", RootSessionID: "root", DelegationDepth: 1, State: st},
						{ID: "sibling", TenantID: "acme", ParentSessionID: "root", RootSessionID: "root", DelegationDepth: 1, State: session.StateRunning},
					}
					for _, r := range rows {
						if err := store.Create(ctx, r); err != nil {
							t.Fatalf("seed %s: %v", r.ID, err)
						}
					}
					if inRegistry {
						reg.Put(&podsession.BindResult{SessionID: "node", TenantID: "acme", SandboxName: "sbx-node"})
					}
					from, _ := store.Get(ctx, "acme", trigger)
					root := srv.treeRoot(ctx, from)
					if root != "root" {
						t.Fatalf("treeRoot(%s) = %q, want root", trigger, root)
					}
					if _, err := srv.treeRecovery.RecoverTree(ctx, "acme", root); err != nil {
						t.Fatalf("RecoverTree: %v", err)
					}
					srv.WaitResumeIdle("node")
					got, _ := store.Get(ctx, "acme", "node")
					want := st
					if st == session.StateResumePending {
						// The binderless driver commits the selected node.
						want = session.StateRunning
					}
					if got.State != want {
						t.Errorf("node state = %q, want %q", got.State, want)
					}
					if sib, _ := store.Get(ctx, "acme", "sibling"); sib.State != session.StateRunning {
						t.Errorf("sibling state = %q, want running untouched", sib.State)
					}
				})
			}
		}
	}
}

// spec: §8.10 (Delegation Tree Recovery), §7.3 (Resume driver)
// diagnosis: ReattachNode returns only after the node leaves resume_pending
// and resuming: nil for any state a committed restore reaches (running,
// input_required, and suspended), and an error for awaiting_client_action or
// a terminal state. A primary-read error on a poll fails no node, and a wait
// whose context ends returns at that deadline. A failure means a node is
// counted recovered before its restore's outcome, a restored node that left
// running before the poll is counted failed, or a read error fails a node.
func TestTreeRecoveryReattachWaitsForTheRestoreOutcome_spec_8_10(t *testing.T) {
	for _, tc := range []struct {
		final   session.State
		wantErr bool
	}{
		{session.StateRunning, false},
		{session.StateInputRequired, false},
		{session.StateSuspended, false},
		{session.StateAwaitingClientAction, true},
		{session.StateFailed, true},
		{session.StateCancelled, true},
		{session.StateExpired, true},
	} {
		t.Run(string(tc.final), func(t *testing.T) {
			inner := memstore.New()
			store := &flakyPrimary{Store: inner}
			store.fails.Store(2)
			srv := New(store, Options{DevTenantHeader: true})
			srv.treePollInterval = 2 * time.Millisecond
			ctx := context.Background()
			node := sessionstore.Session{ID: "n", TenantID: "acme", State: session.StateResumePending}
			if err := inner.Create(ctx, node); err != nil {
				t.Fatalf("seed: %v", err)
			}
			done := make(chan error, 1)
			go func() { done <- (sessionNodeReattacher{s: srv}).ReattachNode(ctx, node) }()

			// The wait holds through resume_pending, resuming, and the
			// read errors.
			time.Sleep(20 * time.Millisecond)
			if _, err := inner.Update(ctx, "acme", "n", func(r *sessionstore.Session) error {
				r.State = session.StateResuming
				return nil
			}); err != nil {
				t.Fatalf("move to resuming: %v", err)
			}
			time.Sleep(20 * time.Millisecond)
			select {
			case err := <-done:
				t.Fatalf("ReattachNode returned %v while the node was still restoring", err)
			default:
			}
			if _, err := inner.Update(ctx, "acme", "n", func(r *sessionstore.Session) error {
				r.State = tc.final
				return nil
			}); err != nil {
				t.Fatalf("move to %s: %v", tc.final, err)
			}
			select {
			case err := <-done:
				if (err != nil) != tc.wantErr {
					t.Errorf("ReattachNode = %v, want error %v", err, tc.wantErr)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("ReattachNode did not return after the restore's outcome")
			}
			if store.calls.Load() < 3 {
				t.Errorf("primary reads = %d, want the failed reads retried", store.calls.Load())
			}
		})
	}

	t.Run("deadline", func(t *testing.T) {
		store := memstore.New()
		srv := New(store, Options{DevTenantHeader: true})
		srv.treePollInterval = 2 * time.Millisecond
		node := sessionstore.Session{ID: "n", TenantID: "acme", State: session.StateResumePending}
		if err := store.Create(context.Background(), node); err != nil {
			t.Fatalf("seed: %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()
		begin := time.Now()
		err := (sessionNodeReattacher{s: srv}).ReattachNode(ctx, node)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("ReattachNode = %v, want the deadline", err)
		}
		if elapsed := time.Since(begin); elapsed > 2*time.Second {
			t.Errorf("wait took %v past a 30ms deadline", elapsed)
		}
	})
}

// spec: §8.10 (Delegation Tree Recovery), §7.3 (Resume driver)
// diagnosis: StartNode routes a selected node through DriveResume, so a node
// another replica coordinates starts no attempt here and its wait waits for
// that replica's driver. A failure means tree recovery claimed a pod for a
// node a peer coordinates.
func TestTreeRecoveryStartNodeHonorsTheLeaseGate_spec_8_10(t *testing.T) {
	f := newFunnelFixture(t, funnelOpts{})
	if err := f.store.Create(context.Background(), sessionstore.Session{
		ID: "sess-child", TenantID: "acme", RuntimeRef: "echo", State: session.StateResumePending,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	f.leases.holders[leaseK("acme", "sess-child")] = "rep-2"
	node := f.state(t, "sess-child")

	(sessionNodeReattacher{s: f.srv}).StartNode(context.Background(), node)
	if f.srv.resumeAttempts.size() != 0 {
		t.Errorf("attempts in flight = %d, want none on a replica without the lease", f.srv.resumeAttempts.size())
	}
	if got := f.state(t, "sess-child"); got.State != session.StateResumePending || got.CoordinationGeneration != node.CoordinationGeneration {
		t.Errorf("row = %q gen %d, want resume_pending gen %d unchanged", got.State, got.CoordinationGeneration, node.CoordinationGeneration)
	}
	if calls := f.exec.callList(); len(calls) != 0 {
		t.Errorf("executor calls = %v, want none", calls)
	}
}

// waitShort sleeps one short polling interval.
func waitShort() { time.Sleep(2 * time.Millisecond) }
