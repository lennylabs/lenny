// SPDX-License-Identifier: MIT

package adapter

import (
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
	// timers holds the slot's §4.9 direct-mode lease-expiry timers, keyed
	// by provider, independent of sibling slots and the single-slot set.
	timers map[string]*expiryTimer
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

// ensureSlotStateLocked returns the slot's state, creating the registry entry
// and its on-disk tree on first reference. It is idempotent: a second
// call for the same slot returns the existing state without recreating
// the tree's content. Callers hold s.mu.
//
// spec: §6.4 — the gateway mints the slot's identifier at claim time,
// and the adapter creates that session's slot tree on the first
// reference to the identifier.
func (s *Server) ensureSlotStateLocked(slotID string) (*slotState, error) {
	if s.slots == nil {
		s.slots = map[string]*slotState{}
	}
	if st, ok := s.slots[slotID]; ok {
		return st, nil
	}
	paths, err := s.resolveSlotPaths(slotID)
	if err != nil {
		return nil, err
	}
	if err := slotlayout.EnsureTree(paths); err != nil {
		return nil, err
	}
	st := &slotState{
		paths:  paths,
		creds:  map[string]*adapterv1.CredentialLease{},
		timers: map[string]*expiryTimer{},
	}
	s.slots[slotID] = st
	return st, nil
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
func (s *Server) ensureSlotPaths(slotID string) (slotlayout.SlotPaths, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.ensureSlotStateLocked(slotID)
	if err != nil {
		return slotlayout.SlotPaths{}, err
	}
	return st.paths, nil
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
// named session: /workspace/slots/{sessionId}/current under
// WorkspacePrefix and /sessions/{sessionId} under SessionsPrefix, because
// the session tmpfs is itself per-session. A session with no registry
// entry or no bound session is rejected with FailedPrecondition so a
// checkpoint never captures an empty or nonexistent subtree for an
// unassigned slot; the adapter fails closed on the slot gate. spec: §5.2
// (per-slot checkpoint granularity), §6.4 (per-slot export target),
// §4.4 (durability contract).
func (s *Server) checkpointRootsForSession(sessionID string) ([]workspace.NamedRoot, error) {
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
		return nil, status.Errorf(codes.FailedPrecondition,
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
	return roots, nil
}

// removeSlotTree removes the slot's per-slot directory tree on cleanup.
// spec: §6.4.
func removeSlotTree(st *slotState) error {
	return slotlayout.RemoveTree(st.paths)
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
