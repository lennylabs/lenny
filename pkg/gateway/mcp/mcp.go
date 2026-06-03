// SPDX-License-Identifier: MIT

// Package mcp implements the §15.2 / §9.1 MCP adapter — the
// Model Context Protocol surface the gateway exposes at `/mcp`.
//
// The adapter speaks JSON-RPC 2.0 over HTTP POST. v1 implements the
// three core MCP methods:
//
//   - `initialize`  — capability negotiation + serverInfo.
//   - `tools/list`  — the platform tool catalog (§8.5 delegation
//     tools plus the session-lifecycle tools).
//   - `tools/call`  — dispatch a tool invocation to the gateway.
//
// The MCP adapter is a thin translation layer: a `tools/call` for
// `lenny/create_session` lands on the same session-creation path as
// `POST /v1/sessions`. Building the adapter over the existing
// gateway services keeps the REST and MCP surfaces in lockstep per
// the §15.2.1 REST/MCP consistency contract.
//
// Streaming (the MCP Streamable-HTTP SSE channel) is post-v1 in this
// minimal adapter — every method returns a single JSON-RPC response.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/gateway/errorclassify"
	environmentmw "github.com/lennylabs/lenny/pkg/gateway/middleware/environment"
)

// The supported MCP protocol versions, the negotiation rules, and the
// deprecation header live in version.go (§15.2 "Version negotiation").

// jsonRPCRequest is the JSON-RPC 2.0 request envelope.
type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// jsonRPCResponse is the JSON-RPC 2.0 response envelope.
type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

// jsonRPCError is the JSON-RPC 2.0 error object.
type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// LennyErrorDetail is the §15.2.1 / §15.2 lenny error envelope the
// MCP transport carries inside jsonRPCError.Data. It mirrors the REST
// errorBody field-for-field so a parity test can compare the same
// fields on both surfaces. The shared §15.2.1 errorclassify table
// drives the category and retryable values.
type LennyErrorDetail struct {
	Code      string         `json:"code"`
	Category  string         `json:"category"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details,omitempty"`
}

// NewLennyErrorDetail builds the parity envelope from a lenny code
// and message, consulting the shared classifier so REST and MCP
// agree on the category and retryable values.
func NewLennyErrorDetail(code, message string, details map[string]any) LennyErrorDetail {
	cat, retryable := errorclassify.Classify(code)
	return LennyErrorDetail{
		Code:      code,
		Category:  string(cat),
		Message:   message,
		Retryable: retryable,
		Details:   details,
	}
}

// JSON-RPC 2.0 + MCP error codes.
const (
	errParse          = -32700
	errInvalidRequest = -32600
	errMethodNotFound = -32601
	errInvalidParams  = -32602
	errInternal       = -32603
)

// Stability classifies an MCP tool's §15.5 item 6 stability tier so
// consumers can programmatically discover which catalog entries are
// covered by the platform's versioning guarantees and which may change
// without notice. The value is surfaced as the `x-lenny-stability`
// extension on every entry in the `tools/list` response.
//
//   - StabilityStable: covered by §15.5 items 1–5.
//   - StabilityBeta: may change between minor releases with deprecation notice.
//   - StabilityAlpha: may change without notice — do not build production code against it.
//
// spec: §15.5 item 6. F-15.5.10.
type Stability string

const (
	StabilityStable Stability = "stable"
	StabilityBeta   Stability = "beta"
	StabilityAlpha  Stability = "alpha"
)

// Tool is one entry in the MCP tool catalog.
//
// spec: §15.5 item 6 — `Stability` is the catalog-discoverable
// stability label for the tool; empty defaults to `stable` at
// serialization time so an unannotated tool is treated as covered by
// the platform versioning guarantees. F-15.5.10.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	// Stability is the §15.5 item 6 tier the tool ships under.
	// Marshalled as `x-lenny-stability`; empty serialises as
	// `"stable"` so consumers can rely on the field always being
	// present.
	Stability Stability `json:"x-lenny-stability,omitempty"`
}

// MarshalJSON serialises the Tool descriptor with `x-lenny-stability`
// always set: an empty Stability collapses to StabilityStable so the
// §15.5 item 6 tier is discoverable on every catalog entry. F-15.5.10.
func (t Tool) MarshalJSON() ([]byte, error) {
	type alias Tool
	stab := t.Stability
	if stab == "" {
		stab = StabilityStable
	}
	a := alias(t)
	a.Stability = stab
	return json.Marshal(a)
}

// ToolResult is the §15.2 tool-call result content.
type ToolResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// ToolContent is one content block in a tool result.
type ToolContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// ToolHandler executes a tool call. arguments is the raw JSON
// `arguments` object from the `tools/call` params. The handler
// returns the tool result or an error; a returned error becomes an
// isError=true ToolResult whose first content block carries the
// human-readable message and second content block carries the
// §15.2.1 lenny error envelope (JSON-encoded LennyErrorDetail). A
// returned *ToolError supplies the lenny code, message, and
// details; any other error falls back to INTERNAL_ERROR with the
// error's String() as the message.
type ToolHandler func(ctx context.Context, arguments json.RawMessage) (ToolResult, error)

// LennyErrorContentType is the §15.2.1 / §15.2 content-block type the
// MCP tool-error path uses to surface the structured lenny error
// envelope alongside the human-readable text. The block's Text field
// holds the JSON-encoded LennyErrorDetail; MCP clients that do not
// know this extension type continue to read the first text block.
const LennyErrorContentType = "lenny/error"

// ToolError is the structured error a tool handler returns to drive
// the §15.2.1 REST↔MCP error envelope parity. The lenny Code is the
// canonical error code (e.g., "VALIDATION_ERROR"); the classifier
// (pkg/gateway/errorclassify) derives (category, retryable) from it
// so REST and MCP return identical pairs for the same code.
type ToolError struct {
	Code    string
	Msg     string
	Details map[string]any
}

// Error implements error.
func (e *ToolError) Error() string { return e.Msg }

// NewToolError returns a ToolError with the given code, message, and
// optional details.
func NewToolError(code, msg string, details map[string]any) *ToolError {
	return &ToolError{Code: code, Msg: msg, Details: details}
}

// ResultInterceptor runs over a successful tool result before it is
// delivered back to the agent. It is the transport-layer hook the
// gateway uses to run the §4.8 PreToolResult interceptor phase without
// coupling this transport-only adapter to the policy engine. callID is
// the JSON-RPC request id (the originating tool_call.id, immutable per
// §4.8 line 1053); name is the tool name. A non-nil error rejects
// delivery and the dispatcher surfaces it through the standard tool
// error path. A returned ToolResult replaces the handler's result
// (a MODIFY may redact content or set isError). spec: §4.8 line 1053.
type ResultInterceptor func(ctx context.Context, callID, name string, result ToolResult) (ToolResult, error)

// Server is the §15.2 MCP adapter.
type Server struct {
	tools             map[string]Tool
	handlers          map[string]ToolHandler
	order             []string
	resultInterceptor ResultInterceptor
	// idem holds the §11.5 idempotency wiring (Store + tenant resolver
	// + allow-list of tools that admit an idempotencyKey field). A
	// zero IdempotencyConfig disables the per-tool idempotency path.
	// spec: §11.5 line 277; F-11.5.1.
	idem IdempotencyConfig
	// wsAuth carries the §27.5.4 WebSocket revocation watch wiring: the
	// per-connection principal extractor and the playground revocation
	// checker that together close a revoked origin=playground bearer
	// mid-stream with WebSocket code 4401. A zero value leaves the watch
	// off, so a non-playground MCP WebSocket client serves frames without
	// it. spec: §27.3.1 line 167; §27.5.4.
	wsAuth wsAuthConfig
}

// SetResultInterceptor installs the §4.8 PreToolResult hook. A nil
// interceptor (the default) leaves tool results unmodified.
func (s *Server) SetResultInterceptor(ri ResultInterceptor) {
	s.resultInterceptor = ri
}

// NewServer returns an empty MCP Server. Register tools with
// RegisterTool before mounting Handler.
func NewServer() *Server {
	return &Server{
		tools:    map[string]Tool{},
		handlers: map[string]ToolHandler{},
	}
}

// RegisterTool adds a tool to the catalog. A later RegisterTool with
// the same name replaces the earlier registration.
func (s *Server) RegisterTool(t Tool, h ToolHandler) {
	if _, exists := s.tools[t.Name]; !exists {
		s.order = append(s.order, t.Name)
	}
	s.tools[t.Name] = t
	s.handlers[t.Name] = h
}

// Handler returns the http.Handler that serves POST /mcp.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /mcp", s.handleRPC)
	return mux
}

// EnvironmentHandler returns the http.Handler that serves the §10.6
// explicit-environment MCP surface at POST /mcp/environments/{name}. It
// runs the same JSON-RPC dispatch as POST /mcp, with the named
// environment attached to the request context so a tool that creates a
// session or discovers runtimes defaults to that environment scope
// without the caller repeating it on each call. spec: §10.6 line 557.
func (s *Server) EnvironmentHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /mcp/environments/{name}", func(w http.ResponseWriter, r *http.Request) {
		if name := r.PathValue("name"); name != "" {
			r = r.WithContext(environmentmw.WithExplicitEnvironment(r.Context(), name))
		}
		s.handleRPC(w, r)
	})
	return mux
}

func (s *Server) handleRPC(w http.ResponseWriter, r *http.Request) {
	body := http.MaxBytesReader(w, r.Body, 1<<20)
	defer body.Close()

	var req jsonRPCRequest
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		// spec: §15.2.1 rule 3 (line 1384) — even transport-level
		// JSON-RPC errors carry the shared lenny envelope (code,
		// category, retryable) in error.data so a client applies one
		// error-handling strategy across REST and MCP. F-15.2.6.
		s.WriteLennyError(w, nil, errParse, "VALIDATION_ERROR", "request is not valid JSON", nil)
		return
	}
	if req.JSONRPC != "2.0" {
		s.WriteLennyError(w, req.ID, errInvalidRequest, "VALIDATION_ERROR", "jsonrpc must be \"2.0\"", nil)
		return
	}

	switch req.Method {
	case "initialize":
		// spec: §15.2 lines 1310-1316 — negotiate the highest mutually
		// supported MCP spec version, reject an unsupported/retired one
		// with the structured lenny error, and set the deprecation
		// warning header when the connection lands on the previous
		// version. F-15.2.1, F-15.5.4.
		result, deprecated, nerr := initializeResult(requestedProtocolVersion(req.Params))
		if nerr != nil {
			s.WriteLennyError(w, req.ID, errInvalidParams, nerr.code, nerr.message,
				map[string]any{"supportedVersions": SupportedProtocolVersions()})
			return
		}
		if deprecated {
			w.Header().Set(headerMCPVersionDeprecated, result["protocolVersion"].(string))
		}
		s.writeResult(w, req.ID, result)
	case "tools/list":
		s.writeResult(w, req.ID, map[string]any{"tools": s.toolList()})
	case "tools/call":
		s.handleToolCall(w, r, req)
	case "ping":
		s.writeResult(w, req.ID, map[string]any{})
	default:
		// spec: §15.2.1 rule 3 — an unknown JSON-RPC method is a
		// permanent client error; surface RESOURCE_NOT_FOUND so the
		// client does not retry. F-15.2.6.
		s.WriteLennyError(w, req.ID, errMethodNotFound, "RESOURCE_NOT_FOUND", "unknown method "+req.Method, nil)
	}
}

func (s *Server) toolList() []Tool {
	out := make([]Tool, 0, len(s.order))
	for _, name := range s.order {
		out = append(out, s.tools[name])
	}
	return out
}

func (s *Server) handleToolCall(w http.ResponseWriter, r *http.Request, req jsonRPCRequest) {
	ctx := r.Context()
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.WriteLennyError(w, req.ID, errInvalidParams, "VALIDATION_ERROR", "params is not a valid tools/call object", nil)
		return
	}
	handler, ok := s.handlers[params.Name]
	if !ok {
		// spec: §15.2.1 rule 3 — a tools/call naming an unregistered
		// tool is RESOURCE_NOT_FOUND (the tool resource does not
		// exist), permanent and not retryable. F-15.2.6.
		s.WriteLennyError(w, req.ID, errMethodNotFound, "RESOURCE_NOT_FOUND", "unknown tool "+params.Name, nil)
		return
	}
	// spec: §11.5 line 277 — when the tool admits an idempotencyKey field
	// and the caller supplied one, route the dispatch through the §11.5
	// Claim/Replay/Finalize flow so retries collapse to one execution.
	// dispatchIdempotent returns handled=true when the path consumed the
	// call; handled=false falls through to the regular handler. Closes
	// F-11.5.1, F-11.5.6.
	var (
		result ToolResult
		err    error
	)
	if s.idem.Store != nil && s.idem.Tools[params.Name] {
		callerKey := extractIdempotencyKey(params.Arguments)
		if callerKey != "" {
			tenant := ""
			if s.idem.TenantFromRequest != nil {
				tenant = s.idem.TenantFromRequest(r)
			}
			if tenant == "" {
				// Mirror the REST middleware's fail-closed posture: a
				// missing tenant means the auth chain was bypassed for
				// this request, so collapsing keys under a shared
				// bucket would violate §11.5's per-tenant scope.
				s.WriteLennyError(w, req.ID, errInvalidRequest, "UNAUTHORIZED",
					"idempotency: tenant could not be resolved for MCP tool call; auth chain must precede MCP dispatch", nil)
				return
			}
			var handled bool
			result, handled, err = s.dispatchIdempotent(ctx, tenant, params.Name, callerKey, params.Arguments, handler)
			if !handled {
				result, err = handler(ctx, params.Arguments)
			}
		} else {
			result, err = handler(ctx, params.Arguments)
		}
	} else {
		result, err = handler(ctx, params.Arguments)
	}
	if err != nil {
		// A tool execution failure surfaces as an MCP tool result with
		// isError=true (the call reached the tool, the tool reported
		// failure) plus the §15.2.1 lenny error envelope so the REST
		// and MCP transports report identical (code, category,
		// retryable) triples for the same error. A *ToolError supplies
		// the lenny code; any other error falls back to INTERNAL_ERROR.
		s.writeResult(w, req.ID, toolErrorResult("INTERNAL_ERROR", err))
		return
	}
	// §4.8 PreToolResult: run the interceptor chain over the tool
	// result before delivering it back to the agent. A REJECT (or an
	// immutable-id MODIFY violation) surfaces through the same isError
	// + lenny-envelope path as a handler failure; a MODIFY substitutes
	// the rewritten result. spec: §4.8 line 1053.
	if s.resultInterceptor != nil {
		modified, ierr := s.resultInterceptor(ctx, string(req.ID), params.Name, result)
		if ierr != nil {
			s.writeResult(w, req.ID, toolErrorResult("INTERCEPTOR_REJECTED", ierr))
			return
		}
		result = modified
	}
	s.writeResult(w, req.ID, result)
}

// Catalog returns the registered tool descriptors in registration
// order. It is the read side the §9.1 intra-pod platform MCP server
// uses (over GatewayControl.ListPlatformTools) to advertise the same
// catalog the gateway-edge /mcp surface serves, so the two surfaces
// never drift. spec: §9.1 lines 14-31. F-9.1.1.
func (s *Server) Catalog() []Tool {
	return s.toolList()
}

// DispatchTool runs a registered tool handler (and the §4.8
// PreToolResult interceptor) outside the HTTP transport. It is the
// dispatch side the §9.1 intra-pod platform MCP server reaches over
// GatewayControl.CallPlatformTool: a type:agent runtime's tools/call on
// the @lenny-platform-mcp socket lands on the same handler the
// gateway-edge /mcp surface runs, so the intra-pod and edge platform
// tool surfaces stay in lockstep per the §15.2.1 consistency contract.
//
// ok is false when no tool with name is registered (the caller maps
// this to the MCP method-not-found error). A tool-level failure (a
// handler error or an interceptor rejection) is returned as an isError
// ToolResult carrying the §15.2.1 lenny error envelope with err nil, so
// the result reaches the agent the same way it would over HTTP. err is
// non-nil only for a dispatch fault that has no tool-result form.
//
// The idempotency path is intentionally absent: the §11.5 idempotency
// keys gate the client-facing session-lifecycle tools, not the
// agent-facing platform tools this entry point serves.
// spec: §9.1 line 14; §4.8 line 1053. F-9.1.1.
func (s *Server) DispatchTool(ctx context.Context, name string, arguments json.RawMessage) (result ToolResult, ok bool, err error) {
	handler, found := s.handlers[name]
	if !found {
		return ToolResult{}, false, nil
	}
	res, herr := handler(ctx, arguments)
	if herr != nil {
		return toolErrorResult("INTERNAL_ERROR", herr), true, nil
	}
	if s.resultInterceptor != nil {
		modified, ierr := s.resultInterceptor(ctx, "", name, res)
		if ierr != nil {
			return toolErrorResult("INTERCEPTOR_REJECTED", ierr), true, nil
		}
		res = modified
	}
	return res, true, nil
}

// toolErrorResult converts a tool handler (or interceptor) error into
// the §15.2.1 isError ToolResult: the first content block carries the
// human-readable message and the second carries the JSON-encoded lenny
// error envelope. A *ToolError supplies the lenny code; any other error
// falls back to defaultCode. This is the shared builder for both the
// HTTP and DispatchTool error paths. spec: §15.2.1 rule 3. F-9.1.1.
func toolErrorResult(defaultCode string, err error) ToolResult {
	lennyCode := defaultCode
	msg := err.Error()
	var details map[string]any
	var te *ToolError
	if errors.As(err, &te) {
		if te.Code != "" {
			lennyCode = te.Code
		}
		msg = te.Msg
		details = te.Details
	}
	envelope := NewLennyErrorDetail(lennyCode, msg, details)
	envelopeJSON, _ := json.Marshal(envelope)
	return ToolResult{
		Content: []ToolContent{
			{Type: "text", Text: msg},
			{Type: LennyErrorContentType, Text: string(envelopeJSON)},
		},
		IsError: true,
	}
}

func (s *Server) writeResult(w http.ResponseWriter, id json.RawMessage, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	})
}

// WriteLennyError writes a JSON-RPC error response whose Data field
// carries the §15.2.1 lenny error envelope (code, category, message,
// retryable, details). It is the single transport-error writer for
// every JSON-RPC error path (parse, bad version, unknown method,
// invalid params, unknown tool, idempotency tenant): jsonRPCCode is
// the standard JSON-RPC error code and lennyCode identifies the domain
// reason so the same (category, retryable) pair reaches the client on
// both REST and MCP surfaces. The response is HTTP 200 with the error
// in the body per JSON-RPC; a parse error before an id is decoded
// passes a nil id. spec: §15.2.1 rule 3 (line 1384). F-15.2.6.
func (s *Server) WriteLennyError(w http.ResponseWriter, id json.RawMessage, jsonRPCCode int, lennyCode, message string, details map[string]any) {
	envelope := NewLennyErrorDetail(lennyCode, message, details)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &jsonRPCError{Code: jsonRPCCode, Message: message, Data: envelope},
	})
}

// internalError is kept for callers that need to signal a transport
// failure (vs a tool failure). Unused by the v1 dispatch but exposed
// so tool handlers can distinguish the two when richer error
// mapping lands.
var _ = errInternal
