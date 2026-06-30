// SPDX-License-Identifier: MIT

package leasecontrol

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// ErrPlatformToolNotFound is the sentinel a PlatformToolService returns
// when a CallPlatformTool names a tool the gateway platform surface does
// not register. The §9.1 forwarding handler maps it to codes.NotFound so
// the runtime's MCP client reports an unknown tool rather than a
// transport fault. spec: §9.1 line 14. F-9.1.1.
var ErrPlatformToolNotFound = errors.New("leasecontrol: platform tool not found")

// PlatformToolDescriptor is one entry in the §9.1 platform tool catalog
// the gateway advertises to a pod's intra-pod platform MCP server: the
// tool name (e.g. `lenny/delegate_task`), its description, and the
// JSON-Schema for its arguments encoded as JSON bytes. F-9.1.1.
type PlatformToolDescriptor struct {
	Name        string
	Description string
	InputSchema []byte
}

// PlatformToolService is the gateway platform tool surface the §9.1
// GatewayControl forwarding RPCs reach. A pod's adapter forwards a
// type:agent runtime's tools/list and tools/call against the intra-pod
// @lenny-platform-mcp socket here, scoped to the calling session, so the
// intra-pod and gateway-edge (/mcp) platform tool surfaces dispatch
// through the same handlers. The gateway wires a bridge over the
// platform tool *mcp.Server and the session store; tests pass a fake.
//
// spec: §9.1 lines 14-31; §4.7 line 942. F-9.1.1.
type PlatformToolService interface {
	// ListPlatformTools returns the platform tool catalog visible to
	// sessionID. ErrSessionNotFound is returned for an unknown session.
	ListPlatformTools(ctx context.Context, sessionID string) ([]PlatformToolDescriptor, error)

	// CallPlatformTool dispatches one platform tool call on behalf of
	// sessionID and returns the JSON-encoded §15.2 MCP tool result plus
	// its isError flag. A tool-level failure is an isError result with a
	// nil error; ErrSessionNotFound (unknown session) and
	// ErrPlatformToolNotFound (unregistered tool) signal a routing
	// failure the handler maps to a gRPC status.
	CallPlatformTool(ctx context.Context, sessionID, toolName string, arguments []byte) (result []byte, isError bool, err error)
}

// ListPlatformTools is the §9.1 GatewayControl RPC: a pod's adapter asks
// the gateway for the platform tool catalog its intra-pod platform MCP
// server advertises on tools/list. The gateway returns the same catalog
// the gateway-edge /mcp surface serves, so the intra-pod server never
// duplicates tool schemas. spec: §9.1 lines 14-31. F-9.1.1.
func (s *Service) ListPlatformTools(ctx context.Context, req *adapterv1.ListPlatformToolsRequest) (*adapterv1.ListPlatformToolsResponse, error) {
	if s.platformTools == nil {
		return nil, status.Error(codes.Unimplemented, "leasecontrol: platform tool forwarding is not configured on this gateway")
	}
	sessionID := req.GetSessionId().GetValue()
	if sessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "leasecontrol: ListPlatformTools request carries no session id")
	}
	tools, err := s.platformTools.ListPlatformTools(ctx, sessionID)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			return nil, status.Errorf(codes.NotFound, "leasecontrol: ListPlatformTools for unknown session %s", sessionID)
		}
		return nil, status.Errorf(codes.Internal, "leasecontrol: list platform tools for session %s: %v", sessionID, err)
	}
	out := &adapterv1.ListPlatformToolsResponse{Tools: make([]*adapterv1.PlatformTool, 0, len(tools))}
	for _, t := range tools {
		out.Tools = append(out.Tools, &adapterv1.PlatformTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}
	return out, nil
}

// CallPlatformTool is the §9.1 GatewayControl RPC: a pod's adapter
// forwards one platform tool call a type:agent runtime made against the
// intra-pod @lenny-platform-mcp socket. The gateway dispatches it under
// the calling session's principal against the same platform tool surface
// the gateway-edge /mcp endpoint serves. A tool-level failure is carried
// as an is_error result (the same isError ToolResult the runtime would
// receive over /mcp); only a routing failure returns a gRPC status.
//
// The adapter is the trusted, mesh-authenticated (§10.3) infrastructure
// for its pod's session, so the gateway dispatches under the session id
// the adapter presents. spec: §9.1 line 14; §4.7 line 942. F-9.1.1.
func (s *Service) CallPlatformTool(ctx context.Context, req *adapterv1.CallPlatformToolRequest) (*adapterv1.CallPlatformToolResponse, error) {
	if s.platformTools == nil {
		return nil, status.Error(codes.Unimplemented, "leasecontrol: platform tool forwarding is not configured on this gateway")
	}
	sessionID := req.GetSessionId().GetValue()
	if sessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "leasecontrol: CallPlatformTool request carries no session id")
	}
	if req.GetToolName() == "" {
		return nil, status.Error(codes.InvalidArgument, "leasecontrol: CallPlatformTool request carries no tool name")
	}
	result, isErr, err := s.platformTools.CallPlatformTool(ctx, sessionID, req.GetToolName(), req.GetArguments())
	if err != nil {
		switch {
		case errors.Is(err, ErrSessionNotFound):
			return nil, status.Errorf(codes.NotFound, "leasecontrol: CallPlatformTool for unknown session %s", sessionID)
		case errors.Is(err, ErrPlatformToolNotFound):
			return nil, status.Errorf(codes.NotFound, "leasecontrol: unknown platform tool %s", req.GetToolName())
		default:
			return nil, status.Errorf(codes.Internal, "leasecontrol: dispatch platform tool %s for session %s: %v", req.GetToolName(), sessionID, err)
		}
	}
	return &adapterv1.CallPlatformToolResponse{Result: result, IsError: isErr}, nil
}
