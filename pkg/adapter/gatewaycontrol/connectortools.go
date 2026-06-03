// SPDX-License-Identifier: MIT

package gatewaycontrol

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/lennylabs/lenny/pkg/adapter/mcp"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// ListSessionConnectors asks the gateway which §9.3 connectors sessionID's
// effective delegation policy permits. The adapter opens one intra-pod
// @lenny-connector-<id> MCP server per returned connector and lists them
// in the manifest connectorServers array. The gateway filters the list by
// the session's policy, so a connector the policy denies never reaches the
// pod. spec: §9.3 line 142. F-9.1.2.
func (c *Client) ListSessionConnectors(ctx context.Context, sessionID string) ([]mcp.ConnectorRef, error) {
	resp, err := c.rpc.ListSessionConnectors(ctx, &adapterv1.ListSessionConnectorsRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
	})
	if err != nil {
		return nil, fmt.Errorf("gatewaycontrol: ListSessionConnectors for session %s: %w", sessionID, err)
	}
	refs := make([]mcp.ConnectorRef, 0, len(resp.GetConnectors()))
	for _, conn := range resp.GetConnectors() {
		refs = append(refs, mcp.ConnectorRef{
			ID:          conn.GetId(),
			DisplayName: conn.GetDisplayName(),
		})
	}
	return refs, nil
}

// ListConnectorTools fetches the §9.3 tool catalog one connector exposes
// for the adapter's intra-pod per-connector MCP server to advertise on
// tools/list. The gateway dials the external endpoint as the MCP client
// with the gateway-held credential. The returned tools carry no handler
// (the intra-pod server forwards each tools/call via CallConnectorTool).
// spec: §9.3 lines 142-164. F-9.1.2.
func (c *Client) ListConnectorTools(ctx context.Context, sessionID, connectorID string) ([]mcp.Tool, error) {
	resp, err := c.rpc.ListConnectorTools(ctx, &adapterv1.ListConnectorToolsRequest{
		SessionId:   &adapterv1.SessionId{Value: sessionID},
		ConnectorId: connectorID,
	})
	if err != nil {
		return nil, fmt.Errorf("gatewaycontrol: ListConnectorTools %s for session %s: %w", connectorID, sessionID, err)
	}
	tools := make([]mcp.Tool, 0, len(resp.GetTools()))
	for _, t := range resp.GetTools() {
		tools = append(tools, mcp.Tool{
			Name:        t.GetName(),
			Description: t.GetDescription(),
			InputSchema: json.RawMessage(t.GetInputSchema()),
		})
	}
	return tools, nil
}

// CallConnectorTool forwards one §9.3 external tool call a type:agent
// runtime made against the intra-pod @lenny-connector-<id> socket to the
// gateway, scoped to sessionID and the connector. It returns the
// JSON-encoded §15.2 MCP tool result (a `content` array, optionally with
// `isError`); a tool-level failure is carried inside that result. A
// transport, routing, or policy failure (unknown session, denied
// connector) is returned as an error. spec: §9.3 lines 142-164. F-9.1.2.
func (c *Client) CallConnectorTool(ctx context.Context, sessionID, connectorID, toolName string, arguments json.RawMessage) (json.RawMessage, error) {
	resp, err := c.rpc.CallConnectorTool(ctx, &adapterv1.CallConnectorToolRequest{
		SessionId:   &adapterv1.SessionId{Value: sessionID},
		ConnectorId: connectorID,
		ToolName:    toolName,
		Arguments:   arguments,
	})
	if err != nil {
		return nil, fmt.Errorf("gatewaycontrol: CallConnectorTool %s/%s for session %s: %w", connectorID, toolName, sessionID, err)
	}
	return json.RawMessage(resp.GetResult()), nil
}
