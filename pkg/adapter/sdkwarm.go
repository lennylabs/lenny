// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"os"
	"strconv"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// CapabilityPreConnect is the §4.7 / §6.1 capability token an SDK-warm
// adapter advertises during version negotiation. The gateway uses it to
// drive the pod through ConfigureWorkspace rather than StartSession.
const CapabilityPreConnect = "preConnect"

// DefaultDemoteTimeout is the §6.1 line 67 default bound (5s) the adapter
// applies to its SIGTERM-time DemoteSDK teardown before it force-terminates
// the SDK process. The deployer overrides it via LENNY_DEMOTE_TIMEOUT_SECONDS.
// spec: §6.1 line 67.
const DefaultDemoteTimeout = 5 * time.Second

// demoteTimeoutEnvVar is the §6.1 line 67 environment variable that bounds
// the SIGTERM-time DemoteSDK teardown.
const demoteTimeoutEnvVar = "LENNY_DEMOTE_TIMEOUT_SECONDS"

// ForceTerminator is the optional capability an SDKWarmRuntime implements so
// the adapter can hard-stop the SDK process when the §6.1 line 67 bounded
// DemoteSDK teardown overruns its timeout. A runtime that does not implement
// it relies on adapter-process exit to reap the SDK. spec: §6.1 line 67.
type ForceTerminator interface {
	// ForceTerminate hard-stops the SDK process immediately, without waiting
	// for a graceful teardown. The adapter calls it only after a DemoteSDK
	// teardown has exceeded its bounded timeout, so an abandoned SDK process
	// cannot leak credentials or hold provider connections open.
	ForceTerminate()
}

// DemoteTimeoutFromEnv resolves the §6.1 line 67 bounded DemoteSDK timeout
// from LENNY_DEMOTE_TIMEOUT_SECONDS, falling back to DefaultDemoteTimeout
// when the variable is unset or not a positive integer. spec: §6.1 line 67.
func DemoteTimeoutFromEnv() time.Duration {
	v := os.Getenv(demoteTimeoutEnvVar)
	if v == "" {
		return DefaultDemoteTimeout
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return DefaultDemoteTimeout
	}
	return time.Duration(n) * time.Second
}

// ShutdownDemoteSDK runs the §6.1 line 67 SIGTERM-during-sdk_connecting
// teardown. For an SDK-warm pod whose SDK has been pre-connected it calls
// DemoteSDK bounded by timeout so the in-progress SDK connection is torn
// down cleanly; if DemoteSDK does not return within the bound it
// force-terminates the SDK process so it is not abandoned mid-connection and
// cannot leak credentials or hold provider connections open. It is a no-op
// for a pod-warm pod (no SDK to tear down) or an SDK-warm pod that never
// pre-connected, so a SIGTERM handler may call it unconditionally before
// GracefulStop. A non-positive timeout falls back to DefaultDemoteTimeout.
// spec: §6.1 line 67.
func (s *Server) ShutdownDemoteSDK(timeout time.Duration) {
	sw, ok := s.sdkWarmRuntime()
	if !ok {
		return // pod-warm: there is no pre-connected SDK to tear down.
	}
	s.mu.Lock()
	connected := s.sdkConnected
	s.mu.Unlock()
	if !connected {
		return // SDK-warm but never pre-connected or already demoted.
	}
	if timeout <= 0 {
		timeout = DefaultDemoteTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		// DemoteSDK clears sdkConnected and releases the session on success.
		_, _ = s.DemoteSDK(ctx, &adapterv1.DemoteSDKRequest{})
	}()
	select {
	case <-done:
		// Clean teardown (or a runtime error); either way the SDK is no
		// longer mid-connection.
		return
	case <-ctx.Done():
		// §6.1 line 67 step 2 — the bounded DemoteSDK overran; force-terminate
		// the SDK process so it is not abandoned mid-connection. The DemoteSDK
		// goroutine is left to exit as the adapter process terminates.
		if ft, ok := sw.(ForceTerminator); ok {
			ft.ForceTerminate()
		}
		s.mu.Lock()
		s.sdkConnected = false
		s.mu.Unlock()
	}
}

// SDKWarmRuntime is implemented by a RuntimeProcess that supports the
// §6.1 SDK-warm fast path. Such a runtime declares
// capabilities.preConnect: true and pre-connects its agent SDK at warm
// time, before session assignment. Rather than starting the runtime fresh
// (StartSession), the adapter points the already-connected SDK at the
// finalized workspace with ConfigureWorkspace, and tears it down with
// DemoteSDK when the gateway falls back to pod-warm. A RuntimeProcess that
// does not implement this interface is pod-warm only, and the adapter's
// ConfigureWorkspace / DemoteSDK RPCs return Unimplemented per the §4.7
// contract for non-preConnect pods.
type SDKWarmRuntime interface {
	RuntimeProcess
	// PreConnect starts the agent SDK at warm time, before any session is
	// assigned, leaving it connected and waiting for its first prompt
	// (§6.1 line 30). The adapter calls it once during startup for a
	// preConnect runtime. It MUST be idempotent: a repeat call while the
	// SDK is already connected is a no-op success.
	PreConnect(ctx context.Context) error
	// ConfigureWorkspace points the pre-connected SDK session at cwd and
	// binds it to sessionID. It MUST be idempotent: a repeat call with the
	// same cwd is a no-op success (§4.7, the gateway bounds it at 10s).
	ConfigureWorkspace(ctx context.Context, sessionID, cwd string) error
	// DemoteSDK tears down the pre-connected SDK process so the pod falls
	// back to pod-warm materialization (§4.7, the gateway bounds it at 5s).
	// After it returns, a StartSession starts a fresh runtime.
	DemoteSDK(ctx context.Context) error
}

// sdkWarmRuntime returns the runtime as an SDKWarmRuntime when it supports
// the §4.7 SDK-warm fast path, reporting false for a pod-warm runtime.
func (s *Server) sdkWarmRuntime() (SDKWarmRuntime, bool) {
	sw, ok := s.Runtime.(SDKWarmRuntime)
	return sw, ok
}

// PreConnect runs the §6.1 line 30 SDK-warm pre-connect: for a preConnect
// runtime it starts the agent SDK at warm time so a later
// ConfigureWorkspace points the already-connected SDK at the workspace
// rather than starting a runtime from cold. It is a no-op success for a
// pod-warm runtime (there is no SDK to pre-connect) and idempotent for an
// already-connected SDK. The adapter calls it once during startup; on
// failure the pod stays pod-warm and the gateway uses StartSession.
func (s *Server) PreConnect(ctx context.Context) error {
	sw, ok := s.sdkWarmRuntime()
	if !ok {
		return nil
	}
	s.mu.Lock()
	already := s.sdkConnected
	s.mu.Unlock()
	if already {
		return nil
	}
	if err := sw.PreConnect(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	s.sdkConnected = true
	s.mu.Unlock()
	return nil
}

// SDKWarmReady reports whether this pod is SDK-warm and its SDK has
// completed pre-connection. It is false for a pod-warm runtime (which has
// no SDK to pre-connect) and false for an SDK-warm pod whose PreConnect has
// not yet completed or that was demoted. A readiness probe consumes it so
// the §6.1 readiness gate can hold the pod un-claimable until the SDK is
// connected.
func (s *Server) SDKWarmReady() bool {
	if _, ok := s.sdkWarmRuntime(); !ok {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sdkConnected
}

// ConfigureWorkspace is the §4.7 SDK-warm counterpart of StartSession: it
// points a pre-connected SDK session at the finalized working directory
// instead of starting a runtime fresh. It applies only to runtimes that
// declare capabilities.preConnect: true; a pod-warm adapter returns
// Unimplemented.
//
// The call is idempotent (spec §4.7): a repeat for the same session
// re-points the runtime at cwd without rewriting the manifest or
// restarting the platform MCP server, so the §15.4.3 nonce the runtime
// already authenticated with stays valid. A different session on an
// already-claimed pod is Unavailable. A runtime that cannot accept the
// workspace is reported as a gRPC error so the gateway runs the DemoteSDK
// fallback.
func (s *Server) ConfigureWorkspace(ctx context.Context, req *adapterv1.ConfigureWorkspaceRequest) (*adapterv1.ConfigureWorkspaceResponse, error) {
	sessionID := req.GetSessionId().GetValue()
	if sessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "ConfigureWorkspace requires a session id")
	}
	cwd := req.GetCwd()
	if cwd == "" {
		return nil, status.Error(codes.InvalidArgument, "ConfigureWorkspace requires a cwd")
	}
	if s.Runtime == nil {
		return nil, status.Error(codes.FailedPrecondition, "adapter is not configured with a runtime")
	}
	sw, ok := s.sdkWarmRuntime()
	if !ok {
		return nil, status.Error(codes.Unimplemented,
			"ConfigureWorkspace applies only to SDK-warm pods that declare capabilities.preConnect: true; this is a pod-warm adapter")
	}

	fresh, err := s.claimSessionForConfigure(sessionID)
	if err != nil {
		return nil, err
	}
	if fresh {
		// §9.3 line 142: resolve the session's permitted connectors so the
		// pre-connected runtime gets one per-connector MCP server per
		// connector its policy admits. Best-effort.
		connectors := s.sessionConnectors(ctx, sessionID)
		// §15.4: write the adapter manifest the pre-connected runtime
		// re-reads when pointed at the workspace, and start the platform
		// MCP server keyed on the freshly written nonce.
		nonce, err := s.writeSessionManifest(manifestInputs{
			sessionID:         sessionID,
			experimentContext: req.GetExperimentContext(),
			tracingContext:    req.GetTracingContext(),
			connectors:        connectors,
		})
		if err != nil {
			s.releaseSession()
			return nil, status.Errorf(codes.Internal, "write adapter manifest: %v", err)
		}
		if s.RuntimeKind != RuntimeKindMCP {
			if err := s.startPlatformMCP(nonce); err != nil {
				s.releaseSession()
				return nil, status.Errorf(codes.Internal, "start platform MCP server: %v", err)
			}
			// §9.3 lines 142-164: open the per-connector MCP servers. F-9.1.2.
			s.startConnectorMCPServers(sessionID, nonce, connectors)
		}
	}

	if err := sw.ConfigureWorkspace(ctx, sessionID, cwd); err != nil {
		if fresh {
			s.releaseSession()
		}
		return nil, status.Errorf(codes.Internal, "configure SDK-warm workspace: %v", err)
	}
	return &adapterv1.ConfigureWorkspaceResponse{}, nil
}

// DemoteSDK is the §4.7 SDK-warm demotion RPC. The gateway calls it to
// tear down a pre-connected SDK session and return the pod to pod-warm
// state, either proactively (the incoming WorkspacePlan matches the
// runtime's sdkWarmBlockingPaths) or as the ConfigureWorkspace failure
// fallback. It applies only to pods whose runtime declares
// capabilities.preConnect: true; a pod-warm adapter returns Unimplemented
// as the §4.7 contract specifies. After it returns, the pod is idle and a
// StartSession starts a fresh runtime.
func (s *Server) DemoteSDK(ctx context.Context, _ *adapterv1.DemoteSDKRequest) (*adapterv1.DemoteSDKResponse, error) {
	sw, ok := s.sdkWarmRuntime()
	if !ok {
		return nil, status.Error(codes.Unimplemented,
			"DemoteSDK applies only to SDK-warm pods that declare capabilities.preConnect: true; this is a pod-warm adapter")
	}
	if err := sw.DemoteSDK(ctx); err != nil {
		return nil, status.Errorf(codes.Internal, "demote SDK: %v", err)
	}
	// Return the pod to pod-warm: the SDK is no longer connected, so clear
	// the warm-readiness flag, drop any tentatively configured session, and
	// stop the platform MCP so a subsequent StartSession starts fresh.
	s.mu.Lock()
	s.sdkConnected = false
	s.mu.Unlock()
	s.releaseSession()
	return &adapterv1.DemoteSDKResponse{Demoted: true}, nil
}

// claimSessionForConfigure claims the pod for sessionID on the SDK-warm
// path. It reports fresh=true on the first claim and fresh=false on an
// idempotent repeat for the same session (§4.7 ConfigureWorkspace
// idempotency). A different session on an already-claimed pod is
// Unavailable, matching StartSession.
func (s *Server) claimSessionForConfigure(sessionID string) (fresh bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessionID == sessionID {
		return false, nil
	}
	if s.sessionID != "" {
		return false, status.Errorf(codes.Unavailable,
			"pod is not idle: session %s is already assigned", s.sessionID)
	}
	s.sessionID = sessionID
	return true, nil
}
