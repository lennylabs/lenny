// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// startSessionSlot is the §6.4 concurrent-workspace counterpart of
// StartSession: it claims one of the pod's slots rather than the whole
// pod. It ensures the slot's per-slot directory tree exists, registers
// the slot alongside any sibling slots, builds a runtime process scoped
// to the slot's cwd via RuntimeFactory, and starts it. Unlike the
// one-session-only base path, two distinct slot ids may be active at
// once; re-claiming an already-active slot id is rejected with
// Unavailable.
//
// spec: §6.4 lines 385-405; §5.2 concurrent mode.
func (s *Server) startSessionSlot(ctx context.Context, req *adapterv1.StartSessionRequest, slotID string) (*adapterv1.StartSessionResponse, error) {
	sessionID := req.GetSessionId().GetValue()
	if s.RuntimeFactory == nil {
		return nil, status.Error(codes.FailedPrecondition,
			"adapter is not configured for concurrent-workspace slots")
	}

	s.mu.Lock()
	st, err := s.ensureSlotStateLocked(slotID)
	if err != nil {
		s.mu.Unlock()
		return nil, status.Errorf(codes.InvalidArgument, "resolve slot %s: %v", slotID, err)
	}
	if st.sessionID != "" {
		s.mu.Unlock()
		return nil, status.Errorf(codes.Unavailable,
			"slot %s is not idle: session %s is already assigned", slotID, st.sessionID)
	}
	st.sessionID = sessionID
	paths := st.paths
	s.mu.Unlock()

	rt, err := s.RuntimeFactory(slotID, SlotRuntimePaths{
		Current:        paths.Current,
		Staging:        paths.Staging,
		Sessions:       paths.Sessions,
		Artifacts:      paths.Artifacts,
		CredentialsDir: paths.CredentialsDir,
	})
	if err != nil {
		s.releaseSlot(ctx, slotID)
		return nil, status.Errorf(codes.Internal, "build slot %s runtime: %v", slotID, err)
	}

	s.mu.Lock()
	st.runtime = rt
	s.mu.Unlock()

	if err := rt.Start(ctx, sessionID); err != nil {
		s.releaseSlot(ctx, slotID)
		return nil, status.Errorf(codes.Internal, "start slot %s runtime: %v", slotID, err)
	}
	return &adapterv1.StartSessionResponse{}, nil
}

// shutdownSlot tears down one §6.4 concurrent-workspace slot: it closes
// the slot's runtime, cancels its expiry timers, removes the slot's
// per-slot directory tree (the §6.4 "removes it during slot cleanup"
// responsibility), and deregisters the slot so a sibling slot is
// unaffected. spec: §6.4 lines 401-405.
func (s *Server) shutdownSlot(ctx context.Context, sessionID, slotID string, deadlineMs int32) (*adapterv1.ShutdownResponse, error) {
	s.mu.Lock()
	st, ok := s.slotStateLocked(slotID)
	if !ok {
		s.mu.Unlock()
		return nil, status.Errorf(codes.FailedPrecondition, "slot %s has no assigned session", slotID)
	}
	if st.sessionID != sessionID {
		s.mu.Unlock()
		return nil, status.Errorf(codes.NotFound,
			"session %s is not assigned to slot %s", sessionID, slotID)
	}
	rt := st.runtime
	for provider := range st.timers {
		s.cancelSlotExpiryTimerLocked(st, provider)
	}
	s.mu.Unlock()

	closeErr := error(nil)
	if rt != nil {
		closeCtx, cancel := contextWithGraceDeadline(ctx, time.Duration(deadlineMs)*time.Millisecond)
		closeErr = rt.Close(closeCtx, sessionID)
		cancel()
	}
	s.releaseSlot(ctx, slotID)
	return &adapterv1.ShutdownResponse{ExitedCleanly: closeErr == nil}, nil
}

// releaseSlot removes the slot's per-slot directory tree and deregisters
// it. It is best-effort on the filesystem removal: a removal error is
// logged by RemoveTree's caller but the slot is always deregistered so
// the registry does not leak. Callers must not hold s.mu.
func (s *Server) releaseSlot(_ context.Context, slotID string) {
	s.mu.Lock()
	st, ok := s.slots[slotID]
	if ok {
		delete(s.slots, slotID)
	}
	s.mu.Unlock()
	if ok {
		_ = removeSlotTree(st)
	}
}

// checkSlotSession validates an inbound RPC against the session recorded
// for the slot. It is the §6.4 per-slot counterpart of checkSession.
func (s *Server) checkSlotSession(sessionID, slotID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.slotStateLocked(slotID)
	if !ok {
		return status.Errorf(codes.FailedPrecondition, "slot %s has no assigned session", slotID)
	}
	if st.sessionID != sessionID {
		return status.Errorf(codes.NotFound,
			"session %s is not assigned to slot %s", sessionID, slotID)
	}
	return nil
}
