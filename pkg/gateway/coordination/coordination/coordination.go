// SPDX-License-Identifier: MIT

// Package coordination maintains the §10.1 session-coordination
// leases. A gateway replica drives the sessions it owns; the Sweeper
// periodically renews the lease for every session this replica binds or
// already holds the lease for, stamping this replica as the holder, and
// adopts a lapsed lease only for a still-running-pod session it holds no
// binding for. When a replica crashes its leases lapse on their TTL, so
// another replica's sweeper can take the orphaned sessions over. The
// lease is acquired at bind on the binding replica, so the sweep never
// lands the lease on a replica that holds no binding for the session.
//
// The leases are held in Redis via pkg/gateway/leasestore. leasestore
// Acquire is idempotent for the current holder — it refreshes the TTL
// — so one Acquire call per session per sweep both claims new
// sessions and renews held ones. The sweeper holds the leasestore.LeaseStore
// interface, so a Redis outage transparently routes through the §12.4
// Postgres advisory-lock fallback when a leasestore.Failover is wired.
package coordination

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/coordination/coordlease"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"
)

// TenantLister enumerates the tenants whose sessions are swept.
type TenantLister interface {
	ListTenants(ctx context.Context) ([]string, error)
}

// BindingRegistry is the consumer-side view of this replica's live pod
// bindings that the Sweeper needs to keep the coordination lease
// co-located with the pod binding. The production implementation is
// wired over the per-replica podsession registry and the pod executor
// in the gateway binary; the coordination package defines the interface
// here so it does not import podsession or the executor directly.
// spec: §4.6.1 (coordinating replica holds the lease), §10.1.
type BindingRegistry interface {
	// Bound reports whether this replica holds a live pod binding for the
	// session. It is sourced from the podsession registry's Get.
	Bound(sessionID string) bool
	// ConnAlive reports whether the bound session's gateway-to-pod gRPC
	// channel is still live. It is meaningful only when Bound is true; a
	// failed channel surfaces a dead binding that pins a lease the pod can
	// no longer be reached over. spec: §10.1 (hold state on connection loss).
	ConnAlive(sessionID string) bool
	// EvictBinding drops the session's binding from the podsession registry
	// and the executor's cached Attach stream in one call, so a
	// dead-connection eviction surfaces the lease for re-adoption without
	// leaving a stale cached stream that would shadow a same-replica
	// re-adopt. spec: §10.1, §4.7 (single content consumer per session).
	EvictBinding(sessionID string)
}

// Options configures a Sweeper.
type Options struct {
	// ReplicaID identifies this gateway replica; it becomes the lease
	// holder. Required.
	ReplicaID string
	// TTL is the session lease lifetime. It must exceed Interval by a
	// comfortable margin so a lease does not lapse between sweeps.
	TTL time.Duration
	// Interval is the sweep cadence.
	Interval time.Duration
	// Mirror is the §10.1 line 165 coordination_lease barrier-target
	// mirror. When set, the sweep upserts a mirror row for every lease
	// this replica holds (so a cross-replica handoff overwrites
	// coordinator_replica with the new holder) and marks a terminal
	// session's row released. Nil disables mirroring; the preStop barrier
	// then falls back entirely to the in-memory lease cache. spec: §10.1
	// line 165.
	Mirror coordlease.Store
	// Bindings is the consumer-side view of this replica's live pod
	// bindings. The Sweeper renews the lease for the sessions this replica
	// binds, and on a bound session whose held gateway-to-pod channel has
	// died it evicts the binding and releases the lease instead of renewing
	// it. Nil means this replica reports no local bindings, so the sweep
	// renews only leases it already holds and adopts still-running-pod
	// orphans. spec: §4.6.1 (coordinating replica holds the lease), §10.1.
	Bindings BindingRegistry
}

// Sweeper renews the coordination leases for a gateway replica.
type Sweeper struct {
	tenants   TenantLister
	sessions  sessionstore.Store
	leases    leasestore.LeaseStore
	mirror    coordlease.Store
	bindings  BindingRegistry
	replicaID string
	ttl       time.Duration
	interval  time.Duration
}

// NewSweeper returns a Sweeper. Interval defaults to 15s and TTL to
// four sweep intervals when not set.
func NewSweeper(tenants TenantLister, sessions sessionstore.Store, leases leasestore.LeaseStore, opts Options) *Sweeper {
	interval := opts.Interval
	if interval <= 0 {
		interval = 15 * time.Second
	}
	ttl := opts.TTL
	if ttl <= 0 {
		ttl = 4 * interval
	}
	return &Sweeper{
		tenants:   tenants,
		sessions:  sessions,
		leases:    leases,
		mirror:    opts.Mirror,
		bindings:  opts.Bindings,
		replicaID: opts.ReplicaID,
		ttl:       ttl,
		interval:  interval,
	}
}

// boundHere reports whether this replica holds a live pod binding for the
// session. A nil binding registry reports no local bindings.
func (s *Sweeper) boundHere(sessionID string) bool {
	return s.bindings != nil && s.bindings.Bound(sessionID)
}

// connAlive reports whether a bound session's held gateway-to-pod gRPC
// channel is still live. It is consulted only for a bound session. A nil
// binding registry cannot probe liveness, so it reports alive and a
// nil-wired sweeper never evicts a binding.
func (s *Sweeper) connAlive(sessionID string) bool {
	return s.bindings == nil || s.bindings.ConnAlive(sessionID)
}

// evictBinding drops the session's binding from the podsession registry
// and the executor's cached Attach stream. It is a no-op when no binding
// registry is wired.
func (s *Sweeper) evictBinding(sessionID string) {
	if s.bindings != nil {
		s.bindings.EvictBinding(sessionID)
	}
}

// isRunningPod reports whether a session row is a still-running-pod
// session eligible for crash-takeover adoption: it is running (or its
// input_required sub-state) and carries a persisted pod assignment. The
// Sweeper adopts a lapsed lease only for such a session, so a committed
// but never-bound row (created, finalizing, ready, or suspended) is never
// picked up by a peer sweep before its owning replica's at-bind Acquire
// runs. spec: §10.1 (coordinator handoff re-adopts the still-running pod).
func isRunningPod(row sessionstore.Session) bool {
	return (row.State == session.StateRunning || row.State == session.StateInputRequired) && row.PodAssignment != ""
}

// Sweep runs one maintenance pass. For every non-terminal session it
// renews the coordination lease when this replica binds the session or
// already holds the lease, and adopts a lapsed lease only for a
// still-running-pod session (running or input_required with a persisted
// pod assignment) it holds no binding for; every other session is left
// alone without an Acquire attempt, so the sweep never lands the lease on
// a replica that holds no binding. A bound session whose held
// gateway-to-pod channel has died is evicted and its lease released so a
// subsequent sweep can re-adopt it. Sessions whose lease a different
// replica holds are skipped on ErrHeld. Returns the number of leases this
// replica holds after the pass. spec: §4.6.1, §10.1.
//
// When this replica acquires a lease that was previously held by a
// different replica — the cross-replica coordinator-handoff case — the
// sweeper increments the session row's `coordination_generation`
// counter via the SessionStore Update path. The store enforces the §4.2
// monotonicity floor so a handoff under racing writers still advances
// the counter exactly once per observed handoff on this replica.
// spec: §4.2 line 156 — "incremented on coordinator handoff across
// gateway replicas".
func (s *Sweeper) Sweep(ctx context.Context) (int, error) {
	tenants, err := s.tenants.ListTenants(ctx)
	if err != nil {
		return 0, err
	}
	held := 0
	for _, tenantID := range tenants {
		rows, err := s.sessions.List(ctx, tenantID, sessionstore.ListFilter{})
		if err != nil {
			return held, err
		}
		for _, row := range rows {
			if session.IsTerminal(row.State) {
				// §10.1 line 165 — a terminal session is no longer
				// coordinated by anyone; mark its mirror row released so the
				// barrier-target query stops returning it. Best-effort.
				s.releaseMirror(ctx, tenantID, row.ID)
				continue
			}
			// Inspect the current holder before Acquire so a successful
			// Acquire that changed the holder can be detected as a
			// handoff. ErrNotFound means the lease is unheld (fresh
			// acquisition); any other error short-circuits the sweep
			// the same way the prior code did.
			var priorHolder string
			existing, getErr := s.leases.Get(ctx, tenantID, row.ID)
			if getErr == nil {
				priorHolder = existing.Holder
			} else if !errors.Is(getErr, leasestore.ErrNotFound) {
				return held, getErr
			}

			bound := s.boundHere(row.ID)
			leaseUnheld := errors.Is(getErr, leasestore.ErrNotFound)
			adoptable := leaseUnheld && isRunningPod(row)

			// A bound session whose held gateway-to-pod gRPC channel has
			// died is evicted and its lease released rather than renewed, so
			// the session reverts to a lapsed-lease still-running-pod orphan
			// that a subsequent sweep (a peer, or this replica) re-adopts and
			// re-fences before the pod's hold-state self-termination. Without
			// this a dead-connection binding keeps boundHere true and would
			// renew the lease forever, leaving no lapsed lease for any peer's
			// adoption predicate to fire on. The eviction drops the
			// executor's cached Attach stream too, so a same-replica re-adopt
			// is not shadowed by the stale dead stream.
			// spec: §10.1 (hold state on connection loss; TTL-lapse recovery).
			if bound && !s.connAlive(row.ID) {
				s.evictBinding(row.ID)
				if err := s.leases.Release(ctx, tenantID, row.ID, s.replicaID); err != nil {
					log.Printf("coordination: release dead-connection lease for session %s: %v", row.ID, err)
				}
				continue
			}

			// Attempt Acquire only for a session this replica binds (renew),
			// one it already holds the lease for after a takeover whose
			// binding it has not yet published (priorHolder == s.replicaID,
			// renew so the taken-over lease does not lapse on its TTL and
			// re-orphan the session), or an unheld/lapsed lease of an
			// adoptable still-running-pod session (crash-takeover adoption).
			// Every other non-terminal session — created, finalizing, ready,
			// suspended, or one whose lease a live foreign replica holds — is
			// skipped without an Acquire attempt, so a peer sweep never lands
			// the lease on a replica that holds no binding for the session.
			// spec: §4.6.1 (coordinating replica holds the lease),
			// §10.1 (per-session coordination lease).
			eligible := bound || priorHolder == s.replicaID || adoptable
			if !eligible {
				continue
			}

			if _, err := s.leases.Acquire(ctx, tenantID, row.ID, s.replicaID, s.ttl); err != nil {
				if errors.Is(err, leasestore.ErrHeld) {
					// Another replica owns this session; skip it.
					continue
				}
				return held, err
			}
			// A handoff occurred when the prior holder was a different
			// replica. A self-renew (priorHolder == s.replicaID) and a
			// fresh acquisition on an unheld lease (priorHolder == "")
			// are not handoffs.
			if priorHolder != "" && priorHolder != s.replicaID {
				s.RecordHandoff(ctx, tenantID, row.ID)
			}
			// §10.1 line 165 — mirror the held lease into Postgres so the
			// preStop barrier-target query observes it. A cross-replica
			// handoff overwrites coordinator_replica with this replica.
			s.upsertMirror(ctx, tenantID, row.ID, row.CoordinationGeneration)
			held++
		}
	}
	return held, nil
}

// RecordHandoff bumps the §4.2 coordination_generation counter on the
// named session row. The SessionStore Update path enforces the §4.2
// monotonicity floor; a transient store error is logged but does not
// fail the sweep, because the next sweep cycle will reattempt the bump
// from a fresh handoff observation.
//
// Exported so the sessionserver and component tests can verify the
// bump-once-per-handoff contract directly without racing the sweeper's
// internal Get→Acquire window. The sweeper invokes it internally on
// every observed cross-replica handoff.
// spec: §4.2 line 156.
func (s *Sweeper) RecordHandoff(ctx context.Context, tenantID, sessionID string) {
	_, err := s.sessions.Update(ctx, tenantID, sessionID, func(row *sessionstore.Session) error {
		row.CoordinationGeneration++
		return nil
	})
	if err != nil {
		log.Printf("coordination: bump coordination_generation for session %s: %v", sessionID, err)
	}
}

// upsertMirror records the §10.1 line 165 barrier-target row for a lease
// this replica holds. Best-effort: a transient mirror error is logged but
// does not fail the sweep — the next sweep cycle re-upserts, and the
// barrier coordinator falls back to the in-memory lease cache when the
// Postgres read fails anyway.
func (s *Sweeper) upsertMirror(ctx context.Context, tenantID, sessionID string, generation int64) {
	if s.mirror == nil {
		return
	}
	if err := s.mirror.Upsert(ctx, coordlease.Lease{
		TenantID:               tenantID,
		SessionID:              sessionID,
		CoordinatorReplica:     s.replicaID,
		CoordinationGeneration: generation,
	}); err != nil {
		log.Printf("coordination: mirror upsert for session %s: %v", sessionID, err)
	}
}

// releaseMirror marks a terminal session's §10.1 line 165 barrier-target
// row released. Best-effort.
func (s *Sweeper) releaseMirror(ctx context.Context, tenantID, sessionID string) {
	if s.mirror == nil {
		return
	}
	if err := s.mirror.Release(ctx, tenantID, sessionID); err != nil {
		log.Printf("coordination: mirror release for session %s: %v", sessionID, err)
	}
}

// Run sweeps on Interval until ctx is cancelled. Sweep failures are
// logged and the loop continues — a transient store error must not
// stop lease maintenance.
func (s *Sweeper) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := s.Sweep(ctx); err != nil && ctx.Err() == nil {
				log.Printf("coordination: lease sweep failed: %v", err)
			}
		}
	}
}
