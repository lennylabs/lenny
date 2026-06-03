// SPDX-License-Identifier: MIT

package leasecontrol

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// ErrConnectorNotPermitted is the sentinel a ConnectorToolService returns
// when a ListConnectorTools or CallConnectorTool names a connector the
// calling session's §8.3 effective delegation policy does not permit. The
// §9.3 forwarding handlers map it to codes.PermissionDenied so the
// runtime's MCP client reports a policy denial rather than a transport
// fault. spec: §9.3 line 164. F-9.1.2.
var ErrConnectorNotPermitted = errors.New("leasecontrol: connector is not permitted for session")

// SessionConnectorDescriptor is one §9.3 connector the calling session's
// effective delegation policy permits: the registry id (e.g. `github`)
// the adapter derives the intra-pod socket name from, and the
// human-facing display name. F-9.1.2.
type SessionConnectorDescriptor struct {
	ID          string
	DisplayName string
}

// ConnectorToolService is the gateway connector-invocation surface the
// §9.3 GatewayControl forwarding RPCs reach. A pod's adapter resolves the
// session's permitted connectors, opens one intra-pod
// @lenny-connector-<id> MCP server per connector, and forwards each
// tools/list and tools/call here, scoped to the calling session. The
// gateway acts as the MCP client to the external endpoint with the
// gateway-held credential, so the credential never transits the pod.
//
// spec: §9.3 lines 142-164. F-9.1.2.
type ConnectorToolService interface {
	// ListSessionConnectors returns the connectors sessionID's effective
	// delegation policy permits. ErrSessionNotFound is returned for an
	// unknown session.
	ListSessionConnectors(ctx context.Context, sessionID string) ([]SessionConnectorDescriptor, error)

	// ListConnectorTools returns the tool catalog the named connector
	// exposes, scoped to the calling session. ErrSessionNotFound (unknown
	// session) and ErrConnectorNotPermitted (policy denial) signal a
	// routing failure the handler maps to a gRPC status.
	ListConnectorTools(ctx context.Context, sessionID, connectorID string) ([]PlatformToolDescriptor, error)

	// CallConnectorTool forwards one external tool call on behalf of
	// sessionID and returns the JSON-encoded §15.2 MCP tool result plus
	// its isError flag. A tool-level failure is an isError result with a
	// nil error; ErrSessionNotFound and ErrConnectorNotPermitted signal a
	// routing failure the handler maps to a gRPC status.
	CallConnectorTool(ctx context.Context, sessionID, connectorID, toolName string, arguments []byte) (result []byte, isError bool, err error)
}

// ListSessionConnectors is the §9.3 GatewayControl RPC: a pod's adapter
// asks the gateway which connectors the calling session's effective
// delegation policy permits. The adapter opens one intra-pod
// @lenny-connector-<id> MCP server per returned connector and lists them
// in the manifest connectorServers array. spec: §9.3 line 142. F-9.1.2.
func (s *Service) ListSessionConnectors(ctx context.Context, req *adapterv1.ListSessionConnectorsRequest) (*adapterv1.ListSessionConnectorsResponse, error) {
	if s.connectorTools == nil {
		return nil, status.Error(codes.Unimplemented, "leasecontrol: connector tool forwarding is not configured on this gateway")
	}
	sessionID := req.GetSessionId().GetValue()
	if sessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "leasecontrol: ListSessionConnectors request carries no session id")
	}
	conns, err := s.connectorTools.ListSessionConnectors(ctx, sessionID)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			return nil, status.Errorf(codes.NotFound, "leasecontrol: ListSessionConnectors for unknown session %s", sessionID)
		}
		return nil, status.Errorf(codes.Internal, "leasecontrol: list session connectors for session %s: %v", sessionID, err)
	}
	out := &adapterv1.ListSessionConnectorsResponse{Connectors: make([]*adapterv1.SessionConnector, 0, len(conns))}
	for _, c := range conns {
		out.Connectors = append(out.Connectors, &adapterv1.SessionConnector{
			Id:          c.ID,
			DisplayName: c.DisplayName,
		})
	}
	return out, nil
}

// ListConnectorTools is the §9.3 GatewayControl RPC: a pod's adapter asks
// the gateway for the tool catalog one connector exposes, for the
// adapter's intra-pod per-connector MCP server to advertise on
// tools/list. The gateway dials the external endpoint as the MCP client
// using the gateway-held credential. spec: §9.3 lines 142-164. F-9.1.2.
func (s *Service) ListConnectorTools(ctx context.Context, req *adapterv1.ListConnectorToolsRequest) (*adapterv1.ListConnectorToolsResponse, error) {
	if s.connectorTools == nil {
		return nil, status.Error(codes.Unimplemented, "leasecontrol: connector tool forwarding is not configured on this gateway")
	}
	sessionID := req.GetSessionId().GetValue()
	if sessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "leasecontrol: ListConnectorTools request carries no session id")
	}
	if req.GetConnectorId() == "" {
		return nil, status.Error(codes.InvalidArgument, "leasecontrol: ListConnectorTools request carries no connector id")
	}
	tools, err := s.connectorTools.ListConnectorTools(ctx, sessionID, req.GetConnectorId())
	if err != nil {
		switch {
		case errors.Is(err, ErrSessionNotFound):
			return nil, status.Errorf(codes.NotFound, "leasecontrol: ListConnectorTools for unknown session %s", sessionID)
		case errors.Is(err, ErrConnectorNotPermitted):
			return nil, status.Errorf(codes.PermissionDenied, "leasecontrol: connector %s not permitted for session %s", req.GetConnectorId(), sessionID)
		default:
			return nil, status.Errorf(codes.Internal, "leasecontrol: list connector %s tools for session %s: %v", req.GetConnectorId(), sessionID, err)
		}
	}
	out := &adapterv1.ListConnectorToolsResponse{Tools: make([]*adapterv1.PlatformTool, 0, len(tools))}
	for _, t := range tools {
		out.Tools = append(out.Tools, &adapterv1.PlatformTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}
	return out, nil
}

// CallConnectorTool is the §9.3 GatewayControl RPC: a pod's adapter
// forwards one external tool call a type:agent runtime made against the
// intra-pod @lenny-connector-<id> socket. The gateway validates the
// connector_id against the calling session's effective delegation policy
// (§9.3 line 164) and proxies the tools/call to the external MCP endpoint
// with the gateway-held credential. A tool-level failure is carried as an
// is_error result (the same isError ToolResult the runtime would receive
// over the local socket); only a routing or policy failure returns a gRPC
// status. spec: §9.3 lines 142-164. F-9.1.2.
func (s *Service) CallConnectorTool(ctx context.Context, req *adapterv1.CallConnectorToolRequest) (*adapterv1.CallConnectorToolResponse, error) {
	if s.connectorTools == nil {
		return nil, status.Error(codes.Unimplemented, "leasecontrol: connector tool forwarding is not configured on this gateway")
	}
	sessionID := req.GetSessionId().GetValue()
	if sessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "leasecontrol: CallConnectorTool request carries no session id")
	}
	if req.GetConnectorId() == "" {
		return nil, status.Error(codes.InvalidArgument, "leasecontrol: CallConnectorTool request carries no connector id")
	}
	if req.GetToolName() == "" {
		return nil, status.Error(codes.InvalidArgument, "leasecontrol: CallConnectorTool request carries no tool name")
	}
	result, isErr, err := s.connectorTools.CallConnectorTool(ctx, sessionID, req.GetConnectorId(), req.GetToolName(), req.GetArguments())
	if err != nil {
		switch {
		case errors.Is(err, ErrSessionNotFound):
			return nil, status.Errorf(codes.NotFound, "leasecontrol: CallConnectorTool for unknown session %s", sessionID)
		case errors.Is(err, ErrConnectorNotPermitted):
			return nil, status.Errorf(codes.PermissionDenied, "leasecontrol: connector %s not permitted for session %s", req.GetConnectorId(), sessionID)
		default:
			return nil, status.Errorf(codes.Internal, "leasecontrol: dispatch connector %s tool %s for session %s: %v", req.GetConnectorId(), req.GetToolName(), sessionID, err)
		}
	}
	return &adapterv1.CallConnectorToolResponse{Result: result, IsError: isErr}, nil
}
