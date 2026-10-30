// SPDX-License-Identifier: MIT

package mcp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// ClientName identifies the adapter's MCP client in the initialize
// handshake's clientInfo. A type: mcp runtime's agent is an MCP server
// and the adapter drives it as an MCP client (§5.1, §9.1).
const ClientName = "lenny-adapter"

// ClientInfo is the initialize handshake's clientInfo object.
type clientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InitializeResult is the subset of the MCP `initialize` response the
// adapter records: the negotiated protocol version and the server's
// self-reported identity.
type InitializeResult struct {
	ProtocolVersion string `json:"protocolVersion"`
	ServerInfo      struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"serverInfo"`
	Capabilities json.RawMessage `json:"capabilities"`
}

// ToolDescriptor is one entry of an MCP `tools/list` response.
type ToolDescriptor struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// Client is a minimal MCP client over a single bidirectional byte
// stream (a subprocess's stdio pipes in production). It speaks the
// JSON-RPC 2.0 request/response subset the adapter needs to drive a
// type: mcp runtime: `initialize`, `tools/list`, and `tools/call`.
//
// The client is synchronous: each call writes one request and blocks
// until the matching response arrives. A monotonic id generator tags
// requests; the reader matches responses by id. One call is in flight
// at a time, which matches the adapter's per-session, one-message-at-a-
// time relay for a type: mcp runtime.
type Client struct {
	w   io.Writer
	dec *json.Decoder

	mu     sync.Mutex
	nextID int64
	closed bool
}

// NewClient returns an MCP client that reads JSON-RPC responses from r
// and writes JSON-RPC requests to w. The caller owns the lifetime of r
// and w (in production, the subprocess pipes).
func NewClient(r io.Reader, w io.Writer) *Client {
	return &Client{
		w:   w,
		dec: json.NewDecoder(bufio.NewReader(r)),
	}
}

// Initialize performs the MCP `initialize` handshake and sends the
// `notifications/initialized` notification the MCP spec requires after
// a successful initialize. It returns the server's negotiated protocol
// version and identity.
func (c *Client) Initialize() (InitializeResult, error) {
	params := map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      clientInfo{Name: ClientName, Version: "1"},
	}
	raw, err := c.call(initializeMethod, params)
	if err != nil {
		return InitializeResult{}, err
	}
	var result InitializeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return InitializeResult{}, fmt.Errorf("mcp: decode initialize result: %w", err)
	}
	// The MCP spec has the client send `notifications/initialized` once
	// the handshake succeeds. It carries no id and draws no response.
	if err := c.notify("notifications/initialized", nil); err != nil {
		return InitializeResult{}, fmt.Errorf("mcp: send initialized notification: %w", err)
	}
	return result, nil
}

// ListTools issues an MCP `tools/list` request and returns the server's
// tool descriptors.
func (c *Client) ListTools() ([]ToolDescriptor, error) {
	raw, err := c.call("tools/list", nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Tools []ToolDescriptor `json:"tools"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("mcp: decode tools/list result: %w", err)
	}
	return result.Tools, nil
}

// CallTool issues an MCP `tools/call` request for the named tool with
// the given arguments object and returns the raw result. A nil
// arguments value is sent as an empty object.
func (c *Client) CallTool(name string, arguments json.RawMessage) (json.RawMessage, error) {
	if len(arguments) == 0 {
		arguments = json.RawMessage(`{}`)
	}
	params := map[string]json.RawMessage{
		"name":      mustMarshal(name),
		"arguments": arguments,
	}
	return c.call("tools/call", params)
}

// Close marks the client closed. Subsequent calls fail fast rather than
// blocking on a reader that will never produce a response. Close does
// not touch the underlying stream — the caller owns the subprocess
// pipes and closes them as part of process teardown.
func (c *Client) Close() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
}

// ErrClientClosed reports a call attempted after Close.
var ErrClientClosed = errors.New("mcp: client is closed")

// call writes one JSON-RPC request and blocks for the matching
// response. The adapter drives one call at a time, so the reader does
// not need to demultiplex; it does verify the response id matches and
// surfaces a JSON-RPC error result as a Go error.
func (c *Client) call(method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ErrClientClosed
	}
	c.nextID++
	id := c.nextID
	c.mu.Unlock()

	req := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		req["params"] = params
	}
	if err := c.writeMessage(req); err != nil {
		return nil, fmt.Errorf("mcp: write %s request: %w", method, err)
	}

	for {
		var resp rpcResponseIn
		if err := c.dec.Decode(&resp); err != nil {
			if err == io.EOF {
				return nil, fmt.Errorf("mcp: server closed the connection awaiting %s response", method)
			}
			return nil, fmt.Errorf("mcp: read %s response: %w", method, err)
		}
		// Skip server-initiated notifications and requests (no id, or an
		// id that does not match ours): the adapter does not implement the
		// server-to-client direction for a type: mcp runtime.
		if len(resp.ID) == 0 || string(resp.ID) == "null" {
			continue
		}
		var gotID int64
		if err := json.Unmarshal(resp.ID, &gotID); err != nil || gotID != id {
			continue
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("mcp: %s returned error %d: %s", method, resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	}
}

// notify writes a JSON-RPC notification (no id, no response expected).
func (c *Client) notify(method string, params any) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClientClosed
	}
	c.mu.Unlock()
	msg := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		msg["params"] = params
	}
	return c.writeMessage(msg)
}

// writeMessage encodes v as a single newline-delimited JSON object.
// Newline framing matches the server side of this package and the
// stdio convention reference MCP servers use.
func (c *Client) writeMessage(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = c.w.Write(b)
	return err
}

// rpcResponseIn is an inbound JSON-RPC response. Result and Error are
// decoded lazily so a successful call need not unmarshal the error
// object and vice versa.
type rpcResponseIn struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

func mustMarshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		// The only inputs are strings the adapter controls; a failure
		// here is a programming error, not a runtime condition.
		panic(fmt.Sprintf("mcp: marshal client param: %v", err))
	}
	return b
}
