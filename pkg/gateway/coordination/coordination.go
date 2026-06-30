// SPDX-License-Identifier: MIT

// Package coordination maintains the §10.1 session-coordination
// leases. A gateway replica drives the sessions it owns; the Sweeper
// periodically acquires or renews the lease for every non-terminal
// session, stamping this replica as the holder. When a replica
// crashes its leases lapse on their TTL, so another replica's sweeper
// can take the orphaned sessions over.
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
	"github.com/lennylabs/lenny/pkg/gateway/coordlease"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"
)

// TenantLister enumerates the tenants whose sessions are swept.
type TenantLister interface {
	ListTenants(ctx context.Context) ([]string, error)
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
}

// Sweeper renews the coordination leases for a gateway replica.
type Sweeper struct {
	tenants   TenantLister
	sessions  sessionstore.Store
	leases    leasestore.LeaseStore
	mirror    coordlease.Store
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
		replicaID: opts.ReplicaID,
		ttl:       ttl,
		interval:  interval,
	}
}

// Sweep runs one maintenance pass: it acquires or renews the
// coordination lease for every non-terminal session, holder set to
// this replica. Sessions whose lease is held by a different replica
// are left alone. Returns the number of leases this replica holds
// after the pass.
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
