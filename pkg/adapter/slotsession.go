// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// claimSessionSlot binds sessionID's slot entry and marks it started. It
// is the one claim every start path takes: StartSession, Resume, and the
// SDK-warm ConfigureWorkspace.
//
// The claim discriminates on whether the session has started rather than
// on whether its entry is bound, because the §4.7 bind sequence assigns
// credentials before the start and assignCredentialsSlot binds the entry
// when it runs first. A rule keyed on the binding would refuse the first
// start of every session on every pool that configures a credential pool
// or a user credential provider. It therefore admits the three states an
// entry can be in ahead of a start — absent, registered, and
// bound-not-started — and refuses a started one.
//
// sdkWarm gates the different-session refusal. §6.1 admits preConnect
// only at maxConcurrentSessions: 1, so the specification itself fixes
// this pod class at one session and a second session there has no
// runtime of its own: the pod holds one pre-connected runtime process
// with one working directory the handler re-points in place, and
// admitting a second session would rewrite the §15.4.3 nonce the
// incumbent runtime already authenticated with. The refusal is therefore
// keyed on the pod already holding another session's bound entry rather
// than on the started flag the same-session arm reads, and it is that
// ceiling of one rather than the live slot count. A pod stranded by a
// bind that failed between AssignCredentials and ConfigureWorkspace is
// recovered by DemoteSDK's release and by the gateway reclaiming the
// pod. On a pod-warm pod a different session arrives on its own slot and
// is admitted.
//
// idempotentRepeat reports a repeat for an already-started session as
// fresh=false rather than refusing it, which is the §4.7
// ConfigureWorkspace idempotency. r is the resolve the calling handler
// asserts; ConfigureWorkspace sets its allowStarted from the same value as
// idempotentRepeat, so §4.7.1 rule 6 admits the repeat it exempts.
//
// The claim reports a slotClaim; see its fields for what each one means.
//
// spec: §4.7; §4.7.1 (role and gateway RPC contract); §5.2; §15.4.3.
func (s *Server) claimSessionSlot(sessionID string, r slotResolve, sdkWarm, idempotentRepeat bool) (slotClaim, error) {
	claim, stale, err := s.claimSessionSlotUnderLock(sessionID, r, sdkWarm, idempotentRepeat)
	// A surface armed by a session the registry no longer holds is torn
	// down outside s.mu, before the claimant arms its own on a fresh
	// nonce. spec: §15.4.3.
	runCancels(stale)
	return claim, err
}

// slotClaim is what a start claim reports to the handler that took it.
type slotClaim struct {
	// fresh is false for an idempotent repeat of an already-started
	// session, which the §4.7 ConfigureWorkspace idempotency admits.
	fresh bool
	// startMCP reports that this claim took the once-per-pod intra-pod MCP
	// start. The decision is taken inside the claim's critical section
	// rather than as a read followed by a bind, because the platform
	// server binds the one socket the controller renders for the whole pod
	// and two concurrent claims that both observed it free would hand the
	// loser EADDRINUSE.
	startMCP bool
	// attempt is the bind attempt token the entry carried inside the
	// critical section that claimed it. The handler holds it across
	// Runtime.Start and passes it to noteRuntimeStarted, so the start
	// confirmation compares against the value the claim itself was
	// admitted against. On a start that carries no token of its own it is
	// the entry's own stamp, so an entry no later attempt replaced compares
	// equal to itself. spec: §4.7.1 (role and gateway RPC contract), rule 8.
	attempt string
	// entry is the registry entry the claim was admitted against. The
	// handler's failure rollbacks release the slot only while the registry
	// still holds this entry under the session identifier, so an abandoned
	// attempt's late failure cannot remove the entry a successor attempt
	// created after a reclaim. Pointer identity is compared rather than the
	// token alone, because two untokened entries for one session carry the
	// same empty token. spec: §4.7.1 (role and gateway RPC contract).
	entry *slotState
}

// claimSessionSlotUnderLock is claimSessionSlot's critical section. It
// returns the cancel functions of a stale pod MCP surface the claim took
// over, for the caller to run once the lock is released.
func (s *Server) claimSessionSlotUnderLock(sessionID string, r slotResolve, sdkWarm, idempotentRepeat bool) (slotClaim, []context.CancelFunc, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sdkWarm {
		for id, other := range s.slots {
			if id != sessionID && other.sessionID != "" {
				return slotClaim{}, nil, status.Errorf(codes.Unavailable,
					"pod is not idle: session %s is already bound on this pod", id)
			}
		}
	}
	st, err := s.ensureSlotStateLocked(sessionID, r)
	if err != nil {
		return slotClaim{}, nil, slotResolveError(err,
			fmt.Sprintf("resolve slot for session %s", sessionID))
	}
	// The local statement of §4.7.1 rule 6. The resolve refuses a started
	// entry first unless the caller asserted allowStarted, and s.mu forbids
	// the entry starting between the resolve and here, so this arm answers
	// the idempotent repeat and states the refusal for a later reader.
	if st.started {
		if idempotentRepeat {
			return slotClaim{attempt: st.bindAttempt, entry: st}, nil, nil
		}
		return slotClaim{}, nil, errSlotBindAlreadyStarted(sessionID)
	}
	st.sessionID = sessionID
	st.started = true
	startMCP, stale := s.claimPodMCPStartLocked(sessionID)
	return slotClaim{fresh: true, startMCP: startMCP, attempt: st.bindAttempt, entry: st}, stale, nil
}

// claimPodMCPStartLocked reports whether the caller must arm the pod's
// platform and per-connector MCP servers, and records the claiming
// session so a concurrent claim does not also take the one socket. The
// servers are pod-wide, so they are armed only while the registry holds
// no entry but the claimant's; a claim on a co-tenanted pod finds the
// surface already accounted for.
//
// A claim that holds the pod alone takes the arming even when a surface
// is still up, because that surface was armed by a session the registry
// no longer holds and serves that session's nonce. Its cancel functions
// are returned so the claimant tears it down before arming its own. The
// arming decision and the cancellation predicate read and write the same
// identifier under s.mu, so a departing session's release cannot cancel
// the surface a successor's claim has already taken.
//
// Callers hold s.mu. spec: §15.4.3.
func (s *Server) claimPodMCPStartLocked(sessionID string) (startMCP bool, stale []context.CancelFunc) {
	if len(s.slots) != 1 || s.mcpSession == sessionID {
		return false, nil
	}
	stale = s.takePodMCPCancelsLocked()
	s.mcpSession = sessionID
	return true, stale
}

// takePodMCPCancelsLocked clears the pod's intra-pod MCP surface state
// and returns the cancel functions that stop the running servers, for the
// caller to run once s.mu is released. Callers hold s.mu.
// spec: §15.4.3; §5.1 — F-5.1.11.
func (s *Server) takePodMCPCancelsLocked() []context.CancelFunc {
	cancels := make([]context.CancelFunc, 0, len(s.connectorCancels)+1)
	if s.mcpCancel != nil {
		cancels = append(cancels, s.mcpCancel)
		s.mcpCancel = nil
	}
	cancels = append(cancels, s.connectorCancels...)
	s.connectorCancels = nil
	s.mcpHandshakeSeen = false
	s.mcpArmedNonce = ""
	return cancels
}

// PodMCPArming returns the session whose claim took the once-per-pod
// intra-pod MCP start and the nonce the pod's running servers
// authenticate. Both are empty on a pod whose surface is unarmed, and the
// nonce alone is empty when the claimant is a type: mcp runtime, for
// which the adapter arms no server. It is the exported reading of the
// arming a caller cannot recover from the pod's one manifest file, which
// each start republishes by renaming a freshly staged document over it:
// the manifest names whichever rename landed last, while this names the
// start the live servers belong to. spec: §15.4.3.
func (s *Server) PodMCPArming() (sessionID, nonce string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mcpSession, s.mcpArmedNonce
}

// runCancels runs every cancel function in order. It is called with no
// lock held: a cancel stops a server goroutine that takes s.mu itself.
func runCancels(cancels []context.CancelFunc) {
	for _, c := range cancels {
		c()
	}
}

// deregisterSlotLocked is the first of the two release steps: under s.mu
// it cancels every direct-mode lease-expiry timer armed on the session's
// entry, deletes the entry, and reports whether any bound entry remains.
// It returns the deregistered state so the caller can run the second step
// after the lock is released.
//
// The cancellation belongs here because an armed timer left behind fires
// AUTH_EXPIRED against a session that has already ended, and both teardown
// paths this step replaces cancelled before the runtime close. It is
// unconditional on the binding, and Shutdown's reclaim of an unbound or
// unstarted entry relies on that as much as the bound teardown does.
//
// The bound-entry answer is the outcome of the same critical section that
// removed the entry rather than a read taken before it, so two co-tenants
// ending at once cannot each observe the other and both decline the
// pod-wide action the answer gates. Callers hold s.mu.
//
// spec: §4.9; §15.4.2.
func (s *Server) deregisterSlotLocked(sessionID string) (st *slotState, removed, boundRemains bool) {
	st, removed = s.slots[sessionID]
	if removed {
		for provider := range st.timers {
			s.cancelSlotExpiryTimerLocked(st, provider)
		}
		delete(s.slots, sessionID)
	}
	for _, other := range s.slots {
		if other.sessionID != "" {
			boundRemains = true
			break
		}
	}
	return st, removed, boundRemains
}

// reclaimSlotLocked is the deregistration every release that then destroys
// the slot's tree takes. It runs deregisterSlotLocked and, when that removed
// an entry, opens the §5.2 slot-identifier reclaim hold in the same critical
// section, so no bind can be admitted onto the identifier between the
// removal and the destructive steps that follow it.
//
// release is never nil and is idempotent. The caller defers it and takes it
// only on the arm where the cleanup it then ran completed, because §5.2 ends
// the hold on a completed cleanup and keeps the identifier held for the life
// of the pod on one that did not. A call that removed nothing opened no hold
// and returns a no-op. Callers hold s.mu.
//
// spec: §5.2 (slot-identifier reclaim hold); §4.7.1 (role and gateway RPC
// contract), the registry critical section.
func (s *Server) reclaimSlotLocked(sessionID string) (st *slotState, removed, boundRemains bool, release func()) {
	st, removed, boundRemains = s.deregisterSlotLocked(sessionID)
	if !removed {
		return st, false, boundRemains, noHoldRelease
	}
	return st, true, boundRemains, s.openReclaimHoldLocked(sessionID)
}

// reclaimSlotIfOwnedLocked is reclaimSlotLocked for a release made on behalf
// of one claim: it deregisters the session's entry only while the registry
// still holds the entry that claim was admitted against. On a mismatch it
// removes nothing, opens no hold and returns a no-op release.
//
// The slot identifier equals the session identifier, so after a
// compensating Shutdown reclaims an attempt's entry a successor attempt at
// the same session creates a new entry under the same key. A rollback keyed
// on the identifier alone would then remove the successor's entry, its
// staged tree and its credential directory. The comparison runs in the same
// critical section as the deregistration, which is the §4.7.1 registry
// critical section. Callers hold s.mu.
//
// spec: §4.7.1 (role and gateway RPC contract), the stamp-once rule; §5.2
// (slot-identifier reclaim hold).
func (s *Server) reclaimSlotIfOwnedLocked(sessionID string, entry *slotState) (st *slotState, removed bool, release func()) {
	if cur, ok := s.slots[sessionID]; !ok || cur != entry {
		return nil, false, noHoldRelease
	}
	st, removed, _, release = s.reclaimSlotLocked(sessionID)
	return st, removed, release
}

// releaseClaimedSlot is the compensating release a start handler that holds
// no guard takes when a step after its claim fails: StartSession and the
// SDK-warm ConfigureWorkspace on a fresh claim. It is releaseSessionSlot
// fenced on the claim's entry, so it undoes the claim only while that
// entry is still the one registered under the session identifier. When a
// reclaim removed the entry, or a successor replaced it, the release
// removes nothing and takes only the pod-wide MCP half, as the refused
// start confirmation does; the runtime half is absent because the failure
// arms run before a successful Runtime.Start.
//
// spec: §4.7; §4.7.1 (role and gateway RPC contract); §5.2
// (slot-identifier reclaim hold); §15.4.3.
func (s *Server) releaseClaimedSlot(ctx context.Context, sessionID string, claim slotClaim) {
	unlock, guarded := s.lockSlotGuard(ctx, sessionID)
	defer unlock()
	if !guarded {
		warnSlotGuardNotAcquired(sessionID, "releaseClaimedSlot")
	}
	s.mu.Lock()
	st, removed, release := s.reclaimSlotIfOwnedLocked(sessionID, claim.entry)
	s.mu.Unlock()
	if !removed {
		slog.Info("slot_release_skipped_entry_not_owned", "slot_id", sessionID)
	}
	s.finishSlotRelease(sessionID, st, removed, guarded, release)
}

// releaseSessionSlot runs both release steps in immediate succession and
// then the pod-surface cancellation, under the slot's per-slot guard. It
// removes whatever entry the registry holds under the identifier, so it
// serves a caller that owns the slot whatever attempt created it, such as
// the SDK-warm DemoteSDK. A start handler's failure rollback uses
// releaseClaimedSlot instead, which releases only the entry its claim was
// admitted against.
//
// The guard is acquired on ctx, the caller's own context, ahead of s.mu. At
// the StartSession and SDK-warm rollbacks, which share this acquisition
// through releaseClaimedSlot, that is the context whose expiry
// failed Runtime.Start, which is why the acquisition takes a free guard
// whatever state ctx is in. An acquisition that outlives ctx is logged as
// slot_guard_not_acquired and the release still runs, unguarded, because
// abandoning the removal would leave a worse residue than an unordered one;
// the cleanup then counts as not completed and the reclaim hold is kept.
// The guard's release is deferred ahead of the body's hold release, so the
// guard outlives the hold.
//
// spec: §4.7; §5.2 (slot-identifier reclaim hold); §15.4.3.
func (s *Server) releaseSessionSlot(ctx context.Context, sessionID string) {
	unlock, guarded := s.lockSlotGuard(ctx, sessionID)
	defer unlock()
	if !guarded {
		warnSlotGuardNotAcquired(sessionID, "releaseSessionSlot")
	}
	s.releaseSessionSlotUnderGuard(sessionID, guarded)
}

// releaseSessionSlotUnderGuard is releaseSessionSlot's body, for a caller
// that already holds the slot's guard or whose acquisition expired. Resume
// calls it directly at its rollback sites, because it holds the guard from
// ahead of its claim and a second acquisition of the capacity-one guard
// would block rather than re-enter.
//
// The pod-wide MCP teardown is gated on the release leaving the pod's
// shared runtime process serving no session and on the session that armed
// the surface no longer holding a slot, so a rollback on a co-tenanted pod
// cancels nothing and the co-tenant keeps the surface it is using. A
// rollback that armed the servers and never reached a start deregisters
// its own entry first, so it finds that process idle and the arming
// unheld and cancels them, which is how a pod recovers from a failed
// StartSession, Resume, or ConfigureWorkspace.
//
// The deregistration opens the §5.2 reclaim hold, and the hold ends only
// when the cleanup completed: the section ran under its guard and the tree
// removal returned without error, which is the whole cleanup this release
// owes the slot because it closes no runtime. A removal that fails is
// logged and keeps the identifier held for the life of the pod. The
// release is deferred, so a panic out of the removal is a cleanup that did
// not complete.
//
// spec: §4.7; §5.2 (slot-identifier reclaim hold); §15.4.3.
func (s *Server) releaseSessionSlotUnderGuard(sessionID string, guarded bool) {
	s.mu.Lock()
	st, removed, _, release := s.reclaimSlotLocked(sessionID)
	s.mu.Unlock()
	s.finishSlotRelease(sessionID, st, removed, guarded, release)
}

// finishSlotRelease is the part of a release that runs after the
// deregistration, outside s.mu: it removes the deregistered entry's tree,
// ends the reclaim hold only on a guarded removal that returned without
// error, and cancels the pod-wide MCP surface when no session still uses
// it. A release that removed nothing takes the MCP half alone.
// spec: §5.2 (slot-identifier reclaim hold); §15.4.3.
func (s *Server) finishSlotRelease(sessionID string, st *slotState, removed, guarded bool, release func()) {
	completed := false
	defer func() {
		if completed {
			release()
		}
	}()
	if removed {
		if err := s.removeSlotTreeVia(st); err != nil {
			slog.Warn("slot_tree_removal_failed", "slot_id", sessionID, "error", err)
		} else {
			completed = guarded
		}
	}
	s.cancelPodMCPIfRuntimeIdle()
}

// cancelPodMCPIfRuntimeIdle stops the pod's platform and per-connector
// MCP servers and clears the §15.4.3 handshake signal when the pod's
// shared runtime process is serving no session and the session that armed
// the surface no longer holds a slot. A release that ends the generation
// cancels a surface no surviving session can use, and a release that
// leaves the process serving a session cancels nothing.
//
// The arming identifier is half the predicate because the two writers
// interleave: a successor's claim can take the arming between a departing
// session's deregistration and the return of that session's
// Runtime.Close, and the departing release then finds the process idle.
// Cancelling there would leave the successor holding a manifest nonce
// with no server listening, and its own claim has already returned, so
// nothing would re-arm for the life of the pod.
//
// spec: §15.4.3; §5.1 — F-5.1.11.
func (s *Server) cancelPodMCPIfRuntimeIdle() {
	s.mu.Lock()
	if !s.runtimeIdleLocked() || s.mcpArmingHeldLocked() {
		s.mu.Unlock()
		return
	}
	cancels := s.takePodMCPCancelsLocked()
	s.mcpSession = ""
	s.mu.Unlock()
	runCancels(cancels)
}

// mcpArmingHeldLocked reports that the session which armed the pod's MCP
// surface still holds a slot on the pod, so the surface is one a live
// claimant is using rather than a departed session's residue. Callers
// hold s.mu. spec: §15.4.3.
func (s *Server) mcpArmingHeldLocked() bool {
	if s.mcpSession == "" {
		return false
	}
	_, held := s.slots[s.mcpSession]
	return held
}

// boundSlotState validates an inbound session-scoped RPC against the slot
// registry and returns the entry it resolved. Every session is bound to a
// slot on every pod, so this is the one session check: it admits an entry
// bound to the named session (started or not) and refuses one that is
// absent or registered but not yet bound.
//
// A handler that goes on to read or write the session's §10.1 coordination
// state takes the entry from here and holds it for the life of the call,
// rather than looking the session up a second time: the deregistration
// paths delete the map key while returning the pointer with no field
// zeroed, so a later lookup can find nothing for a call that is still
// running. spec: §5.2; §6.4; §10.1.2.
func (s *Server) boundSlotState(sessionID string) (*slotState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.slotStateLocked(sessionID)
	if !ok || st.sessionID == "" {
		return nil, status.Errorf(codes.FailedPrecondition,
			"session %s is not assigned to this pod", sessionID)
	}
	return st, nil
}

// checkSessionBound is boundSlotState for a handler that needs the guard
// alone. spec: §5.2; §6.4.
func (s *Server) checkSessionBound(sessionID string) error {
	_, err := s.boundSlotState(sessionID)
	return err
}

// slotStateForSession returns the registry entry for the named session, or
// nil when the registry holds none. It admits a registered-but-unbound
// entry, so a caller that needs the §5.2 binding guard uses boundSlotState
// instead. spec: §6.4.
func (s *Server) slotStateForSession(sessionID string) *slotState {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.slotStateLocked(sessionID)
	if !ok {
		return nil
	}
	return st
}

// heldSession is one member of the set the §10.1 hold timeout terminates:
// a session the adapter had started and whose registry entry pass 1
// deregistered, carried with the deregistered state so pass 2 can remove
// its per-slot tree, and with the release of the §5.2 reclaim hold pass 1
// opened for it, which pass 2 takes only when the member's cleanup
// completed. spec: §10.1.4; §5.2; §6.4.
type heldSession struct {
	sessionID string
	state     *slotState
	release   func()
}

// countStartedSessionsLocked reports how many registry entries carry the
// started flag. Callers hold s.mu. spec: §4.7.
func (s *Server) countStartedSessionsLocked() int {
	n := 0
	for _, st := range s.slots {
		if st.started {
			n++
		}
	}
	return n
}

// hasStartedSession reports whether the adapter has started any session on
// this pod. The §10.1 coordinator hold arms on it: a closed gateway
// control stream is a coordinator loss only while the pod is serving a
// session it started.
//
// The predicate is the started flag rather than the entry's bound state,
// because the §4.7 bind sequence assigns credentials before the start, so
// a bind that failed after credential assignment leaves a bound entry for
// a session the gateway has since re-placed on another pod. Arming on that
// entry would terminate, notify, and bill a session that is live
// elsewhere. spec: §10.1; §4.7.
func (s *Server) hasStartedSession() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.countStartedSessionsLocked() > 0
}

// startedSessionCount returns how many sessions the adapter has started on
// this pod. The §10.1 arming log carries it in place of a session
// identifier, because the hold names the pod rather than a session.
// spec: §10.1.
func (s *Server) startedSessionCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.countStartedSessionsLocked()
}

// deregisterStartedSessions is the first pass of the §10.1.4 hold
// termination: under one s.mu hold it collects every started entry,
// cancels each entry's direct-mode expiry timers, deletes each entry, opens
// each member's §5.2 reclaim hold, and returns the members sorted by session
// identifier.
//
// Emptying the registry in one critical section before any termination
// work is what makes the termination and a concurrent gateway Shutdown
// mutually exclusive. That handler decides on the outcome of its own
// locked cancel-deregister step, so its step and this one are two
// acquisitions of one lock: a request whose step runs after this pass
// removes nothing and skips its teardown, and one whose step runs first
// takes the member out of the set collected here. Without the pass, every
// terminated session's entry would survive for the life of the pod,
// holding the §15.4.2 drain gate false and the §28.5.3 inbound count above
// one on a pod that goes on serving.
//
// The order is fixed rather than incidental: s.slots is a map and Go
// randomizes a map's range order, so an unsorted collection would leave
// the order pass 2 terminates in undetermined.
//
// spec: §10.1.4; §4.7; §4.9.
func (s *Server) deregisterStartedSessions() []heldSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.slots))
	for id, st := range s.slots {
		if st.started {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	members := make([]heldSession, 0, len(ids))
	for _, id := range ids {
		// The bound-entry result exists to decide the §15.4.2 drain, which
		// this path does not send, so it is discarded here. The reclaim hold
		// opens with the deregistration and pass 2 carries its release.
		st, removed, _, release := s.reclaimSlotLocked(id)
		if !removed {
			continue
		}
		members = append(members, heldSession{sessionID: id, state: st, release: release})
	}
	return members
}

// slotCount reports the entries the slot registry holds, bound or
// registered-but-unbound. It is the quantity §28.5.3's resolve-or-reject
// rule reads: counting a registered-but-unbound entry makes the rule fail
// closed while a second session's workspace is being prepared, so an
// unaddressed frame is never resolved to the incumbent session on a pod
// that is about to serve two. spec: §28.5.3.
func (s *Server) slotCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.slots)
}
