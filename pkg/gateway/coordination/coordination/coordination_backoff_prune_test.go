// SPDX-License-Identifier: MIT

package coordination

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"
)

// adoptionBackoffLen reports how many sessions hold an adoption backoff entry.
func (s *Sweeper) adoptionBackoffLen() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.backoffUntil)
}

// hasAdoptionBackoff reports whether the session holds an adoption backoff
// entry, expired or not.
func (s *Sweeper) hasAdoptionBackoff(sessionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.backoffUntil[sessionID]
	return ok
}

// mutableTenants is a TenantLister whose tenant set a test changes between
// sweeps, so a tenant can vanish from the listing.
type mutableTenants struct {
	mu  sync.Mutex
	ids []string
}

func (m *mutableTenants) ListTenants(context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.ids...), nil
}

func (m *mutableTenants) set(ids ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ids = ids
}

// listFailingStore fails List for one tenant, modeling a sweep whose listing
// ends part-way through.
type listFailingStore struct {
	sessionstore.Store
	mu         sync.Mutex
	failTenant string
}

var errListFailed = errors.New("list failed")

func (s *listFailingStore) List(ctx context.Context, tenantID string, f sessionstore.ListFilter) ([]sessionstore.Session, error) {
	s.mu.Lock()
	fail := tenantID == s.failTenant
	s.mu.Unlock()
	if fail {
		return nil, errListFailed
	}
	return s.Store.List(ctx, tenantID, f)
}

func (s *listFailingStore) failListing(tenantID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failTenant = tenantID
}

// relinquishOrphan creates a running orphan with a pod and sweeps once with a
// readopter whose fence relinquishes, so the Sweeper records an adoption
// backoff for it.
func relinquishOrphan(t *testing.T, sw *Sweeper, sessions sessionstore.Store, tenantID, id string) {
	t.Helper()
	mustCreate(t, sessions, sessionstore.Session{ID: id, TenantID: tenantID, State: session.StateRunning, PodAssignment: "pod-" + id})
	if _, err := sw.Sweep(context.Background()); err != nil {
		t.Fatalf("seeding Sweep: %v", err)
	}
	if !sw.hasAdoptionBackoff(id) {
		t.Fatalf("session %s holds no adoption backoff after a relinquished takeover", id)
	}
}

// TestSweepDropsAdoptionBackoffOfTerminalAndAbsentRows_spec_10_1_1 pins that
// the Sweeper's adoption-backoff entry for a relinquished takeover does not
// outlive the session. It sweeps the row inside the backoff window after the
// row moved to every terminal state, after a hard delete, and after its
// tenant left the listing: each drops the entry. A row that is still live
// (running, or moved to the adopted resume_pending state) keeps the entry so
// its window still holds, and a sibling session's entry is never touched.
//
// Before the fix every terminal and absent case kept its entry for the
// lifetime of the process, because the lazy expiry in inAdoptionBackoff runs
// only for a row the sweep still considers adopting.
//
// spec: 10.1.1 (Stateless Replicas and Per-Session Coordination), 10.1.2
// (Coordinator Handoff Protocol)
func TestSweepDropsAdoptionBackoffOfTerminalAndAbsentRows_spec_10_1_1(t *testing.T) {
	type mutation func(t *testing.T, sessions sessionstore.Store, tenants *mutableTenants)
	toState := func(st session.State) mutation {
		return func(t *testing.T, sessions sessionstore.Store, _ *mutableTenants) {
			updateRow(t, sessions, "orphan", func(r *sessionstore.Session) { r.State = st })
		}
	}
	cases := []struct {
		name      string
		mutate    mutation
		wantEntry bool
	}{
		{"hard_deleted", func(t *testing.T, sessions sessionstore.Store, _ *mutableTenants) {
			if err := sessions.Delete(context.Background(), "acme", "orphan"); err != nil {
				t.Fatalf("delete: %v", err)
			}
		}, false},
		{"tenant_gone", func(_ *testing.T, _ sessionstore.Store, tenants *mutableTenants) {
			tenants.set("globex")
		}, false},
		{"still_running", func(*testing.T, sessionstore.Store, *mutableTenants) {}, true},
		{"resume_pending", toState(session.StateResumePending), true},
	}
	for _, st := range session.TerminalStates() {
		cases = append(cases, struct {
			name      string
			mutate    mutation
			wantEntry bool
		}{string(st), toState(st), false})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sessions := memstore.New()
			leases := newFakeLeases()
			tenants := &mutableTenants{ids: []string{"acme", "globex"}}
			readopter := &fakeReadopter{
				fail:   map[string]bool{"orphan": true, "sibling": true},
				leases: leases, tenantID: "acme", replicaID: "rep-1",
			}
			now := time.Unix(1000, 0).UTC()
			sw := NewSweeper(tenants, sessions, leases, Options{
				ReplicaID: "rep-1", Readopter: readopter,
				AdoptionBackoff: time.Hour, Clock: func() time.Time { return now },
			})
			relinquishOrphan(t, sw, sessions, "globex", "sibling")
			relinquishOrphan(t, sw, sessions, "acme", "orphan")

			tc.mutate(t, sessions, tenants)
			now = now.Add(time.Minute) // still inside the backoff window
			if _, err := sw.Sweep(context.Background()); err != nil {
				t.Fatalf("Sweep: %v", err)
			}
			if got := sw.hasAdoptionBackoff("orphan"); got != tc.wantEntry {
				t.Errorf("orphan backoff entry present = %v, want %v", got, tc.wantEntry)
			}
			if !sw.hasAdoptionBackoff("sibling") {
				t.Errorf("sibling backoff entry dropped; only the orphan's row changed")
			}
			if tc.wantEntry && len(readopter.calls) != 2 {
				t.Errorf("ReadoptAndFence calls = %d, want 2 (no re-adopt inside the window)", len(readopter.calls))
			}
		})
	}
}

// TestSweepKeepsAdoptionBackoffOnPartialListing_spec_10_1_1 pins that the
// absent-row prune runs only after a complete listing. A sweep whose listing
// fails part-way returns the error and keeps every entry, including one whose
// row the failed listing did not reach, so a transient store error never
// reopens a live session's backoff window.
//
// spec: 10.1.1 (Stateless Replicas and Per-Session Coordination), 10.1.2
// (Coordinator Handoff Protocol)
func TestSweepKeepsAdoptionBackoffOnPartialListing_spec_10_1_1(t *testing.T) {
	store := &listFailingStore{Store: memstore.New()}
	leases := newFakeLeases()
	tenants := &mutableTenants{ids: []string{"acme", "globex"}}
	readopter := &fakeReadopter{
		fail:   map[string]bool{"a1": true, "g1": true},
		leases: leases, replicaID: "rep-1",
	}
	now := time.Unix(1000, 0).UTC()
	sw := NewSweeper(tenants, store, leases, Options{
		ReplicaID: "rep-1", Readopter: readopter, AdoptionBackoff: time.Hour,
		Clock: func() time.Time { return now },
	})
	relinquishOrphan(t, sw, store, "acme", "a1")
	relinquishOrphan(t, sw, store, "globex", "g1")

	store.failListing("globex")
	if _, err := sw.Sweep(context.Background()); !errors.Is(err, errListFailed) {
		t.Fatalf("Sweep error = %v, want the listing failure", err)
	}
	if got := sw.adoptionBackoffLen(); got != 2 {
		t.Errorf("backoff entries after a partial listing = %d, want 2 (no prune)", got)
	}

	// Once the listing recovers, a complete pass with g1 hard-deleted prunes
	// only g1.
	store.failListing("")
	if err := store.Delete(context.Background(), "globex", "g1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := sw.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if sw.hasAdoptionBackoff("g1") || !sw.hasAdoptionBackoff("a1") {
		t.Errorf("after a complete pass: g1 entry %v, a1 entry %v; want g1 dropped and a1 kept",
			sw.hasAdoptionBackoff("g1"), sw.hasAdoptionBackoff("a1"))
	}
}

// syncLeases is a mutex-guarded fakeLeases for the concurrent sweep test.
type syncLeases struct {
	mu sync.Mutex
	f  *fakeLeases
}

func (s *syncLeases) Acquire(ctx context.Context, tenantID, sessionID, holder string, ttl time.Duration) (leasestore.Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.f.Acquire(ctx, tenantID, sessionID, holder, ttl)
}

func (s *syncLeases) Renew(ctx context.Context, tenantID, sessionID, holder string, ttl time.Duration) (leasestore.Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.f.Renew(ctx, tenantID, sessionID, holder, ttl)
}

func (s *syncLeases) Release(ctx context.Context, tenantID, sessionID, holder string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.f.Release(ctx, tenantID, sessionID, holder)
}

func (s *syncLeases) Get(ctx context.Context, tenantID, sessionID string) (leasestore.Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.f.Get(ctx, tenantID, sessionID)
}

func (s *syncLeases) DeleteByUser(context.Context, string, string) (int, error) { return 0, nil }
func (s *syncLeases) DeleteByTenant(context.Context, string) (int, error)       { return 0, nil }

// relinquishingReadopter always fails the fence after releasing the lease, as
// the production Fencer does on a terminal fence failure. It is safe for
// concurrent use.
type relinquishingReadopter struct {
	leases    leasestore.LeaseStore
	replicaID string
}

func (r relinquishingReadopter) ReadoptAndFence(ctx context.Context, tenantID, sessionID string, _ int64) (func(), error) {
	_ = r.leases.Release(ctx, tenantID, sessionID, r.replicaID)
	return nil, errFenceRelinquished
}

// TestSweepBackoffPruneUnderConcurrentSweepsAndTerminalWrites_spec_10_1_1
// runs concurrent sweeps while other goroutines end, hard-delete, and leave
// running a set of relinquished orphans in a randomized order, under -race.
// After the writers finish, one settling sweep must leave exactly the entries
// of the sessions still running: the backoff map never retains an entry for
// a terminal or absent session, whatever interleaving produced it.
//
// spec: 10.1.1 (Stateless Replicas and Per-Session Coordination), 10.1.2
// (Coordinator Handoff Protocol)
func TestSweepBackoffPruneUnderConcurrentSweepsAndTerminalWrites_spec_10_1_1(t *testing.T) {
	const n = 60
	seed := time.Now().UnixNano()
	rng := rand.New(rand.NewSource(seed))
	t.Logf("seed %d", seed)

	sessions := memstore.New()
	leases := &syncLeases{f: newFakeLeases()}
	sw := NewSweeper(fakeTenants{ids: []string{"acme"}}, sessions, leases, Options{
		ReplicaID: "rep-1", Readopter: relinquishingReadopter{leases: leases, replicaID: "rep-1"},
		AdoptionBackoff: time.Hour,
	})
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("s%03d", i)
		mustCreate(t, sessions, sessionstore.Session{ID: ids[i], TenantID: "acme", State: session.StateRunning, PodAssignment: "pod-" + ids[i]})
	}
	if _, err := sw.Sweep(context.Background()); err != nil {
		t.Fatalf("seeding Sweep: %v", err)
	}
	if got := sw.adoptionBackoffLen(); got != n {
		t.Fatalf("backoff entries after seeding = %d, want %d", got, n)
	}

	// Each session gets a fate: 0 stays running, 1 deleted, 2..5 a terminal state.
	terminal := session.TerminalStates()
	fate := make([]int, n)
	for i := range fate {
		fate[i] = rng.Intn(2 + len(terminal))
	}
	order := rng.Perm(n)

	ctx, cancel := context.WithCancel(context.Background())
	var sweepers sync.WaitGroup
	for range 4 {
		sweepers.Add(1)
		go func() {
			defer sweepers.Done()
			for ctx.Err() == nil {
				if _, err := sw.Sweep(context.Background()); err != nil {
					t.Errorf("concurrent Sweep: %v", err)
					return
				}
			}
		}()
	}
	var writers sync.WaitGroup
	for w := range 3 {
		writers.Add(1)
		go func(w int) {
			defer writers.Done()
			for k := w; k < n; k += 3 {
				i := order[k]
				switch f := fate[i]; f {
				case 0:
				case 1:
					if err := sessions.Delete(context.Background(), "acme", ids[i]); err != nil {
						t.Errorf("delete %s: %v", ids[i], err)
					}
				default:
					st := terminal[f-2]
					if _, err := sessions.Update(context.Background(), "acme", ids[i], func(r *sessionstore.Session) error {
						r.State = st
						return nil
					}); err != nil {
						t.Errorf("update %s: %v", ids[i], err)
					}
				}
			}
		}(w)
	}
	writers.Wait()
	cancel()
	sweepers.Wait()

	if _, err := sw.Sweep(context.Background()); err != nil {
		t.Fatalf("settling Sweep: %v", err)
	}
	wantLive := 0
	for i, id := range ids {
		live := fate[i] == 0
		if live {
			wantLive++
		}
		if got := sw.hasAdoptionBackoff(id); got != live {
			t.Errorf("session %s (fate %d) backoff entry present = %v, want %v", id, fate[i], got, live)
		}
	}
	if got := sw.adoptionBackoffLen(); got != wantLive {
		t.Errorf("backoff entries = %d, want %d (only the running sessions)", got, wantLive)
	}
}
