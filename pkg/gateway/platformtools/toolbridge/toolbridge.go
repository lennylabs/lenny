// SPDX-License-Identifier: MIT

// Package toolbridge bridges the §9.1 GatewayControl platform tool
// forwarding RPCs to the gateway platform tool surface.
//
// A type:agent runtime reaches the §9.1 platform tools (lenny/delegate_task,
// lenny/await_children, ...) by dialing its pod's intra-pod platform MCP
// server (the @lenny-platform-mcp socket). The adapter forwards each
// tools/list and tools/call to the gateway over
// GatewayControl.{ListPlatformTools,CallPlatformTool}; this bridge is the
// gateway-side dispatch:
//
//   - tools/list returns the same catalog the gateway-edge /mcp surface
//     advertises, so the intra-pod server never duplicates tool schemas.
//   - tools/call has the session core attach the calling session's
//     principal, which those §9.1 tool handlers read off the request
//     context, through GWSEAM-AttachCallingSession, and dispatches against
//     the same *mcp.Server the gateway-edge /mcp endpoint serves.
//
// The adapter is the mesh-authenticated (§10.3) infrastructure for its
// pod's one session, so the bridge dispatches under the session id the
// adapter presents. The intra-pod socket's SO_PEERCRED / nonce check
// (§4.7) keeps a compromised peer process out, so once
// this bridge is wired the §4.7 boundary ("a compromised child
// process cannot call privileged platform tools") is enforceable.
//
// spec: §9.1; §4.7. F-9.1.1.
package toolbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/lennylabs/lenny/pkg/gateway/platformtools/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
)

// Dispatcher is the gateway platform tool surface the bridge reaches:
// the same *mcp.Server the gateway-edge /mcp endpoint serves. *mcp.Server
// satisfies it via Catalog and DispatchTool.
type Dispatcher interface {
	// Catalog returns the registered tool descriptors in registration
	// order.
	Catalog() []mcp.Tool
	// DispatchTool runs the named tool's handler (and the §4.8 result
	// interceptor) and returns the result. ok is false when no tool with
	// name is registered.
	DispatchTool(ctx context.Context, name string, arguments json.RawMessage) (mcp.ToolResult, bool, error)
}

// Bridge implements leasecontrol.PlatformToolService over a Dispatcher
// and the session core's calling session operation.
type Bridge struct {
	tools    Dispatcher
	sessions seam.CallingSessions
}

// New returns a Bridge. Both dependencies are required.
func New(tools Dispatcher, sessions seam.CallingSessions) *Bridge {
	return &Bridge{tools: tools, sessions: sessions}
}

var _ leasecontrol.PlatformToolService = (*Bridge)(nil)

// ListPlatformTools returns the gateway platform tool catalog. The
// catalog is uniform across type:agent sessions (the §10.6
// mcpRuntimeFilters scope delegation *targets* at discovery, not the
// platform tools themselves), so the session id is not consulted here;
// it is accepted for parity with CallPlatformTool and future per-session
// scoping. spec: §9.1. F-9.1.1.
func (b *Bridge) ListPlatformTools(_ context.Context, _ string) ([]leasecontrol.PlatformToolDescriptor, error) {
	catalog := b.tools.Catalog()
	out := make([]leasecontrol.PlatformToolDescriptor, 0, len(catalog))
	for _, t := range catalog {
		out = append(out, leasecontrol.PlatformToolDescriptor{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: append([]byte(nil), t.InputSchema...),
		})
	}
	return out, nil
}

// CallPlatformTool dispatches one platform tool call on behalf of
// sessionID. The session core attaches the calling session's principal,
// which the §9.1 tool handlers read off the context, and the bridge runs
// the tool against the gateway platform tool surface. The
// JSON-encoded §15.2 MCP tool result is returned with its isError flag;
// a tool-level failure is an isError result with a nil error. An unknown
// session maps to leasecontrol.ErrSessionNotFound and an unregistered
// tool to leasecontrol.ErrPlatformToolNotFound so the GatewayControl
// handler can return the right gRPC status. spec: §9.1. F-9.1.1.
func (b *Bridge) CallPlatformTool(ctx context.Context, sessionID, toolName string, arguments []byte) ([]byte, bool, error) {
	ctx, err := b.sessions.AttachCallingSession(ctx, sessionID)
	if errors.Is(err, seam.ErrSessionNotFound) {
		return nil, false, leasecontrol.ErrSessionNotFound
	}
	if err != nil {
		return nil, false, fmt.Errorf("toolbridge: %w", err)
	}

	result, ok, err := b.tools.DispatchTool(ctx, toolName, json.RawMessage(arguments))
	if err != nil {
		return nil, false, fmt.Errorf("toolbridge: dispatch %s: %w", toolName, err)
	}
	if !ok {
		return nil, false, leasecontrol.ErrPlatformToolNotFound
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, false, fmt.Errorf("toolbridge: encode result of %s: %w", toolName, err)
	}
	return encoded, result.IsError, nil
}
