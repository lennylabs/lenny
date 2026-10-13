// SPDX-License-Identifier: MIT

// Package coordination maintains the §10.1 session-coordination
// leases. A gateway replica drives the sessions it owns; the Sweeper
// periodically renews the lease for every session this replica binds or
// already holds the lease for, stamping this replica as the holder, and
// adopts a lapsed lease only for a session in an adopted state
// (resume_pending, awaiting_client_action, or running, input_required, or
// suspended with a pod) it holds no binding for. A podless suspended session
// holds no lease. When a replica crashes its leases lapse on their TTL, so
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

// errHandoffRefused signals that the locked session row no longer matches the
// row the sweep listed: its coordination_generation moved past the listed
// value, or its state is no longer one a lapsed lease is adopted in (a
// terminal state, resuming, or a podless suspended row). The handoff is the
// §10.1.2 step 1 compare-and-swap, so a miss discards the lease claim rather
// than bumping a generation another writer already owns.
// spec: §10.1.2 (Coordinator Handoff Protocol).
var errHandoffRefused = errors.New("coordination: session row changed before the handoff compare-and-swap")

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
	// and the executor's cached Attach stream in one call. The Sweeper calls
	// it on two triggers. A bound session whose held gateway-to-pod channel
	// has died is evicted and its lease released, so the lease surfaces for
	// re-adoption without a stale cached stream that would shadow a
	// same-replica re-adopt. A bound session that a peer replica has taken
	// over (the lease is held elsewhere and the row's coordination_generation
	// has advanced past the generation stamped on this replica's binding) is
	// evicted without any lease write, because this replica no longer
	// coordinates it. spec: §10.1, §10.1.1, §10.1.5, §4.7 (single content
	// consumer per session).
	EvictBinding(sessionID string)
	// BindingGeneration returns the coordination_generation stamped on the
	// session's current binding: the generation at which this replica
	// published it (a start, a resume commit, or a takeover readopt). It
	// returns 0 when the session is unbound. The peer-takeover check compares
	// the row's generation with this stamp, so a session released and rebound
	// on this replica is compared against its own new binding rather than an
	// earlier one. spec: §10.1.1 (Stateless Replicas and Per-Session
	// Coordination), §10.1.5 (Stale Replica Behavior).
	BindingGeneration(sessionID string) int64
}

// HeldDeliverer delivers the messages a session held while it had no
// coordinating replica, once a takeover has re-adopted the session's pod. The
// session server implements it; the coordination package declares it here so
// it does not import the session server. The Sweeper calls it once per
// takeover that published a binding, after the publish, with the generation
// the takeover's compare-and-swap wrote. Implementations return at once and do
// the delivery asynchronously. spec: §10.1.2 (Coordinator Handoff Protocol),
// §7.2 (Message delivery routing — seven paths, path 6).
type HeldDeliverer interface {
	DeliverHeldAfterTakeover(tenantID, sessionID string, generation int64)
}

// TerminalReleaser releases or evicts this replica's binding for a session
// whose row is terminal. The terminal write can land on any replica, and a
// binding can be published after it, so the post-terminal hook on the
// writer's replica does not reach every binding. The Sweeper hands each
// terminal row this replica binds to the releaser on every listing; the
// releaser owns the generation check and the release, and defers while a
// terminal writer on this replica is still running its post-terminal hook.
// Implementations return at once. spec: §10.1.1 (Stateless Replicas and
// Per-Session Coordination), §7.2 (Interactive Session Model).
type TerminalReleaser interface {
	ReleaseTerminalBinding(row sessionstore.Session)
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
// it and backs off per §10.1.2), returning a non-nil error. The
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

// minAdoptionBackoff and maxAdoptionBackoff bound the §10.1.2 jittered
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
	// Mirror is the §10.1.8 coordination_lease barrier-target
	// mirror. When set, the sweep upserts a mirror row for every lease
	// this replica holds (so a cross-replica handoff overwrites
	// coordinator_replica with the new holder) and marks a terminal
	// session's row released. Nil disables mirroring; the preStop barrier
	// then falls back entirely to the in-memory lease cache. spec: §10.1.
	Mirror coordlease.Store
	// Bindings is the consumer-side view of this replica's live pod
	// bindings. The Sweeper renews the lease for the sessions this replica
	// binds. It evicts a bound session's binding on two triggers: when the
	// held gateway-to-pod channel has died, it evicts the binding and
	// releases the lease instead of renewing it; when a peer replica holds
	// the lease and the row's coordination_generation has advanced past the
	// generation stamped on the binding, it evicts the binding and
	// writes no lease, mirror, or session row. Nil means this replica
	// reports no local bindings, so the sweep renews only leases it already
	// holds and adopts still-running-pod orphans. spec: §4.6.1 (coordinating
	// replica holds the lease), §10.1, §10.1.5 (a replica that is no longer
	// the coordinator discards its cached streams).
	Bindings BindingRegistry
	// Readopter re-adopts and fences a still-running pod on the
	// crash-takeover edge, publishing the re-established serving binding
	// only after the fence acknowledges. Nil disables the re-adopt; the
	// generation bump and the lease acquire still stand, but this replica
	// does not re-establish the serving binding. spec: §10.1 (coordinator
	// handoff re-adopts the still-running pod).
	Readopter Readopter
	// AdoptionBackoff overrides the §10.1.2 re-adoption delay applied
	// after a relinquished crash-takeover. A value <= 0 selects a jittered
	// delay across the 2s-to-16s window; a positive value is used verbatim,
	// so an operator (or a test) can pin the delay. spec: §10.1.2
	// (relinquish-and-backoff).
	AdoptionBackoff time.Duration
	// Clock supplies the current time for the adoption-backoff window. Nil
	// selects time.Now; a test injects a controllable clock to exercise the
	// backoff deterministically.
	Clock func() time.Time
	// HeldDeliverer is called once after each takeover that published a
	// binding, so the messages held while the session had no coordinator
	// reach the re-adopted pod. Nil skips the call. spec: §10.1.2
	// (Coordinator Handoff Protocol), §7.2 (Message delivery routing — seven
	// paths, path 6).
	HeldDeliverer HeldDeliverer
	// TerminalReleaser is handed each terminal row this replica binds, so a
	// binding whose session another replica (or a write after the publish)
	// ended is released or evicted. Nil skips the call. spec: §10.1.1
	// (Stateless Replicas and Per-Session Coordination), §7.2 (Interactive
	// Session Model).
	TerminalReleaser TerminalReleaser
}

// Sweeper renews the coordination leases for a gateway replica.
type Sweeper struct {
	tenants          TenantLister
	sessions         sessionstore.Store
	leases           leasestore.LeaseStore
	mirror           coordlease.Store
	bindings         BindingRegistry
	readopter        Readopter
	heldDeliverer    HeldDeliverer
	terminalReleaser TerminalReleaser
	replicaID        string
	ttl              time.Duration
	interval         time.Duration
	adoptionBackoff  time.Duration
	now              func() time.Time

	// mu guards backoffUntil, the per-session adoption-backoff window a
	// relinquished crash-takeover records so the fixed sweep interval does
	// not re-adopt inside the §10.1.2 jittered backoff window. The
	// peer-takeover check keeps no per-replica record: it reads the
	// generation stamped on the binding (BindingRegistry.BindingGeneration).
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
		tenants:          tenants,
		sessions:         sessions,
		leases:           leases,
		mirror:           opts.Mirror,
		bindings:         opts.Bindings,
		readopter:        opts.Readopter,
		heldDeliverer:    opts.HeldDeliverer,
		terminalReleaser: opts.TerminalReleaser,
		replicaID:        opts.ReplicaID,
		ttl:              ttl,
		interval:         interval,
		adoptionBackoff:  opts.AdoptionBackoff,
		now:              now,
		backoffUntil:     map[string]time.Time{},
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
// and the executor's cached Attach stream. The registry call is a no-op when
// no binding registry is wired.
func (s *Sweeper) evictBinding(sessionID string) {
	if s.bindings != nil {
		s.bindings.EvictBinding(sessionID)
	}
}

// bindingGeneration returns the coordination_generation stamped on this
// replica's binding for the session, or 0 when the session is unbound or no
// binding registry is wired.
func (s *Sweeper) bindingGeneration(sessionID string) int64 {
	if s.bindings == nil {
		return 0
	}
	return s.bindings.BindingGeneration(sessionID)
}

// podHeld reports whether the row's state keeps a pod bound to the session
// and the row names that pod: running, input_required, or suspended with a
// non-empty PodAssignment. A takeover of such a row re-adopts and re-fences
// the pod. spec: §10.1.1 (Stateless Replicas and Per-Session Coordination),
// §10.1.2 (Coordinator Handoff Protocol).
func podHeld(row sessionstore.Session) bool {
	switch row.State {
	case session.StateRunning, session.StateInputRequired, session.StateSuspended:
		return row.PodAssignment != ""
	default:
		return false
	}
}

// adoptedState reports whether a lapsed coordination lease on the row is
// adopted by a peer replica: any resume_pending or awaiting_client_action row,
// and a running, input_required, or suspended row whose PodAssignment names a
// pod. A running, input_required, or suspended row with no pod is a session
// that was never bound (or a suspended session whose pod was released), so no
// peer lands the lease on a replica that holds no binding for it. A
// resume_pending or awaiting_client_action row is adopted without its pod,
// because that pod failed; the adopter's resume driver restores the session.
// resuming is excluded because every replica's watchdog moves it on, and
// created, finalizing, ready, and starting are excluded because the spec
// states no adoption for them. spec: §10.1.1 (Stateless Replicas and
// Per-Session Coordination).
func adoptedState(row sessionstore.Session) bool {
	switch row.State {
	case session.StateResumePending, session.StateAwaitingClientAction:
		return true
	default:
		return podHeld(row)
	}
}

// podlessSuspended reports whether the row is a suspended session whose pod
// was released. Such a session holds no coordination lease and has no
// coordinating replica until a write moves it to resume_pending.
// spec: §10.1.1 (Stateless Replicas and Per-Session Coordination).
func podlessSuspended(row sessionstore.Session) bool {
	return row.State == session.StateSuspended && row.PodAssignment == ""
}

// peerTookOver reports whether the row's coordination_generation has advanced
// past the generation stamped on this replica's binding. An unstamped binding
// (bindGen 0) reports false, so ErrHeld alone never evicts it.
// spec: §10.1.1 (Stateless Replicas and Per-Session Coordination), §10.1.5
// (Stale Replica Behavior).
func peerTookOver(row sessionstore.Session, bindGen int64) bool {
	return bindGen > 0 && row.CoordinationGeneration > bindGen
}

// Sweep runs one maintenance pass over every tenant's sessions and returns the
// number of leases this replica holds after the pass.
//
// For each non-terminal session it renews the coordination lease when this
// replica binds the session or already holds the lease, and adopts a lapsed
// lease for a session in an adopted state (adoptedState): resume_pending,
// awaiting_client_action, and running, input_required, or suspended with a
// pod. A takeover of a row that still holds a pod re-adopts and re-fences that
// pod; a takeover of a recovering row bumps the generation and re-adopts
// nothing. Every other session is left alone without an Acquire attempt, so
// the sweep never lands the lease on a replica that holds no binding.
//
// A podless suspended session is neither renewed nor adopted, so its lease
// lapses on its TTL; while this replica is its prior holder the sweep releases
// its §10.1.8 mirror row.
//
// A terminal session's mirror row is released, and when this replica binds
// the session the row is handed to the TerminalReleaser, which releases or
// evicts the binding. The terminal branch makes no lease write and no
// EvictBinding call.
//
// A bound session is evicted on two triggers. A dead held channel evicts the
// binding and releases the lease. A peer takeover evicts the binding with no
// lease, mirror, or session-row write: Acquire returns ErrHeld and the row's
// coordination_generation exceeds the generation stamped on the binding.
// ErrHeld alone does not evict, because leasestore.Failover can report a stale
// Redis holder after a Redis outage while the generation is unchanged.
//
// spec: §4.6.1, §10.1.1 (Stateless Replicas and Per-Session Coordination),
// §10.1.2 (Coordinator Handoff Protocol), §10.1.5 (Stale Replica Behavior),
// §10.1.8 (Rolling Updates and the CheckpointBarrier Protocol), §7.2
// (Interactive Session Model).
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
			holds, err := s.sweepRow(ctx, tenantID, row)
			if err != nil {
				return held, err
			}
			if holds {
				held++
			}
		}
	}
	return held, nil
}

// sweepRow runs one row of a sweep and reports whether this replica holds the
// row's lease afterwards. A returned error ends the sweep.
func (s *Sweeper) sweepRow(ctx context.Context, tenantID string, row sessionstore.Session) (bool, error) {
	if session.IsTerminal(row.State) {
		s.sweepTerminal(ctx, tenantID, row)
		return false, nil
	}
	// Inspect the current holder before Acquire so a successful Acquire that
	// changed the holder can be detected as a handoff. ErrNotFound means the
	// lease is unheld; any other error ends the sweep.
	var priorHolder string
	existing, getErr := s.leases.Get(ctx, tenantID, row.ID)
	if getErr == nil {
		priorHolder = existing.Holder
	} else if !errors.Is(getErr, leasestore.ErrNotFound) {
		return false, getErr
	}

	// A podless suspended session holds no lease: no Acquire and no Release,
	// whatever bound and priorHolder say, so the lease lapses on its TTL. While
	// this replica is the prior holder, release the mirror row so the §10.1.8
	// barrier-target set stops naming a session with no pod; repeating it each
	// sweep until the lapse also undoes an upsert by a sweep that listed the
	// row before the suspension release cleared its pod. A binding still
	// present is the suspension release's to remove.
	// spec: §10.1.1 (Stateless Replicas and Per-Session Coordination), §10.1.8
	// (Rolling Updates and the CheckpointBarrier Protocol).
	if podlessSuspended(row) {
		if priorHolder == s.replicaID {
			s.releaseMirror(ctx, tenantID, row.ID)
		}
		return false, nil
	}

	bound := s.boundHere(row.ID)
	// A bound session whose held gateway-to-pod channel has died is evicted
	// and its lease released rather than renewed, so the session reverts to a
	// lapsed lease that a subsequent sweep (a peer, or this replica) adopts and
	// re-fences before the pod's hold-state self-termination. The eviction
	// drops the executor's cached Attach stream too, so a same-replica
	// re-adopt is not shadowed by the stale dead stream.
	// spec: §10.1 (hold state on connection loss; TTL-lapse recovery).
	if bound && !s.connAlive(row.ID) {
		s.evictBinding(row.ID)
		if err := s.leases.Release(ctx, tenantID, row.ID, s.replicaID); err != nil {
			log.Printf("coordination: release dead-connection lease for session %s: %v", row.ID, err)
		}
		return false, nil
	}

	// Attempt Acquire only for a session this replica binds (renew), one it
	// already holds the lease for after a takeover whose binding it has not
	// published (renew, so the taken-over lease does not lapse), or an unheld
	// lease of a session in an adopted state that is outside its
	// post-relinquish adoption backoff (takeover). Every other non-terminal
	// session is skipped without an Acquire.
	// spec: §4.6.1 (coordinating replica holds the lease), §10.1.1
	// (Stateless Replicas and Per-Session Coordination), §10.1.2 (Coordinator
	// Handoff Protocol).
	leaseUnheld := errors.Is(getErr, leasestore.ErrNotFound)
	adoptable := leaseUnheld && adoptedState(row) && !s.inAdoptionBackoff(row.ID)
	if !bound && priorHolder != s.replicaID && !adoptable {
		return false, nil
	}

	if _, err := s.leases.Acquire(ctx, tenantID, row.ID, s.replicaID, s.ttl); err != nil {
		if !errors.Is(err, leasestore.ErrHeld) {
			return false, err
		}
		if bound && peerTookOver(row, s.bindingGeneration(row.ID)) {
			// Another replica holds the lease and has advanced the generation
			// past this replica's binding stamp, so it has taken the session
			// over. Drop the binding and the cached Attach stream. No lease,
			// mirror, or session-row write follows.
			// spec: §10.1.1, §10.1.5 (a replica that is no longer the
			// coordinator discards its cached streams).
			s.evictBinding(row.ID)
			log.Printf("coordination: evict binding for session %s: generation advanced to %d by another replica", row.ID, row.CoordinationGeneration)
		}
		return false, nil
	}

	// A successful Acquire by a replica that neither binds the session nor
	// held its lease is the crash-takeover edge: the prior coordinator's lease
	// lapsed (priorHolder empty) or was observed departing in the
	// Get-then-Acquire race. A renew of a bound or self-held session is not a
	// takeover.
	if !bound && priorHolder != s.replicaID {
		return s.takeover(ctx, tenantID, row), nil
	}
	s.upsertMirror(ctx, tenantID, row.ID, row.CoordinationGeneration)
	return true, nil
}

// sweepTerminal handles a terminal row: it releases the row's §10.1.8 mirror
// row, then hands the row to the TerminalReleaser when this replica binds the
// session. It writes no lease and evicts nothing itself; the releaser owns
// the generation check and the release.
// spec: §10.1.1 (Stateless Replicas and Per-Session Coordination), §7.2
// (Interactive Session Model), §10.1.8 (Rolling Updates and the
// CheckpointBarrier Protocol).
func (s *Sweeper) sweepTerminal(ctx context.Context, tenantID string, row sessionstore.Session) {
	s.releaseMirror(ctx, tenantID, row.ID)
	if s.terminalReleaser != nil && s.boundHere(row.ID) {
		s.terminalReleaser.ReleaseTerminalBinding(row)
	}
}

// takeover runs the crash-takeover edge after a successful Acquire and reports
// whether this replica holds the lease afterwards. It bumps the generation
// through the §10.1.2 step 1 compare-and-swap at the listed generation. A miss
// discards the lease claim. When the locked row still holds a pod, it
// re-adopts and fences that pod at the new generation, publishes the binding
// only after the fence acknowledges, and then calls the HeldDeliverer. A
// recovering row (resume_pending or awaiting_client_action) is adopted with no
// readopt, because its pod failed. Both branches clear the adoption backoff
// and mirror the lease at the new generation.
// spec: §10.1.2 (Coordinator Handoff Protocol), §10.1.1 (Stateless Replicas
// and Per-Session Coordination), §4.2, §4.7 (Attach content stream stays
// lazy), §7.2 (Message delivery routing — seven paths, path 6).
func (s *Sweeper) takeover(ctx context.Context, tenantID string, row sessionstore.Session) bool {
	generation, holdsPod := s.RecordHandoff(ctx, tenantID, row.ID, row.CoordinationGeneration)
	if generation == 0 {
		// The compare-and-swap missed or the store write failed. The new
		// coordinator must fence to the post-handoff generation, so it never
		// fences at the listed one: release the just-acquired lease so the
		// next sweep re-observes the row and restarts the handoff, and a
		// row that left the adopted states is never resurrected.
		// spec: §10.1.2 (Coordinator Handoff Protocol).
		if err := s.leases.Release(ctx, tenantID, row.ID, s.replicaID); err != nil {
			log.Printf("coordination: release lease after refused handoff for session %s: %v", row.ID, err)
		}
		return false
	}
	if holdsPod {
		publish, err := s.readoptAndFence(ctx, tenantID, row.ID, generation)
		if err != nil {
			// The re-adopt relinquished the lease it could not turn into a
			// serving binding. Record a per-session adoption backoff so the
			// fixed sweep does not re-adopt inside the §10.1.2 jittered
			// window. The generation increment stays in the store.
			log.Printf("coordination: re-adopt fence for session %s relinquished: %v", row.ID, err)
			s.recordAdoptionBackoff(row.ID)
			return false
		}
		// Publish only after the fence acknowledged, so no operational RPC
		// reaches the pod first. spec: §10.1.2.
		publish()
		s.deliverHeld(tenantID, row.ID, generation)
	}
	s.clearAdoptionBackoff(row.ID)
	// Mirror the post-handoff generation, never the listed one.
	s.upsertMirror(ctx, tenantID, row.ID, generation)
	return true
}

// deliverHeld calls the HeldDeliverer once for a takeover that published a
// binding. A nil hook is skipped.
func (s *Sweeper) deliverHeld(tenantID, sessionID string, generation int64) {
	if s.heldDeliverer != nil {
		s.heldDeliverer.DeliverHeldAfterTakeover(tenantID, sessionID, generation)
	}
}

// RecordHandoff is the §10.1.2 step 1 compare-and-swap of a coordinator
// handoff. Under one store Update it bumps the session's
// coordination_generation by one when the locked row's generation equals
// expected (the generation the caller's listing read) and the locked row is
// in an adopted state. It returns the new generation and whether the locked
// row holds a pod (podHeld), which decides whether the takeover re-adopts and
// fences a pod.
//
// It returns 0 and false when the compare-and-swap misses: the generation
// moved (another writer, such as the failure funnel's move to resume_pending
// or a peer's takeover, owns the change), or the row left the adopted states
// (a terminal state, resuming, or a suspended row whose pod was released).
// A store error also returns 0 and false and is logged. On either the caller
// discards its lease claim and restarts the handoff on a later sweep.
//
// Exported so component tests can drive the compare-and-swap directly.
// spec: §10.1.2 (Coordinator Handoff Protocol), §4.2.
func (s *Sweeper) RecordHandoff(ctx context.Context, tenantID, sessionID string, expected int64) (int64, bool) {
	var holdsPod bool
	updated, err := s.sessions.Update(ctx, tenantID, sessionID, func(row *sessionstore.Session) error {
		if row.CoordinationGeneration != expected || !adoptedState(*row) {
			return errHandoffRefused
		}
		row.CoordinationGeneration++
		holdsPod = podHeld(*row)
		return nil
	})
	if err != nil {
		if !errors.Is(err, errHandoffRefused) {
			log.Printf("coordination: bump coordination_generation for session %s: %v", sessionID, err)
		}
		return 0, false
	}
	return updated.CoordinationGeneration, holdsPod
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
// does not accumulate stale entries. spec: §10.1.
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

// recordAdoptionBackoff opens the §10.1.2 jittered re-adoption backoff
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

// nextBackoff returns the §10.1.2 re-adoption delay. A positive
// operator override (Options.AdoptionBackoff) is used verbatim; otherwise
// the delay is jittered uniformly across the 2s-to-16s window.
func (s *Sweeper) nextBackoff() time.Duration {
	if s.adoptionBackoff > 0 {
		return s.adoptionBackoff
	}
	return minAdoptionBackoff + time.Duration(rand.Int63n(int64(maxAdoptionBackoff-minAdoptionBackoff)))
}

// upsertMirror records the §10.1.8 barrier-target row for a lease
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

// releaseMirror marks a terminal session's §10.1.8 barrier-target
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
