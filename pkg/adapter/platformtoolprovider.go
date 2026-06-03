// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"encoding/json"

	"github.com/lennylabs/lenny/pkg/adapter/mcp"
)

// PlatformToolForwarder forwards the §9.1 platform tool surface to the
// gateway on behalf of a session: ListPlatformTools fetches the catalog
// the intra-pod platform MCP server advertises on tools/list, and
// CallPlatformTool dispatches one tools/call. *gatewaycontrol.Client
// satisfies it; the interface is the seam that keeps startPlatformMCP
// testable without a live gateway. spec: §9.1 lines 14-31. F-9.1.1.
type PlatformToolForwarder interface {
	ListPlatformTools(ctx context.Context, sessionID string) ([]mcp.Tool, error)
	CallPlatformTool(ctx context.Context, sessionID, toolName string, arguments json.RawMessage) (json.RawMessage, error)
}

// platformToolProvider adapts a PlatformToolForwarder to the
// mcp.ToolProvider the intra-pod platform MCP server consumes, binding
// every List/Call to the pod's single session. spec: §9.1 line 14.
// F-9.1.1.
type platformToolProvider struct {
	forwarder PlatformToolForwarder
	sessionID string
}

func (p *platformToolProvider) List(ctx context.Context) ([]mcp.Tool, error) {
	return p.forwarder.ListPlatformTools(ctx, p.sessionID)
}

func (p *platformToolProvider) Call(ctx context.Context, name string, arguments json.RawMessage) (json.RawMessage, error) {
	return p.forwarder.CallPlatformTool(ctx, p.sessionID, name, arguments)
}
