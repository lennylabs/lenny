// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// cascadeListingStore records the filters of every List call and runs
// afterFirstPrimary once, after the first listing from the primary returns.
type cascadeListingStore struct {
	*memstore.Store
	mu                sync.Mutex
	primaryListings   int
	replicaListings   int
	afterFirstPrimary func()
}

func (s *cascadeListingStore) List(ctx context.Context, tenantID string, f sessionstore.ListFilter) ([]sessionstore.Session, error) {
	rows, err := s.Store.List(ctx, tenantID, f)
	s.mu.Lock()
	var hook func()
	if f.Primary {
		s.primaryListings++
		if s.primaryListings == 1 {
			hook = s.afterFirstPrimary
		}
	} else {
		s.replicaListings++
	}
	s.mu.Unlock()
	if hook != nil {
		hook()
	}
	return rows, err
}

func (s *cascadeListingStore) counts() (primary, replica int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.primaryListings, s.replicaListings
}

func seedCascadeRow(t *testing.T, s sessionstore.Store, id, parent string, depth uint32, st session.State) sessionstore.Session {
	t.Helper()
	row := sessionstore.Session{
		UserID: testOwner,
		ID:     id, TenantID: "acme", RuntimeRef: "claude-code", State: st,
		ParentSessionID: parent, DelegationDepth: depth,
	}
	if err := s.Create(context.Background(), row); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
	got, err := s.Get(context.Background(), "acme", id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	return got
}

// TestCascadeToChildrenListsPrimaryPerLevel_spec_12_8 asserts that the
// cancel_all cascade lists the tenant from the primary once per tree level,
// and that a grandchild inserted under a running child after the first
// listing returned, as a child the creation fence admitted before its
// parent's terminal write is, is reached by the listing taken after that
// child's terminal write and ends cancelled. A walk that lists once before
// its first write leaves the grandchild running.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 8.10 (Delegation Tree Recovery)
func TestCascadeToChildrenListsPrimaryPerLevel_spec_12_8(t *testing.T) {
	store := &cascadeListingStore{Store: memstore.New()}
	srv := New(store, Options{DevTenantHeader: true})
	parent := seedCascadeRow(t, store, "p_end", "", 0, session.StateRunning)
	seedCascadeRow(t, store, "c_run", "p_end", 1, session.StateRunning)
	store.afterFirstPrimary = func() {
		seedCascadeRow(t, store, "g_late", "c_run", 2, session.StateRunning)
	}
	parent, err := store.Update(context.Background(), "acme", parent.ID, func(r *sessionstore.Session) error {
		r.State = session.StateFailed
		return nil
	})
	if err != nil {
		t.Fatalf("parent terminal write: %v", err)
	}

	srv.cascadeToChildren(context.Background(), parent)

	for _, id := range []string{"c_run", "g_late"} {
		row, err := store.Get(context.Background(), "acme", id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		if row.State != session.StateCancelled {
			t.Errorf("%s state = %q after the cascade, want cancelled", id, row.State)
		}
	}
	// Levels: the parent's children, the child's children, and the
	// grandchild's (empty) children.
	if primary, _ := store.counts(); primary != 3 {
		t.Errorf("primary listings = %d, want 3 (one per tree level)", primary)
	}
}

// TestCascadeToChildrenListsNoReplica_spec_12_8 asserts that a cancel_all
// cascade over a one-level tree reads no listing from the read pool.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 12.3 (Read replicas)
func TestCascadeToChildrenListsNoReplica_spec_12_8(t *testing.T) {
	store := &cascadeListingStore{Store: memstore.New()}
	srv := New(store, Options{DevTenantHeader: true})
	seedCascadeRow(t, store, "p_one", "", 0, session.StateRunning)
	seedCascadeRow(t, store, "c_one", "p_one", 1, session.StateRunning)
	parent, err := store.Update(context.Background(), "acme", "p_one", func(r *sessionstore.Session) error {
		r.State = session.StateCancelled
		return nil
	})
	if err != nil {
		t.Fatalf("parent terminal write: %v", err)
	}
	before, replicaBefore := store.counts()
	srv.cascadeToChildren(context.Background(), parent)
	after, replicaAfter := store.counts()
	if after-before < 1 {
		t.Fatalf("cascade made no primary listing")
	}
	if replicaAfter != replicaBefore {
		t.Fatalf("cascade made %d read-pool listings, want none", replicaAfter-replicaBefore)
	}
}
