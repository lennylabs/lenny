// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"time"
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

	// RequireChallenge enables the §4.7 nonce-only-mode challenge-response
	// supplement: after the manifest-nonce handshake, the adapter sends a
	// per-connection adapterChallenge and requires the agent's
	// HMAC-SHA256 reply before dispatching tools. The adapter sets it when
	// SO_PEERCRED is disabled (--require-so-peercred=false), where the
	// static nonce alone is replayable (spec lines 879-883).
	RequireChallenge bool

	// Provider supplies the §9.1 platform tools the intra-pod server does
	// not register locally: List backs tools/list and Call backs
	// tools/call for any name not registered locally. In production the
	// platform MCP server registers no tools directly and forwards every
	// tool to the gateway through the Provider, so the catalog and
	// dispatch stay in lockstep with the gateway-edge /mcp surface. Nil
	// leaves the server serving only its registered tools (the dev / test
	// default). spec: §9.1 lines 14-31. F-9.1.1.
	Provider ToolProvider
}

// ToolProvider supplies tools a Server forwards rather than handles
// locally. The §9.1 intra-pod platform MCP server uses it to proxy the
// platform tool catalog and dispatch to the gateway: List returns the
// catalog to advertise on tools/list, and Call dispatches a tools/call
// for a name the server does not register locally. spec: §9.1 lines
// 14-31. F-9.1.1.
type ToolProvider interface {
	// List returns the provider's tool catalog. The server appends it to
	// any locally-registered tools on tools/list.
	List(ctx context.Context) ([]Tool, error)
	// Call dispatches one tool invocation and returns the JSON-RPC
	// result. The returned bytes are the MCP tools/call result object (a
	// `content` array, optionally with `isError`); a tool-level failure
	// is carried inside that object, while a returned error is a
	// transport/routing failure the server reports as a JSON-RPC error.
	Call(ctx context.Context, name string, arguments json.RawMessage) (json.RawMessage, error)
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

// Serve accepts MCP connections on lis until ctx is cancelled or the
// listener fails, handling each accepted connection with ServeConn in
// its own goroutine. Production passes an abstract-Unix-socket listener
// (§15.4.3, Linux-only); the listener type does not affect the protocol
// handling. Every connection is authenticated by the manifest-nonce
// handshake before any tool is dispatched.
func (s *Server) Serve(ctx context.Context, lis net.Listener, nonce string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-ctx.Done()
		_ = lis.Close()
	}()
	for {
		conn, err := lis.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("mcp: accept connection: %w", err)
		}
		go func() { _ = s.ServeConn(conn, nonce) }()
	}
}

// ServeConn handles one MCP connection. The first message must be the
// nonce-authenticated `initialize` request (§15.4.3); a connection that
// fails the nonce handshake is closed without dispatching any tool.
// After a successful handshake ServeConn serves tools/list and
// tools/call until the peer closes the connection.
func (s *Server) ServeConn(conn net.Conn, nonce string) error {
	defer conn.Close()
	// The §9.1 Provider calls (tools/list, tools/call forwarding to the
	// gateway) apply their own per-call deadline; the connection lifetime
	// is bounded by the peer closing the socket and by Serve closing the
	// listener on ctx cancel.
	ctx := context.Background()
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
	// §4.7 lines 879-883: in nonce-only mode the static manifest nonce is
	// replayable, so supplement it with a per-connection challenge-response
	// before dispatching any tool. Failure closes the socket with no
	// protocol response.
	if s.RequireChallenge {
		if err := s.challenge(conn, dec, enc, nonce); err != nil {
			return err
		}
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
		resp, respond := s.dispatch(ctx, req)
		if !respond {
			continue
		}
		if err := enc.Encode(resp); err != nil {
			return fmt.Errorf("mcp: write response: %w", err)
		}
	}
}

// challenge runs the §4.7 nonce-only-mode challenge-response exchange on
// an already-nonce-authenticated connection: it sends a fresh 128-bit
// adapterChallenge, then reads the agent's HMAC reply under a 500 ms
// deadline and validates it as HMAC-SHA256(key=nonce, data=challenge). A
// missing field, a mismatch, or a timeout returns an error so ServeConn
// closes the socket without a protocol response (spec lines 879-883).
//
// The agent has not sent anything since its initialize request — it
// blocks for the challenge — so the decoder buffer is empty and the read
// observes the deadline. The deadline is cleared on return so the
// post-handshake tool loop reads without one.
func (s *Server) challenge(conn net.Conn, dec *json.Decoder, enc *json.Encoder, nonce string) error {
	challenge, err := newChallenge()
	if err != nil {
		return err
	}
	if err := enc.Encode(map[string]string{ChallengeParamKey: challenge}); err != nil {
		return fmt.Errorf("mcp: write adapterChallenge: %w", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(ChallengeTimeout)); err != nil {
		return fmt.Errorf("mcp: set challenge deadline: %w", err)
	}
	defer func() { _ = conn.SetReadDeadline(time.Time{}) }()

	var response json.RawMessage
	if err := dec.Decode(&response); err != nil {
		return fmt.Errorf("mcp: read challenge response: %w", err)
	}
	return ValidateChallengeResponse(response, nonce, challenge)
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
func (s *Server) dispatch(ctx context.Context, req rpcRequest) (resp rpcResponse, respond bool) {
	if req.isNotification() {
		return rpcResponse{}, false
	}
	switch req.Method {
	case "tools/list":
		list, err := s.toolList(ctx)
		if err != nil {
			return s.fail(req.ID, errInternal, err.Error()), true
		}
		return s.ok(req.ID, list), true
	case "tools/call":
		return s.callTool(ctx, req)
	default:
		return s.fail(req.ID, errMethodNotFound, "unknown method "+req.Method), true
	}
}

// toolList returns the locally-registered tool descriptors followed by
// the §9.1 Provider's catalog (when a Provider is wired), so the
// intra-pod platform MCP server advertises the gateway's platform tools
// without duplicating their schemas. F-9.1.1.
func (s *Server) toolList(ctx context.Context) (map[string]any, error) {
	tools := make([]map[string]any, 0, len(s.tools))
	for _, t := range s.tools {
		tools = append(tools, toolDescriptor(t))
	}
	if s.Provider != nil {
		provided, err := s.Provider.List(ctx)
		if err != nil {
			return nil, err
		}
		for _, t := range provided {
			tools = append(tools, toolDescriptor(t))
		}
	}
	return map[string]any{"tools": tools}, nil
}

func toolDescriptor(t Tool) map[string]any {
	descriptor := map[string]any{"name": t.Name, "description": t.Description}
	if len(t.InputSchema) > 0 {
		descriptor["inputSchema"] = t.InputSchema
	}
	return descriptor
}

func (s *Server) callTool(ctx context.Context, req rpcRequest) (rpcResponse, bool) {
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &call); err != nil {
		return s.fail(req.ID, errInvalidParams, "malformed tools/call params"), true
	}
	if tool, ok := s.tools[call.Name]; ok {
		result, err := tool.Handler(call.Arguments)
		if err != nil {
			return s.fail(req.ID, errInternal, err.Error()), true
		}
		return s.ok(req.ID, result), true
	}
	// §9.1: a tool the intra-pod server does not register locally is a
	// platform tool the Provider forwards to the gateway. F-9.1.1.
	if s.Provider != nil {
		result, err := s.Provider.Call(ctx, call.Name, call.Arguments)
		if err != nil {
			return s.fail(req.ID, errInternal, err.Error()), true
		}
		return s.ok(req.ID, result), true
	}
	return s.fail(req.ID, errInvalidParams, "unknown tool "+call.Name), true
}

func (s *Server) ok(id json.RawMessage, result any) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func (s *Server) fail(id json.RawMessage, code int, message string) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message}}
}
