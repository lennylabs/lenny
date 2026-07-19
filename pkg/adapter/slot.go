// SPDX-License-Identifier: MIT

package adapter

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter/slotlayout"
	"github.com/lennylabs/lenny/pkg/adapter/workspace"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// slotState is one §6.4 concurrent-workspace slot's independent state: the
// session bound to the slot, its resolved per-slot filesystem tree, and
// its §6.1 per-slot credential lease set. Sibling slots share none of this,
// so a rotation, teardown, or workspace change on one slot does not disturb
// another. The single pod-global runtime serves every slot, multiplexed on
// slotId over the one runtime connection (spec/05:509, spec/15:1459), so a
// slot owns no runtime process of its own. spec: §6.4 lines 385-409;
// §6.1 line 28.
type slotState struct {
	// sessionID is the session assigned to this slot. The gateway uses
	// the session id as the slot id, but the adapter validates inbound
	// RPCs against the recorded session id independently.
	sessionID string
	// paths is the slot's resolved per-slot directory tree.
	paths slotlayout.SlotPaths
	// creds is the slot's independent credential lease set, keyed by
	// provider. Written to paths.CredentialsFile.
	creds map[string]*adapterv1.CredentialLease
	// timers holds the slot's §4.9 direct-mode lease-expiry timers, keyed
	// by provider, independent of sibling slots and the single-slot set.
	timers map[string]*expiryTimer
}

// useSlot reports whether the adapter should take the §6.4 per-slot path
// for slotID: the RPC carries a non-empty slot id. A slotId-bearing frame
// is by definition a concurrent-pool frame (spec/15:1459: runtimes on
// maxConcurrentSessions == 1 pods never see a slotId), so the per-slot
// decision keys on slotId presence alone. The single-session base layout
// (slotID == "") is left untouched.
func (s *Server) useSlot(slotID string) bool {
	return slotID != ""
}

// concurrentRoots derives the §6.4 base directories the per-slot trees
// nest under from the adapter's configured roots: the workspace base is
// the parent of the single WorkspaceRoot (WorkspaceBase), and sessions,
// artifacts, and credentials reuse the same roots the single-slot layout
// uses.
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

// ensureSlotState returns the slot's state, creating the registry entry
// and its on-disk tree on first reference. It is idempotent: a second
// call for the same slot returns the existing state without recreating
// the tree's content. Callers hold s.mu.
//
// spec: §6.4 lines 401-405 — "The adapter creates the slot directory on
// slotId assignment".
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

// workspaceRootForSlot returns the cwd the adapter-local tool dispatch
// resolves against for slotID: the slot's /workspace/slots/{slotId}/current
// for a concurrent slot, or the pod-global WorkspaceRoot for the
// single-session base path. An unknown slot falls back to WorkspaceRoot.
func (s *Server) workspaceRootForSlot(slotID string) string {
	if !s.useSlot(slotID) {
		return s.WorkspaceRoot
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.slots[slotID]; ok && st.paths.Current != "" {
		return st.paths.Current
	}
	return s.WorkspaceRoot
}

// checkpointRootsForSlot returns the §4.4 checkpoint bundle for slotID.
// For the single-session base path (slotID == "") it returns the pod-global
// s.checkpointRoots() slice unchanged, so an absent slot_id checkpoints
// byte-for-byte as before. For a concurrent slot it swaps both roots to the
// slot subtree: /workspace/slots/{slotId}/current under WorkspacePrefix and
// /sessions/{slotId} under SessionsPrefix, because the session tmpfs is
// itself slot-scoped. A slot with no registry entry or no bound session is
// rejected with FailedPrecondition so a checkpoint never captures an empty
// or nonexistent subtree for an unassigned slot; the adapter fails closed
// on the slot gate. spec: §5.2 (per-slot checkpoint granularity), §6.4
// (slot-qualified export target /workspace/slots/{slotId}/current and
// /sessions/{slotId}), §4.4 (durability contract).
func (s *Server) checkpointRootsForSlot(slotID string) ([]workspace.NamedRoot, error) {
	if !s.useSlot(slotID) {
		return s.checkpointRoots(), nil
	}
	s.mu.Lock()
	st, ok := s.slotStateLocked(slotID)
	var current, sessions, sess string
	if ok {
		current = st.paths.Current
		sessions = st.paths.Sessions
		sess = st.sessionID
	}
	s.mu.Unlock()
	if !ok || sess == "" {
		return nil, status.Errorf(codes.FailedPrecondition,
			"slot %s has no assigned session", slotID)
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
// spec: §6.4 lines 401-405.
func removeSlotTree(st *slotState) error {
	return slotlayout.RemoveTree(st.paths)
}

// runtimeForSlot returns the runtime process that drives slotID. The
// single pod-global Runtime serves every slot, multiplexed on slotId over
// the one runtime connection (spec/05:509, spec/15:1459), so an assigned
// concurrent slot resolves to the same Runtime as the single-session base
// layout. An unknown (unassigned) slot returns nil so the caller surfaces
// a FailedPrecondition.
func (s *Server) runtimeForSlot(slotID string) RuntimeProcess {
	if !s.useSlot(slotID) {
		return s.Runtime
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.slots[slotID]; ok {
		return s.Runtime
	}
	return nil
}
