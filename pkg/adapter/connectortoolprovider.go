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
// without a live gateway. spec: §9.3 lines 142-164. F-9.1.2.
type ConnectorToolForwarder interface {
	ListSessionConnectors(ctx context.Context, sessionID string) ([]mcp.ConnectorRef, error)
	ListConnectorTools(ctx context.Context, sessionID, connectorID string) ([]mcp.Tool, error)
	CallConnectorTool(ctx context.Context, sessionID, connectorID, toolName string, arguments json.RawMessage) (json.RawMessage, error)
}

// connectorToolProvider adapts a ConnectorToolForwarder to the
// mcp.ToolProvider one intra-pod @lenny-connector-<id> MCP server
// consumes, binding every List/Call to the pod's single session and the
// one connector the socket serves. spec: §9.3 line 142. F-9.1.2.
type connectorToolProvider struct {
	forwarder   ConnectorToolForwarder
	sessionID   string
	connectorID string
}

func (p *connectorToolProvider) List(ctx context.Context) ([]mcp.Tool, error) {
	return p.forwarder.ListConnectorTools(ctx, p.sessionID, p.connectorID)
}

func (p *connectorToolProvider) Call(ctx context.Context, name string, arguments json.RawMessage) (json.RawMessage, error) {
	return p.forwarder.CallConnectorTool(ctx, p.sessionID, p.connectorID, name, arguments)
}
