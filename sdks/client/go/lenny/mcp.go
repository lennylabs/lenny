// SPDX-License-Identifier: MIT

package lenny

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
)

// mcpProtocolVersion is the MCP protocol revision this client requests
// in the §15.2 initialize handshake. The gateway negotiates the
// highest version it and the client both support; the negotiated
// value is reported by Initialize on InitializeResult.ProtocolVersion.
const mcpProtocolVersion = "2025-06-18"

// mcpClientName identifies this SDK in the §15.2 initialize
// clientInfo.
const mcpClientName = "lenny-client-sdk-go"

// MCPTool is one entry in the §15.2 MCP tool catalog returned by
// MCPClient.ListTools.
type MCPTool struct {
	// Name is the tool identifier, for example lenny/create_session.
	Name string `json:"name"`

	// Description is the human-readable tool description.
	Description string `json:"description"`

	// InputSchema is the JSON Schema for the tool's arguments object.
	InputSchema json.RawMessage `json:"inputSchema"`
}

// MCPToolResult is the §15.2 tools/call result. A tool that reports a
// failure returns IsError true with the failure text in Content; the
// JSON-RPC transport itself succeeded.
type MCPToolResult struct {
	// Content is the list of result content blocks.
	Content []MCPContent `json:"content"`

	// IsError reports whether the tool reported a failure. The MCP
	// spec surfaces a tool-level failure as a result with this flag
	// set rather than as a transport error.
	IsError bool `json:"isError,omitempty"`
}

// Text returns the concatenation of every text content block in the
// result. Non-text blocks are skipped.
func (r MCPToolResult) Text() string {
	var b strings.Builder
	for _, c := range r.Content {
		if c.Type == "text" {
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

// LennyError is the §15.2.1 shared error envelope a Lenny MCP tool
// surfaces in a `lenny/error` content block when IsError is true. The
// Code/Category/Retryable triple is the §15.2.1 REST↔MCP parity contract
// (item 5(d)): a client compares them across the REST and MCP surfaces
// and gets the same retry decision either way. spec: §15.2 line 944, 972.
type LennyError struct {
	// Code is the machine-readable error code from the §15.2.1 catalog
	// (for example `VALIDATION_ERROR`, `INVALID_STATE_TRANSITION`,
	// `INTERCEPTOR_WEAKENING_COOLDOWN`).
	Code string `json:"code"`

	// Category is the §16.3 envelope category, one of `TRANSIENT`,
	// `PERMANENT`, `POLICY`, `UPSTREAM`.
	Category string `json:"category"`

	// Message is the human-readable description.
	Message string `json:"message"`

	// Retryable reports whether the client should retry. Authoritative
	// across REST and MCP under the §15.2.1 parity contract.
	Retryable bool `json:"retryable"`

	// Details carries error-specific context. Structure varies by Code.
	Details json.RawMessage `json:"details,omitempty"`
}

// Error implements the error interface so callers can return *LennyError
// directly from helpers that unwrap a §15.2.1 failure envelope.
func (e *LennyError) Error() string {
	if e == nil {
		return ""
	}
	if e.Code == "" {
		return e.Message
	}
	return fmt.Sprintf("lenny: %s (%s, retryable=%t): %s",
		e.Code, e.Category, e.Retryable, e.Message)
}

// LennyError returns the §15.2.1 error envelope surfaced in the result's
// `lenny/error` content block, when present. Returns nil when the
// result carries no such block (a transport-level success without a
// tool-level failure envelope) or when the envelope JSON is malformed —
// callers that want to fall back to the human Text() can do so on nil.
// F-15.2.10.
func (r MCPToolResult) LennyError() *LennyError {
	for _, c := range r.Content {
		if c.Type != "lenny/error" {
			continue
		}
		var env LennyError
		if err := json.Unmarshal([]byte(c.Text), &env); err == nil {
			return &env
		}
	}
	return nil
}

// MCPContent is one content block in an MCP tool result.
type MCPContent struct {
	// Type is the content block type. The gateway tools emit text.
	Type string `json:"type"`

	// Text carries the inline text when Type is text.
	Text string `json:"text,omitempty"`
}

// InitializeResult is the §15.2 initialize handshake response.
type InitializeResult struct {
	// ProtocolVersion is the MCP spec version the gateway negotiated.
	// The connection is pinned to this version for its lifetime.
	ProtocolVersion string `json:"protocolVersion"`

	// Capabilities is the gateway's advertised MCP capability set.
	Capabilities map[string]any `json:"capabilities"`

	// ServerInfo identifies the gateway MCP server.
	ServerInfo MCPServerInfo `json:"serverInfo"`
}

// MCPServerInfo identifies an MCP server in the initialize response.
type MCPServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// MCPError is the typed form of a §15.2 JSON-RPC error object. A
// tools/call that fails at the transport level (unknown tool, invalid
// params) returns this error; a tool that runs and reports a failure
// returns an MCPToolResult with IsError set instead. MCPError
// implements the error interface.
type MCPError struct {
	// Code is the JSON-RPC error code. The MCP and JSON-RPC 2.0
	// reserved codes are negative (for example -32601 method not
	// found, -32602 invalid params).
	Code int `json:"code"`

	// Message is the human-readable error description.
	Message string `json:"message"`

	// Data carries error-specific context when the gateway supplies
	// it.
	Data any `json:"data,omitempty"`
}

// Error implements the error interface.
func (e *MCPError) Error() string {
	return fmt.Sprintf("lenny: MCP error %d: %s", e.Code, e.Message)
}

// MCPClient drives the §15.2 gateway MCP API. It speaks JSON-RPC 2.0
// over HTTP POST to the gateway's /mcp endpoint: the same connection
// carries the initialize handshake, tool discovery, and tool calls.
//
// Construct an MCPClient with Client.MCP so it inherits the REST
// client's base URL, HTTP client, authentication credential, and
// tenant header. An MCPClient is safe for concurrent use by multiple
// goroutines.
type MCPClient struct {
	endpoint   string
	httpClient *http.Client
	auth       Authenticator
	tenantID   string

	// idSeq assigns a monotonically increasing JSON-RPC request id so
	// concurrent callers do not collide on the id field.
	idSeq atomic.Uint64

	// mu guards initialized so a concurrent first call observes a
	// consistent handshake state.
	mu          sync.Mutex
	initialized bool
}

// MCP returns an MCPClient bound to the same gateway as the REST
// Client. The MCPClient targets the gateway's /mcp endpoint and
// reuses the Client's HTTP client, authentication credential, and
// development tenant header.
func (c *Client) MCP() *MCPClient {
	return &MCPClient{
		endpoint:   c.baseURL + "/mcp",
		httpClient: c.httpClient,
		auth:       c.auth,
		tenantID:   c.tenantID,
	}
}

// Initialize performs the §15.2 MCP initialize handshake. It sends the
// client's supported protocol version and clientInfo, and returns the
// gateway's negotiated protocol version, capability set, and
// serverInfo.
//
// Calling Initialize is optional before ListTools or CallTool: those
// methods perform the handshake on first use when it has not run yet.
// Call it explicitly to read the negotiated protocol version or the
// gateway serverInfo.
func (m *MCPClient) Initialize(ctx context.Context) (*InitializeResult, error) {
	params := map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    mcpClientName,
			"version": "0.1.0",
		},
	}
	raw, err := m.call(ctx, "initialize", params)
	if err != nil {
		return nil, err
	}
	var out InitializeResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("lenny: decode MCP initialize result: %w", err)
	}
	m.mu.Lock()
	m.initialized = true
	m.mu.Unlock()
	return &out, nil
}

// ListTools calls the §15.2 tools/list method and returns the platform
// MCP tool catalog (lenny/create_session, lenny/send_message, and the
// others). It runs the initialize handshake first when it has not run
// yet.
func (m *MCPClient) ListTools(ctx context.Context) ([]MCPTool, error) {
	if err := m.ensureInitialized(ctx); err != nil {
		return nil, err
	}
	raw, err := m.call(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var out struct {
		Tools []MCPTool `json:"tools"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("lenny: decode MCP tools/list result: %w", err)
	}
	return out.Tools, nil
}

// CallTool calls the §15.2 tools/call method, invoking the named tool
// with arguments. arguments is marshaled to the JSON-RPC arguments
// object; pass nil for a tool that takes no arguments.
//
// A transport-level failure (unknown tool, invalid params) returns an
// *MCPError. A tool that runs and reports a failure returns a
// non-nil MCPToolResult with IsError set and a nil error, matching
// the MCP contract that a tool failure is a result rather than a
// transport error. CallTool runs the initialize handshake first when
// it has not run yet.
func (m *MCPClient) CallTool(ctx context.Context, name string, arguments any) (*MCPToolResult, error) {
	if err := m.ensureInitialized(ctx); err != nil {
		return nil, err
	}
	if name == "" {
		return nil, fmt.Errorf("lenny: MCP tool name is required")
	}
	argsJSON := json.RawMessage("{}")
	if arguments != nil {
		b, err := json.Marshal(arguments)
		if err != nil {
			return nil, fmt.Errorf("lenny: marshal MCP tool arguments: %w", err)
		}
		argsJSON = b
	}
	raw, err := m.call(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": argsJSON,
	})
	if err != nil {
		return nil, err
	}
	var out MCPToolResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("lenny: decode MCP tools/call result: %w", err)
	}
	return &out, nil
}

// MCPCreateSessionResult is the decoded result of the
// lenny/create_session MCP tool.
type MCPCreateSessionResult struct {
	// SessionID is the identifier of the created session.
	SessionID string `json:"sessionId"`

	// State is the session state the gateway reports for the new
	// session.
	State string `json:"state"`
}

// CreateSession invokes the §15.2 lenny/create_session MCP tool and
// returns the created session identifier and state. It is the MCP
// counterpart of Client.CreateSession.
func (m *MCPClient) CreateSession(ctx context.Context, runtimeRef, userID string) (*MCPCreateSessionResult, error) {
	args := map[string]any{"runtimeRef": runtimeRef}
	if userID != "" {
		args["userId"] = userID
	}
	res, err := m.CallTool(ctx, "lenny/create_session", args)
	if err != nil {
		return nil, err
	}
	if res.IsError {
		return nil, fmt.Errorf("lenny: lenny/create_session reported a failure: %s", res.Text())
	}
	var out MCPCreateSessionResult
	if err := json.Unmarshal([]byte(res.Text()), &out); err != nil {
		return nil, fmt.Errorf("lenny: decode lenny/create_session result: %w", err)
	}
	return &out, nil
}

// SendMessage invokes the §15.2 lenny/send_message MCP tool, delivering
// content to the session and returning the agent's text reply. It is
// the MCP counterpart of a §15.1 send-message REST call.
func (m *MCPClient) SendMessage(ctx context.Context, sessionID, content string) (string, error) {
	res, err := m.CallTool(ctx, "lenny/send_message", map[string]any{
		"sessionId": sessionID,
		"content":   content,
	})
	if err != nil {
		return "", err
	}
	if res.IsError {
		return "", fmt.Errorf("lenny: lenny/send_message reported a failure: %s", res.Text())
	}
	return res.Text(), nil
}

// ensureInitialized runs the initialize handshake once. A second call
// after a successful handshake is a no-op.
func (m *MCPClient) ensureInitialized(ctx context.Context) error {
	m.mu.Lock()
	done := m.initialized
	m.mu.Unlock()
	if done {
		return nil
	}
	_, err := m.Initialize(ctx)
	return err
}

// mcpRPCRequest is the JSON-RPC 2.0 request envelope sent to the
// gateway MCP endpoint.
type mcpRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      uint64          `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// mcpRPCResponse is the JSON-RPC 2.0 response envelope the gateway MCP
// endpoint returns.
type mcpRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *MCPError       `json:"error,omitempty"`
}

// call executes one JSON-RPC 2.0 method against the gateway MCP
// endpoint and returns the raw result. A JSON-RPC error object in the
// response is returned as an *MCPError; a non-2xx HTTP status is
// returned as the typed REST *APIError so a single error-handling
// strategy covers both surfaces.
func (m *MCPClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	var paramsJSON json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("lenny: marshal MCP %s params: %w", method, err)
		}
		paramsJSON = b
	}
	reqBody, err := json.Marshal(mcpRPCRequest{
		JSONRPC: "2.0",
		ID:      m.idSeq.Add(1),
		Method:  method,
		Params:  paramsJSON,
	})
	if err != nil {
		return nil, fmt.Errorf("lenny: marshal MCP %s request: %w", method, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("lenny: build MCP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	if m.tenantID != "" {
		req.Header.Set("X-Lenny-Tenant-ID", m.tenantID)
	}
	if m.auth != nil {
		if err := m.auth.Apply(req); err != nil {
			return nil, fmt.Errorf("lenny: apply auth: %w", err)
		}
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("lenny: read MCP response body: %w", err)
	}
	// A JSON-RPC transport error still returns HTTP 200; the error is
	// in the body. A non-2xx status is a gateway-level failure (auth
	// rejection, an unmounted endpoint) and is surfaced as the typed
	// REST error.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, decodeAPIError(resp.StatusCode, respBody)
	}

	var rpc mcpRPCResponse
	if err := json.Unmarshal(respBody, &rpc); err != nil {
		return nil, fmt.Errorf("lenny: decode MCP %s response: %w", method, err)
	}
	if rpc.Error != nil {
		return nil, rpc.Error
	}
	return rpc.Result, nil
}
