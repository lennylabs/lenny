// SPDX-License-Identifier: MIT

package adapter

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter/workspace"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// RuntimeProcess manages the pod's runtime process. The §4.7 adapter
// starts it at session start, forwards message envelopes to it,
// signals it on interrupt, and closes it at session teardown.
type RuntimeProcess interface {
	// Start spawns the runtime process for the session.
	Start(ctx context.Context, sessionID string) error
	// WriteEnvelope forwards a pre-encoded message envelope to the
	// runtime's stdin.
	WriteEnvelope(sessionID string, envelope []byte) error
	// Output streams the runtime's output envelopes. Each value is one
	// §15.4.1 JSONL frame the runtime wrote to stdout. The channel is
	// closed when the runtime's output ends; the context bounds the
	// reader so a stalled consumer does not leak it.
	Output(ctx context.Context, sessionID string) (<-chan []byte, error)
	// Interrupt signals the runtime process. A hard interrupt sends
	// SIGKILL; a clean interrupt sends SIGTERM so the runtime can pause
	// or checkpoint within the gateway's grace deadline.
	Interrupt(ctx context.Context, sessionID string, hard bool) error
	// Close tears the runtime process down.
	Close(ctx context.Context, sessionID string) error
}

// StartSession assigns a session to the pod (§4.7, §6.1). It rejects
// the call with Unavailable when the pod already holds a session,
// materializes the workspace from the request's WorkspacePlan, runs
// the plan's setup commands, and starts the runtime process. A
// session-mode pod is one-session-only: the pod is terminated and
// replaced after the session ends rather than reused.
//
// On any failure after the session is tentatively claimed, the pod is
// returned to the idle state so a retry can land on a fresh pod.
func (s *Server) StartSession(ctx context.Context, req *adapterv1.StartSessionRequest) (*adapterv1.StartSessionResponse, error) {
	sessionID := req.GetSessionId().GetValue()
	if sessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "StartSession requires a session id")
	}
	if s.WorkspaceRoot == "" || s.Runtime == nil {
		return nil, status.Error(codes.FailedPrecondition,
			"adapter is not configured with a workspace root and runtime")
	}

	if err := s.claimSession(sessionID); err != nil {
		return nil, err
	}

	plan := req.GetWorkspacePlan()
	if err := workspace.Materialize(s.WorkspaceRoot, plan.GetSources()); err != nil {
		s.releaseSession()
		return nil, status.Errorf(codes.InvalidArgument, "materialize workspace: %v", err)
	}
	// §5.1 setupPolicy aggregate cap: the manifest does not yet carry
	// the runtime's setupPolicy, so no aggregate cap applies. Only the
	// per-command timeouts bound the setup phase until that wiring lands.
	if err := workspace.RunSetup(ctx, s.WorkspaceRoot, plan.GetSetupCommands(), workspace.SetupOptions{}); err != nil {
		s.releaseSession()
		return nil, status.Errorf(codes.FailedPrecondition, "run setup commands: %v", err)
	}
	// §15.4: write the adapter manifest the runtime reads at startup,
	// carrying the §8.3 experimentContext. Skipped when no ManifestDir
	// is configured.
	if s.ManifestDir != "" {
		if err := WriteManifest(s.ManifestDir, Manifest{
			Version:           ManifestVersion,
			SessionID:         sessionID,
			WorkspaceRoot:     s.WorkspaceRoot,
			ExperimentContext: manifestExperimentContext(req.GetExperimentContext()),
			TracingContext:    req.GetTracingContext(),
		}); err != nil {
			s.releaseSession()
			return nil, status.Errorf(codes.Internal, "write adapter manifest: %v", err)
		}
	}
	if err := s.Runtime.Start(ctx, sessionID); err != nil {
		s.releaseSession()
		return nil, status.Errorf(codes.Internal, "start runtime: %v", err)
	}
	return &adapterv1.StartSessionResponse{}, nil
}

// SendMessage delivers a content message to the pod's runtime (§4.7).
// The request carries a §15.4.1 message envelope already encoded by
// the gateway; the adapter writes it verbatim to the runtime's stdin.
// The runtime's response is surfaced asynchronously, so SendMessage
// returns once the envelope is delivered.
func (s *Server) SendMessage(_ context.Context, req *adapterv1.SendMessageRequest) (*adapterv1.SendMessageResponse, error) {
	sessionID := req.GetSessionId().GetValue()
	if sessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "SendMessage requires a session id")
	}
	if len(req.GetEnvelopeJson()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "SendMessage requires a message envelope")
	}
	if err := s.checkSession(sessionID); err != nil {
		return nil, err
	}
	if err := s.Runtime.WriteEnvelope(sessionID, req.GetEnvelopeJson()); err != nil {
		return nil, status.Errorf(codes.Internal, "deliver message to runtime: %v", err)
	}
	return &adapterv1.SendMessageResponse{}, nil
}

// Shutdown terminates the pod's runtime and releases the session
// (§4.7). It closes the runtime process and returns the pod toward
// termination; a session-mode pod is replaced rather than reused.
func (s *Server) Shutdown(ctx context.Context, req *adapterv1.ShutdownRequest) (*adapterv1.ShutdownResponse, error) {
	sessionID := req.GetSessionId().GetValue()
	if sessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "Shutdown requires a session id")
	}
	if err := s.checkSession(sessionID); err != nil {
		return nil, err
	}
	closeErr := s.Runtime.Close(ctx, sessionID)
	s.releaseSession()
	return &adapterv1.ShutdownResponse{ExitedCleanly: closeErr == nil}, nil
}

// checkSession confirms sessionID is the session currently assigned to
// the pod.
func (s *Server) checkSession(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessionID == "" {
		return status.Error(codes.FailedPrecondition, "pod has no assigned session")
	}
	if s.sessionID != sessionID {
		return status.Errorf(codes.NotFound, "session %s is not assigned to this pod", sessionID)
	}
	return nil
}

// claimSession marks the pod as holding sessionID, returning a gRPC
// Unavailable error when the pod is not idle. The §4.7 StartSession
// contract specifies Unavailable for a pod that is not idle.
func (s *Server) claimSession(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessionID != "" {
		return status.Errorf(codes.Unavailable,
			"pod is not idle: session %s is already assigned", s.sessionID)
	}
	s.sessionID = sessionID
	return nil
}

// releaseSession returns the pod to the idle state.
func (s *Server) releaseSession() {
	s.mu.Lock()
	s.sessionID = ""
	s.mu.Unlock()
}
