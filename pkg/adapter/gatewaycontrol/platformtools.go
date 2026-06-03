// SPDX-License-Identifier: MIT

package gatewaycontrol

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/lennylabs/lenny/pkg/adapter/mcp"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// ListPlatformTools fetches the §9.1 platform tool catalog the gateway
// advertises for sessionID. The adapter's intra-pod platform MCP server
// returns this catalog on tools/list, so a type:agent runtime that dials
// @lenny-platform-mcp and calls tools/list sees the same tools the
// gateway-edge /mcp surface exposes. The returned tools carry no handler
// (the intra-pod server forwards each tools/call to the gateway via
// CallPlatformTool). spec: §9.1 lines 14-31. F-9.1.1.
func (c *Client) ListPlatformTools(ctx context.Context, sessionID string) ([]mcp.Tool, error) {
	resp, err := c.rpc.ListPlatformTools(ctx, &adapterv1.ListPlatformToolsRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
	})
	if err != nil {
		return nil, fmt.Errorf("gatewaycontrol: ListPlatformTools for session %s: %w", sessionID, err)
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

// CallPlatformTool forwards one §9.1 platform tool call a type:agent
// runtime made against the intra-pod @lenny-platform-mcp socket to the
// gateway, scoped to sessionID. It returns the JSON-encoded §15.2 MCP
// tool result (a `content` array, optionally with `isError`); a
// tool-level failure is carried inside that result. A transport or
// routing failure (unknown session, unregistered tool) is returned as an
// error. spec: §9.1 line 14. F-9.1.1.
func (c *Client) CallPlatformTool(ctx context.Context, sessionID, toolName string, arguments json.RawMessage) (json.RawMessage, error) {
	resp, err := c.rpc.CallPlatformTool(ctx, &adapterv1.CallPlatformToolRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
		ToolName:  toolName,
		Arguments: arguments,
	})
	if err != nil {
		return nil, fmt.Errorf("gatewaycontrol: CallPlatformTool %s for session %s: %w", toolName, sessionID, err)
	}
	return json.RawMessage(resp.GetResult()), nil
}
