// SPDX-License-Identifier: MIT

package adapterregistry

import "net/http"

// mcpPathPrefix is the §15.2 gateway-edge MCP path prefix the MCPAdapter
// owns on the shared mux.
const mcpPathPrefix = "/mcp"

// MCPAdapter is the §15.2 external protocol adapter for the gateway-edge
// MCP surface. It is the platform's primary streaming surface, so unlike
// the OpenAI Completions and Open Responses adapters (which embed the
// no-op BaseAdapter outbound default via SimpleAdapter) it MUST override
// OutboundCapabilities() with the explicit declaration from §15.2 — the
// mandatory override the spec calls out: inheriting the BaseAdapter no-op
// would leave the streaming transport empty and contradict the adapter's
// documented role.
//
// The HTTP surface (initialize / tools/list / tools/call, plus the
// Streamable HTTP and WebSocket transports) stays in pkg/gateway/mcp; the
// MCPAdapter wraps that handler and adds only the §15.0 lifecycle/outbound
// contract the registry dispatches through. spec: §15 line 1335.
type MCPAdapter struct {
	BaseAdapter
	handler http.Handler
}

// NewMCPAdapter wraps the gateway-edge MCP HTTP handler as the §15.2
// MCPAdapter. The capability declaration is fixed by the spec, so the
// constructor takes only the handler.
func NewMCPAdapter(handler http.Handler) *MCPAdapter {
	return &MCPAdapter{handler: handler}
}

// Name returns the registry identifier for the MCP adapter.
func (*MCPAdapter) Name() string { return "mcp" }

// HTTPHandler returns the wrapped gateway-edge MCP handler.
func (a *MCPAdapter) HTTPHandler() http.Handler { return a.handler }

// Capabilities returns the §15.2 MCP adapter discovery declaration. The
// MCP transport natively supports the hop-by-hop elicitation chain
// (§9.2), so SupportsElicitation is true; this also satisfies the §15.0
// capability-consistency invariant against the elicitation outbound kind
// declared below.
func (*MCPAdapter) Capabilities() Capabilities {
	return Capabilities{
		PathPrefix:                mcpPathPrefix,
		Protocol:                  "mcp",
		SupportsSessionContinuity: true,
		SupportsDelegation:        true,
		SupportsElicitation:       true,
		SupportsInterrupt:         true,
	}
}

// OutboundCapabilities returns the §15.2 mandatory MCPAdapter override:
// push-notifications on, every kind in the closed SessionEventKind enum
// supported, and unlimited concurrent OutboundChannel subscriptions (one
// per attached attach_session stream). spec: §15 lines 1335-1354.
func (*MCPAdapter) OutboundCapabilities() OutboundCapabilitySet {
	return OutboundCapabilitySet{
		PushNotifications:          true,
		SupportedEventKinds:        AllSessionEventKinds(),
		MaxConcurrentSubscriptions: 0, // unlimited; one OutboundChannel per attached attach_session stream
	}
}
