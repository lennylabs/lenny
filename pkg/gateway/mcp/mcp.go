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
)

// ProtocolVersion is the MCP protocol revision this adapter
// implements.
const ProtocolVersion = "2025-06-18"

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

// Tool is one entry in the MCP tool catalog.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
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

// Server is the §15.2 MCP adapter.
type Server struct {
	tools    map[string]Tool
	handlers map[string]ToolHandler
	order    []string
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

func (s *Server) handleRPC(w http.ResponseWriter, r *http.Request) {
	body := http.MaxBytesReader(w, r.Body, 1<<20)
	defer body.Close()

	var req jsonRPCRequest
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		s.writeError(w, nil, errParse, "request is not valid JSON")
		return
	}
	if req.JSONRPC != "2.0" {
		s.writeError(w, req.ID, errInvalidRequest, "jsonrpc must be \"2.0\"")
		return
	}

	switch req.Method {
	case "initialize":
		s.writeResult(w, req.ID, map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "lenny-gateway", "version": "0.1.0"},
		})
	case "tools/list":
		s.writeResult(w, req.ID, map[string]any{"tools": s.toolList()})
	case "tools/call":
		s.handleToolCall(w, r.Context(), req)
	case "ping":
		s.writeResult(w, req.ID, map[string]any{})
	default:
		s.writeError(w, req.ID, errMethodNotFound, "unknown method "+req.Method)
	}
}

func (s *Server) toolList() []Tool {
	out := make([]Tool, 0, len(s.order))
	for _, name := range s.order {
		out = append(out, s.tools[name])
	}
	return out
}

func (s *Server) handleToolCall(w http.ResponseWriter, ctx context.Context, req jsonRPCRequest) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.writeError(w, req.ID, errInvalidParams, "params is not a valid tools/call object")
		return
	}
	handler, ok := s.handlers[params.Name]
	if !ok {
		s.writeError(w, req.ID, errMethodNotFound, "unknown tool "+params.Name)
		return
	}
	result, err := handler(ctx, params.Arguments)
	if err != nil {
		// A tool execution failure surfaces as an MCP tool result with
		// isError=true (the call reached the tool, the tool reported
		// failure) plus the §15.2.1 lenny error envelope so the REST
		// and MCP transports report identical (code, category,
		// retryable) triples for the same error. A *ToolError supplies
		// the lenny code; any other error falls back to INTERNAL_ERROR.
		lennyCode := "INTERNAL_ERROR"
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
		s.writeResult(w, req.ID, ToolResult{
			Content: []ToolContent{
				{Type: "text", Text: msg},
				{Type: LennyErrorContentType, Text: string(envelopeJSON)},
			},
			IsError: true,
		})
		return
	}
	s.writeResult(w, req.ID, result)
}

func (s *Server) writeResult(w http.ResponseWriter, id json.RawMessage, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	})
}

func (s *Server) writeError(w http.ResponseWriter, id json.RawMessage, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	// JSON-RPC transport errors still return HTTP 200; the error is
	// in the body. Parse errors are the exception — there is no id.
	_ = json.NewEncoder(w).Encode(jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &jsonRPCError{Code: code, Message: message},
	})
}

// WriteLennyError writes a JSON-RPC error response whose Data field
// carries the §15.2.1 lenny error envelope (code, category, message,
// retryable, details). The JSON-RPC code stays the generic
// errInvalidRequest for client-side errors and errInternal for
// server-side ones; the lenny code identifies the domain reason on
// both REST and MCP surfaces so the parity contract holds.
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
