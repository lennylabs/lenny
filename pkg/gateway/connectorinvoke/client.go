// SPDX-License-Identifier: MIT

// Package connectorinvoke is the gateway's outbound Streamable-HTTP MCP
// client for registered §9.3 connectors. The connector registry is the
// SSRF allowlist for external MCP traffic; this package is the dialer
// that actually issues `initialize`, `tools/list`, and `tools/call` to a
// registered connector endpoint and carries the gateway-held connector
// credential on the call.
//
// spec: §9.1 line 10 ("Gateway ↔ external MCP tools | MCP | Tool
// invocation, OAuth flows"); §9.3 lines 142-164 (the gateway acts as
// MCP client to the external tool). The OAuth flow stores the
// credential; this package is the piece that uses it.
package connectorinvoke

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
)

// ProtocolVersion is the MCP protocol revision the gateway advertises
// on the outbound `initialize` handshake to a connector. The server
// echoes its negotiated version, which the session records.
const ProtocolVersion = "2025-06-18"

// clientName identifies the gateway in the connector handshake's
// clientInfo.
const clientName = "lenny-gateway"

// Doer is the subset of *http.Client the transport needs. Tests inject
// a fake; production wires an *http.Client with the egress timeout.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client issues JSON-RPC 2.0 messages to a connector's Streamable-HTTP
// MCP endpoint. It is stateless across connectors; a per-endpoint
// handshake produces a Session that carries the negotiated protocol
// version and the server-assigned Mcp-Session-Id.
type Client struct {
	http Doer
}

// New returns a Client that issues requests through doer. A nil doer
// falls back to http.DefaultClient; production must pass a client with
// a bounded timeout because the endpoint is an untrusted external host.
func New(doer Doer) *Client {
	if doer == nil {
		doer = http.DefaultClient
	}
	return &Client{http: doer}
}

// InitializeResult is the subset of the MCP `initialize` response the
// gateway records.
type InitializeResult struct {
	ProtocolVersion string `json:"protocolVersion"`
	ServerInfo      struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"serverInfo"`
	Capabilities json.RawMessage `json:"capabilities"`
}

// ToolDescriptor is one entry of an MCP `tools/list` response, including
// the §5.1 annotations the gateway feeds to capability inference.
type ToolDescriptor struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	InputSchema json.RawMessage  `json:"inputSchema"`
	Annotations *ToolAnnotations `json:"annotations,omitempty"`
}

// ToolAnnotations is the §5.1 MCP annotation block. Fields are pointers
// so an absent hint is distinguishable from an explicit false.
type ToolAnnotations struct {
	ReadOnlyHint    *bool `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool `json:"destructiveHint,omitempty"`
	OpenWorldHint   *bool `json:"openWorldHint,omitempty"`
}

// Session is a negotiated MCP connection to one connector endpoint. A
// Session is not safe for concurrent use; callers serialize requests.
type Session struct {
	client    *Client
	endpoint  string
	bearer    string
	sessionID string
	protocol  string
	nextID    int64
}

// NegotiatedVersion reports the protocol version the connector selected
// on the handshake.
func (s *Session) NegotiatedVersion() string { return s.protocol }

// Initialize performs the MCP `initialize` handshake against endpoint,
// carrying bearer as the Authorization credential when non-empty, then
// sends the `notifications/initialized` notification the MCP spec
// requires. It returns a Session bound to the negotiated version and the
// server-assigned Mcp-Session-Id (when the server issues one).
func (c *Client) Initialize(ctx context.Context, endpoint, bearer string) (*Session, InitializeResult, error) {
	s := &Session{client: c, endpoint: endpoint, bearer: bearer}
	params := map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]string{"name": clientName, "version": "1"},
	}
	raw, sessionID, err := s.call(ctx, "initialize", params)
	if err != nil {
		return nil, InitializeResult{}, err
	}
	var result InitializeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, InitializeResult{}, fmt.Errorf("connectorinvoke: decode initialize result: %w", err)
	}
	s.sessionID = sessionID
	s.protocol = result.ProtocolVersion
	if err := s.notify(ctx, "notifications/initialized", nil); err != nil {
		return nil, InitializeResult{}, fmt.Errorf("connectorinvoke: send initialized notification: %w", err)
	}
	return s, result, nil
}

// ListTools issues `tools/list` and returns the connector's tool
// descriptors.
func (s *Session) ListTools(ctx context.Context) ([]ToolDescriptor, error) {
	raw, _, err := s.call(ctx, "tools/list", nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Tools []ToolDescriptor `json:"tools"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("connectorinvoke: decode tools/list result: %w", err)
	}
	return result.Tools, nil
}

// CallTool issues `tools/call` for the named tool. A nil arguments value
// is sent as an empty object. The raw JSON result is returned verbatim.
func (s *Session) CallTool(ctx context.Context, name string, arguments json.RawMessage) (json.RawMessage, error) {
	if len(arguments) == 0 {
		arguments = json.RawMessage(`{}`)
	}
	params := map[string]any{"name": name, "arguments": arguments}
	raw, _, err := s.call(ctx, "tools/call", params)
	return raw, err
}

// rpcError is a JSON-RPC 2.0 error object.
type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("connectorinvoke: rpc error %d: %s", e.Code, e.Message)
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

// call POSTs a JSON-RPC request and returns the result payload plus any
// Mcp-Session-Id the server assigned on this response. It accepts both
// the application/json single-response form and the text/event-stream
// (SSE) form of the Streamable-HTTP transport.
func (s *Session) call(ctx context.Context, method string, params any) (json.RawMessage, string, error) {
	id := atomic.AddInt64(&s.nextID, 1)
	body := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		body["params"] = params
	}
	resp, sessionID, err := s.post(ctx, body)
	if err != nil {
		return nil, "", err
	}
	if resp.Error != nil {
		return nil, sessionID, resp.Error
	}
	return resp.Result, sessionID, nil
}

// notify POSTs a JSON-RPC notification (no id, no response body
// expected). A 2xx with an empty or SSE body is treated as success.
func (s *Session) notify(ctx context.Context, method string, params any) error {
	body := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		body["params"] = params
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := s.newRequest(ctx, buf)
	if err != nil {
		return err
	}
	httpResp, err := s.client.http.Do(req)
	if err != nil {
		return fmt.Errorf("connectorinvoke: %s: %w", method, err)
	}
	defer drainClose(httpResp.Body)
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return fmt.Errorf("connectorinvoke: %s: unexpected status %d", method, httpResp.StatusCode)
	}
	return nil
}

func (s *Session) post(ctx context.Context, body map[string]any) (rpcResponse, string, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return rpcResponse{}, "", err
	}
	req, err := s.newRequest(ctx, buf)
	if err != nil {
		return rpcResponse{}, "", err
	}
	httpResp, err := s.client.http.Do(req)
	if err != nil {
		return rpcResponse{}, "", fmt.Errorf("connectorinvoke: post: %w", err)
	}
	defer drainClose(httpResp.Body)
	sessionID := httpResp.Header.Get("Mcp-Session-Id")
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return rpcResponse{}, sessionID, fmt.Errorf("connectorinvoke: unexpected status %d", httpResp.StatusCode)
	}
	payload, err := readJSONRPC(httpResp)
	if err != nil {
		return rpcResponse{}, sessionID, err
	}
	var resp rpcResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return rpcResponse{}, sessionID, fmt.Errorf("connectorinvoke: decode response: %w", err)
	}
	return resp, sessionID, nil
}

func (s *Session) newRequest(ctx context.Context, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("connectorinvoke: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if s.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+s.bearer)
	}
	if s.protocol != "" {
		req.Header.Set("MCP-Protocol-Version", s.protocol)
	}
	if s.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", s.sessionID)
	}
	return req, nil
}

// readJSONRPC extracts the JSON-RPC payload from a Streamable-HTTP
// response. application/json carries the object directly;
// text/event-stream carries it as the first `data:` event's payload.
func readJSONRPC(resp *http.Response) ([]byte, error) {
	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "text/event-stream") {
		return firstSSEData(resp.Body)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
}

// firstSSEData reads an SSE stream and returns the JSON payload of the
// first event carrying a non-empty `data:` field. A `data:` value may
// span multiple lines per the SSE spec; they are joined with newlines.
func firstSSEData(r io.Reader) ([]byte, error) {
	sc := bufio.NewScanner(io.LimitReader(r, maxResponseBytes))
	sc.Buffer(make([]byte, 0, 64*1024), maxResponseBytes)
	var data []string
	flush := func() ([]byte, bool) {
		if len(data) == 0 {
			return nil, false
		}
		return []byte(strings.Join(data, "\n")), true
	}
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			if out, ok := flush(); ok {
				return out, nil
			}
			data = nil
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("connectorinvoke: read event stream: %w", err)
	}
	if out, ok := flush(); ok {
		return out, nil
	}
	return nil, errors.New("connectorinvoke: event stream carried no data event")
}

// maxResponseBytes caps a connector response so a hostile or runaway
// endpoint cannot exhaust gateway memory.
const maxResponseBytes = 4 << 20 // 4 MiB

func drainClose(rc io.ReadCloser) {
	if rc == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(rc, maxResponseBytes))
	_ = rc.Close()
}
