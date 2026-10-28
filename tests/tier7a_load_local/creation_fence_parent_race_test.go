// SPDX-License-Identifier: MIT

//go:build load_local

package tier7a_load_local_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// runParentTerminalRound races delegated child creates under one running
// parent against one Update that moves the parent to a terminal state,
// lists the tenant from the primary as soon as that Update returns, and
// checks that every admitted child is on that listing and every refused
// create carries DELEGATION_PARENT_REVOKED and left no row.
func runParentTerminalRound(t *testing.T, s fenceStores, rng *rand.Rand, creates int) {
	t.Helper()
	ctx := context.Background()
	tenant := s.newTenant(t)
	parent := sessionstore.Session{ID: uuid.NewString(), TenantID: tenant, UserID: "alice", RuntimeRef: "echo", State: session.StateRunning}
	if err := s.sessions.Create(ctx, parent); err != nil {
		t.Fatalf("seed parent: %v", err)
	}
	delays := make([]time.Duration, creates)
	for i := range delays {
		delays[i] = time.Duration(rng.Intn(3000)) * time.Microsecond
	}
	terminalDelay := time.Duration(rng.Intn(3000)) * time.Microsecond
	terminal := session.TerminalStates()[rng.Intn(len(session.TerminalStates()))]

	outcomes := make([]createOutcome, creates)
	var wg sync.WaitGroup
	for i := 0; i < creates; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			time.Sleep(delays[i])
			row := sessionstore.Session{
				ID: uuid.NewString(), TenantID: tenant, UserID: "alice", RuntimeRef: "echo",
				State: session.StateRunning, ParentSessionID: parent.ID, RootSessionID: parent.ID, DelegationDepth: 1,
			}
			outcomes[i] = createOutcome{row: row, err: s.sessions.Create(ctx, row)}
		}(i)
	}
	time.Sleep(terminalDelay)
	if _, err := s.sessions.Update(ctx, tenant, parent.ID, func(r *sessionstore.Session) error {
		r.State = terminal
		return nil
	}); err != nil {
		t.Fatalf("parent terminal write: %v", err)
	}
	// The listing the cascade would take, while creates are still in flight.
	byParent, err := sessionstore.ChildrenByParent(ctx, s.sessions, tenant)
	if err != nil {
		t.Fatalf("list after the terminal write: %v", err)
	}
	listed := map[string]bool{}
	for _, c := range byParent[parent.ID] {
		listed[c.ID] = true
	}
	wg.Wait()
	for _, o := range outcomes {
		var ref *sessionstore.CreationRefusal
		switch {
		case o.err == nil:
			if !listed[o.row.ID] {
				t.Fatalf("admitted child %s is missing from the primary listing taken after the parent's terminal write", o.row.ID)
			}
		case errors.As(o.err, &ref):
			if ref.Code != sessionstore.RefusalDelegationParentRevoked {
				t.Fatalf("child %s refused with %s, want DELEGATION_PARENT_REVOKED", o.row.ID, ref.Code)
			}
			if _, gerr := s.sessions.Get(ctx, tenant, o.row.ID); !errors.Is(gerr, sessionstore.ErrNotFound) {
				t.Fatalf("refused child %s left a row (%v)", o.row.ID, gerr)
			}
		default:
			t.Fatalf("child %s: unexpected error %v", o.row.ID, o.err)
		}
	}
}

// TestCreationFenceRaceAgainstParentTerminalWrite_spec_12_8 runs randomized
// interleavings of delegated child creates against their parent's terminal
// write, over the in-memory store and the Postgres store, under -race.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 8.2 (Parent freshness), 8.10 (Delegation Tree Recovery)
// diagnosis: a failure means a delegated child can commit after its parent's
// terminal write without appearing on the cascade's primary listing, or a
// refused child leaves a row or carries another code: the fence's parent read
// is not serialized with the parent's terminal Update on that store, so a
// child escapes the cascade.
func TestCreationFenceRaceAgainstParentTerminalWrite_spec_12_8(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewSource(seed))
	postures := []struct {
		name    string
		stores  func(t *testing.T) fenceStores
		rounds  int
		creates int
	}{
		{"memstore", func(*testing.T) fenceStores { return memFenceStores() }, 60, 24},
		{"pgstore", pgFenceStores, 16, 12},
	}
	for _, p := range postures {
		t.Run(p.name, func(t *testing.T) {
			s := p.stores(t)
			for r := 0; r < p.rounds; r++ {
				t.Run(fmt.Sprintf("round-%d", r), func(t *testing.T) {
					runParentTerminalRound(t, s, rng, p.creates)
				})
			}
		})
	}
}
