// SPDX-License-Identifier: MIT

package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
)

// ProtocolVersion is the MCP spec version the adapter's local MCP
// servers speak (§15.4.3).
const ProtocolVersion = "2025-03-26"

// ServerName identifies the adapter's MCP server in the initialize
// handshake's serverInfo.
const ServerName = "lenny-adapter-mcp"

// JSON-RPC 2.0 error codes used by the MCP server.
const (
	errMethodNotFound = -32601
	errInvalidParams  = -32602
	errInternal       = -32603
)

// ToolHandler executes one MCP tool call. arguments is the raw
// `arguments` object from the tools/call request; the returned value is
// JSON-encoded into the response result.
type ToolHandler func(arguments json.RawMessage) (any, error)

// Tool is one tool exposed by an MCP server: its name, a human
// description, the JSON Schema for its arguments, and the handler that
// runs it.
type Tool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
	Handler     ToolHandler
}

// Server is an adapter-local MCP server (§15.4.3). It exposes a set of
// registered tools to a runtime over a single connection per ServeConn
// call; the connection is authenticated by the manifest-nonce
// handshake before any tool is dispatched.
type Server struct {
	tools map[string]Tool
}

// NewServer returns an MCP server with no tools registered.
func NewServer() *Server {
	return &Server{tools: make(map[string]Tool)}
}

// Register adds a tool to the server. A later registration with the
// same name replaces the earlier one.
func (s *Server) Register(t Tool) {
	s.tools[t.Name] = t
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// isNotification reports whether a request is a JSON-RPC notification
// (no id): the server must not send a response to one.
func (r rpcRequest) isNotification() bool {
	return len(r.ID) == 0 || string(r.ID) == "null"
}

// ServeConn handles one MCP connection. The first message must be the
// nonce-authenticated `initialize` request (§15.4.3); a connection that
// fails the nonce handshake is closed without dispatching any tool.
// After a successful handshake ServeConn serves tools/list and
// tools/call until the peer closes the connection.
func (s *Server) ServeConn(conn net.Conn, nonce string) error {
	defer conn.Close()
	dec := json.NewDecoder(conn)
	enc := json.NewEncoder(conn)

	var first json.RawMessage
	if err := dec.Decode(&first); err != nil {
		return fmt.Errorf("mcp: read initialize request: %w", err)
	}
	cleaned, err := AuthenticateInitialize(first, nonce)
	if err != nil {
		return err
	}
	var initReq rpcRequest
	if err := json.Unmarshal(cleaned, &initReq); err != nil {
		return fmt.Errorf("mcp: decode initialize request: %w", err)
	}
	if err := enc.Encode(s.initializeResponse(initReq.ID)); err != nil {
		return fmt.Errorf("mcp: write initialize response: %w", err)
	}

	for {
		var req rpcRequest
		if err := dec.Decode(&req); err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("mcp: read request: %w", err)
		}
		resp, respond := s.dispatch(req)
		if !respond {
			continue
		}
		if err := enc.Encode(resp); err != nil {
			return fmt.Errorf("mcp: write response: %w", err)
		}
	}
}

func (s *Server) initializeResponse(id json.RawMessage) rpcResponse {
	return rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result: map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": ServerName, "version": "1"},
		},
	}
}

// dispatch handles one post-handshake request. respond is false for a
// JSON-RPC notification, which receives no reply.
func (s *Server) dispatch(req rpcRequest) (resp rpcResponse, respond bool) {
	if req.isNotification() {
		return rpcResponse{}, false
	}
	switch req.Method {
	case "tools/list":
		return s.ok(req.ID, s.toolList()), true
	case "tools/call":
		return s.callTool(req)
	default:
		return s.fail(req.ID, errMethodNotFound, "unknown method "+req.Method), true
	}
}

func (s *Server) toolList() map[string]any {
	tools := make([]map[string]any, 0, len(s.tools))
	for _, t := range s.tools {
		descriptor := map[string]any{"name": t.Name, "description": t.Description}
		if len(t.InputSchema) > 0 {
			descriptor["inputSchema"] = t.InputSchema
		}
		tools = append(tools, descriptor)
	}
	return map[string]any{"tools": tools}
}

func (s *Server) callTool(req rpcRequest) (rpcResponse, bool) {
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &call); err != nil {
		return s.fail(req.ID, errInvalidParams, "malformed tools/call params"), true
	}
	tool, ok := s.tools[call.Name]
	if !ok {
		return s.fail(req.ID, errInvalidParams, "unknown tool "+call.Name), true
	}
	result, err := tool.Handler(call.Arguments)
	if err != nil {
		return s.fail(req.ID, errInternal, err.Error()), true
	}
	return s.ok(req.ID, result), true
}

func (s *Server) ok(id json.RawMessage, result any) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func (s *Server) fail(id json.RawMessage, code int, message string) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message}}
}
