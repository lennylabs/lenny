// SPDX-License-Identifier: MIT

package opsserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/lennylabs/lenny/pkg/observability/correlation"
	"github.com/lennylabs/lenny/pkg/ops/gateway"
	"github.com/lennylabs/lenny/pkg/ops/mcp"
)

// mcpInvoker returns the §25.12 Invoker that routes an MCP management
// tool call to its underlying endpoint. §25.12 mandates transparent dual
// routing: an ops-owned tool dispatches to a local lenny-ops handler and
// a gateway-owned tool proxies to the gateway admin API through
// GatewayClient. opsOwned is the routing predicate (the ops-owned tool
// names, built from the drift-guarded RouteSchemas() membership); gw is
// the identity-forwarding gateway proxy for the gateway-owned tools, nil
// in a local-only dev posture (every gateway-owned tool then reports
// ENDPOINT_UNAVAILABLE).
//
// spec: §25.12 — "Tool invocations are routed transparently to either
// lenny-ops handlers ... or Gateway admin API via GatewayClient."
func (s *Server) mcpInvoker(opsOwned map[string]bool, gw *gateway.Client) mcp.Invoker {
	return &opsInvoker{server: s, opsOwned: opsOwned, gateway: gw}
}

// opsOwnedToolNames builds the §25.12 ops-vs-gateway routing predicate:
// the set of tool names lenny-ops owns and dispatches locally. It is the
// non-empty MCPTool values of the drift-guarded RouteSchemas() (the
// lenny_* operability tools plus admin.me and admin.me_authorized_tools).
// Every other tool in the generated inventory is gateway-owned and
// proxies to the gateway admin API. Deriving the set from RouteSchemas()
// keeps it bound to the single ownership source TestRouteSchemasMatchLiveMux
// guards, so no second drift guard is needed.
//
// spec: §25.12 (transparent dual routing; ops-vs-gateway routing predicate).
func opsOwnedToolNames() map[string]bool {
	names := make(map[string]bool)
	for _, rs := range RouteSchemas() {
		if rs.MCPTool != "" {
			names[rs.MCPTool] = true
		}
	}
	return names
}

// opsInvoker is the §25.12 Invoker implementing transparent dual routing.
// An ops-owned tool (a member of opsOwned) is replayed locally against
// the lenny-ops mux; a gateway-owned tool is proxied to the gateway admin
// API with the caller identity forwarded.
type opsInvoker struct {
	server *Server
	// opsOwned is the ops-vs-gateway routing predicate: a tool whose name
	// is in this set is dispatched locally, every other tool is proxied to
	// the gateway.
	opsOwned map[string]bool
	// gateway is the identity-forwarding proxy for gateway-owned tools. A
	// nil client (dev / no-gateway posture) makes every gateway-owned tool
	// report ENDPOINT_UNAVAILABLE.
	gateway *gateway.Client
}

// Invoke runs an MCP management tool through the §25.12 transparent
// dual-routing dispatch. It branches on the ops-vs-gateway routing
// predicate: an ops-owned tool (a member of i.opsOwned) is replayed
// locally against the lenny-ops mux; a gateway-owned tool is proxied to
// the gateway admin API with the caller identity forwarded so the gateway
// re-authorizes as the real caller.
//
// spec: §25.12 — "Tool invocations are routed transparently to either
// lenny-ops handlers ... or Gateway admin API via GatewayClient."
func (i *opsInvoker) Invoke(ctx context.Context, tool mcp.Tool, args json.RawMessage) (mcp.ToolResult, error) {
	if i.opsOwned[tool.Name] {
		return i.invokeLocal(ctx, tool, args)
	}
	return i.invokeGateway(ctx, tool, args)
}

// invokeLocal dispatches an ops-owned tool by translating it into the
// HTTP request its §25.12 mapping names and replaying that request
// against the lenny-ops mux. The captured response becomes the tool
// result; §25.2 dry-run previews (200 with dryRun:true) are surfaced so
// the management server can apply the §25.12 dry-run mapping.
//
// The replay inherits ctx from the originating /mcp/management request,
// so the caller's already-verified principal, scope claim, and
// correlation values carry onto the replayed request. §25.12 requires
// that the tool's mapped REST call "passes through the standard OIDC/JWT
// middleware and role-based authorization check"; the replay routes
// through the post-authentication chain (mcpReplayHandler), which re-runs
// the §25.4 role gate and §25.1 scope gate against that principal without
// re-verifying a bearer that is not forwarded in-process. Without
// propagating the principal the role gate would reject the principal-less
// replay (401/403) and every management tool invocation would fail
// whenever the §25.4 auth gate is configured.
//
// spec: §25.12 — "Every MCP tool invocation that passes the scope check
// is translated into a REST call ... That REST call passes through the
// standard OIDC/JWT middleware and role-based authorization check."
func (i *opsInvoker) invokeLocal(ctx context.Context, tool mcp.Tool, args json.RawMessage) (mcp.ToolResult, error) {
	path, query, body, err := buildToolRequest(tool, args)
	if err != nil {
		return mcp.ToolResult{}, err
	}
	url := path
	if query != "" {
		url += "?" + query
	}
	var reader *bytes.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(tool.Method, url, reader)
	// Inherit the caller's verified principal, scope claim, and
	// correlation context from the originating /mcp/management request so
	// the replay is authorized as the real caller (spec: §25.12).
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	// §25.12: the management server is the caller; mark the invocation
	// so audit and identity attribution see the MCP path. This is only a
	// fallback: when a principal is on the context the handlers attribute
	// to the principal's subject, not this header.
	req.Header.Set("X-Lenny-Caller", "mcp-management")
	// §25.12 Headers and Correlation: carry the operation ID and agent
	// name the MCP adapter derived from the tools/call request (tool input
	// operationId or _meta.operationId, and clientInfo.name) onto the
	// replayed REST request as the standard correlation headers, so the
	// underlying call is tied to the remediation effort the same way a
	// direct REST call with these headers would be. The values ride on the
	// inherited context; project them onto the request headers so the REST
	// call carries them explicitly.
	//
	// spec: §25.12 — "X-Lenny-Operation-ID from the tool input's optional
	// operationId field (if present) OR from ... _meta.operationId ... →
	// same HTTP header on REST calls." and "X-Lenny-Agent-Name from the MCP
	// clientInfo.name → same HTTP header on REST calls."
	fields := correlation.From(ctx)
	if fields.OperationID != "" {
		req.Header.Set(correlation.HeaderOperationID, fields.OperationID)
	}
	if fields.AgentName != "" {
		req.Header.Set(correlation.HeaderAgentName, fields.AgentName)
	}
	rec := httptest.NewRecorder()
	i.server.mcpReplay.ServeHTTP(rec, req)

	result := mcp.ToolResult{
		Status: rec.Code,
		Body:   append(json.RawMessage(nil), rec.Body.Bytes()...),
	}
	applyDryRunProbe(&result, rec.Code, rec.Body.Bytes())
	// §25.12: a 503 from an ops endpoint that is not configured maps to
	// the -32000 ENDPOINT_UNAVAILABLE error.
	if rec.Code == http.StatusServiceUnavailable {
		result.Unavailable = true
	}
	return result, nil
}

// invokeGateway dispatches a gateway-owned tool by proxying its mapped
// admin-API call to the gateway through the identity-forwarding
// ProxyAdminCall. It reuses buildToolRequest to form the path, query, and
// body, assembles the forward headers (the caller identity the opsserver
// MCP bridge middleware captured, read via MCPCallerIdentityHeaders, plus
// Content-Type, the X-Lenny-Caller marker, and the §25.12 correlation
// headers), and maps the raw gateway (status, body)
// into the tool result with the same §25.2 dry-run probe the local path
// applies. A gateway RBAC denial (a 4xx/5xx body) re-emits verbatim as an
// isError tool result. A transport failure, or a nil gateway client in a
// local-only dev posture, sets Unavailable so handleToolsCall returns
// -32000 ENDPOINT_UNAVAILABLE and the tool stays listed in tools/list.
//
// spec: §25.12 — the authenticated identity from the MCP session maps to
// the Authorization header on gateway calls; a transport failure maps to
// ENDPOINT_UNAVAILABLE.
func (i *opsInvoker) invokeGateway(ctx context.Context, tool mcp.Tool, args json.RawMessage) (mcp.ToolResult, error) {
	if i.gateway == nil {
		return mcp.ToolResult{Unavailable: true}, nil
	}
	path, query, body, err := buildToolRequest(tool, args)
	if err != nil {
		return mcp.ToolResult{}, err
	}
	url := path
	if query != "" {
		url += "?" + query
	}
	status, respBody, err := i.gateway.ProxyAdminCall(ctx, tool.Method, url, body, gatewayForwardHeaders(ctx))
	if err != nil {
		// A transport-level failure (the gateway never completed the
		// request) maps to ENDPOINT_UNAVAILABLE, keeping the tool listed.
		return mcp.ToolResult{Unavailable: true}, nil
	}
	result := mcp.ToolResult{
		Status: status,
		Body:   append(json.RawMessage(nil), respBody...),
	}
	applyDryRunProbe(&result, status, respBody)
	return result, nil
}

// gatewayForwardHeaders assembles the headers the gateway proxy forwards
// for a gateway-owned tool call. The caller identity the opsserver MCP
// bridge middleware captured, read via MCPCallerIdentityHeaders (the raw
// Authorization bearer or, under AllowDevHeaders, the dev-mode identity
// headers), is forwarded first so the gateway
// re-authorizes as the real caller; the invoker then adds the
// Content-Type, the X-Lenny-Caller marker, and the §25.12 correlation
// headers exactly as the local replay does. ProxyAdminCall sets no
// service-account bearer, so the forwarded identity is the one the
// gateway's OIDC/RBAC evaluates.
//
// spec: §25.12 (Headers and Correlation; the authenticated identity from
// the MCP session maps to the Authorization header on gateway calls).
func gatewayForwardHeaders(ctx context.Context) http.Header {
	h := http.Header{}
	for name, values := range MCPCallerIdentityHeaders(ctx) {
		for _, v := range values {
			h.Add(name, v)
		}
	}
	h.Set("Content-Type", "application/json")
	h.Set("X-Lenny-Caller", "mcp-management")
	fields := correlation.From(ctx)
	if fields.OperationID != "" {
		h.Set(correlation.HeaderOperationID, fields.OperationID)
	}
	if fields.AgentName != "" {
		h.Set(correlation.HeaderAgentName, fields.AgentName)
	}
	return h
}

// applyDryRunProbe surfaces a §25.2 dry-run preview: a 200 response
// carrying dryRun:true is a preview rather than a mutation, so the flag
// and the preview object are lifted onto the tool result. Both the local
// replay and the gateway proxy apply the same probe.
//
// spec: §25.2 (dry-run preview), §25.12 (dry-run mapping).
func applyDryRunProbe(result *mcp.ToolResult, status int, body []byte) {
	if status != http.StatusOK {
		return
	}
	var probe struct {
		DryRun  bool            `json:"dryRun"`
		Preview json.RawMessage `json:"preview"`
	}
	if json.Unmarshal(body, &probe) == nil && probe.DryRun {
		result.DryRun = true
		result.Preview = probe.Preview
	}
}

// buildToolRequest translates a §25.12 tool and its arguments into the
// concrete request path, query string, and body. Path parameters
// ({id}, {name}, {component}) are substituted from the arguments; for a
// GET the remaining arguments become query parameters, and for a body
// method they become the JSON request body.
func buildToolRequest(tool mcp.Tool, args json.RawMessage) (path, query string, body []byte, err error) {
	var argMap map[string]any
	if len(args) > 0 {
		if err := json.Unmarshal(args, &argMap); err != nil {
			return "", "", nil, err
		}
	}
	path = tool.Path
	consumed := make(map[string]bool)
	// Substitute {param} path segments from the arguments.
	for {
		open := strings.IndexByte(path, '{')
		if open < 0 {
			break
		}
		closeIdx := strings.IndexByte(path[open:], '}')
		if closeIdx < 0 {
			break
		}
		name := path[open+1 : open+closeIdx]
		value := stringArg(argMap[name])
		path = path[:open] + value + path[open+closeIdx+1:]
		consumed[name] = true
	}
	if tool.Method == http.MethodGet {
		query = encodeQuery(argMap, consumed)
		return path, query, nil, nil
	}
	// Body methods: the unconsumed arguments form the JSON request body.
	rest := make(map[string]any)
	for k, v := range argMap {
		if !consumed[k] {
			rest[k] = v
		}
	}
	if len(rest) > 0 {
		body, err = json.Marshal(rest)
		if err != nil {
			return "", "", nil, err
		}
	}
	return path, query, body, nil
}

// stringArg renders an argument value as a path/query string.
func stringArg(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	default:
		b, _ := json.Marshal(t)
		return strings.Trim(string(b), `"`)
	}
}

// encodeQuery encodes the unconsumed string-valued arguments as a query
// string; non-string and consumed arguments are skipped.
func encodeQuery(argMap map[string]any, consumed map[string]bool) string {
	values := make([]string, 0, len(argMap))
	for k, v := range argMap {
		if consumed[k] {
			continue
		}
		s, ok := v.(string)
		if !ok || s == "" {
			continue
		}
		values = append(values, queryEscape(k)+"="+queryEscape(s))
	}
	// Deterministic order keeps the replayed request reproducible.
	sortStrings(values)
	return strings.Join(values, "&")
}

// queryEscape percent-escapes a query component.
func queryEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isUnreserved(c) {
			b.WriteByte(c)
			continue
		}
		const hex = "0123456789ABCDEF"
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&0x0f])
	}
	return b.String()
}

// isUnreserved reports whether c is an RFC 3986 unreserved character.
func isUnreserved(c byte) bool {
	switch {
	case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		return true
	case c == '-', c == '_', c == '.', c == '~':
		return true
	default:
		return false
	}
}

// sortStrings sorts s in place (small slices; insertion sort).
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
