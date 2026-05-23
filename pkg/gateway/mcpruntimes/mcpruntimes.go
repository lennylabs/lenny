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

	"github.com/lennylabs/lenny/pkg/gateway/errorclassify"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
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
	if err := validateJSONRPC(body); err != nil {
		writeError(w, http.StatusBadRequest, errParse,
			"VALIDATION_ERROR", err.Error(), nil)
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

func validateJSONRPC(body []byte) error {
	if len(body) == 0 {
		return errors.New("request body is empty")
	}
	var req jsonRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return errors.New("request is not valid JSON")
	}
	if req.JSONRPC != "2.0" {
		return errors.New(`jsonrpc must be "2.0"`)
	}
	if req.Method == "" {
		return errors.New("method is required")
	}
	return nil
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
