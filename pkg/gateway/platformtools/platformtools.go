// SPDX-License-Identifier: MIT

// Package platformtools bridges the §9.1 GatewayControl platform tool
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
//   - tools/call resolves the calling session's tenant and owner, builds
//     the authenticated principal those §9.1 tool handlers read off the
//     request context, and dispatches against the same *mcp.Server the
//     gateway-edge /mcp endpoint serves.
//
// The adapter is the mesh-authenticated (§10.3) infrastructure for its
// pod's one session, so the bridge dispatches under the session id the
// adapter presents. The intra-pod socket's SO_PEERCRED / nonce check
// (§4.7 lines 879-883) keeps a compromised peer process out, so once
// this bridge is wired the §4.7 line 942 boundary ("a compromised child
// process cannot call privileged platform tools") is enforceable.
//
// spec: §9.1 lines 8-31; §4.7 line 942. F-9.1.1.
package platformtools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/lennylabs/lenny/pkg/gateway/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/mcp"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
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

// SessionResolver resolves a session id to its row tenant-agnostically.
// *sessionstore via GetByID satisfies it. The bridge needs the
// tenant-agnostic lookup because the adapter forwards only the session
// id; the session's own tenant scopes the dispatch.
type SessionResolver interface {
	GetByID(ctx context.Context, id string) (sessionstore.Session, error)
}

// Bridge implements leasecontrol.PlatformToolService over a Dispatcher
// and a SessionResolver.
type Bridge struct {
	tools    Dispatcher
	sessions SessionResolver
}

// New returns a Bridge. Both dependencies are required.
func New(tools Dispatcher, sessions SessionResolver) *Bridge {
	return &Bridge{tools: tools, sessions: sessions}
}

var _ leasecontrol.PlatformToolService = (*Bridge)(nil)

// ListPlatformTools returns the gateway platform tool catalog. The
// catalog is uniform across type:agent sessions (the §10.6
// mcpRuntimeFilters scope delegation *targets* at discovery, not the
// platform tools themselves), so the session id is not consulted here;
// it is accepted for parity with CallPlatformTool and future per-session
// scoping. spec: §9.1 lines 14-31. F-9.1.1.
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
// sessionID. It resolves the session's tenant and owner, installs the
// authenticated principal the §9.1 tool handlers read off the context,
// and runs the tool against the gateway platform tool surface. The
// JSON-encoded §15.2 MCP tool result is returned with its isError flag;
// a tool-level failure is an isError result with a nil error. An unknown
// session maps to leasecontrol.ErrSessionNotFound and an unregistered
// tool to leasecontrol.ErrPlatformToolNotFound so the GatewayControl
// handler can return the right gRPC status. spec: §9.1 line 14. F-9.1.1.
func (b *Bridge) CallPlatformTool(ctx context.Context, sessionID, toolName string, arguments []byte) ([]byte, bool, error) {
	sess, err := b.sessions.GetByID(ctx, sessionID)
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			return nil, false, leasecontrol.ErrSessionNotFound
		}
		return nil, false, fmt.Errorf("platformtools: resolve session %s: %w", sessionID, err)
	}

	// spec: §9.1 line 14 — dispatch under the calling session's principal
	// so the platform tool handlers resolve the same (session, tenant,
	// owner) they would for a gateway-edge /mcp call. CallerType marks the
	// in-pod agent origin.
	ctx = authmw.WithPrincipal(ctx, authmw.Principal{
		Subject:    sess.UserID,
		TenantID:   sess.TenantID,
		SessionID:  sessionID,
		CallerType: "agent",
	})

	result, ok, err := b.tools.DispatchTool(ctx, toolName, json.RawMessage(arguments))
	if err != nil {
		return nil, false, fmt.Errorf("platformtools: dispatch %s: %w", toolName, err)
	}
	if !ok {
		return nil, false, leasecontrol.ErrPlatformToolNotFound
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, false, fmt.Errorf("platformtools: encode result of %s: %w", toolName, err)
	}
	return encoded, result.IsError, nil
}
