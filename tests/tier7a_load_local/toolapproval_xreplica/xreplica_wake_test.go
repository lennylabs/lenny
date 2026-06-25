// SPDX-License-Identifier: MIT

//go:build load_local

// Package toolapproval_xreplica exercises the §7.2 tool-approval await's
// cross-replica wake fallback (F-IA1) under the tier-7a load_local build
// tag with the race detector enabled.
//
// On the supported multi-replica gateway topology the §7.2 approve/deny
// endpoint may land on a replica other than the one whose ToolApprovalGate
// is blocked in AwaitApproval. The coordinating replica's in-process waiter
// registry is the fast path; a non-coordinator approve updates only the
// Postgres-backed shared interaction store. Without a store poll the blocked
// call would wait out the full approval_timeout before the runtime's tool
// call could proceed. This test resolves the approval ONLY through the
// shared store (the local registry is never resolved) and asserts every
// concurrently-blocked await wakes well before its approval_timeout, with
// the correct verdict, under -race.
//
// TESTING.md §12.7.a regression scenarios.
package toolapproval_xreplica

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/toolapproval"
)

// approvalTimeout is the worst-case block bound the test asserts the
// store-poll wake beats by a wide margin. A correct poll wakes within a few
// poll intervals (the gate polls every 25ms); this generous timeout fails
// the test only if the await falls through to the timeout denial, which is
// the exact F-IA1 regression (no store-poll fallback, so the cross-replica
// approve is invisible to the blocked replica).
const approvalTimeout = 5 * time.Second

// wakeBudget bounds how long a correctly-polling await may take to observe
// the store-side resolution. It is far below approvalTimeout so a regressed
// gate that wakes only on the timeout is caught.
const wakeBudget = 2 * time.Second

// spec: §7.2 (tool-use approve/deny resolution; cross-replica wake), F-IA1.
// diagnosis: a failure means the §7.2 tool-approval await did not wake from
// a resolution that landed only on the shared interaction store (the
// non-coordinator-replica approve/deny path). Either the store poll is
// absent (the F-IA1 regression — the await blocks until approval_timeout) or
// it reports the wrong verdict. Under -race it also catches a data race
// between the poll loop and the store writer.
func TestCrossReplicaStoreWakesBlockedApproval(t *testing.T) {
	t.Parallel()

	const sessions = 16

	// One shared interaction store stands in for the Postgres-backed
	// cross-replica store; the per-gate registry stands in for one
	// replica's in-process waiter set. The approve writes go to the store
	// alone, never to the registry, modelling an approve POSTed to a
	// different replica.
	inter := interactionstore.NewMemory()
	store := memstore.New()
	gate := sessionserver.NewToolApprovalGate(
		store, inter, sessionevents.NewBus(64), toolapproval.NewRegistry(),
		time.Now, approvalTimeout,
	)

	type outcome struct {
		decision executor.ApprovalDecision
		err      error
		waited   time.Duration
	}

	var wg sync.WaitGroup
	outcomes := make([]outcome, sessions)
	deny := func(i int) bool { return i%2 == 1 } // half approve, half deny

	for i := 0; i < sessions; i++ {
		sessionID := fmt.Sprintf("sess_%02d", i)
		if err := store.Create(context.Background(), sessionstore.Session{
			ID: sessionID, TenantID: "acme", UserID: "alice",
		}); err != nil {
			t.Fatalf("seed session %s: %v", sessionID, err)
		}
	}

	started := make(chan struct{}, sessions)
	for i := 0; i < sessions; i++ {
		i := i
		sessionID := fmt.Sprintf("sess_%02d", i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			started <- struct{}{}
			begin := time.Now()
			d, err := gate.AwaitApproval(context.Background(), "acme", sessionID,
				executor.PendingToolCall{ID: "tc-1", Name: "lenny/deploy"})
			outcomes[i] = outcome{decision: d, err: err, waited: time.Since(begin)}
		}()
	}

	// Wait until every await has entered AwaitApproval before resolving so
	// the resolution genuinely races the blocked poll rather than landing
	// before the interaction is recorded.
	for i := 0; i < sessions; i++ {
		<-started
	}

	// Resolve each interaction ONLY through the shared store, the way a
	// non-coordinator replica's approve/deny endpoint would, after waiting
	// for the gate to record the pending interaction. The local registry is
	// deliberately never resolved.
	for i := 0; i < sessions; i++ {
		sessionID := fmt.Sprintf("sess_%02d", i)
		waitForPendingInteraction(t, inter, sessionID)
		wantDeny := deny(i)
		if _, err := inter.Resolve(context.Background(), "acme", sessionID, "alice", "tc-1",
			func(in *interactionstore.Interaction) error {
				if wantDeny {
					in.Phase = interactionstore.PhaseDenied
					in.Reason = "unsafe"
				} else {
					in.Phase = interactionstore.PhaseApproved
				}
				return nil
			}); err != nil {
			t.Fatalf("store-side resolve %s: %v", sessionID, err)
		}
	}

	wg.Wait()

	for i := 0; i < sessions; i++ {
		o := outcomes[i]
		if o.err != nil {
			t.Errorf("session %02d: AwaitApproval returned error %v", i, o.err)
			continue
		}
		if o.waited >= wakeBudget {
			t.Errorf("session %02d: woke after %s, want < %s (store poll did not wake the await before approval_timeout — F-IA1 regression)",
				i, o.waited, wakeBudget)
		}
		if deny(i) {
			if o.decision.Approved {
				t.Errorf("session %02d: decision approved, want denied from the store resolution", i)
			}
			if o.decision.Reason != "unsafe" {
				t.Errorf("session %02d: deny reason = %q, want the persisted %q", i, o.decision.Reason, "unsafe")
			}
		} else if !o.decision.Approved {
			t.Errorf("session %02d: decision denied (%q), want approved from the store resolution", i, o.decision.Reason)
		}
	}
}

// waitForPendingInteraction blocks until the gate has recorded the pending
// KindToolUse interaction for the session, so the store-side resolve targets
// an existing pending row rather than racing ahead of the Put.
func waitForPendingInteraction(t *testing.T, store interactionstore.Store, sessionID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, err := store.Get(context.Background(), "acme", sessionID, "alice", "tc-1")
		if err == nil && got.Phase == interactionstore.PhasePending {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("pending interaction for %s never recorded", sessionID)
}
