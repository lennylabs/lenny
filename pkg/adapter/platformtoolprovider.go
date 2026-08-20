// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"encoding/json"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter/mcp"
)

// PlatformToolForwarder forwards the §9.1 platform tool surface to the
// gateway on behalf of a session: ListPlatformTools fetches the catalog
// the intra-pod platform MCP server advertises on tools/list, and
// CallPlatformTool dispatches one tools/call. *gatewaycontrol.Client
// satisfies it; the interface is the seam that keeps startPlatformMCP
// testable without a live gateway. spec: §9.1. F-9.1.1.
type PlatformToolForwarder interface {
	ListPlatformTools(ctx context.Context, sessionID string) ([]mcp.Tool, error)
	CallPlatformTool(ctx context.Context, sessionID, toolName string, arguments json.RawMessage) (json.RawMessage, error)
}

// platformToolProvider adapts a PlatformToolForwarder to the
// mcp.ToolProvider the intra-pod platform MCP server consumes.
//
// The server binds one pod-wide socket and the one runtime process that
// dials it serves every slot, so the provider cannot carry a session
// captured at construction: the gateway installs the forwarded session's
// user and tenant as the authenticated principal for the call, and a pod
// holding more than one session would execute a co-tenant's tool calls
// under the first session's user. It resolves the calling session at call
// time instead, and refuses when the resolution cannot name one.
//
// The check runs on each List and Call rather than at server start or at
// connection accept, because a connection opened while the pod was
// sole-occupied outlives that state.
//
// spec: §9.1; §15.4.3. F-9.1.1.
type platformToolProvider struct {
	forwarder PlatformToolForwarder
	server    *Server
}

func (p *platformToolProvider) List(ctx context.Context) ([]mcp.Tool, error) {
	sessionID, err := p.server.callingSession()
	if err != nil {
		return nil, err
	}
	return p.forwarder.ListPlatformTools(ctx, sessionID)
}

func (p *platformToolProvider) Call(ctx context.Context, name string, arguments json.RawMessage) (json.RawMessage, error) {
	sessionID, err := p.server.callingSession()
	if err != nil {
		return nil, err
	}
	return p.forwarder.CallPlatformTool(ctx, sessionID, name, arguments)
}

// callingSession names the session an intra-pod MCP call may be forwarded
// under. It is soleSession, refused with FailedPrecondition when empty: a
// non-empty result is the statement that the pod's one shared runtime
// process has been given no session but this one since it was last
// serving none, which is the condition under which a forwarded identifier
// cannot name a session other than the caller. spec: §9.1; §15.4.3.
func (s *Server) callingSession() (string, error) {
	sessionID := s.soleSession()
	if sessionID == "" {
		return "", status.Error(codes.FailedPrecondition,
			"the pod's shared runtime process is serving more than one session; the intra-pod MCP surface cannot name the calling session")
	}
	return sessionID, nil
}
