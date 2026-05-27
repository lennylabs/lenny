// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter/workspace"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// setupOptionsFromProto converts the §5.1 setupPolicy message into the
// workspace.SetupOptions bounding the setup phase. A nil policy yields
// a near-zero SetupOptions — no aggregate cap, shell mode (legacy
// `/bin/sh -c`). on_timeout "warn" proceeds past the cap; any other
// value, including empty, is the conservative "fail" default. Env is
// seeded with the §7.5 line 479 minimal whitelist (DefaultSetupEnv) so
// a setup command does not see the adapter's process environment.
// Shell mirrors the §7.5 line 490 setupCommandPolicy.shell flag the
// gateway sets per runtime (true→`/bin/sh -c`, false→exec argv). spec:
// §7.5 line 490 — F-7.5.2 / F-7.5.8.
func setupOptionsFromProto(p *adapterv1.SetupPolicy, workdir string) workspace.SetupOptions {
	opts := workspace.SetupOptions{
		Env:   workspace.DefaultSetupEnv(workdir),
		Shell: true,
	}
	if p == nil {
		return opts
	}
	opts.AggregateTimeout = time.Duration(p.GetTimeoutSeconds()) * time.Second
	opts.FailOnAggregateTimeout = p.GetOnTimeout() != "warn"
	opts.Shell = p.GetShell()
	return opts
}

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

// StartSession assigns a session to the pod and starts the runtime
// (§4.7, §6.1). It is the final RPC of the §4.7 session assignment
// sequence: the workspace is already materialized by FinalizeWorkspace
// and setup is already run by RunSetup, so StartSession claims the pod,
// writes the §15.4 adapter manifest, and starts the runtime process. It
// rejects the call with Unavailable when the pod already holds a
// session. A session-mode pod is one-session-only: the pod is
// terminated and replaced after the session ends rather than reused.
//
// On any failure after the session is tentatively claimed, the pod is
// returned to the idle state so a retry can land on a fresh pod.
func (s *Server) StartSession(ctx context.Context, req *adapterv1.StartSessionRequest) (*adapterv1.StartSessionResponse, error) {
	sessionID := req.GetSessionId().GetValue()
	if sessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "StartSession requires a session id")
	}
	if s.Runtime == nil {
		return nil, status.Error(codes.FailedPrecondition,
			"adapter is not configured with a runtime")
	}

	if err := s.claimSession(sessionID); err != nil {
		return nil, err
	}

	// §15.4: write the adapter manifest the runtime reads at startup.
	nonce, err := s.writeSessionManifest(manifestInputs{
		sessionID:          sessionID,
		taskID:             req.GetTaskId(),
		experimentContext:  req.GetExperimentContext(),
		tracingContext:     req.GetTracingContext(),
		agentInterface:     req.GetAgentInterface(),
		minPlatformVersion: req.GetMinPlatformVersion(),
	})
	if err != nil {
		s.releaseSession()
		return nil, status.Errorf(codes.Internal, "write adapter manifest: %v", err)
	}
	// §4.7: start the platform MCP server the runtime connects to. A
	// type: mcp runtime is "oblivious to Lenny" (§5.1) and never connects
	// to the platform MCP server, so the adapter does not start one for
	// it — the adapter drives the type: mcp agent's own MCP server as a
	// client instead.
	if s.RuntimeKind != RuntimeKindMCP {
		if err := s.startPlatformMCP(nonce); err != nil {
			s.releaseSession()
			return nil, status.Errorf(codes.Internal, "start platform MCP server: %v", err)
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
	// §4.7 lines 661-662: flush a final usage report onto the gateway
	// control stream before the stream closes, so the gateway can run
	// budget_return.lua (§8.3) with the session's complete token totals.
	s.emitFinalUsage(ctx, sessionID)
	closeErr := s.Runtime.Close(ctx, sessionID)
	s.releaseSession()
	return &adapterv1.ShutdownResponse{ExitedCleanly: closeErr == nil}, nil
}

// emitFinalUsage reads the session's accumulated usage and pushes a
// FINAL_USAGE_REPORT control event. It is best-effort: a nil usage meter
// or a read error leaves the gateway to fall back to stream-close, which
// the §8.3 contract already tolerates.
func (s *Server) emitFinalUsage(ctx context.Context, sessionID string) {
	if s.Usage == nil {
		return
	}
	u, err := s.Usage.Usage(ctx, sessionID)
	if err != nil {
		return
	}
	s.EmitFinalUsageReport(u)
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

// releaseSession returns the pod to the idle state and stops the
// session's platform MCP server, if one was started.
//
// credSessionID is INTENTIONALLY left set: the §6.1 lines 5/16/24
// invariant ("After a session completes or fails in `executionMode:
// session`, the pod is terminated and replaced — never recycled for a
// different session") is primarily enforced by the gateway-side
// teardown loop (binder.Release → drain → terminated → replaced); the
// sticky credSessionID is the adapter-side defense-in-depth that
// rejects an AssignCredentials for a different session if a pod
// somehow survives termination. Clearing it here would weaken that
// backstop. F-6.1.12.
func (s *Server) releaseSession() {
	s.mu.Lock()
	s.sessionID = ""
	cancel := s.mcpCancel
	s.mcpCancel = nil
	// §4.9 line 1149: drop the direct-mode expiry timers so a stale lease
	// cannot fire AUTH_EXPIRED against a session that has already ended.
	s.cancelAllExpiryTimers()
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
