// SPDX-License-Identifier: MIT

// Package mcpruntimes serves the §4.1 dedicated MCP endpoints for
// `type: mcp` runtimes at `/mcp/runtimes/{name}`.
//
// The §4.1 spec lists this endpoint among the gateway's responsibilities:
//
//	Serve dedicated MCP endpoints for `type: mcp` runtimes at
//	/mcp/runtimes/{name}.
//
// A `type: mcp` runtime's agent is itself an MCP server (§5.1). The
// dedicated per-runtime endpoint lets a client speak MCP to a specific
// runtime without first opening a Lenny session: the gateway looks up
// the runtime by name, verifies it carries `type: mcp`, and relays the
// JSON-RPC body to the runtime's MCP client.
//
// The handler returns structured JSON-RPC errors per §15.2.1:
//
//   - HTTP 404 + `RESOURCE_NOT_FOUND`     when the runtime does not exist.
//   - HTTP 400 + `INVALID_RUNTIME_TYPE`   when the runtime is not type:mcp.
//   - HTTP 503 + `RUNTIME_UNAVAILABLE`    when the runtime exists but no
//     active MCP client is registered for it on this replica.
//
// spec: §4.1 line 53; §5.1 type: mcp; §15.2 MCP transport
package mcpruntimes

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/lennylabs/lenny/pkg/gateway/environment/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/errorclassify"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/capabilityinference"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
)

// Dispatcher reaches a `type: mcp` runtime's MCP client and returns
// the raw JSON-RPC response body. A nil Dispatcher on the Handler
// makes the endpoint reply with RUNTIME_UNAVAILABLE for every runtime
// (the runtime registration is observable; no live MCP client is wired
// on this replica).
//
// Production wires a Dispatcher backed by the per-runtime
// pkg/adapter/mcp.Client owned by sessionserver; the interface stays
// minimal so a v1 deployment without per-runtime persistent clients
// can serve the endpoint with structured errors.
type Dispatcher interface {
	// Dispatch routes the JSON-RPC body (already validated by the
	// handler) to the named runtime's MCP client and returns the raw
	// response bytes. The runtime is guaranteed to be `type: mcp`.
	// When no live MCP client exists for the runtime, Dispatch
	// returns ErrNoActiveClient.
	Dispatch(ctx context.Context, runtimeName string, body []byte) ([]byte, error)
}

// ErrNoActiveClient is the sentinel a Dispatcher returns when no
// live MCP client is wired for the named runtime on this replica.
// The handler surfaces this as HTTP 503 RUNTIME_UNAVAILABLE so the
// client can retry against another replica (or after the runtime
// pool warms up).
var ErrNoActiveClient = errors.New("mcpruntimes: no active MCP client for runtime")

// Handler is the http.Handler for POST /mcp/runtimes/{name}. It mounts
// inside the gateway's main mux:
//
//	mux.Handle("/mcp/runtimes/", mcpruntimes.New(store, disp))
//
// The handler is bounded; every method besides POST returns 405, every
// unknown runtime returns 404, every non-mcp runtime returns 400, and
// JSON-RPC payload validation errors return the standard JSON-RPC
// error envelope.
type Handler struct {
	store      runtimestore.Store
	dispatcher Dispatcher
	maxBytes   int64
	// environments resolves the §10.6 environment named by the optional
	// `?environment=` query parameter so a tools/call can be gated by that
	// environment's mcpRuntimeFilters. nil leaves the capability gate off.
	// spec: §10.6 line 607.
	environments EnvironmentResolver
}

// EnvironmentResolver loads a §10.6 environment by (tenant, name). It is
// the seam the per-runtime dispatcher uses to apply an environment's
// mcpRuntimeFilters capability filter; *environmentstore.Memory and the
// pgstore satisfy it. spec: §10.6 line 607.
type EnvironmentResolver interface {
	Get(ctx context.Context, tenantID, name string) (environmentstore.Environment, error)
}

// New returns a Handler bound to the supplied runtime registry and
// dispatcher. dispatcher may be nil; in that case every request that
// passes the runtime-type validation returns RUNTIME_UNAVAILABLE.
func New(store runtimestore.Store, dispatcher Dispatcher) *Handler {
	return &Handler{
		store:      store,
		dispatcher: dispatcher,
		maxBytes:   1 << 20, // 1 MiB JSON-RPC body cap, matches /mcp.
	}
}

// WithEnvironments wires the §10.6 environment registry so a tools/call
// scoped to an environment (`?environment=<name>`) is gated by that
// environment's mcpRuntimeFilters capability filter. spec: §10.6 line 607.
func (h *Handler) WithEnvironments(envs EnvironmentResolver) *Handler {
	h.environments = envs
	return h
}

// mcpRuntimeFilterDenies reports whether the §10.6 environment named by
// the request's `?environment=` query parameter has an mcpRuntimeFilter
// that denies the tools/call's tool on this runtime. It returns the
// blocked capability for the rejection detail. A request that names no
// environment, an unknown environment, a method other than tools/call,
// or a runtime no filter admits is not gated. The tool's capability is
// resolved from the runtime's §5.1 toolCapabilityOverrides and inference
// mode; an un-annotated, un-overridden tool resolves to the conservative
// admin default so a restrictive filter fails closed.
//
// spec: §10.6 line 607; §5.1 capability inference.
func (h *Handler) mcpRuntimeFilterDenies(r *http.Request, rt runtimestore.Runtime, req jsonRPCRequest) (bool, string) {
	if h.environments == nil {
		return false, ""
	}
	tool := req.toolName()
	if tool == "" {
		return false, ""
	}
	envName := r.URL.Query().Get("environment")
	if envName == "" {
		return false, ""
	}
	tenantID := r.Header.Get("X-Lenny-Tenant-ID")
	if tenantID == "" {
		return false, ""
	}
	env, err := h.environments.Get(r.Context(), tenantID, envName)
	if err != nil {
		// An unknown environment scope carries no filter; the §10.6
		// transparent-filtering boundary on runtime visibility is a
		// separate concern enforced at discovery.
		return false, ""
	}
	filter, ok := env.MCPRuntimeFilterFor(rt.Name, string(rt.Type), rt.Labels)
	if !ok {
		return false, ""
	}
	res := capabilityinference.Resolve(tool, capabilityinference.ToolAnnotations{},
		rt.CapabilityInferenceMode, rt.ToolCapabilityOverrides)
	caps := make([]string, len(res.Capabilities))
	for i, c := range res.Capabilities {
		caps[i] = string(c)
	}
	permitted, blocked := filter.PermitTool(caps)
	return !permitted, blocked
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, errMethodNotAllowed,
			"METHOD_NOT_ALLOWED", "only POST is supported on /mcp/runtimes/{name}", nil)
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusNotFound, errMethodNotFound,
			"RESOURCE_NOT_FOUND", "runtime not found", nil)
		return
	}
	if h.store == nil {
		writeError(w, http.StatusNotFound, errMethodNotFound,
			"RESOURCE_NOT_FOUND", "runtime not found", nil)
		return
	}
	rt, err := runtimestore.Resolve(r.Context(), h.store, name)
	if err != nil || !rt.IsActive() {
		writeError(w, http.StatusNotFound, errMethodNotFound,
			"RESOURCE_NOT_FOUND", "runtime not found", nil)
		return
	}
	if rt.Type != runtimestore.TypeMCP {
		writeError(w, http.StatusBadRequest, errInvalidRequest,
			"INVALID_RUNTIME_TYPE",
			"runtime is not type:mcp; only type:mcp runtimes accept direct MCP calls", nil)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.maxBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, errInvalidRequest,
			"VALIDATION_ERROR", "could not read request body: "+err.Error(), nil)
		return
	}
	defer r.Body.Close()

	// Verify the body is at least a syntactically well-formed
	// JSON-RPC 2.0 request so callers get a JSON-RPC parse error
	// rather than a "no active client" status when the payload is
	// malformed.
	req, err := parseJSONRPC(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, errParse,
			"VALIDATION_ERROR", err.Error(), nil)
		return
	}

	// spec: §10.6 line 607 — when the caller scopes the call to an
	// environment (`?environment=<name>`), the environment's
	// mcpRuntimeFilters gate a tools/call on this type:mcp runtime by the
	// tool's inferred §5.1 capability. A denied tool is rejected before
	// dispatch with TOOL_CAPABILITY_DENIED. F-10.6.2.
	if blocked, cap := h.mcpRuntimeFilterDenies(r, rt, req); blocked {
		writeError(w, http.StatusForbidden, errInvalidRequest,
			"TOOL_CAPABILITY_DENIED",
			"tool denied by the environment mcpRuntimeFilters capability filter",
			map[string]any{"capability": cap, "tool": req.toolName()})
		return
	}

	if h.dispatcher == nil {
		writeError(w, http.StatusServiceUnavailable, errInternal,
			"RUNTIME_UNAVAILABLE",
			"no active MCP client is registered for runtime "+name, nil)
		return
	}

	resp, err := h.dispatcher.Dispatch(r.Context(), name, body)
	if err != nil {
		if errors.Is(err, ErrNoActiveClient) {
			writeError(w, http.StatusServiceUnavailable, errInternal,
				"RUNTIME_UNAVAILABLE",
				"no active MCP client is registered for runtime "+name, nil)
			return
		}
		writeError(w, http.StatusBadGateway, errInternal,
			"INTERNAL_ERROR", "MCP runtime dispatch failed: "+err.Error(), nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(resp)
}

// jsonRPCRequest is the minimal envelope the handler validates before
// dispatching to the runtime. Full JSON-RPC method routing is the
// responsibility of the runtime's MCP server.
type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// toolName returns the tool named by a tools/call request, or empty when
// the request is not a tools/call or the params do not name a tool.
func (req jsonRPCRequest) toolName() string {
	if req.Method != "tools/call" || len(req.Params) == 0 {
		return ""
	}
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return ""
	}
	return p.Name
}

// parseJSONRPC validates that body is a syntactically well-formed
// JSON-RPC 2.0 request and returns the parsed envelope.
func parseJSONRPC(body []byte) (jsonRPCRequest, error) {
	if len(body) == 0 {
		return jsonRPCRequest{}, errors.New("request body is empty")
	}
	var req jsonRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return jsonRPCRequest{}, errors.New("request is not valid JSON")
	}
	if req.JSONRPC != "2.0" {
		return jsonRPCRequest{}, errors.New(`jsonrpc must be "2.0"`)
	}
	if req.Method == "" {
		return jsonRPCRequest{}, errors.New("method is required")
	}
	return req, nil
}

// JSON-RPC + MCP error codes mirrored from pkg/gateway/mcp so the
// error envelopes line up across the two surfaces.
const (
	errParse            = -32700
	errInvalidRequest   = -32600
	errMethodNotFound   = -32601
	errMethodNotAllowed = -32601
	errInternal         = -32603
)

// errorEnvelope is the §15.2.1 lenny error envelope carried inside
// JSON-RPC error.data.
type errorEnvelope struct {
	Code      string         `json:"code"`
	Category  string         `json:"category"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details,omitempty"`
}

// jsonRPCError is the JSON-RPC 2.0 error object.
type jsonRPCError struct {
	Code    int           `json:"code"`
	Message string        `json:"message"`
	Data    errorEnvelope `json:"data,omitempty"`
}

// jsonRPCResponse is the JSON-RPC 2.0 response envelope used for
// error responses on the dedicated runtime endpoint.
type jsonRPCResponse struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      any           `json:"id"`
	Error   *jsonRPCError `json:"error,omitempty"`
}

// writeError emits an HTTP status + a JSON-RPC error response whose
// data field carries the §15.2.1 lenny error envelope (code,
// category, message, retryable, details). The category and retryable
// values come from the shared §15.2.1 errorclassify table so the
// REST and MCP surfaces report identical triples.
func writeError(w http.ResponseWriter, httpStatus, rpcCode int, lennyCode, message string, details map[string]any) {
	cat, retryable := errorclassify.Classify(lennyCode)
	env := errorEnvelope{
		Code:      lennyCode,
		Category:  string(cat),
		Message:   message,
		Retryable: retryable,
		Details:   details,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	_ = json.NewEncoder(w).Encode(jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      nil,
		Error: &jsonRPCError{
			Code:    rpcCode,
			Message: message,
			Data:    env,
		},
	})
}
