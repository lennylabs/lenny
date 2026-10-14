// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// activityUntappedPrefix opens the status message of a SealSession refusal
// for a runtime whose output the adapter does not read. The gateway reads
// the prefix as a refusal and moves its idle clock to the current time.
const activityUntappedPrefix = "activity_untapped: "

// SealSession implements the §4.7.1 SealSession RPC: it seals the named
// session against message delivery when the adapter observed no activity
// for it in the requested window (§4.7.1 **Session seal.**). In one hold of
// s.mu it decides, in the order that block lists:
//
//   - a session whose session_start the adapter has not written is refused
//     with FailedPrecondition;
//   - every session is refused with FailedPrecondition and a message
//     beginning activity_untapped: when the adapter reads no runtime frame;
//   - a session with a platform or connector call in flight answers not
//     sealed with an idle age of zero;
//   - a session whose last activity is within the window answers not
//     sealed with the time since that activity;
//   - otherwise the session is sealed, or keeps its seal, and the answer is
//     sealed.
//
// A not-sealed answer leaves a seal placed earlier in place. The handler
// takes no operation lock and writes nothing to the runtime.
//
// spec: §4.7.1 (Role and Gateway RPC Contract), §28.5.1 (CH-ATTACH)
func (s *Server) SealSession(_ context.Context, req *adapterv1.SealSessionRequest) (*adapterv1.SealSessionResponse, error) {
	sessionID := req.GetSessionId().GetValue()
	st, err := s.sealTarget("SealSession", sessionID, req.GetCoordinationGeneration())
	if err != nil {
		return nil, err
	}
	if req.GetIdleSeconds() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "SealSession requires a positive idle_seconds")
	}
	window := time.Duration(req.GetIdleSeconds()) * time.Second

	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, ok := s.slotStateLocked(sessionID); !ok || cur != st {
		return nil, status.Errorf(codes.FailedPrecondition, "session %s is not assigned to this pod", sessionID)
	}
	if !st.sessionStartWritten {
		return nil, envelopeWriteStatus("seal session", errSessionNotOpened)
	}
	if !s.activityTapped {
		return nil, status.Errorf(codes.FailedPrecondition,
			"%sthe adapter does not read the runtime's output, so it observes no activity for session %s", activityUntappedPrefix, sessionID)
	}
	if st.toolCalls > 0 {
		return &adapterv1.SealSessionResponse{Sealed: false, IdleAgeSeconds: 0}, nil
	}
	if age := s.now().Sub(st.lastActivity); age < window {
		return &adapterv1.SealSessionResponse{Sealed: false, IdleAgeSeconds: int64(age / time.Second)}, nil
	}
	st.sealed = true
	return &adapterv1.SealSessionResponse{Sealed: true}, nil
}

// UnsealSession implements the §4.7.1 UnsealSession RPC: it lifts the named
// session's seal. It takes no operation lock and writes nothing to the
// runtime.
//
// spec: §4.7.1 (Role and Gateway RPC Contract), §28.5.1 (CH-ATTACH)
func (s *Server) UnsealSession(_ context.Context, req *adapterv1.UnsealSessionRequest) (*adapterv1.UnsealSessionResponse, error) {
	sessionID := req.GetSessionId().GetValue()
	st, err := s.sealTarget("UnsealSession", sessionID, req.GetCoordinationGeneration())
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, ok := s.slotStateLocked(sessionID); !ok || cur != st {
		return nil, status.Errorf(codes.FailedPrecondition, "session %s is not assigned to this pod", sessionID)
	}
	st.sealed = false
	return &adapterv1.UnsealSessionResponse{}, nil
}

// sealTarget resolves the registry entry a seal RPC acts on. It refuses a
// request with no session ID with InvalidArgument, a session the registry
// does not bind with the bound-session guard's error, and a request whose
// coordination generation differs from the generation the adapter last
// fenced for the session with FailedPrecondition and
// coordinator_handoff_stale, as CheckpointBarrier does. An entry no
// coordinator has fenced passes the generation check.
// spec: §4.7.1 (Role and Gateway RPC Contract), §10.1.2 (Coordinator
// Handoff Protocol)
func (s *Server) sealTarget(rpc, sessionID string, gen int64) (*slotState, error) {
	if sessionID == "" {
		return nil, status.Errorf(codes.InvalidArgument, "%s requires a session id", rpc)
	}
	st, err := s.boundSlotState(sessionID)
	if err != nil {
		return nil, err
	}
	st.coord.mu.Lock()
	fenced := st.coord.lastFenced
	initialized := st.coord.initialized
	st.coord.mu.Unlock()
	if initialized && gen != fenced {
		return nil, status.Errorf(codes.FailedPrecondition,
			"coordinator_handoff_stale: %s generation %d does not match last fenced %d", rpc, gen, fenced)
	}
	return st, nil
}
