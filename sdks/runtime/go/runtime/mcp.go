// SPDX-License-Identifier: MIT

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// mcpProtocolVersion is the §15.4.3 intra-pod MCP spec version the
// adapter's local MCP servers speak.
const mcpProtocolVersion = "2025-03-26"

// nonceParamKey is the §15.4.3 canonical injection key for the intra-pod
// MCP nonce: the top-level field of the MCP initialize request's params
// object. The adapter validates and strips it before forwarding the
// request to its MCP server implementation.
const nonceParamKey = "_lennyNonce"

// Tools is the §15.7 platform MCP tool surface a Standard-level or
// Full-level runtime uses. It wraps the §15.4.3 intra-pod MCP client to
// the adapter's platform MCP server and exposes typed helpers for the
// §8.5 / §4.7 platform tool set.
//
// A handler reaches Tools through SessionTools, which is populated once
// Run is configured with WithStandardLevel or WithFullLevel.
type Tools struct {
	platform   *mcpClient
	connectors map[string]*mcpClient
}

// Connector returns the MCP client for the named §4.7 connector MCP
// server, or nil when no connector with that id was advertised.
func (t *Tools) Connector(id string) *mcpClient {
	if t == nil {
		return nil
	}
	return t.connectors[id]
}

// close releases the platform and connector MCP connections.
func (t *Tools) close() {
	if t == nil {
		return
	}
	if t.platform != nil {
		t.platform.close()
	}
	for _, c := range t.connectors {
		c.close()
	}
}

// dialTools dials the §15.4.3 platform MCP server and every connector
// MCP server advertised in the manifest, completing the manifest-nonce
// handshake on each.
func (s *session) dialTools(ctx context.Context) (*Tools, error) {
	if s.manifest == nil {
		return nil, errors.New("no adapter manifest; Standard level requires the manifest")
	}
	if s.manifest.PlatformMCPServer == nil || s.manifest.PlatformMCPServer.Socket == "" {
		return nil, errors.New("adapter manifest has no platform MCP server socket")
	}
	platform, err := connectMCP(ctx, s.manifest.PlatformMCPServer.Socket, s.manifest.MCPNonce, "lenny-runtime-sdk-go", s.cfg.dialTimeout)
	if err != nil {
		return nil, fmt.Errorf("connect platform MCP server: %w", err)
	}
	tools := &Tools{platform: platform, connectors: map[string]*mcpClient{}}
	for _, conn := range s.manifest.ConnectorServers {
		cc, err := connectMCP(ctx, conn.Socket, s.manifest.MCPNonce, "lenny-runtime-sdk-go", s.cfg.dialTimeout)
		if err != nil {
			tools.close()
			return nil, fmt.Errorf("connect connector MCP server %q: %w", conn.ID, err)
		}
		tools.connectors[conn.ID] = cc
	}
	return tools, nil
}

// --- typed §8.5 / §4.7 platform tool helpers ---------------------------

// TaskHandle is the §8.2 lenny/delegate_task return value.
type TaskHandle struct {
	TaskID string `json:"taskId"`
}

// TaskResult mirrors the §8.8 TaskResult schema returned by
// lenny/await_children, restricted to the fields the SDK decodes.
type TaskResult struct {
	SchemaVersion int            `json:"schemaVersion,omitempty"`
	TaskID        string         `json:"taskId"`
	State         string         `json:"state"`
	Output        TaskOutput     `json:"output"`
	Error         *ResponseError `json:"error,omitempty"`
}

// TaskOutput is the output object of a §8.8 TaskResult.
type TaskOutput struct {
	Parts []MessagePart `json:"parts,omitempty"`
}

// DelegateTask invokes the §8.2 lenny/delegate_task platform tool. It
// spawns a child sub-task whose input is parts and returns the child
// TaskHandle. budget, when non-nil, is forwarded as the delegation
// budget metadata the §8.3 policy validates.
func (t *Tools) DelegateTask(target string, parts []MessagePart, budget map[string]any) (TaskHandle, error) {
	if t == nil || t.platform == nil {
		return TaskHandle{}, errors.New("delegate_task requires the platform MCP server (Standard level)")
	}
	task := map[string]any{"input": stampParts(parts)}
	if budget != nil {
		task["budget"] = budget
	}
	raw, err := t.platform.callTool("lenny/delegate_task", map[string]any{
		"target": target,
		"task":   task,
	})
	if err != nil {
		return TaskHandle{}, err
	}
	var h TaskHandle
	if err := json.Unmarshal(raw, &h); err != nil {
		return TaskHandle{}, fmt.Errorf("decode TaskHandle: %w", err)
	}
	if h.TaskID == "" {
		return TaskHandle{}, errors.New("lenny/delegate_task returned an empty taskId")
	}
	return h, nil
}

// AwaitChildren invokes the §8.5 lenny/await_children platform tool. It
// blocks until the named children settle per mode (all, any, settled)
// and returns their TaskResult values. A single-result encoding is
// normalized to a one-element slice.
func (t *Tools) AwaitChildren(childIDs []string, mode string) ([]TaskResult, error) {
	if t == nil || t.platform == nil {
		return nil, errors.New("await_children requires the platform MCP server (Standard level)")
	}
	if mode == "" {
		mode = "all"
	}
	raw, err := t.platform.callTool("lenny/await_children", map[string]any{
		"child_ids": childIDs,
		"mode":      mode,
	})
	if err != nil {
		return nil, err
	}
	return decodeTaskResults(raw)
}

// CancelChild invokes the §8.5 lenny/cancel_child platform tool.
func (t *Tools) CancelChild(childID string) error {
	if t == nil || t.platform == nil {
		return errors.New("cancel_child requires the platform MCP server (Standard level)")
	}
	_, err := t.platform.callTool("lenny/cancel_child", map[string]any{"child_id": childID})
	return err
}

// DiscoverAgents invokes the §4.7 lenny/discover_agents platform tool
// and returns the raw result for the runtime to decode.
func (t *Tools) DiscoverAgents(query map[string]any) (json.RawMessage, error) {
	if t == nil || t.platform == nil {
		return nil, errors.New("discover_agents requires the platform MCP server (Standard level)")
	}
	return t.platform.callTool("lenny/discover_agents", query)
}

// Output invokes the §4.7 lenny/output platform tool, emitting output
// parts incrementally to the parent or client. The stdout response
// frame is still required to signal turn completion (§15.4.1).
func (t *Tools) Output(parts []MessagePart) error {
	if t == nil || t.platform == nil {
		return errors.New("output requires the platform MCP server (Standard level)")
	}
	_, err := t.platform.callTool("lenny/output", map[string]any{"output": stampParts(parts)})
	return err
}

// RequestInput invokes the §4.7 lenny/request_input platform tool. It
// blocks until an answer arrives and returns the answer parts.
func (t *Tools) RequestInput(prompt []MessagePart) ([]MessagePart, error) {
	if t == nil || t.platform == nil {
		return nil, errors.New("request_input requires the platform MCP server (Standard level)")
	}
	raw, err := t.platform.callTool("lenny/request_input", map[string]any{"parts": stampParts(prompt)})
	if err != nil {
		return nil, err
	}
	return decodeParts(raw)
}

// RequestElicitation invokes the §4.7 lenny/request_elicitation
// platform tool and returns the raw result.
func (t *Tools) RequestElicitation(args map[string]any) (json.RawMessage, error) {
	if t == nil || t.platform == nil {
		return nil, errors.New("request_elicitation requires the platform MCP server (Standard level)")
	}
	return t.platform.callTool("lenny/request_elicitation", args)
}

// SendMessage invokes the §4.7 lenny/send_message platform tool and
// returns the raw delivery_receipt for the runtime to decode.
func (t *Tools) SendMessage(args map[string]any) (json.RawMessage, error) {
	if t == nil || t.platform == nil {
		return nil, errors.New("send_message requires the platform MCP server (Standard level)")
	}
	return t.platform.callTool("lenny/send_message", args)
}

// MemoryWrite invokes the §4.7 lenny/memory_write platform tool.
func (t *Tools) MemoryWrite(args map[string]any) error {
	if t == nil || t.platform == nil {
		return errors.New("memory_write requires the platform MCP server (Standard level)")
	}
	_, err := t.platform.callTool("lenny/memory_write", args)
	return err
}

// MemoryQuery invokes the §4.7 lenny/memory_query platform tool and
// returns the raw result.
func (t *Tools) MemoryQuery(args map[string]any) (json.RawMessage, error) {
	if t == nil || t.platform == nil {
		return nil, errors.New("memory_query requires the platform MCP server (Standard level)")
	}
	return t.platform.callTool("lenny/memory_query", args)
}

// GetTaskTree invokes the §4.7 lenny/get_task_tree platform tool and
// returns the raw result.
func (t *Tools) GetTaskTree(args map[string]any) (json.RawMessage, error) {
	if t == nil || t.platform == nil {
		return nil, errors.New("get_task_tree requires the platform MCP server (Standard level)")
	}
	return t.platform.callTool("lenny/get_task_tree", args)
}

// SetTracingContext invokes the §4.7 lenny/set_tracing_context platform
// tool, registering tracing identifiers that propagate through
// delegation (§16.3).
func (t *Tools) SetTracingContext(ctx map[string]any) error {
	if t == nil || t.platform == nil {
		return errors.New("set_tracing_context requires the platform MCP server (Standard level)")
	}
	_, err := t.platform.callTool("lenny/set_tracing_context", ctx)
	return err
}

// Call invokes an arbitrary tool on the platform MCP server by name. It
// is the escape hatch for tools the typed helpers above do not cover.
func (t *Tools) Call(name string, arguments map[string]any) (json.RawMessage, error) {
	if t == nil || t.platform == nil {
		return nil, errors.New("Call requires the platform MCP server (Standard level)")
	}
	return t.platform.callTool(name, arguments)
}

// decodeTaskResults decodes a lenny/await_children result, accepting
// either a list (mode all/settled) or a single object (mode any).
func decodeTaskResults(raw json.RawMessage) ([]TaskResult, error) {
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "[") {
		var list []TaskResult
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, fmt.Errorf("decode await_children list: %w", err)
		}
		return list, nil
	}
	var single TaskResult
	if err := json.Unmarshal(raw, &single); err != nil {
		return nil, fmt.Errorf("decode await_children result: %w", err)
	}
	return []TaskResult{single}, nil
}

// decodeParts decodes a result that is either a bare MessagePart array or
// an object with a parts field.
func decodeParts(raw json.RawMessage) ([]MessagePart, error) {
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "[") {
		var parts []MessagePart
		if err := json.Unmarshal(raw, &parts); err != nil {
			return nil, err
		}
		return parts, nil
	}
	var wrapped struct {
		Parts []MessagePart `json:"parts"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, err
	}
	return wrapped.Parts, nil
}

// --- intra-pod MCP client ----------------------------------------------

// mcpClient is a minimal §15.4.3 intra-pod MCP client over a single Unix
// socket. It speaks JSON-RPC 2.0: an initialize request carrying the
// manifest nonce, then tools/list and tools/call.
type mcpClient struct {
	conn netConn
	dec  *json.Decoder
	enc  *json.Encoder
	mu   sync.Mutex
	id   atomic.Int64
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// connectMCP dials the intra-pod MCP socket, completes the
// nonce-authenticated initialize handshake (§15.4.3), and discovers the
// tool set via tools/list. The nonce is presented as the top-level
// params._lennyNonce field of the initialize request.
func connectMCP(ctx context.Context, socket, nonce, clientName string, timeout time.Duration) (*mcpClient, error) {
	conn, err := dialUnixSocket(ctx, socket, timeout)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", socket, err)
	}
	c := &mcpClient{
		conn: conn,
		dec:  json.NewDecoder(conn),
		enc:  json.NewEncoder(conn),
	}
	c.enc.SetEscapeHTML(false)

	if _, err := c.call("initialize", map[string]any{
		nonceParamKey:     nonce,
		"protocolVersion": mcpProtocolVersion,
		"clientInfo": map[string]any{
			"name":    clientName,
			"version": "1.0.0",
		},
	}); err != nil {
		conn.Close()
		return nil, fmt.Errorf("initialize: %w", err)
	}
	if _, err := c.call("tools/list", map[string]any{}); err != nil {
		conn.Close()
		return nil, fmt.Errorf("tools/list: %w", err)
	}
	return c, nil
}

// callTool invokes one MCP tool via tools/call and returns the raw
// result JSON.
func (c *mcpClient) callTool(name string, arguments any) (json.RawMessage, error) {
	return c.call("tools/call", map[string]any{
		"name":      name,
		"arguments": arguments,
	})
}

// call sends one JSON-RPC request and reads the matching response. The
// connection is serialized by mu; the SDK issues calls sequentially.
func (c *mcpClient) call(method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.id.Add(1)
	if err := c.enc.Encode(rpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}); err != nil {
		return nil, fmt.Errorf("write %s request: %w", method, err)
	}
	var resp rpcResponse
	if err := c.dec.Decode(&resp); err != nil {
		return nil, fmt.Errorf("read %s response: %w", method, err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("%s: rpc error %d: %s", method, resp.Error.Code, resp.Error.Message)
	}
	return resp.Result, nil
}

// close releases the MCP connection.
func (c *mcpClient) close() {
	if c != nil && c.conn != nil {
		_ = c.conn.Close()
	}
}
