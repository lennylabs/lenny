// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"nhooyr.io/websocket"
)

// §4.1 / §15.2 streamable MCP transport — the WebSocket alternative to
// the single-shot POST /mcp JSON-RPC endpoint. Per §4.1 the gateway
// proxies long-lived interactive streams over WebSocket / gRPC bidi /
// Streamable HTTP; this file implements the WebSocket leg. Each
// inbound text frame is one JSON-RPC 2.0 request; the response goes
// back as a text frame on the same connection. The same dispatch
// table (initialize, tools/list, tools/call, ping) the HTTP transport
// uses applies here so a client that speaks MCP over WebSocket sees
// identical semantics.

// wsReadFrameTimeout bounds how long the gateway waits for a client
// frame between requests. A WebSocket connection that goes silent for
// the full timeout is closed; clients should ping the connection
// (the upgrader honors the standard control frames) to keep it open.
const wsReadFrameTimeout = 5 * time.Minute

// wsWriteTimeout bounds how long the gateway will spend writing a
// single response frame. A subscriber whose network buffers are full
// MUST not block the dispatcher for the entire request lifetime.
const wsWriteTimeout = 30 * time.Second

// wsMaxMessageBytes is the per-frame ceiling for inbound WebSocket
// messages. It matches the http.MaxBytesReader cap on the POST /mcp
// handler (1 MiB).
const wsMaxMessageBytes = 1 << 20

// WebSocketHandler returns the http.Handler that serves the §4.1
// streaming MCP transport at /mcp/v1/ws. The handler upgrades the
// connection, then reads JSON-RPC frames in a loop, dispatching each
// through the same handler logic that backs POST /mcp.
//
// The handler returns when the client closes the connection, when
// the request context is cancelled (graceful shutdown), or when an
// I/O error is observed. Connection close is logged as a normal
// closure unless the dispatcher's error indicates a protocol
// violation. The path prefix /mcp/v1/ws matches the playground UI
// config in pkg/gateway/playground/assets.go.
func (s *Server) WebSocketHandler() http.Handler {
	return http.HandlerFunc(s.handleWebSocket)
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	// §4.1 — Accept the upgrade with no Origin-list restriction; the
	// gateway expects upstream middleware (auth, CSP) to bound which
	// callers reach this handler. CompressionMode is disabled because
	// JSON-RPC frames are small and the compression layer adds CPU on
	// the hot dispatch path.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		return
	}
	conn.SetReadLimit(wsMaxMessageBytes)
	defer conn.Close(websocket.StatusNormalClosure, "")

	for {
		readCtx, cancelRead := context.WithTimeout(r.Context(), wsReadFrameTimeout)
		msgType, data, err := conn.Read(readCtx)
		cancelRead()
		if err != nil {
			// A normal close, idle timeout, or context cancellation
			// terminates the loop without writing further frames.
			return
		}
		if msgType != websocket.MessageText {
			// MCP frames are JSON text per the spec; a binary frame is a
			// protocol violation and the connection is closed.
			conn.Close(websocket.StatusUnsupportedData, "mcp frames must be text")
			return
		}
		// Dispatch through the same JSON-RPC handler the POST /mcp
		// path uses so REST/MCP semantics stay in lockstep per
		// §15.2.1. dispatchRPC returns the response envelope ready for
		// JSON encoding.
		respBytes, fatal := s.dispatchFrameBytes(r.Context(), data)
		writeCtx, cancelWrite := context.WithTimeout(r.Context(), wsWriteTimeout)
		writeErr := conn.Write(writeCtx, websocket.MessageText, respBytes)
		cancelWrite()
		if writeErr != nil {
			return
		}
		if fatal {
			// A non-recoverable transport error (the parse layer
			// returned a parse-error envelope without an id) closes
			// the connection so the client reconnects with a fresh
			// state.
			conn.Close(websocket.StatusProtocolError, "json-rpc parse error")
			return
		}
	}
}

// dispatchFrameBytes parses raw JSON-RPC bytes, dispatches through the
// existing tool handlers, and returns the response envelope as a JSON
// byte slice. The second return is true when the frame caused a fatal
// transport error (a parse error with no id), in which case the
// caller closes the connection after writing the envelope.
func (s *Server) dispatchFrameBytes(ctx context.Context, data []byte) ([]byte, bool) {
	var req jsonRPCRequest
	if err := json.Unmarshal(data, &req); err != nil {
		// JSON-RPC parse-error envelope per §15.2.1 / the MCP spec.
		envelope := jsonRPCResponse{
			JSONRPC: "2.0",
			Error:   &jsonRPCError{Code: errParse, Message: "request is not valid JSON"},
		}
		b, _ := json.Marshal(envelope)
		return b, true
	}
	if req.JSONRPC != "2.0" {
		envelope := jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &jsonRPCError{Code: errInvalidRequest, Message: "jsonrpc must be \"2.0\""},
		}
		b, _ := json.Marshal(envelope)
		return b, false
	}

	switch req.Method {
	case "initialize":
		return marshalResult(req.ID, map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "lenny-gateway", "version": "0.1.0"},
		}), false
	case "tools/list":
		return marshalResult(req.ID, map[string]any{"tools": s.toolList()}), false
	case "tools/call":
		return s.dispatchToolCall(ctx, req), false
	case "ping":
		return marshalResult(req.ID, map[string]any{}), false
	default:
		envelope := jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &jsonRPCError{Code: errMethodNotFound, Message: "unknown method " + req.Method},
		}
		b, _ := json.Marshal(envelope)
		return b, false
	}
}

// dispatchToolCall is the WebSocket leg's tools/call handler. It
// mirrors handleToolCall (the POST /mcp implementation) but writes
// its envelope to a byte slice instead of an http.ResponseWriter so
// the WebSocket reader loop can send the encoded frame back through
// the connection's Write method.
func (s *Server) dispatchToolCall(ctx context.Context, req jsonRPCRequest) []byte {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		envelope := jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &jsonRPCError{Code: errInvalidParams, Message: "params is not a valid tools/call object"},
		}
		b, _ := json.Marshal(envelope)
		return b
	}
	handler, ok := s.handlers[params.Name]
	if !ok {
		envelope := jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &jsonRPCError{Code: errMethodNotFound, Message: "unknown tool " + params.Name},
		}
		b, _ := json.Marshal(envelope)
		return b
	}
	result, err := handler(ctx, params.Arguments)
	if err != nil {
		// §15.2.1 / §15.2 REST↔MCP error envelope parity: surface the
		// lenny error envelope alongside the human-readable text.
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
		return marshalResult(req.ID, ToolResult{
			Content: []ToolContent{
				{Type: "text", Text: msg},
				{Type: LennyErrorContentType, Text: string(envelopeJSON)},
			},
			IsError: true,
		})
	}
	return marshalResult(req.ID, result)
}

// marshalResult serializes a JSON-RPC success envelope for the
// WebSocket transport.
func marshalResult(id json.RawMessage, result any) []byte {
	envelope := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	b, _ := json.Marshal(envelope)
	return b
}
