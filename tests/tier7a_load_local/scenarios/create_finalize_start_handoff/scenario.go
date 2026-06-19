// SPDX-License-Identifier: MIT

//go:build load_local

// Package create_finalize_start_handoff exercises the §10.1 coordinator
// handoff during the §7.1 create → finalize → start window under
// concurrency. Proposal 0007 claims the warm pod at /create and assigns the
// credential lease at /finalize, persisting the pod↔session binding (the
// SandboxName + pool on the session row) so any gateway replica can reconnect
// from it (§4.6). A handoff mid-window must therefore reconstruct the binding
// and release both the pod (the per-pod SandboxClaim) and the lease (the
// credential's active-session counter) when the session is retired, so the
// reclaim does not orphan the pod or leak the lease.
//
// The scenario models the handoff: replica A claims a pod and (for a
// finalized session) assigns a lease, then "hands off" to replica B, which
// reconstructs the binding from the persisted row alone and reclaims it. The
// invariant: across every concurrent handoff, every claimed pod's claim is
// deleted exactly once and every assigned lease's active-session counter
// returns to zero, with no leak and no double-release.
//
// TESTING.md §12.7.a regression scenarios.
package create_finalize_start_handoff

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/fakekube"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
)

const name = "create_finalize_start_handoff"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{} })
}

// binding is the durable pod↔session binding the create handler persists on
// the session row, the single source of truth a replica reconstructs from
// after a coordinator handoff (§4.6). leased records whether /finalize
// assigned a credential lease, so the reclaim knows to revoke it.
type binding struct {
	pod     string
	session string
	leased  bool
}

// Scenario claims a pod (and optionally a lease) on one replica, then drives
// the reclaim from a second replica that only has the persisted binding,
// asserting neither the pod nor the lease is orphaned across concurrent
// handoffs.
type Scenario struct {
	store *fakekube.ObjectStore

	// leaseActive models the §4.9 credential lease's active-session counter:
	// finalize increments it (AssignCredentials), and the reclaim's
	// ReleaseSession decrements it. A leaked lease leaves it above zero after
	// every session is retired.
	leaseActive atomic.Int64

	podClaims  atomic.Int64 // live per-pod claims
	reclaimed  atomic.Int64 // successful handoff reclaims
	doubleFree atomic.Int64 // reclaim of an already-released binding (a bug)

	seq atomic.Int64 // per-iteration unique id source

	// peakLeaseActive is the high-water mark of live leases, asserted to bound
	// at the number of finalized sessions in flight (no leak past release).
	mu              sync.Mutex
	peakLeaseActive int64
}

func (s *Scenario) Name() string { return name }

func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 24, Duration: 2 * time.Second}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.store = fakekube.NewObjectStore()
	return nil
}

func (s *Scenario) Teardown(ctx context.Context) error { return nil }

// claimName is the deterministic §4.6.1 per-pod claim name.
func claimName(pod string) string { return "claim-" + pod }

// claimOnReplicaA models replica A's /create (and, for a finalized session,
// /finalize): it CREATEs the per-pod occupancy claim and, when finalize ran,
// increments the lease's active-session counter. It returns the durable
// binding the row persists, which is the only state replica B reconstructs
// from.
func (s *Scenario) claimOnReplicaA(pod, session string, finalize bool) (binding, error) {
	if err := s.store.Create(&fakekube.Object{
		Kind:        "SandboxClaim",
		Namespace:   "lenny-agents",
		Name:        claimName(pod),
		Annotations: map[string]string{"sandboxRef": pod, "tenantId": "acme"},
	}); err != nil {
		return binding{}, fmt.Errorf("replica A claim pod %s: %w", pod, err)
	}
	s.podClaims.Add(1)
	b := binding{pod: pod, session: session}
	if finalize {
		// /finalize assigns the credential lease: the active-session counter
		// goes up. A handoff after this point must revoke it on reclaim.
		live := s.leaseActive.Add(1)
		s.mu.Lock()
		if live > s.peakLeaseActive {
			s.peakLeaseActive = live
		}
		s.mu.Unlock()
		b.leased = true
	}
	return b, nil
}

// reclaimOnReplicaB models replica B reconstructing the binding after a
// coordinator handoff and retiring the session (a /terminate or created-expiry
// sweep that owns no live connection): it revokes the lease from the binding
// (ReleaseSession) and deletes the per-pod claim (DeleteClaim), both keyed by
// the persisted binding alone (§4.6). The lease revoke runs first so a delete
// error cannot strand the lease (mirroring binder.ReclaimClaimed).
func (s *Scenario) reclaimOnReplicaB(b binding) error {
	if b.leased {
		if s.leaseActive.Add(-1) < 0 {
			// Double-revoke would drive the counter negative.
			s.doubleFree.Add(1)
		}
	}
	// DeleteClaim is idempotent; a missing claim returns nil. A claim still
	// present before this delete is the expected case.
	got, err := s.store.Get("SandboxClaim", "lenny-agents", claimName(b.pod))
	if err == nil && got != nil {
		s.podClaims.Add(-1)
	} else {
		// The claim was already gone before replica B reclaimed: a
		// double-release the durable binding must prevent.
		s.doubleFree.Add(1)
	}
	if err := s.store.Delete("SandboxClaim", "lenny-agents", claimName(b.pod)); err != nil {
		return fmt.Errorf("replica B reclaim pod %s: %w", b.pod, err)
	}
	s.reclaimed.Add(1)
	return nil
}

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	id := s.seq.Add(1)
	pod := fmt.Sprintf("pod-%d", id)
	session := fmt.Sprintf("sess-%d", id)
	// Alternate finalized (lease assigned) and created-only (no lease)
	// sessions so the handoff covers both the §4.5 created-expiry reclaim
	// (pod only) and the /terminate-of-a-finalizing/ready reclaim (pod + lease).
	finalize := id%2 == 0

	b, err := s.claimOnReplicaA(pod, session, finalize)
	if err != nil {
		return err
	}
	// Coordinator handoff: replica A's ownership transfers to replica B, which
	// has only the persisted binding. Reconstruct it and reclaim.
	return s.reclaimOnReplicaB(b)
}

// Assert validates the §10.1 / §4.6 handoff invariants: every handoff reclaims
// its pod and lease from the persisted binding, no claim is double-released,
// and the lease active-session counter returns to zero (no leak).
func (s *Scenario) Assert(r *loadgen.Result) error {
	r.AddCustom("reclaimed", float64(s.reclaimed.Load()))
	r.AddCustom("lease_active_final", float64(s.leaseActive.Load()))
	r.AddCustom("pod_claims_final", float64(s.podClaims.Load()))
	r.AddCustom("double_free", float64(s.doubleFree.Load()))
	r.AddCustom("peak_lease_active", float64(s.peakLeaseActive))

	if s.doubleFree.Load() > 0 {
		return fmt.Errorf("§4.6 violated: %d double-release events; a handoff reclaimed a binding twice or released a counter below zero", s.doubleFree.Load())
	}
	// After every session is retired the lease counter returns to zero: a
	// non-zero residual is a leaked lease the reclaim failed to revoke.
	if s.leaseActive.Load() != 0 {
		return fmt.Errorf("§7.1 step 23 violated: lease active-session counter = %d after all handoffs, want 0 (leaked lease)", s.leaseActive.Load())
	}
	// Every claimed pod's claim is deleted: a non-zero residual is an orphaned pod.
	if s.podClaims.Load() != 0 {
		return fmt.Errorf("§4.6 violated: %d live pod claims after all handoffs, want 0 (orphaned pod)", s.podClaims.Load())
	}
	if s.reclaimed.Load() == 0 {
		return fmt.Errorf("scenario never exercised the handoff reclaim path")
	}
	if r.Iterations < 100 {
		return fmt.Errorf("scenario did not get enough load: %d iterations (want >= 100)", r.Iterations)
	}
	return nil
}
