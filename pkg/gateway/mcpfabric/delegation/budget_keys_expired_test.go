// SPDX-License-Identifier: MIT

package delegation_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/delegation/lease"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treebudget"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// recordingKeysExpired counts BUDGET_KEYS_EXPIRED cleanup calls by root.
type recordingKeysExpired struct {
	mu    sync.Mutex
	roots []string
}

func (h *recordingKeysExpired) HandleBudgetKeysExpired(_ context.Context, tenantID, rootSessionID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.roots = append(h.roots, tenantID+"/"+rootSessionID)
}

func (h *recordingKeysExpired) calls() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.roots...)
}

// fakeGrants returns a fixed token grant or error and counts reads.
type fakeGrants struct {
	tokens int64
	err    error
	reads  int
}

func (g *fakeGrants) TreeTokenGrant(context.Context, string, string) (int64, error) {
	g.reads++
	return g.tokens, g.err
}

// listFailStore fails every ListByRoot read.
type listFailStore struct{ sessionstore.Store }

func (listFailStore) ListByRoot(context.Context, string, string) ([]sessionstore.Session, error) {
	return nil, errors.New("postgres down")
}

// capturingReserver records each reservation it forwards and runs onExpired
// once, after the first attempt that meets ErrBudgetKeysExpired returns from
// Redis and before the caller sees it.
type capturingReserver struct {
	inner     delegation.TreeBudgetReserver
	seen      []treebudget.Reservation
	onExpired func()
}

func (c *capturingReserver) Reserve(ctx context.Context, r treebudget.Reservation) (treebudget.Totals, error) {
	c.seen = append(c.seen, r)
	tot, err := c.inner.Reserve(ctx, r)
	if errors.Is(err, treebudget.ErrBudgetKeysExpired) && c.onExpired != nil {
		hook := c.onExpired
		c.onExpired = nil
		hook()
	}
	return tot, err
}

func (c *capturingReserver) Return(ctx context.Context, r treebudget.Reservation) error {
	return c.inner.Return(ctx, r)
}

// expiryFixture is a delegation Service over a memstore and a miniredis
// reserver, with a recording cleanup handler and an optional grant reader.
type expiryFixture struct {
	store    sessionstore.Store
	mr       *miniredis.Miniredis
	reserver *capturingReserver
	handler  *recordingKeysExpired
	svc      *delegation.Service
}

func newExpiryFixture(t *testing.T, store sessionstore.Store, grants delegation.TreeGrantReader, ids ...string) *expiryFixture {
	t.Helper()
	mr := miniredis.RunT(t)
	cl := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = cl.Close() })
	f := &expiryFixture{
		store: store, mr: mr,
		reserver: &capturingReserver{inner: treebudget.New(cl, 0)},
		handler:  &recordingKeysExpired{},
	}
	n := 0
	f.svc = delegation.NewService(store, delegation.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc: func() string {
			id := ids[n%len(ids)]
			n++
			return id
		},
		TreeBudgetReserver: f.reserver,
		TreeGrants:         grants,
		BudgetKeysExpired:  f.handler,
	})
	return f
}

// seedDelegatedChild writes a delegated child row (DelegationDepth 1) under
// the root sess_parent.
func seedDelegatedChild(t *testing.T, store sessionstore.Store, id string) {
	t.Helper()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: "acme", State: session.StateRunning, UserID: "user_alice",
		RuntimeRef: "gemini", PoolRef: "pool-b", IsolationProfile: isolation.ProfileSandboxed,
		ParentSessionID: "sess_parent", RootSessionID: "sess_parent", DelegationDepth: 1,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed delegated child: %v", err)
	}
}

// spec: §8.3 (BUDGET_KEYS_EXPIRED detection and tree cleanup)
// A tree's first delegation meets an absent sentinel, reads no delegated row,
// and is retried marked as the first reservation, which creates the
// tree-wide keys. No cleanup runs.
func TestFirstDelegationCreatesTheTreeKeys_spec_8_3(t *testing.T) {
	store := memstore.New()
	seedParentWithLease(t, store, "sess_parent", &sessionstore.DelegationLease{MaxTreeSize: 10})
	f := newExpiryFixture(t, store, nil, "child_a")
	if _, err := delegateOnce(f.svc); err != nil {
		t.Fatalf("first delegation: %v", err)
	}
	if len(f.reserver.seen) != 2 || f.reserver.seen[0].CreateTree || !f.reserver.seen[1].CreateTree {
		t.Fatalf("reservations = %+v, want an unmarked attempt then a marked one", f.reserver.seen)
	}
	if !f.mr.Exists("{sess_parent}:dlg:tokens") {
		t.Fatal("the first delegation did not create the sentinel")
	}
	if got := f.handler.calls(); len(got) != 0 {
		t.Fatalf("cleanup ran %v on a first delegation", got)
	}
}

// spec: §8.3 (BUDGET_KEYS_EXPIRED detection and tree cleanup)
// B's first reserve sees the sentinel absent; A then creates the sentinel and
// commits its child before B reads the root's sessions. B sees A's delegated
// child, retries unmarked once, finds the sentinel, and is admitted. The root
// does not fail.
func TestConcurrentFirstDelegationIsAdmittedNotDetected_spec_8_3(t *testing.T) {
	store := memstore.New()
	seedParentWithLease(t, store, "sess_parent", &sessionstore.DelegationLease{MaxTreeSize: 10})
	b := newExpiryFixture(t, store, nil, "child_b")
	// A shares the store and B's Redis through its own Service.
	cl := redis.NewClient(&redis.Options{Addr: b.mr.Addr()})
	t.Cleanup(func() { _ = cl.Close() })
	svcA := delegation.NewService(store, delegation.Options{
		IDFunc:             func() string { return "child_a" },
		TreeBudgetReserver: treebudget.New(cl, 0),
	})
	b.reserver.onExpired = func() {
		if _, err := delegateOnce(svcA); err != nil {
			t.Errorf("A's delegation: %v", err)
		}
	}
	if _, err := delegateOnce(b.svc); err != nil {
		t.Fatalf("B's delegation after A's first: %v", err)
	}
	if len(b.reserver.seen) != 2 || b.reserver.seen[1].CreateTree {
		t.Fatalf("B's reservations = %+v, want the retry unmarked", b.reserver.seen)
	}
	if got := b.handler.calls(); len(got) != 0 {
		t.Fatalf("cleanup ran %v for a concurrent first delegation", got)
	}
	tot, err := treebudget.New(cl, 0).Snapshot(context.Background(), "sess_parent")
	if err != nil || tot.TreeSize != 2 {
		t.Fatalf("tree_size = %d (%v), want both children counted", tot.TreeSize, err)
	}
}

// spec: §8.3 (BUDGET_KEYS_EXPIRED detection and tree cleanup)
// Two absent-sentinel results for a tree that holds a delegated row are a
// reservation detection: the cleanup runs once for the root, the delegation
// fails with ErrBudgetKeysExpired, no key is written, and no child row is
// created.
func TestTwoExpiredResultsWithADelegatedRowAreADetection_spec_8_3(t *testing.T) {
	store := memstore.New()
	seedParentWithLease(t, store, "sess_parent", &sessionstore.DelegationLease{MaxTreeSize: 10})
	seedDelegatedChild(t, store, "child_old")
	f := newExpiryFixture(t, store, nil, "child_new")
	_, err := delegateOnce(f.svc)
	if !errors.Is(err, delegation.ErrBudgetKeysExpired) {
		t.Fatalf("err = %v, want ErrBudgetKeysExpired", err)
	}
	if got := f.handler.calls(); len(got) != 1 || got[0] != "acme/sess_parent" {
		t.Fatalf("cleanup calls = %v, want one for acme/sess_parent", got)
	}
	if len(f.reserver.seen) != 2 || f.reserver.seen[0].CreateTree || f.reserver.seen[1].CreateTree {
		t.Fatalf("reservations = %+v, want two unmarked attempts", f.reserver.seen)
	}
	if keys := f.mr.Keys(); len(keys) != 0 {
		t.Fatalf("a detection wrote %v", keys)
	}
	if _, gerr := store.Get(context.Background(), "acme", "child_new"); gerr == nil {
		t.Fatal("a child row was created on a detection")
	}
}

// spec: §8.3 (BUDGET_KEYS_EXPIRED detection and tree cleanup)
// A root whose own row carries a ParentSessionID with DelegationDepth 0 (a
// derived session) and that has no descendant is at its first reservation;
// ParentSessionID does not classify a delegated child.
func TestDerivedRootWithNoDescendantIsAFirstReservation_spec_8_3(t *testing.T) {
	store := memstore.New()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_parent", TenantID: "acme", State: session.StateRunning, UserID: "user_alice",
		RuntimeRef: "claude", PoolRef: "pool-a", IsolationProfile: isolation.ProfileSandboxed,
		ParentSessionID: "sess_source", RootSessionID: "sess_parent",
		DelegationLease: &sessionstore.DelegationLease{MaxTreeSize: 10},
		CreatedAt:       now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed derived root: %v", err)
	}
	f := newExpiryFixture(t, store, nil, "child_a")
	if _, err := delegateOnce(f.svc); err != nil {
		t.Fatalf("delegation from a derived root: %v", err)
	}
	if got := f.handler.calls(); len(got) != 0 {
		t.Fatalf("a derived root's first delegation ran cleanup %v", got)
	}
}

// spec: §8.3 (BUDGET_KEYS_EXPIRED detection and tree cleanup), §8.6
// (Extension state)
// A grant read failure and a SessionStore read failure each reject with
// ErrBudgetUnavailable and leave every counter unchanged.
func TestStoreReadFailuresFailClosedWithNoCounterMutated_spec_8_3(t *testing.T) {
	t.Run("grants read", func(t *testing.T) {
		store := memstore.New()
		seedParentWithLease(t, store, "sess_parent", &sessionstore.DelegationLease{MaxTokenBudget: 1000})
		f := newExpiryFixture(t, store, &fakeGrants{err: errors.New("postgres down")}, "child_a")
		if err := f.mr.Set("{sess_parent}:dlg:tokens", "40"); err != nil {
			t.Fatal(err)
		}
		_, err := delegateWithSlice(f.svc, lease.LeaseSlice{MaxTokenBudget: 100})
		if !errors.Is(err, delegation.ErrBudgetUnavailable) {
			t.Fatalf("err = %v, want ErrBudgetUnavailable", err)
		}
		if len(f.reserver.seen) != 0 {
			t.Fatalf("Reserve ran %d times after a grant read failure", len(f.reserver.seen))
		}
		if v, _ := f.mr.Get("{sess_parent}:dlg:tokens"); v != "40" || len(f.mr.Keys()) != 1 {
			t.Fatalf("keys = %v (tokens %q), want only tokens=40", f.mr.Keys(), v)
		}
	})
	t.Run("session store read", func(t *testing.T) {
		mem := memstore.New()
		seedParentWithLease(t, mem, "sess_parent", &sessionstore.DelegationLease{MaxTreeSize: 10})
		f := newExpiryFixture(t, listFailStore{mem}, nil, "child_a")
		_, err := delegateOnce(f.svc)
		if !errors.Is(err, delegation.ErrBudgetUnavailable) {
			t.Fatalf("err = %v, want ErrBudgetUnavailable", err)
		}
		if keys := f.mr.Keys(); len(keys) != 0 {
			t.Fatalf("a ListByRoot failure left keys %v", keys)
		}
		if got := f.handler.calls(); len(got) != 0 {
			t.Fatalf("cleanup ran %v on a store read failure", got)
		}
	})
}

// spec: §8.6 (Extension state), §8.3
// A parent with no finite token cap makes no grant read, and its reservation
// carries no token cap.
func TestZeroBaseTokenCapReadsNoGrants_spec_8_6(t *testing.T) {
	store := memstore.New()
	seedParentWithLease(t, store, "sess_parent", &sessionstore.DelegationLease{MaxTreeSize: 10})
	grants := &fakeGrants{tokens: 5000}
	f := newExpiryFixture(t, store, grants, "child_a")
	if _, err := delegateOnce(f.svc); err != nil {
		t.Fatalf("delegation: %v", err)
	}
	if grants.reads != 0 {
		t.Fatalf("grant reads = %d, want 0 for a zero base cap", grants.reads)
	}
	if f.reserver.seen[0].TokenCap != 0 {
		t.Fatalf("TokenCap = %d, want 0", f.reserver.seen[0].TokenCap)
	}
}

// spec: §8.6 (Extension state), §8.3
// A granted extension raises the reservation's token cap by the tree's
// cumulative ext_tokens, so a delegation that the base cap rejects is admitted.
func TestGrantedExtensionRaisesTokenCap_spec_8_6(t *testing.T) {
	for _, tc := range []struct {
		name    string
		grant   int64
		wantCap int64
		admit   bool
	}{
		{name: "no grant", grant: 0, wantCap: 1000, admit: false},
		{name: "granted", grant: 500, wantCap: 1500, admit: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := memstore.New()
			seedParentWithLease(t, store, "sess_parent", &sessionstore.DelegationLease{MaxTokenBudget: 1000})
			f := newExpiryFixture(t, store, &fakeGrants{tokens: tc.grant}, "child_a", "child_b")
			if _, err := delegateWithSlice(f.svc, lease.LeaseSlice{MaxTokenBudget: 1000}); err != nil {
				t.Fatalf("first delegation: %v", err)
			}
			_, err := delegateWithSlice(f.svc, lease.LeaseSlice{MaxTokenBudget: 500})
			last := f.reserver.seen[len(f.reserver.seen)-1]
			if last.TokenCap != tc.wantCap {
				t.Fatalf("TokenCap = %d, want %d", last.TokenCap, tc.wantCap)
			}
			var bx *lease.BudgetExceededError
			if tc.admit && err != nil {
				t.Fatalf("delegation within the raised cap: %v", err)
			}
			if !tc.admit && !errors.As(err, &bx) {
				t.Fatalf("err = %v, want BUDGET_EXHAUSTED without a grant", err)
			}
		})
	}
}
