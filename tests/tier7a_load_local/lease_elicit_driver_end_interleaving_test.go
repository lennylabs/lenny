// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local coverage for the §8.6 elicitation coordinator when the
// session that drives an elicitation ends before the user decides. Each
// round runs one tree's concurrent extension requests against an elicitor
// that, at random, ends the driving session mid-elicitation (returning
// ErrRequesterEnded, as the production elicitor does on a poll tick) or
// approves after a random delay. A short success cool-off makes the
// coordinator's reclamation timer race the arriving requests.
//
// spec: §8.6 (Elicitation mode flow), §8.6 (Extension state)
package tier7a_load_local_test

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

const (
	driverEndRounds          = 60
	driverEndSessionsPerTree = 8
)

// driverEndSessions is the in-memory session reader the store-backed budget
// source reads; end moves a row to a terminal state.
type driverEndSessions struct {
	mu   sync.Mutex
	rows map[string]sessionstore.Session
}

func (s *driverEndSessions) Get(_ context.Context, tenantID, id string) (sessionstore.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rows[id]
	if !ok || r.TenantID != tenantID {
		return sessionstore.Session{}, sessionstore.ErrNotFound
	}
	return r, nil
}

func (s *driverEndSessions) end(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rows[id]
	r.State = session.StateCancelled
	s.rows[id] = r
}

func (s *driverEndSessions) ended(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return session.IsTerminal(s.rows[id].State)
}

// driverEndElicitor holds each elicitation open for a random delay and then
// either ends the requesting session and reports ErrRequesterEnded, or
// approves. It records the most elicitations it saw open at once.
type driverEndElicitor struct {
	sessions *driverEndSessions
	endProb  float64

	mu      sync.Mutex
	rng     *rand.Rand
	open    int
	maxOpen int
	calls   int
}

func (e *driverEndElicitor) Elicit(ctx context.Context, _ /*tenantID*/, sessionID string) (bool, error) {
	e.mu.Lock()
	e.open++
	e.calls++
	if e.open > e.maxOpen {
		e.maxOpen = e.open
	}
	delay := time.Duration(e.rng.Intn(3000)) * time.Microsecond
	end := e.rng.Float64() < e.endProb
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.open--
		e.mu.Unlock()
	}()

	select {
	case <-time.After(delay):
	case <-ctx.Done():
		return false, ctx.Err()
	}
	if end {
		e.sessions.end(sessionID)
		return false, fmt.Errorf("requester %s ended: %w", sessionID, leasecontrol.ErrRequesterEnded)
	}
	return true, nil
}

// seedDriverEndTree writes one root and its delegated children, each with
// effectively unbounded extension headroom.
func seedDriverEndTree(root string) (*driverEndSessions, []string) {
	s := &driverEndSessions{rows: map[string]sessionstore.Session{}}
	s.rows[root] = sessionstore.Session{
		ID: root, TenantID: "acme", RootSessionID: root, State: session.StateRunning,
		DelegationLease: &sessionstore.DelegationLease{MaxTokenBudget: 100_000},
	}
	ids := []string{root}
	for j := 1; j < driverEndSessionsPerTree; j++ {
		id := fmt.Sprintf("%s-child-%d", root, j)
		parent := id + "-parent"
		s.rows[parent] = sessionstore.Session{ID: parent, TenantID: "acme", RootSessionID: root, State: session.StateRunning, DelegationDepth: 1}
		s.rows[id] = sessionstore.Session{
			ID: id, TenantID: "acme", RootSessionID: root, ParentSessionID: parent,
			DelegationDepth: 2, State: session.StateRunning,
		}
		ids = append(ids, id)
	}
	return s, ids
}

// TestLeaseElicitDriverEndInterleavings_spec_8_6 runs randomized
// driver-end, waiter-arrival, approval, and reclamation-timer interleavings
// and checks that a live session's request is always granted, so it never
// fails because another session's elicitation ended, and that the tree never
// has two elicitations open at once.
//
// spec: §8.6 (Elicitation mode flow), §8.6 (Extension state)
// diagnosis: an error for a live session means the driver's end reached a
// batched request as its outcome instead of redriving it. More than
// one elicitation open at once means two batches were pending for one tree,
// so the reclamation discarded a tree entry that still had a pending batch
// or two redriven requests each opened a batch.
func TestLeaseElicitDriverEndInterleavings_spec_8_6(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewSource(seed))

	for round := 0; round < driverEndRounds; round++ {
		root := fmt.Sprintf("root-%d", round)
		sessions, ids := seedDriverEndTree(root)
		src, err := leasecontrol.NewStoreBudgetSource(sessions, leasecontrol.NewMemoryDenialStore(nil), leasecontrol.ExtensionConfig{
			DeploymentBudget:    1_000_000_000,
			DeploymentMaxBudget: 2_000_000_000,
			ApprovalMode:        leasecontrol.ApprovalModeElicitation,
			SuccessCoolOff:      time.Duration(1+rng.Intn(3)) * time.Millisecond,
		})
		if err != nil {
			t.Fatalf("NewStoreBudgetSource: %v", err)
		}
		el := &driverEndElicitor{sessions: sessions, endProb: 0.5, rng: rand.New(rand.NewSource(rng.Int63()))}
		svc, err := leasecontrol.NewService(leasecontrol.Options{Budgets: src, Elicitor: el})
		if err != nil {
			t.Fatalf("NewService: %v", err)
		}

		type outcome struct {
			id      string
			granted int64
			err     error
		}
		results := make(chan outcome, len(ids)*2)
		var wg sync.WaitGroup
		for _, id := range ids {
			for k := 0; k < 2; k++ {
				wg.Add(1)
				jitter := time.Duration(rng.Intn(4000)) * time.Microsecond
				go func(id string) {
					defer wg.Done()
					time.Sleep(jitter)
					resp, err := svc.ExtendLease(context.Background(), leasecontrol.ExtendRequest{
						TenantID: "acme", SessionID: id, Requested: leasecontrol.Dimensions{Tokens: 1_000},
					})
					results <- outcome{id: id, granted: resp.Granted.Tokens, err: err}
				}(id)
			}
		}
		wg.Wait()
		close(results)

		for r := range results {
			if !sessions.ended(r.id) && (r.err != nil || r.granted <= 0) {
				t.Fatalf("round %d: live session %s got granted=%d err=%v, want a grant", round, r.id, r.granted, r.err)
			}
		}
		el.mu.Lock()
		maxOpen := el.maxOpen
		el.mu.Unlock()
		if maxOpen > 1 {
			t.Fatalf("round %d: %d elicitations open at once for one tree, want at most 1", round, maxOpen)
		}
	}
}
