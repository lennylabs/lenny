// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"encoding/json"

	"github.com/lennylabs/lenny/pkg/adapter/mcp"
)

// ConnectorToolForwarder forwards the §9.3 per-connector tool surface to
// the gateway on behalf of a session: ListSessionConnectors resolves the
// connectors the session's effective delegation policy permits (so the
// adapter knows which intra-pod @lenny-connector-<id> servers to open),
// ListConnectorTools fetches one connector's catalog for tools/list, and
// CallConnectorTool dispatches one tools/call. *gatewaycontrol.Client
// satisfies it; the interface keeps the connector-MCP startup testable
// without a live gateway. spec: §9.3. F-9.1.2.
type ConnectorToolForwarder interface {
	ListSessionConnectors(ctx context.Context, sessionID string) ([]mcp.ConnectorRef, error)
	ListConnectorTools(ctx context.Context, sessionID, connectorID string) ([]mcp.Tool, error)
	CallConnectorTool(ctx context.Context, sessionID, connectorID, toolName string, arguments json.RawMessage) (json.RawMessage, error)
}

// connectorToolProvider adapts a ConnectorToolForwarder to the
// mcp.ToolProvider one intra-pod @lenny-connector-<id> MCP server
// consumes, binding every List/Call to the one connector the socket
// serves. The socket is pod-wide, so the calling session is resolved at
// call time on the same terms as the platform provider rather than
// captured at construction. spec: §9.3; §15.4.3. F-9.1.2.
type connectorToolProvider struct {
	forwarder   ConnectorToolForwarder
	server      *Server
	connectorID string
}

func (p *connectorToolProvider) List(ctx context.Context) ([]mcp.Tool, error) {
	sessionID, err := p.server.callingSession()
	if err != nil {
		return nil, err
	}
	return p.forwarder.ListConnectorTools(ctx, sessionID, p.connectorID)
}

func (p *connectorToolProvider) Call(ctx context.Context, name string, arguments json.RawMessage) (json.RawMessage, error) {
	sessionID, err := p.server.callingSession()
	if err != nil {
		return nil, err
	}
	return p.forwarder.CallConnectorTool(ctx, sessionID, p.connectorID, name, arguments)
}
