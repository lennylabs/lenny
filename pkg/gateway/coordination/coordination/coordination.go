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
	"math/rand"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/coordination/coordlease"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"
)

// errHandoffSessionTerminal signals that the session became terminal between
// the sweep's List snapshot and the atomic handoff bump, so the handoff is
// refused rather than resurrecting a session no longer coordinated by anyone.
// A terminal session is no longer coordinated by any replica, so its
// coordination_generation must not advance on a would-be takeover.
// spec: §10.1 (a terminal session is no longer coordinated by anyone).
var errHandoffSessionTerminal = errors.New("coordination: session went terminal before the handoff bump")

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

// Readopter re-establishes coordination of a still-running pod on the
// crash-takeover edge. The production implementation (wired in the gateway
// binary) dials the pod from its persisted SandboxName and sends
// CoordinatorFence as the first RPC over that connection, before any §15.5
// version handshake, because a crash-takeover pod is already in its §10.1
// hold state and rejects every inbound RPC except CoordinatorFence. It
// drives the fence through the reused coordfence retry/relinquish policy.
//
// On a successful fence acknowledgement it returns a publish callback and a
// nil error; the Sweeper invokes publish to place the re-established
// BindResult into the shared podRegistry and hold the connection open as
// the serving binding, so the pod stays continuously coordinated and does
// not re-enter hold state. The Sweeper calls publish only after the fence
// acknowledges, honoring the §10.1 precondition that no operational RPC
// reaches the pod until the fence acknowledges.
//
// On a terminal fence failure it publishes no binding, closes the
// connection, and relinquishes the lease (the coordfence driver releases
// it and backs off per §10.1 line 35), returning a non-nil error. The
// Sweeper then records a per-session adoption backoff so the fixed sweep
// interval does not re-adopt inside the spec's jittered backoff window.
//
// The coordination package defines the interface here so it imports
// neither podsession, adapterclient, nor coordfence directly. A nil
// Readopter disables the re-adopt (the generation bump and the lease
// acquire still stand); a deployment or test without the seam then relies
// on a peer replica that has it to re-establish the serving binding.
// spec: §10.1 (coordinator handoff re-adopts the still-running pod;
// CoordinatorFence precondition), §4.7 (Attach content stream stays lazy).
type Readopter interface {
	ReadoptAndFence(ctx context.Context, tenantID, sessionID string, generation int64) (publish func(), err error)
}

// minAdoptionBackoff and maxAdoptionBackoff bound the §10.1 line 35 jittered
// re-adoption delay a Sweeper waits after a relinquished crash-takeover
// before it re-adopts the same session. Jittering across the window keeps
// competing replicas from re-adopting a relinquished session in lockstep.
const (
	minAdoptionBackoff = 2 * time.Second
	maxAdoptionBackoff = 16 * time.Second
)

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
	// Readopter re-adopts and fences a still-running pod on the
	// crash-takeover edge, publishing the re-established serving binding
	// only after the fence acknowledges. Nil disables the re-adopt; the
	// generation bump and the lease acquire still stand, but this replica
	// does not re-establish the serving binding. spec: §10.1 (coordinator
	// handoff re-adopts the still-running pod).
	Readopter Readopter
	// AdoptionBackoff overrides the §10.1 line 35 re-adoption delay applied
	// after a relinquished crash-takeover. A value <= 0 selects a jittered
	// delay across the 2s-to-16s window; a positive value is used verbatim,
	// so an operator (or a test) can pin the delay. spec: §10.1 line 35
	// (relinquish-and-backoff).
	AdoptionBackoff time.Duration
	// Clock supplies the current time for the adoption-backoff window. Nil
	// selects time.Now; a test injects a controllable clock to exercise the
	// backoff deterministically.
	Clock func() time.Time
}

// Sweeper renews the coordination leases for a gateway replica.
type Sweeper struct {
	tenants         TenantLister
	sessions        sessionstore.Store
	leases          leasestore.LeaseStore
	mirror          coordlease.Store
	bindings        BindingRegistry
	readopter       Readopter
	replicaID       string
	ttl             time.Duration
	interval        time.Duration
	adoptionBackoff time.Duration
	now             func() time.Time

	// mu guards backoffUntil, the per-session adoption-backoff window a
	// relinquished crash-takeover records so the fixed sweep interval does
	// not re-adopt inside the §10.1 jittered backoff window.
	mu           sync.Mutex
	backoffUntil map[string]time.Time
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
	now := opts.Clock
	if now == nil {
		now = time.Now
	}
	return &Sweeper{
		tenants:         tenants,
		sessions:        sessions,
		leases:          leases,
		mirror:          opts.Mirror,
		bindings:        opts.Bindings,
		readopter:       opts.Readopter,
		replicaID:       opts.ReplicaID,
		ttl:             ttl,
		interval:        interval,
		adoptionBackoff: opts.AdoptionBackoff,
		now:             now,
		backoffUntil:    map[string]time.Time{},
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
			// A session in its post-relinquish adoption backoff is not
			// re-adopted until the §10.1 line 35 jittered window elapses, so
			// the fixed sweep does not re-drive RecordHandoff and the fence on
			// every sweep after a terminal fence failure released the lease.
			adoptable := leaseUnheld && isRunningPod(row) && !s.inAdoptionBackoff(row.ID)

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
			// The crash-takeover edge is a successful Acquire that changed the
			// holder to this replica for an adoptable still-running-pod session
			// this replica does not bind: the prior coordinator crashed, its
			// Redis lease lapsed (priorHolder == "") or was observed departing
			// in the narrow Get-then-Acquire race (priorHolder is a different
			// replica), and this replica adopts the orphan. A self-renew
			// (priorHolder == s.replicaID) and a renew of a session this replica
			// binds are not takeovers. The edge does not depend on the pre-Acquire
			// Get observing the departed holder, because a lapsed Redis lease
			// leaves priorHolder empty and the adoption of an adoptable session
			// is itself the handoff signal.
			//
			// On that edge the new coordinator bumps coordination_generation and
			// re-adopts the still-running pod through the injected seam, which
			// fences the pod as its first RPC and returns a publish callback the
			// Sweeper invokes only after the fence acknowledges. Because the
			// published binding (or, without a re-adopt seam, the self-held
			// lease) makes the next sweep a renew (priorHolder == s.replicaID),
			// the bump and the fence fire exactly once per handoff.
			// spec: §10.1 (coordinator handoff re-adopts the still-running pod;
			// CoordinatorFence precondition; no operational RPC before the fence
			// acknowledges), §4.2 line 156 (generation bump on handoff), §4.7
			// (Attach content stream stays lazy).
			if !bound && priorHolder != s.replicaID {
				generation := s.RecordHandoff(ctx, tenantID, row.ID)
				if generation == 0 {
					// The generation bump did not land: either the store write
					// errored transiently, or the session raced to terminal
					// between the List snapshot and the atomic bump so the
					// handoff was refused (RecordHandoff). §10.1 requires the new
					// coordinator to fence the pod to the post-handoff
					// generation, so a failed bump must restart the handoff from
					// lease acquisition rather than drive CoordinatorFence at the
					// baseline generation 0, which would record a generation
					// below the pod's current fenced value and undermine the
					// monotonic-generation split-brain guard. Release the
					// just-acquired lease and skip the re-adopt so a terminal
					// session is never resurrected, and a transient failure
					// re-observes the unheld lapsed lease on the next sweep and
					// re-runs the full bump-then-fence takeover once the store
					// recovers. Not self-holding the lease across sweeps is what
					// keeps the takeover predicate (!bound && priorHolder !=
					// s.replicaID) able to fire again.
					// spec: §10.1 (CoordinatorFence to the post-handoff
					// generation; a failed or refused generation bump restarts
					// the handoff from lease acquisition; a session that goes
					// terminal during takeover is not resurrected).
					if err := s.leases.Release(ctx, tenantID, row.ID, s.replicaID); err != nil {
						log.Printf("coordination: release lease after failed generation bump for session %s: %v", row.ID, err)
					}
					continue
				}
				publish, ferr := s.readoptAndFence(ctx, tenantID, row.ID, generation)
				if ferr != nil {
					// Terminal fence failure. The coordfence driver already
					// relinquished the lease (released it and backed off per
					// §10.1 line 35) and published no binding. Record a
					// per-session adoption backoff so the fixed sweep does not
					// re-adopt inside the spec's jittered window and re-drive
					// RecordHandoff and the fence every sweep. The generation
					// increment stays in Postgres; the next coordinator to
					// acquire the lease increments it again.
					// spec: §10.1 line 35 (relinquish-and-backoff).
					log.Printf("coordination: re-adopt fence for session %s relinquished: %v", row.ID, ferr)
					s.recordAdoptionBackoff(row.ID)
					continue
				}
				// The fence acknowledged: publish the re-established BindResult
				// to the shared podRegistry and hold the connection open as the
				// serving binding. Publishing only after the acknowledgement
				// honors the §10.1 precondition that no operational RPC reaches
				// the pod until the fence acknowledges.
				// spec: §10.1 (no operational RPC before the fence acknowledges).
				publish()
				s.clearAdoptionBackoff(row.ID)
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
// every observed cross-replica handoff and passes the new generation to
// the re-adopt fence so the pod is fenced to the post-handoff generation.
// A transient store error returns 0, which signals a failed bump: the
// crash-takeover caller then releases the just-acquired lease and skips the
// re-adopt, so the next sweep re-observes the unheld lease and re-runs the
// bump-then-fence takeover from a fresh handoff observation rather than
// fencing the pod at the baseline generation 0.
//
// The bump is refused under the same atomic read when the session has become
// terminal since the sweep's List snapshot observed it running. A terminal
// session is no longer coordinated by any replica, so a takeover that raced a
// concurrent terminal transition must not bump the generation or re-adopt the
// pod; the refusal returns 0 so the crash-takeover caller releases the
// just-acquired lease and skips the re-adopt exactly as it does for a transient
// error, and the next sweep short-circuits the now-terminal row.
// spec: §4.2 line 156, §10.1 (a terminal session is no longer coordinated by
// anyone; a session that goes terminal during takeover is not resurrected).
func (s *Sweeper) RecordHandoff(ctx context.Context, tenantID, sessionID string) int64 {
	updated, err := s.sessions.Update(ctx, tenantID, sessionID, func(row *sessionstore.Session) error {
		if session.IsTerminal(row.State) {
			return errHandoffSessionTerminal
		}
		row.CoordinationGeneration++
		return nil
	})
	if err != nil {
		if errors.Is(err, errHandoffSessionTerminal) {
			// Not an error condition: the session raced to terminal, so the
			// handoff is correctly abandoned without resurrecting it.
			return 0
		}
		log.Printf("coordination: bump coordination_generation for session %s: %v", sessionID, err)
		return 0
	}
	return updated.CoordinationGeneration
}

// readoptAndFence drives the injected re-adopt seam on the crash-takeover
// edge. A nil seam disables the re-adopt: the generation bump and the lease
// acquire stand, but this replica does not re-establish the serving
// binding, so the publish is a no-op and a peer replica that has the seam
// re-adopts the pod.
func (s *Sweeper) readoptAndFence(ctx context.Context, tenantID, sessionID string, generation int64) (func(), error) {
	if s.readopter == nil {
		return func() {}, nil
	}
	return s.readopter.ReadoptAndFence(ctx, tenantID, sessionID, generation)
}

// inAdoptionBackoff reports whether the session is inside its post-relinquish
// re-adoption backoff window. An elapsed window is cleared lazily so the map
// does not accumulate stale entries. spec: §10.1 line 35.
func (s *Sweeper) inAdoptionBackoff(sessionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	until, ok := s.backoffUntil[sessionID]
	if !ok {
		return false
	}
	if !s.now().Before(until) {
		delete(s.backoffUntil, sessionID)
		return false
	}
	return true
}

// recordAdoptionBackoff opens the §10.1 line 35 jittered re-adoption backoff
// window for a session whose crash-takeover fence relinquished the lease.
func (s *Sweeper) recordAdoptionBackoff(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.backoffUntil[sessionID] = s.now().Add(s.nextBackoff())
}

// clearAdoptionBackoff drops any adoption backoff for a session whose
// takeover fence acknowledged, so a later dead-connection re-orphan is
// re-adopted without waiting on a stale window.
func (s *Sweeper) clearAdoptionBackoff(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.backoffUntil, sessionID)
}

// nextBackoff returns the §10.1 line 35 re-adoption delay. A positive
// operator override (Options.AdoptionBackoff) is used verbatim; otherwise
// the delay is jittered uniformly across the 2s-to-16s window.
func (s *Sweeper) nextBackoff() time.Duration {
	if s.adoptionBackoff > 0 {
		return s.adoptionBackoff
	}
	return minAdoptionBackoff + time.Duration(rand.Int63n(int64(maxAdoptionBackoff-minAdoptionBackoff)))
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
