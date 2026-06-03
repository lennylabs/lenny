// SPDX-License-Identifier: MIT

package adapter

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// CapabilityPreConnect is the §4.7 / §6.1 capability token an SDK-warm
// adapter advertises during version negotiation. The gateway uses it to
// drive the pod through ConfigureWorkspace rather than StartSession.
const CapabilityPreConnect = "preConnect"

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
	// Return the pod to pod-warm: drop any tentatively configured session
	// and stop the platform MCP so a subsequent StartSession starts fresh.
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
