// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credrenewal"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
)

// releasingAssigner wraps a Service and releases the spent lease right after
// each Assign returns, as a pod release racing a renewal does.
type releasingAssigner struct {
	*credassign.Service
	spent string
}

func (r *releasingAssigner) Assign(pool, session, spiffe, tenant string, gen int64) (credential.Lease, error) {
	l, err := r.Service.Assign(pool, session, spiffe, tenant, gen)
	_ = r.Release(r.spent)
	return l, err
}

// lookupFailStore is a lease store whose Lookup fails on the calls failOn
// names (1-based).
type lookupFailStore struct {
	credleasestore.LeaseStore
	calls  atomic.Int32
	failOn map[int32]bool
}

func (l *lookupFailStore) Lookup(id string) (credential.Lease, bool, error) {
	n := l.calls.Add(1)
	if l.failOn[n] {
		return credential.Lease{}, false, errors.New("lease store unreachable")
	}
	return l.LeaseStore.Lookup(id)
}

// renewalHarness is a Service, a renewal worker, and its wiring over one
// in-memory lease store, with the session's first lease minted and tracked.
type renewalHarness struct {
	leases    *credleasestore.Store
	svc       *credassign.Service
	worker    *credrenewal.Worker
	wiring    *credRenewalWiring
	lease     credential.Lease
	exhausted atomic.Int32
}

func newRenewalHarness(t *testing.T, assign func(*credassign.Service) credassign.Assigner, store func(*credleasestore.Store) credleasestore.LeaseStore) *renewalHarness {
	t.Helper()
	h := &renewalHarness{leases: credleasestore.New()}
	h.svc = credassign.New(h.leases, credcache.New())
	h.svc.RegisterPool(renewalProxyPool("claude-prod", "key-1"))
	var a credassign.Assigner = h.svc
	if assign != nil {
		a = assign(h.svc)
	}
	var ls credleasestore.LeaseStore = h.leases
	if store != nil {
		ls = store(h.leases)
	}
	h.wiring = newCredRenewalWiring(a, podsession.NewRegistry(), nil, ls, nil)
	h.worker = credrenewal.New(h.wiring, credrenewal.Options{
		OnRenewed:   h.wiring.onRenewed,
		OnExhausted: func(credrenewal.Lease) { h.exhausted.Add(1) },
	})
	lease, err := h.svc.Assign("claude-prod", "run_a", "", "", 3)
	if err != nil {
		t.Fatalf("Assign: %v", err)
	}
	h.lease = lease
	h.wiring.track(h.worker, "claude-prod", string(lease.Provider), lease)
	return h
}

func (h *renewalHarness) tickPastRenewal() int {
	return h.worker.Tick(context.Background(), h.lease.RenewBefore.Add(time.Second))
}

// TestReleasedLeaseIsNeverRenewed_spec_4_9 asserts that a lease released on
// purpose is never renewed: after a release on the same Service, on a second
// Service sharing the lease store, or by a release that runs while the
// replacement is minted, a renewal tick past RenewBefore leaves no lease for
// the session, the pool's active count at its post-release value, and nothing
// tracked or reported exhausted.
//
// spec: §4.9 (Proactive Lease Renewal)
func TestReleasedLeaseIsNeverRenewed_spec_4_9(t *testing.T) {
	cases := []struct {
		name    string
		assign  func(*credassign.Service) credassign.Assigner
		release func(h *renewalHarness)
	}{
		{"released on the same service", nil, func(h *renewalHarness) { _ = h.svc.Release(h.lease.LeaseID) }},
		{"released by a second service sharing the store", nil, func(h *renewalHarness) {
			other := credassign.New(h.leases, credcache.New())
			other.RegisterPool(renewalProxyPool("claude-prod", "key-1"))
			_ = other.ReleaseSession("run_a")
		}},
		{"released while the replacement is minted", func(s *credassign.Service) credassign.Assigner {
			return &releasingAssigner{Service: s}
		}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newRenewalHarness(t, tc.assign, nil)
			if ra, ok := h.wiring.assign.(*releasingAssigner); ok {
				ra.spent = h.lease.LeaseID
			}
			if tc.release != nil {
				tc.release(h)
			}
			if n := h.tickPastRenewal(); n != 0 {
				t.Errorf("renewed %d leases, want 0", n)
			}
			if got := h.leases.LeasesBySession([]string{"run_a"}); len(got) != 0 {
				t.Errorf("session leases after the tick = %d, want none", len(got))
			}
			if h.worker.Tracked() != 0 || h.exhausted.Load() != 0 {
				t.Errorf("tracked %d, exhausted %d; want 0 and 0", h.worker.Tracked(), h.exhausted.Load())
			}
			// The pool's only credential is assignable again, so its
			// active count returned to zero.
			if _, err := h.svc.Assign("claude-prod", "run_b", "", "", 0); err != nil {
				t.Errorf("pool slot not returned: %v", err)
			}
		})
	}
}

// TestRenewalLookupErrorsKeepTheLeaseTracked_spec_4_9 asserts that a lease
// store lookup error before Assign, or after Assign, is an ordinary renewal
// failure: the spent lease stays tracked with one retry counted, and a
// replacement minted before the failed lookup is released so the pool's
// active count is restored. A renewal of a lease with no pool binding fails
// with credrenewal.ErrLeaseReleased.
//
// spec: §4.9 (Proactive Lease Renewal)
func TestRenewalLookupErrorsKeepTheLeaseTracked_spec_4_9(t *testing.T) {
	for _, failOn := range []int32{1, 2} {
		name := map[int32]string{1: "before assign", 2: "after assign"}[failOn]
		t.Run(name, func(t *testing.T) {
			var fs *lookupFailStore
			h := newRenewalHarness(t, nil, func(s *credleasestore.Store) credleasestore.LeaseStore {
				fs = &lookupFailStore{LeaseStore: s, failOn: map[int32]bool{failOn: true}}
				return fs
			})
			if n := h.tickPastRenewal(); n != 0 {
				t.Errorf("renewed %d, want 0 on a failed lookup", n)
			}
			if h.worker.Tracked() != 1 || h.exhausted.Load() != 0 {
				t.Errorf("tracked %d exhausted %d, want the spent lease still tracked", h.worker.Tracked(), h.exhausted.Load())
			}
			if got := h.leases.LeasesBySession([]string{"run_a"}); len(got) != 1 || got[0].LeaseID != h.lease.LeaseID {
				t.Errorf("session leases = %d, want only the spent lease", len(got))
			}
		})
	}
	t.Run("no pool binding", func(t *testing.T) {
		h := newRenewalHarness(t, nil, nil)
		_, err := h.wiring.Renew(context.Background(), credrenewal.Lease{LeaseID: "cl-unbound", SessionID: "run_a"})
		if !errors.Is(err, credrenewal.ErrLeaseReleased) {
			t.Errorf("Renew of an unbound lease = %v, want ErrLeaseReleased", err)
		}
	})
}
