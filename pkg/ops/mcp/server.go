// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/lennylabs/lenny/pkg/common/scopes"
	"github.com/lennylabs/lenny/pkg/ops/conventions"
)

// ProtocolVersion is the MCP protocol version the §25.12 management
// server speaks in the initialize handshake.
const ProtocolVersion = "2025-03-26"

// ServerName identifies the §25.12 management MCP server in the
// initialize handshake's serverInfo.
const ServerName = "lenny-management-mcp"

// JSON-RPC 2.0 / MCP error codes used by the §25.12 management server.
const (
	// errMethodNotFound is the JSON-RPC method-not-found code.
	errMethodNotFound = -32601
	// errInvalidParams is the JSON-RPC invalid-params code.
	errInvalidParams = -32602
	// errEndpointUnavailable is the §25.12 -32000 code for an underlying
	// endpoint that is unreachable.
	errEndpointUnavailable = -32000
	// errScopeForbidden is the §25.12 -32001 code for a tool invoked
	// outside the caller's scope claim.
	errScopeForbidden = -32001
)

// ToolResult is the outcome of a §25.12 tool invocation: the JSON
// payload the underlying endpoint returned, the HTTP status, and
// whether the response was a §25.2 dry-run preview.
type ToolResult struct {
	// Status is the HTTP status the underlying endpoint returned.
	Status int
	// Body is the raw JSON body the underlying endpoint returned.
	Body json.RawMessage
	// DryRun reports whether the response was a §25.2 dry-run preview
	// (the endpoint returned 200 with dryRun:true).
	DryRun bool
	// Preview is the §25.2 preview object when DryRun is true.
	Preview json.RawMessage
	// Unavailable reports that the underlying endpoint was unreachable;
	// §25.12 maps this to the -32000 ENDPOINT_UNAVAILABLE error.
	Unavailable bool
}

// Invoker dispatches a §25.12 tool call to its underlying endpoint —
// either a local lenny-ops handler or, via GatewayClient, a gateway
// admin-API endpoint. §25.12 makes the routing invisible to the MCP
// client. The opsserver wires an Invoker that routes to the in-process
// ops handlers.
type Invoker interface {
	// Invoke runs the tool with the given arguments and returns the
	// result. ctx is the context of the originating /mcp/management
	// request; it carries the caller's verified principal, scope claim,
	// and correlation values so an in-process replay is subject to the
	// §25.4 role gate and §25.1 scope gate against the real caller rather
	// than an anonymous one. tool is the resolved registry entry; args is
	// the raw arguments object from the tools/call request.
	//
	// spec: §25.12 — "Every MCP tool invocation that passes the scope
	// check is translated into a REST call ... That REST call passes
	// through the standard OIDC/JWT middleware and role-based
	// authorization check."
	Invoke(ctx context.Context, tool Tool, args json.RawMessage) (ToolResult, error)
}

// Server is the §25.12 MCP Management Server. It serves the management
// MCP server over HTTP JSON-RPC at /mcp/management: tool discovery
// (tools/list), schema inspection, and invocation (tools/call), with
// the §25.12 capability negotiation and security model.
type Server struct {
	registry *Registry
	invoker  Invoker
}

// NewServer returns a §25.12 management MCP server over the v1 tool
// registry. invoker dispatches tool calls to their underlying
// endpoints; a nil invoker reports every tool as unavailable.
func NewServer(invoker Invoker) *Server {
	return &Server{registry: NewRegistry(), invoker: invoker}
}

// Registry returns the server's §25.12 tool registry.
func (s *Server) Registry() *Registry { return s.registry }

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	Meta    json.RawMessage `json:"_meta,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int            `json:"code"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data,omitempty"`
}

// ServeHTTP serves one §25.12 MCP JSON-RPC request. The management
// server is HTTP-served, so each POST carries a single JSON-RPC request
// object and the response is the single JSON-RPC response object.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeRPC(w, rpcResponse{
			JSONRPC: "2.0",
			Error:   &rpcError{Code: errInvalidParams, Message: "malformed JSON-RPC request"},
		})
		return
	}
	resp := s.dispatch(r, req)
	// A JSON-RPC notification (no id) receives no response.
	if len(req.ID) == 0 || string(req.ID) == "null" {
		if resp.Error == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
	}
	resp.JSONRPC = "2.0"
	resp.ID = req.ID
	writeRPC(w, resp)
}

// dispatch routes a §25.12 MCP method to its handler.
func (s *Server) dispatch(r *http.Request, req rpcRequest) rpcResponse {
	switch req.Method {
	case "initialize":
		return rpcResponse{Result: s.handleInitialize(req)}
	case "notifications/initialized", "notifications/cancelled":
		// MCP lifecycle notifications: acknowledged, no result.
		return rpcResponse{}
	case "tools/list":
		return rpcResponse{Result: s.handleToolsList(req)}
	case "tools/call":
		return s.handleToolsCall(r, req)
	default:
		return rpcResponse{Error: &rpcError{
			Code: errMethodNotFound, Message: "unknown method: " + req.Method,
		}}
	}
}

// handleInitialize answers the §25.12 MCP initialize handshake. It
// reports the protocol version, the management server's serverInfo, and
// the tools capability.
func (s *Server) handleInitialize(rpcRequest) map[string]any {
	return map[string]any{
		"protocolVersion": ProtocolVersion,
		"serverInfo":      map[string]any{"name": ServerName, "version": "v1"},
		"capabilities":    map[string]any{"tools": map[string]any{}},
	}
}

// handleToolsList answers the §25.12 tools/list method. It filters the
// inventory by the caller's declared capabilities; §25.12 keeps this a
// UX convenience, so it does not enforce scope as a security boundary.
func (s *Server) handleToolsList(req rpcRequest) map[string]any {
	caps := capabilitiesFromParams(req.Params)
	tools := s.registry.FilterForList(caps, nil)
	list := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		list = append(list, toolDescriptor(t))
	}
	return map[string]any{"tools": list}
}

// toolDescriptor renders a §25.12 tool for the tools/list response,
// carrying the JSON Schema input and the x-lenny-* extension metadata.
func toolDescriptor(t Tool) map[string]any {
	return map[string]any{
		"name":                    t.Name,
		"description":             t.Description,
		"inputSchema":             t.InputSchema,
		"x-lenny-category":        t.Category,
		"x-lenny-required-role":   t.RequiredRole,
		"x-lenny-scope":           t.Scope,
		"x-lenny-dry-run-support": t.DryRunSupport,
	}
}

// toolsCallParams is the §25.12 tools/call request params.
type toolsCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// handleToolsCall answers the §25.12 tools/call method: it resolves the
// tool, enforces the scope layer, dispatches through the Invoker, and
// maps the result — including the §25.2 dry-run preview — to the MCP
// content envelope.
func (s *Server) handleToolsCall(r *http.Request, req rpcRequest) rpcResponse {
	var p toolsCallParams
	if err := json.Unmarshal(req.Params, &p); err != nil || p.Name == "" {
		return rpcResponse{Error: &rpcError{
			Code: errInvalidParams, Message: "tools/call requires a tool name",
		}}
	}
	tool, ok := s.registry.Lookup(p.Name)
	if !ok {
		return rpcResponse{Error: &rpcError{
			Code: errInvalidParams, Message: "unknown tool: " + p.Name,
		}}
	}
	// §25.12 scope enforcement (the narrowing layer): a caller whose
	// scope claim does not include the tool's x-lenny-scope receives
	// -32001 SCOPE_FORBIDDEN before any REST call is issued.
	if set, scoped := requestScopes(r); scoped && !set.Matches(tool.Scope) {
		return rpcResponse{Error: &rpcError{
			Code:    errScopeForbidden,
			Message: "tool " + tool.Name + " is outside the caller's scope claim",
			Data: map[string]any{
				"code": "SCOPE_FORBIDDEN",
				// §25.2 error category: a scope-claim rejection is an
				// authorization failure (spec: §25.2 lines 302-329).
				"category":      string(conventions.CategoryAuth),
				"retryable":     false,
				"requiredScope": tool.Scope,
				"activeScope":   set.Raw,
			},
		}}
	}
	if s.invoker == nil {
		return endpointUnavailable(tool)
	}
	result, err := s.invoker.Invoke(r.Context(), tool, p.Arguments)
	if err != nil {
		return rpcResponse{Error: &rpcError{
			Code: errEndpointUnavailable, Message: err.Error(),
			Data: map[string]any{"code": "ENDPOINT_UNAVAILABLE", "category": string(conventions.CategoryTransient), "retryable": true},
		}}
	}
	if result.Unavailable {
		return endpointUnavailable(tool)
	}
	return rpcResponse{Result: toolCallResult(result)}
}

// toolCallResult maps a §25.12 ToolResult to the MCP tools/call result
// envelope. §25.12 dry-run mapping: a dry-run preview is isError:false
// with the canonical _meta.lenny.dryRun flag set, so an MCP client can
// distinguish a preview from an applied mutation.
func toolCallResult(res ToolResult) map[string]any {
	out := map[string]any{
		"isError": res.Status >= 400,
		"content": []map[string]any{{"type": "text", "text": string(res.Body)}},
	}
	if res.DryRun {
		// §25.12: a dry-run is a successful preview, not a failure.
		out["isError"] = false
		meta := map[string]any{"lenny.dryRun": true}
		if len(res.Preview) > 0 {
			var preview any
			if json.Unmarshal(res.Preview, &preview) == nil {
				meta["lenny.preview"] = preview
			}
		}
		out["_meta"] = meta
	}
	return out
}

// endpointUnavailable builds the §25.12 -32000 ENDPOINT_UNAVAILABLE
// response for a tool whose underlying endpoint is unreachable. §25.12
// keeps the tool in tools/list during the outage — only the call fails.
func endpointUnavailable(tool Tool) rpcResponse {
	return rpcResponse{Error: &rpcError{
		Code:    errEndpointUnavailable,
		Message: "the endpoint backing " + tool.Name + " is unavailable",
		Data:    map[string]any{"code": "ENDPOINT_UNAVAILABLE", "category": string(conventions.CategoryTransient), "retryable": true},
	}}
}

// ScopeHeader is the §25.1 / §25.12 fallback transport for the
// caller's scope claim when the JWT middleware is not yet in front of
// the management server (e.g. local development, tests). It carries
// the same space-separated value the RFC 9068 JWT claim would.
const ScopeHeader = "X-Lenny-Scope"

type scopeCtxKey struct{}

// WithScopes attaches the parsed §25.1 scope Set to ctx. The admin-
// API middleware calls WithScopes after it validates a Bearer JWT so
// downstream MCP requests inherit the authenticated scope claim from
// the typed Principal — no header round-trip required.
func WithScopes(ctx context.Context, s scopes.Set) context.Context {
	return context.WithValue(ctx, scopeCtxKey{}, s)
}

// scopesFromContext returns the §25.1 scope Set the middleware
// attached, plus a flag reporting whether one was present.
func scopesFromContext(ctx context.Context) (scopes.Set, bool) {
	s, ok := ctx.Value(scopeCtxKey{}).(scopes.Set)
	return s, ok
}

// requestScopes returns the §25.12 caller scope set, preferring the
// validated JWT claim (attached by WithScopes) and falling back to the
// X-Lenny-Scope header for the local-development path. The returned
// flag reports whether a scope claim was supplied; absent → no
// narrowing per §25.1 ("the token's role ceiling applies unmodified").
func requestScopes(r *http.Request) (set scopes.Set, present bool) {
	if s, ok := scopesFromContext(r.Context()); ok {
		return s, s.Present()
	}
	raw := r.Header.Get(ScopeHeader)
	if raw == "" {
		return scopes.Set{}, false
	}
	parsed, err := scopes.Parse(raw)
	if err != nil {
		// A malformed header is treated as a maximally restrictive
		// claim so a typo cannot accidentally elevate the caller; the
		// §25.12 dispatch loop then returns SCOPE_FORBIDDEN.
		return scopes.Set{Raw: raw}, true
	}
	return parsed, parsed.Present()
}

// capabilitiesFromParams decodes the §25.12 initialize/tools-list
// capability declaration from the request params.
func capabilitiesFromParams(params json.RawMessage) Capabilities {
	var p struct {
		Capabilities struct {
			ReadOnly       bool   `json:"readOnly"`
			NonDestructive bool   `json:"nonDestructive"`
			Scope          string `json:"scope"`
		} `json:"capabilities"`
		ClientInfo struct {
			Capabilities struct {
				ReadOnly       bool   `json:"readOnly"`
				NonDestructive bool   `json:"nonDestructive"`
				Scope          string `json:"scope"`
			} `json:"capabilities"`
		} `json:"clientInfo"`
	}
	if len(params) > 0 {
		_ = json.Unmarshal(params, &p)
	}
	caps := Capabilities{
		ReadOnly:       p.Capabilities.ReadOnly,
		NonDestructive: p.Capabilities.NonDestructive,
		Scope:          p.Capabilities.Scope,
	}
	// §25.12 declares capabilities under clientInfo.capabilities during
	// the initialize handshake; honor that form too.
	if ci := p.ClientInfo.Capabilities; ci.ReadOnly || ci.NonDestructive || ci.Scope != "" {
		caps = Capabilities{ReadOnly: ci.ReadOnly, NonDestructive: ci.NonDestructive, Scope: ci.Scope}
	}
	return caps
}

// writeRPC writes a JSON-RPC response.
func writeRPC(w http.ResponseWriter, resp rpcResponse) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
