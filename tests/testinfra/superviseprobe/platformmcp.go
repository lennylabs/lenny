// SPDX-License-Identifier: MIT

package superviseprobe

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/runtimekit"
)

// platformMCPDialTimeout bounds the probe's dial and handshake against the
// platform MCP server.
const platformMCPDialTimeout = 5 * time.Second

// platformMCPCallTimeout bounds each request after the handshake, so a
// forwarded call that never answers cannot hold the probe's MCP command.
const platformMCPCallTimeout = 30 * time.Second

// errNoPlatformMCP reports a manifest that advertises no platform MCP server.
var errNoPlatformMCP = errors.New("the manifest advertises no platform MCP server")

// platformMCP is the probe's connection to the intra-pod platform MCP
// server, opened once per process with the nonce of the manifest the process
// read at start. The probe holds its own connection rather than the runtime
// SDK's Standard-level one, because the SDK ends the process when its startup
// catalog discovery is refused, and the adapter refuses a tools/list from a
// process that holds no session, which is the warm-up launch's state.
// spec: §15.4.3 (Authentication); §4.7.11 item 7; §4.7.10 (Runtime
// supervisor, Launch).
type platformMCP struct {
	mu   sync.Mutex
	conn net.Conn
	enc  *json.Encoder
	dec  *json.Decoder
	id   int64
}

// rpcError is a JSON-RPC error object.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// rpcReply is a JSON-RPC response.
type rpcReply struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

// dialPlatformMCP dials the platform MCP socket, completes the
// nonce-authenticated initialize handshake, and discovers the catalog. A
// discovery refused because the process holds no session is not an error.
// spec: §15.4.3 (Authentication).
func dialPlatformMCP(socket, nonce string) (*platformMCP, error) {
	addr := socket
	if strings.HasPrefix(socket, "@") {
		addr = "\x00" + socket[1:]
	}
	conn, err := net.DialTimeout("unix", addr, platformMCPDialTimeout)
	if err != nil {
		return nil, fmt.Errorf("dial platform MCP server %s: %w", socket, err)
	}
	c := &platformMCP{conn: conn, enc: json.NewEncoder(conn), dec: json.NewDecoder(conn)}
	if err := c.initialize(nonce); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("initialize platform MCP connection: %w", err)
	}
	if _, err := c.call("tools/list", map[string]any{}); err != nil && !refusedForNoSession(err) {
		_ = conn.Close()
		return nil, fmt.Errorf("discover the platform tool catalog: %w", err)
	}
	return c, nil
}

// readPlatformMCPEndpoint reads the platform MCP socket and mcpNonce from
// the manifest at path.
func readPlatformMCPEndpoint(path string) (socket, nonce string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("read manifest: %w", err)
	}
	var m struct {
		MCPNonce          string `json:"mcpNonce"`
		PlatformMcpServer *struct {
			Socket string `json:"socket"`
		} `json:"platformMcpServer"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return "", "", fmt.Errorf("decode manifest: %w", err)
	}
	if m.PlatformMcpServer == nil || m.PlatformMcpServer.Socket == "" {
		return "", "", errNoPlatformMCP
	}
	return m.PlatformMcpServer.Socket, m.MCPNonce, nil
}

// refusedForNoSession reports a tools/list the adapter refused because the
// pod's shared runtime process has not been given exactly one session. The
// adapter carries the gRPC FailedPrecondition status in the JSON-RPC error
// message. spec: §15.4.3 (Authentication).
func refusedForNoSession(err error) bool {
	return strings.Contains(err.Error(), "FailedPrecondition")
}

// initialize sends the initialize request carrying the nonce, answers a
// nonce-only challenge when one arrives in place of the response, and reads
// the response. spec: §15.4.3; §4.7.11 (Nonce-only fallback).
func (c *platformMCP) initialize(nonce string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.conn.SetDeadline(time.Now().Add(platformMCPDialTimeout))
	defer func() { _ = c.conn.SetDeadline(time.Time{}) }()
	if err := c.writeLocked("initialize", map[string]any{
		"_lennyNonce":     nonce,
		"protocolVersion": "2025-03-26",
		"clientInfo":      map[string]any{"name": "supervise-probe", "version": "1"},
	}); err != nil {
		return err
	}
	var raw json.RawMessage
	if err := c.dec.Decode(&raw); err != nil {
		return fmt.Errorf("read initialize response: %w", err)
	}
	if challenge, ok := runtimekit.ChallengeOf(raw); ok {
		if _, err := c.conn.Write(runtimekit.ChallengeResponseLine(nonce, challenge)); err != nil {
			return fmt.Errorf("write challenge response: %w", err)
		}
		if err := c.dec.Decode(&raw); err != nil {
			return fmt.Errorf("read initialize response: %w", err)
		}
	}
	return replyError("initialize", raw)
}

// Call makes one tools/call.
func (c *platformMCP) Call(name string) error {
	_, err := c.call("tools/call", map[string]any{"name": name, "arguments": map[string]any{}})
	return err
}

// call sends one request and reads its response.
func (c *platformMCP) call(method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.conn.SetDeadline(time.Now().Add(platformMCPCallTimeout))
	defer func() { _ = c.conn.SetDeadline(time.Time{}) }()
	if err := c.writeLocked(method, params); err != nil {
		return nil, err
	}
	var raw json.RawMessage
	if err := c.dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("read %s response: %w", method, err)
	}
	if err := replyError(method, raw); err != nil {
		return nil, err
	}
	var reply rpcReply
	_ = json.Unmarshal(raw, &reply)
	return reply.Result, nil
}

// writeLocked writes one request. Callers hold c.mu.
func (c *platformMCP) writeLocked(method string, params any) error {
	c.id++
	if err := c.enc.Encode(map[string]any{"jsonrpc": "2.0", "id": c.id, "method": method, "params": params}); err != nil {
		return fmt.Errorf("write %s request: %w", method, err)
	}
	return nil
}

// replyError decodes raw as a JSON-RPC response and returns its error.
func replyError(method string, raw json.RawMessage) error {
	var reply rpcReply
	if err := json.Unmarshal(raw, &reply); err != nil {
		return fmt.Errorf("decode %s response: %w", method, err)
	}
	if reply.Error != nil {
		return fmt.Errorf("%s: rpc error %d: %s", method, reply.Error.Code, reply.Error.Message)
	}
	return nil
}

// close releases the connection.
func (c *platformMCP) close() {
	if c != nil {
		_ = c.conn.Close()
	}
}
