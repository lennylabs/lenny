// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter/slotlayout"
	"github.com/lennylabs/lenny/pkg/adapter/workspace"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// slotState is one slot's independent state: the session bound to the
// slot, its resolved per-slot filesystem tree, and its §6.1 per-slot
// credential lease set. Sibling slots share none of this, so a rotation,
// teardown, or workspace change on one slot does not disturb another. The
// single pod-global runtime serves every slot, so a slot owns no runtime
// process of its own. Every session is bound to a slot on every pod,
// whatever the pool's concurrency. spec: §5.2; §6.4; §6.1.
type slotState struct {
	// sessionID is the session assigned to this slot. A session-mode
	// slot's identifier is its session's identifier (§5.2), so the
	// registry key and this value are the same string once the slot is
	// bound; the field records whether the binding has happened.
	sessionID string
	// started records that the merged claim has run for this session,
	// which is what separates a bound-not-started entry (credentials
	// assigned ahead of the start, per the §4.7 bind sequence) from a
	// started one. A second start for the same session is refused on it.
	// spec: §4.7.
	started bool
	// paths is the slot's resolved per-slot directory tree.
	paths slotlayout.SlotPaths
	// creds is the slot's independent credential lease set, keyed by
	// provider. Written to paths.CredentialsFile.
	creds map[string]*adapterv1.CredentialLease
	// assigned records that AssignCredentials has run for this entry. It
	// is the fail-closed was-assigned precondition the rotate and revoke
	// handlers restate: the registry lookup those handlers open with
	// tests registration rather than assignment, and every live session
	// holds a registry entry seeded with an empty lease map by the
	// workspace-prep RPCs. Nothing clears the marker while the entry
	// lives, because creds is the live lease set the platform empties on
	// its own (a direct-mode expiry deletes the provider's entry, and a
	// revocation deletes the revoked ones), and a predicate keyed on the
	// lease set would refuse the replacement lease the expiry fallback
	// pushes over RotateCredentials. spec: §6.1; §4.9.
	assigned bool
	// timers holds the slot's §4.9 direct-mode lease-expiry timers, keyed
	// by provider, independent of sibling slots and the single-slot set.
	timers map[string]*expiryTimer
	// coord is this session's §10.1 coordination state: the generation the
	// pod last accepted for it, whether any fence has landed within this
	// binding, and whether a barrier is holding it quiesced. The unit is
	// the session's binding on the pod rather than the pod's lifetime, so
	// one session's fence neither rejects, gap-flags, nor mis-attributes
	// anything belonging to a co-tenant session. spec: §10.1.2.
	coord coordinationState
	// barrier is this session's §10.1.8 quiesce-and-hold gate, carrying the
	// gateway-minted checkpoint id its own Checkpoint stream links. Each
	// co-tenant session drained together holds its own gate, so two
	// concurrent barriers neither overwrite each other's channel nor
	// cross-link each other's checkpoint id. It keeps its own leaf mutex,
	// independent of coord.mu. spec: §10.1.8.
	barrier barrierGate
	// ack is this entry's session_started acknowledgement gate. The open
	// sequence resets it at each start and settles it on the start's
	// outcome, the deregistration releases it, and every session-scoped
	// CH-RUNTIMEOPS sender waits on it, so the adapter writes such a frame
	// only after it has read the session_started that answers the start.
	// It leaves the registry with its entry, so a successor attempt's entry
	// under the same key carries a new gate. spec: §28.5.3 (CH-MSGSOCK,
	// Outbound: session_started); §28.5.3 (CH-RUNTIMEOPS, Messages).
	ack ackGate
	// bindAttempt is the §4.7.1 bind attempt token the request that created
	// the entry carried, empty when that request carried none. It is written
	// once, by the create branch of ensureSlotStateLocked, and never again
	// while the entry lives (the stamp-once rule). It is a capability over
	// the entry's teardown, so it is never logged or returned in a message.
	// spec: §4.7.1 (role and gateway RPC contract).
	bindAttempt string
}

// ackState is one state of an entry's session_started acknowledgement
// gate. spec: §28.5.3 (CH-MSGSOCK, Outbound: session_started).
type ackState int

const (
	// ackNotStarted is the gate of an entry no start has opened yet. A
	// sender waits, so a frame for a session whose start is still ahead is
	// written only once that start's outcome is known.
	ackNotStarted ackState = iota
	// ackPending is the gate of a start that waits for its session_started.
	ackPending
	// ackRead is the gate of a start whose session_started the adapter read.
	ackRead
	// ackFailed is the gate of a start that ended without the record: its
	// wait ended without the frame, the frame carried error, its
	// session_start write failed, or a rule-8 confirmation was refused.
	ackFailed
	// ackNotAwaiting is the gate of a start that did not wait, because the
	// runtime's CH-RUNTIMEOPS handshake had not completed when it began.
	ackNotAwaiting
	// ackReleased is the gate of a removed entry. It is terminal.
	ackReleased
)

// String names the state for logs and test failures.
func (a ackState) String() string {
	switch a {
	case ackNotStarted:
		return "not_started"
	case ackPending:
		return "pending"
	case ackRead:
		return "read"
	case ackFailed:
		return "failed"
	case ackNotAwaiting:
		return "not_awaiting"
	case ackReleased:
		return "released"
	default:
		return fmt.Sprintf("ackState(%d)", int(a))
	}
}

// errSessionStartNotAcknowledged is the error a session-scoped
// CH-RUNTIMEOPS sender receives when the gate of its session's entry
// failed or was released, or when it did not settle before the sender's
// bound. The sender writes no frame and ends as it ends for a frame the
// runtime does not answer. spec: §28.5.3 (CH-RUNTIMEOPS, Messages).
var errSessionStartNotAcknowledged = errors.New("session_started not read for the session")

// ackGate is an entry's session_started acknowledgement gate.
//
// Its lock is a leaf: it is taken after s.mu when both are held and is
// never held across a wait. Every transition closes the current changed
// channel and replaces it, so each waiter wakes on every transition and
// reads the state again. A waiter from before a start therefore wakes at
// the start's reset to pending, waits again, and returns on that start's
// outcome. The zero value is the not-started gate.
type ackGate struct {
	mu      sync.Mutex
	state   ackState
	startID string
	// changed is closed at the next transition. It is created lazily so
	// the zero value is usable.
	changed chan struct{}
}

// transitionLocked moves the gate to next and wakes every waiter. Callers
// hold g.mu.
func (g *ackGate) transitionLocked(next ackState) {
	g.state = next
	if g.changed != nil {
		close(g.changed)
		g.changed = nil
	}
}

// waitChLocked returns the channel the next transition closes. Callers
// hold g.mu.
func (g *ackGate) waitChLocked() <-chan struct{} {
	if g.changed == nil {
		g.changed = make(chan struct{})
	}
	return g.changed
}

// reset opens the gate for one start: pending when the start waits for its
// session_started, not awaiting when it does not. A released gate is left
// unchanged, because an unguarded removal can deregister the entry between
// the open sequence's first confirmation and this reset.
func (g *ackGate) reset(startID string, awaiting bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state == ackReleased {
		return
	}
	g.startID = startID
	if awaiting {
		g.transitionLocked(ackPending)
		return
	}
	g.transitionLocked(ackNotAwaiting)
}

// settle records that the open sequence read the session_started for
// startID carrying no error, which moves the gate to read. It runs only
// from pending and only for the start the gate was reset to, and reports
// whether it moved the gate. A frame carrying error fails the gate
// through fail instead.
func (g *ackGate) settle(startID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state != ackPending || g.startID != startID {
		return false
	}
	g.transitionLocked(ackRead)
	return true
}

// fail moves the gate to failed for the start whose startID it names,
// unless the gate is already released or failed. A start that ends before
// it minted a startID passes the empty string. The transition applies only
// while the gate belongs to that start (its startID is the gate's) or
// while no start has reset it yet, so a start that returns late cannot
// fail a later start's gate on the same entry, which would refuse every
// session-scoped CH-RUNTIMEOPS frame for the session that later start
// runs. spec: §28.5.3 (CH-MSGSOCK, Session frame writes); §5.2 (slot
// serialization).
func (g *ackGate) fail(startID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state == ackReleased || g.state == ackFailed {
		return
	}
	if g.startID != startID && g.state != ackNotStarted {
		return
	}
	g.transitionLocked(ackFailed)
}

// release moves the gate to released, which is terminal.
func (g *ackGate) release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state == ackReleased {
		return
	}
	g.transitionLocked(ackReleased)
}

// current returns the gate's state.
func (g *ackGate) current() ackState {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.state
}

// check reports the gate's verdict without waiting: nil for read and not
// awaiting, an error for failed and released, and, with unsettled set, the
// channel a waiter blocks on for not started and pending.
func (g *ackGate) check() (unsettled <-chan struct{}, err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	switch g.state {
	case ackRead, ackNotAwaiting:
		return nil, nil
	case ackNotStarted, ackPending:
		return g.waitChLocked(), nil
	default:
		return nil, fmt.Errorf("%w: gate %s", errSessionStartNotAcknowledged, g.state)
	}
}

// await returns once the gate admits a session-scoped frame, or with an
// error when the gate failed or was released, or when ctx ends while the
// gate is not started or pending. Only the wait observes ctx, so a caller
// whose context already ended still writes its frame through a settled
// gate.
func (g *ackGate) await(ctx context.Context) error {
	for {
		ch, err := g.check()
		if err != nil || ch == nil {
			return err
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return fmt.Errorf("%w: %w", errSessionStartNotAcknowledged, ctx.Err())
		}
	}
}

// ready is await for a caller that must not wait: a gate that is not
// started or pending is an error.
func (g *ackGate) ready() error {
	ch, err := g.check()
	if err != nil {
		return err
	}
	if ch != nil {
		return fmt.Errorf("%w: gate %s", errSessionStartNotAcknowledged, g.current())
	}
	return nil
}

// lastFencedGeneration returns the generation this session's binding on
// the pod last accepted through CoordinatorFence, or zero when no
// coordinator has fenced it within this binding. spec: §10.1.2.
func (st *slotState) lastFencedGeneration() int64 {
	st.coord.mu.Lock()
	defer st.coord.mu.Unlock()
	return st.coord.lastFenced
}

// concurrentRoots derives the §6.4 base directories the per-slot trees
// nest under from the adapter's configured roots: the workspace base the
// operator renders onto --workspace-base, and the sessions, artifacts,
// and credentials roots.
func (s *Server) concurrentRoots() slotlayout.Roots {
	return slotlayout.Roots{
		Workspace:   s.WorkspaceBase,
		Sessions:    s.SessionsRoot,
		Artifacts:   s.ArtifactsRoot,
		Credentials: s.CredentialsDir,
	}
}

// resolveSlotPaths derives and validates the per-slot tree for slotID. It
// does not touch the filesystem; ensureSlotTree creates it.
func (s *Server) resolveSlotPaths(slotID string) (slotlayout.SlotPaths, error) {
	return slotlayout.Resolve(s.concurrentRoots(), slotID)
}

// ensureSlotStateLocked resolves the slot's registry entry or creates it,
// and admits or refuses the caller in the same indivisible step. Callers
// hold s.mu for the whole of it, as §4.7.1's registry critical section and
// stamp-once rule require.
//
// This function is the adapter's only resolve-or-create step. Its
// production callers, ensureSlotPaths, assignCredentialsSlot and
// claimSessionSlotUnderLock, cover the requests §4.7.1's admission rules
// govern, so the predicate here is the whole of the admission rules that
// read the registry and no handler carries a second copy of them.
// Well-formedness of bind_attempt against mid_session is a property of the
// request alone, checked by validateBindFields at each handler that carries
// the fields, before the resolve, so a malformed request never reaches this
// function.
//
// The create branch creates the slot's on-disk tree on the first reference
// to the identifier the gateway minted at claim time (§6.4), and inserts no
// entry when the identifier is malformed or the tree cannot be created.
//
// spec: §4.7.1 (role and gateway RPC contract), rules 2 through 7; §6.4.
func (s *Server) ensureSlotStateLocked(slotID string, r slotResolve) (*slotState, error) {
	// The reclaim hold, applied before the map lookup: a held identifier has
	// no entry to return.
	if _, held := s.reclaiming[slotID]; held {
		return nil, errSlotReclaimInProgress
	}
	st, ok := s.slots[slotID]
	switch {
	case !ok && !r.allowCreate:
		// The mid-session-create rule. A mid-session request resolves an
		// entry that already exists and never creates one. Creating here
		// would mint an entry carrying no token, which no attempt could ever
		// reclaim and no sweep collects.
		return nil, errSlotMidSessionNoEntry(slotID)
	case !ok:
		// The create-and-stamp rule. The stamp-once rule makes this branch
		// the only writer of bindAttempt.
		return s.createSlotStateLocked(slotID, r.bindAttempt)
	case r.bindAttempt != "" && st.bindAttempt != "" && st.bindAttempt != r.bindAttempt:
		// The attempt identity rule, evaluated ahead of the started-session
		// rule so a stale attempt is refused as the transient condition it is.
		return nil, errSlotBindAttemptSuperseded(slotID)
	case st.started && !r.allowStarted:
		// The started-session rule.
		return nil, errSlotBindAlreadyStarted(slotID)
	default:
		// The admit rule. The entry is returned and the token is not
		// written, which covers both "the entry carries no token" and "the
		// caller asserts no identity".
		return st, nil
	}
}

// createSlotStateLocked is the create branch of ensureSlotStateLocked: it
// validates the identifier, creates the slot's on-disk tree, and inserts
// the entry stamped with bindAttempt. Callers hold s.mu.
// spec: §4.7.1 rule 4; §6.4.
func (s *Server) createSlotStateLocked(slotID, bindAttempt string) (*slotState, error) {
	if s.slots == nil {
		s.slots = map[string]*slotState{}
	}
	paths, err := s.resolveSlotPaths(slotID)
	if err != nil {
		return nil, err
	}
	if err := slotlayout.EnsureTree(paths); err != nil {
		return nil, err
	}
	// The entry's acknowledgement gate starts not started, its zero value,
	// so a session-scoped CH-RUNTIMEOPS sender that resolves the entry
	// before the session's start waits for that start's outcome.
	// spec: §28.5.3 (CH-RUNTIMEOPS, Messages).
	st := &slotState{
		paths:       paths,
		creds:       map[string]*adapterv1.CredentialLease{},
		timers:      map[string]*expiryTimer{},
		bindAttempt: bindAttempt,
	}
	s.slots[slotID] = st
	return st, nil
}

// validateBindFields applies §4.7.1 rule 1, the pairing rule: a request
// that is not marked mid_session must carry a bind attempt token, and one
// marked mid_session must carry none. It reads no registry state and takes
// no lock, because well-formedness is a property of the request alone, and
// each handler whose message carries the fields calls it before any
// resolve. The refusal is returned unwrapped: its code is already the one
// the rule fixes. The message never contains the token.
// spec: §4.7.1 (role and gateway RPC contract), rule 1.
func validateBindFields(bindAttempt string, midSession bool) error {
	if midSession && bindAttempt != "" {
		return status.Error(codes.InvalidArgument,
			"a mid_session request must not carry a bind_attempt")
	}
	if !midSession && bindAttempt == "" {
		return status.Error(codes.InvalidArgument,
			"a request that is not marked mid_session requires a bind_attempt")
	}
	return nil
}

// slotStateLocked returns the slot's state if it has been assigned.
// Callers hold s.mu.
func (s *Server) slotStateLocked(slotID string) (*slotState, bool) {
	st, ok := s.slots[slotID]
	return st, ok
}

// ensureSlotPaths ensures the slot's registry entry and on-disk tree
// exist and returns its resolved paths. The §4.7 workspace-prep RPCs
// (PrepareWorkspace, FinalizeWorkspace, RunSetup) run before StartSession,
// so this creates the slot tree the first time the gateway materializes
// the slot's workspace, ahead of the slot's StartSession claim.
//
// r is the caller's assertion about the entry, passed through to the
// resolve. spec: §4.7.1 (role and gateway RPC contract).
func (s *Server) ensureSlotPaths(slotID string, r slotResolve) (slotlayout.SlotPaths, error) {
	st, err := s.ensureSlotEntry(slotID, r)
	if err != nil {
		return slotlayout.SlotPaths{}, err
	}
	return st.paths, nil
}

// ensureSlotEntry is ensureSlotPaths for a caller that holds on to the
// entry it resolved, such as a mid-session FinalizeWorkspace whose
// files_updated frame waits on that entry's acknowledgement gate.
// spec: §4.7.1 (role and gateway RPC contract); §28.5.3 (CH-RUNTIMEOPS,
// Messages).
func (s *Server) ensureSlotEntry(slotID string, r slotResolve) (*slotState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ensureSlotStateLocked(slotID, r)
}

// workspaceRootForSession returns the cwd the adapter-local tool dispatch
// resolves against for the named session: its
// /workspace/slots/{sessionId}/current. The per-slot tree is the only
// layout, so a session the registry does not hold has no root and the
// caller fails closed rather than dispatching against a pod-global
// directory. spec: §6.4.
func (s *Server) workspaceRootForSession(sessionID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.slots[sessionID]
	if !ok || st.paths.Current == "" {
		return "", status.Errorf(codes.FailedPrecondition,
			"session %s has no workspace on this pod", sessionID)
	}
	return st.paths.Current, nil
}

// checkpointRootsForSession returns the §4.4 checkpoint bundle for the
// named session together with the registry entry it resolved:
// /workspace/slots/{sessionId}/current under WorkspacePrefix and
// /sessions/{sessionId} under SessionsPrefix, because the session tmpfs is
// itself per-session. A session with no registry entry or no bound session
// is rejected with FailedPrecondition so a checkpoint never captures an
// empty or nonexistent subtree for an unassigned slot; the adapter fails
// closed on the slot gate.
//
// The entry is returned so a caller that later links into that session's
// barrier gate holds the pointer this guard validated. A second lookup by
// session identifier would race the deregistration paths, which delete the
// map key while a checkpoint queued behind a co-tenant's upload is still
// running. spec: §5.2 (per-slot checkpoint granularity), §6.4 (per-slot
// export target), §4.4 (durability contract), §10.1.8 (the barrier gate
// the stream links into).
func (s *Server) checkpointRootsForSession(sessionID string) ([]workspace.NamedRoot, *slotState, error) {
	s.mu.Lock()
	st, ok := s.slotStateLocked(sessionID)
	var current, sessions, sess string
	if ok {
		current = st.paths.Current
		sessions = st.paths.Sessions
		sess = st.sessionID
	}
	s.mu.Unlock()
	if !ok || sess == "" {
		return nil, nil, status.Errorf(codes.FailedPrecondition,
			"session %s has no assigned slot on this pod", sessionID)
	}
	roots := []workspace.NamedRoot{
		{Prefix: workspace.WorkspacePrefix, Root: current},
	}
	if sessions != "" {
		roots = append(roots, workspace.NamedRoot{
			Prefix: workspace.SessionsPrefix, Root: sessions,
		})
	}
	return roots, st, nil
}

// removeSlotTree removes the slot's per-slot directory tree on cleanup.
// spec: §6.4.
func removeSlotTree(st *slotState) error {
	return slotlayout.RemoveTree(st.paths)
}

// removeSlotTreeVia removes the slot's per-slot tree through the test seam
// when one is set and through removeSlotTree otherwise. Every release site
// whose reclaim-hold release reads the removal's result calls it, so the
// retained-hold arm can be driven from a unit test.
// spec: §5.2 (slot-identifier reclaim hold); §6.4.
func (s *Server) removeSlotTreeVia(st *slotState) error {
	if s.removeSlotTreeFn != nil {
		return s.removeSlotTreeFn(st)
	}
	return removeSlotTree(st)
}

// runtimeForSession returns the runtime process that drives the named
// session. The single pod-global Runtime serves every slot, so every
// registered session resolves to the same Runtime. A session the registry
// does not hold returns nil so the caller surfaces a FailedPrecondition.
// spec: §6.4.
func (s *Server) runtimeForSession(sessionID string) RuntimeProcess {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.slots[sessionID]; ok {
		return s.Runtime
	}
	return nil
}
