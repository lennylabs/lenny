// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// runStandardBattery executes the Basic checks plus the §15.4.6
// Standard-level test categories. Each Standard-level check spawns the
// runtime with a fake platform MCP server (and two fake connector MCP
// servers) attached and asserts the runtime drives the §15.4.3 intra-pod
// MCP integration and the §8.5 delegation tool surface correctly. The
// fake servers use file-based Unix sockets so the harness runs
// cross-platform; the spec permits both file-based and abstract socket
// addresses (§15.4.3 platform note).
func runStandardBattery(binary string, timeout time.Duration, verbose bool) Report {
	r := Report{
		Harness:   "lenny-compliance/" + harnessVersion,
		Binary:    binary,
		Level:     "standard",
		StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}

	basic := []struct {
		name string
		spec string
		fn   func(string, time.Duration, bool) (string, error)
	}{
		{"binary_exists_and_executes", "15.4", checkBinaryExecutes},
		{"empty_stdin_exits_cleanly", "15.4", checkEmptyStdin},
		{"message_emits_response", "15.4", checkMessageEmitsResponse},
		{"heartbeat_emits_ack", "15.4", checkHeartbeatAck},
		{"unknown_type_ignored", "15.4", checkUnknownTypeIgnored},
		{"shutdown_exits_within_deadline", "15.4", checkShutdownDeadline},
		{"sequential_messages_handled", "15.4", checkSequentialMessages},
	}
	standard := []struct {
		name string
		spec string
		fn   func(string, time.Duration, bool) (string, error)
	}{
		{"mcp_nonce_handshake", "15.4.6", checkMCPNonceHandshake},
		{"platform_mcp_tool_invocation", "15.4.6", checkPlatformMCPToolInvocation},
		{"connector_mcp_server_reachability", "15.4.6", checkConnectorMCPReachability},
		{"tool_call_tool_result_correlation", "15.4.6", checkToolResultCorrelation},
	}

	for _, c := range basic {
		detail, err := c.fn(binary, timeout, verbose)
		r.recordCheck(c.name, c.spec, detail, err)
	}
	for _, c := range standard {
		detail, err := c.fn(binary, timeout, verbose)
		r.recordCheck(c.name, c.spec, detail, err)
	}
	r.Summary.Total = len(r.Checks)
	r.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return r
}

// --- fake platform adapter ---------------------------------------------

// fakePlatformAdapter spins up a fake platform MCP server and two fake
// connector MCP servers on Unix-socket listeners, and writes a manifest
// pointing the runtime at them plus the §15.4.3 intra-pod MCP nonce. The
// returned cleanup MUST be called.
type fakePlatformAdapter struct {
	dir          string
	manifestPath string
	nonce        string

	platform   *fakeMCPServer
	connectors []*fakeMCPServer
}

// fakeMCPServer is the adapter side of one intra-pod MCP connection: a
// JSON-RPC 2.0 listener that enforces the §15.4.3 nonce handshake and
// dispatches a fixed set of tool handlers. It records every tool name it
// dispatched so the conformance checks can assert which platform tools
// the runtime invoked.
type fakeMCPServer struct {
	name     string
	socket   string
	nonce    string
	listener net.Listener
	handlers map[string]func(args json.RawMessage) (any, error)

	mu          sync.Mutex
	calledTools []string
	rejectedBad bool // a connection failed the nonce handshake
	initialized int  // connections that completed the nonce handshake
}

// newFakePlatformAdapter builds the fake platform MCP server, two fake
// connector servers, and the manifest. The platform server is populated
// with the §8.5 delegation tool handlers (lenny/delegate_task,
// lenny/await_children, lenny/request_input, lenny/output).
func newFakePlatformAdapter() (*fakePlatformAdapter, func(), error) {
	// File-based Unix socket paths must stay under the platform's
	// sockaddr_un limit (104 bytes on macOS). The default temp dir on
	// macOS (/var/folders/.../T/) already consumes ~50 bytes, so a
	// nested temp dir plus a socket filename overflows. Root the fixture
	// under /tmp with short directory and socket names instead; /tmp
	// exists on macOS and Linux.
	socketBase := "/tmp"
	if _, err := os.Stat(socketBase); err != nil {
		socketBase = ""
	}
	dir, err := os.MkdirTemp(socketBase, "lc-std-*")
	if err != nil {
		return nil, nil, err
	}
	cleanups := []func(){func() { os.RemoveAll(dir) }}
	runCleanup := func() {
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
	}

	// §15.4.3 intra-pod MCP nonce: a 256-bit hex value. The harness does
	// not need cryptographic strength — a fixed deterministic value is
	// enough to exercise the handshake.
	nonce := "nonce_" + fmt.Sprintf("%064x", time.Now().UnixNano())[:58]

	fa := &fakePlatformAdapter{dir: dir, nonce: nonce}

	platform, stopPlatform, err := startFakeMCPServer("platform", filepath.Join(dir, "p.sock"), nonce, platformToolHandlers())
	if err != nil {
		runCleanup()
		return nil, nil, fmt.Errorf("start platform MCP server: %w", err)
	}
	cleanups = append(cleanups, stopPlatform)
	fa.platform = platform

	// §15.4.6 connector reachability uses two fake connector servers.
	// Socket filenames are kept short (sockaddr_un length limit); the
	// connector id in the manifest is the descriptive name.
	connectorIDs := []string{"github", "filesystem"}
	for i, id := range connectorIDs {
		cs, stop, err := startFakeMCPServer("connector-"+id, filepath.Join(dir, fmt.Sprintf("c%d.sock", i)), nonce, connectorToolHandlers())
		if err != nil {
			runCleanup()
			return nil, nil, fmt.Errorf("start connector MCP server %q: %w", id, err)
		}
		cleanups = append(cleanups, stop)
		fa.connectors = append(fa.connectors, cs)
	}

	fa.manifestPath = filepath.Join(dir, "adapter-manifest.json")
	connectorEntries := make([]map[string]any, len(fa.connectors))
	for i, cs := range fa.connectors {
		connectorEntries[i] = map[string]any{"id": connectorIDs[i], "socket": cs.socket}
	}
	body, _ := json.MarshalIndent(map[string]any{
		"version":           1,
		"sessionId":         "sess_compliance_standard",
		"taskId":            "task_compliance_standard",
		"platformMcpServer": map[string]any{"socket": fa.platform.socket},
		"connectorServers":  connectorEntries,
		"runtimeMcpServers": []any{},
		"adapterLocalTools": []any{},
		"mcpNonce":          nonce,
	}, "", "  ")
	if err := os.WriteFile(fa.manifestPath, body, 0o600); err != nil {
		runCleanup()
		return nil, nil, err
	}
	return fa, runCleanup, nil
}

// startFakeMCPServer listens on socket and serves the JSON-RPC 2.0 MCP
// protocol with handlers, enforcing the §15.4.3 nonce handshake. The
// returned stop closes the listener.
func startFakeMCPServer(name, socket, nonce string, handlers map[string]func(json.RawMessage) (any, error)) (*fakeMCPServer, func(), error) {
	l, err := net.Listen("unix", socket)
	if err != nil {
		return nil, nil, fmt.Errorf("listen %s: %w", socket, err)
	}
	s := &fakeMCPServer{
		name:     name,
		socket:   socket,
		nonce:    nonce,
		listener: l,
		handlers: handlers,
	}
	go s.acceptLoop()
	return s, func() { _ = l.Close() }, nil
}

func (s *fakeMCPServer) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.serveConn(conn)
	}
}

// mcpRPCRequest / mcpRPCResponse are the JSON-RPC 2.0 envelopes the fake
// MCP server reads and writes.
type mcpRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type mcpRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpRPCError    `json:"error,omitempty"`
}

type mcpRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// serveConn handles one MCP connection: the §15.4.3 nonce-authenticated
// `initialize` handshake, then `tools/list` and `tools/call`. A
// connection that fails the nonce handshake is closed without
// dispatching any tool, matching the production adapter (§15.4.3).
func (s *fakeMCPServer) serveConn(conn net.Conn) {
	defer conn.Close()
	dec := json.NewDecoder(conn)
	enc := json.NewEncoder(conn)
	enc.SetEscapeHTML(false)

	var initReq mcpRPCRequest
	if err := dec.Decode(&initReq); err != nil {
		return
	}
	if initReq.Method != "initialize" {
		return
	}
	// §15.4.3: validate _lennyNonce against the manifest nonce. A
	// missing or mismatched nonce is rejected with an immediate close.
	var params map[string]json.RawMessage
	if err := json.Unmarshal(initReq.Params, &params); err != nil {
		return
	}
	rawNonce, ok := params["_lennyNonce"]
	if !ok {
		s.markRejected()
		return
	}
	var nonce string
	if err := json.Unmarshal(rawNonce, &nonce); err != nil {
		s.markRejected()
		return
	}
	if subtle.ConstantTimeCompare([]byte(nonce), []byte(s.nonce)) != 1 {
		s.markRejected()
		return
	}
	if err := enc.Encode(mcpRPCResponse{
		JSONRPC: "2.0",
		ID:      initReq.ID,
		Result: map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "lenny-compliance-fake-mcp", "version": "1"},
		},
	}); err != nil {
		return
	}
	s.markInitialized()

	for {
		var req mcpRPCRequest
		if err := dec.Decode(&req); err != nil {
			return
		}
		// JSON-RPC notifications carry no id; they receive no reply.
		if len(req.ID) == 0 || string(req.ID) == "null" {
			continue
		}
		resp := s.dispatch(req)
		if err := enc.Encode(resp); err != nil {
			return
		}
	}
}

func (s *fakeMCPServer) dispatch(req mcpRPCRequest) mcpRPCResponse {
	switch req.Method {
	case "tools/list":
		names := make([]map[string]any, 0, len(s.handlers))
		for name := range s.handlers {
			names = append(names, map[string]any{"name": name, "description": name})
		}
		return mcpRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": names}}
	case "tools/call":
		var call struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &call); err != nil {
			return mcpRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &mcpRPCError{Code: -32602, Message: "malformed tools/call params"}}
		}
		h, ok := s.handlers[call.Name]
		if !ok {
			return mcpRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &mcpRPCError{Code: -32602, Message: "unknown tool " + call.Name}}
		}
		s.recordCall(call.Name)
		result, err := h(call.Arguments)
		if err != nil {
			return mcpRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &mcpRPCError{Code: -32603, Message: err.Error()}}
		}
		return mcpRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: result}
	default:
		return mcpRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &mcpRPCError{Code: -32601, Message: "unknown method " + req.Method}}
	}
}

func (s *fakeMCPServer) recordCall(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calledTools = append(s.calledTools, name)
}

func (s *fakeMCPServer) markRejected() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rejectedBad = true
}

func (s *fakeMCPServer) markInitialized() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initialized++
}

// initializedCount returns the number of connections that completed the
// §15.4.3 nonce-authenticated initialize handshake against this server.
func (s *fakeMCPServer) initializedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.initialized
}

// toolsCalled returns the set of tool names the server dispatched.
func (s *fakeMCPServer) toolsCalled() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	set := make(map[string]bool, len(s.calledTools))
	for _, n := range s.calledTools {
		set[n] = true
	}
	return set
}

func (s *fakeMCPServer) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calledTools)
}

// platformToolHandlers returns the §8.5 platform MCP tool handlers the
// fake platform MCP server exposes. lenny/delegate_task returns a
// §8.2 TaskHandle; lenny/await_children returns a list of §8.8
// TaskResult; lenny/request_input and lenny/output acknowledge.
func platformToolHandlers() map[string]func(json.RawMessage) (any, error) {
	return map[string]func(json.RawMessage) (any, error){
		// lenny/delegate_task(target, task, lease_slice?) → TaskHandle.
		"lenny/delegate_task": func(args json.RawMessage) (any, error) {
			var a struct {
				Target string `json:"target"`
				Task   struct {
					Input json.RawMessage `json:"input"`
				} `json:"task"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, fmt.Errorf("malformed delegate_task arguments: %w", err)
			}
			if a.Target == "" {
				return nil, errors.New("delegate_task: empty target")
			}
			return map[string]any{
				"taskId": "child_compliance_001",
				"state":  "running",
			}, nil
		},
		// lenny/await_children(child_ids, mode) → [TaskResult].
		"lenny/await_children": func(args json.RawMessage) (any, error) {
			var a struct {
				ChildIDs []string `json:"child_ids"`
				Mode     string   `json:"mode"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, fmt.Errorf("malformed await_children arguments: %w", err)
			}
			if len(a.ChildIDs) == 0 {
				return nil, errors.New("await_children: empty child_ids")
			}
			results := make([]map[string]any, 0, len(a.ChildIDs))
			for _, id := range a.ChildIDs {
				results = append(results, map[string]any{
					"schemaVersion": 1,
					"taskId":        id,
					"state":         "completed",
					"output": map[string]any{
						"parts": []map[string]any{
							{"schemaVersion": 1, "type": "text", "inline": "child echo: " + id},
						},
					},
					"error": nil,
				})
			}
			return results, nil
		},
		// lenny/request_input(parts) → blocks until an answer arrives.
		// The fake answers immediately.
		"lenny/request_input": func(args json.RawMessage) (any, error) {
			return map[string]any{
				"parts": []map[string]any{
					{"schemaVersion": 1, "type": "text", "inline": "confirmed"},
				},
			}, nil
		},
		// lenny/output(output) → emit output parts to the parent/client.
		"lenny/output": func(args json.RawMessage) (any, error) {
			var a struct {
				Output []json.RawMessage `json:"output"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, fmt.Errorf("malformed output arguments: %w", err)
			}
			return map[string]any{"accepted": len(a.Output)}, nil
		},
	}
}

// connectorToolHandlers returns the tool handlers a fake connector MCP
// server exposes. delegation-echo does not invoke connector tools, so a
// single placeholder tool is enough to make tools/list non-empty.
func connectorToolHandlers() map[string]func(json.RawMessage) (any, error) {
	return map[string]func(json.RawMessage) (any, error){
		"connector/noop": func(json.RawMessage) (any, error) {
			return map[string]any{"ok": true}, nil
		},
	}
}

// spawnAgainstPlatform starts the runtime with LENNY_ADAPTER_MANIFEST
// set so it reads the manifest and connects to the fake platform MCP
// server. The caller MUST call reap to wait for the child.
func (fa *fakePlatformAdapter) spawn(ctx context.Context, binary string) (*exec.Cmd, io.WriteCloser, *bufio.Scanner, *bufio.Scanner, error) {
	cmd := exec.CommandContext(ctx, binary)
	cmd.Env = append(os.Environ(), "LENNY_ADAPTER_MANIFEST="+fa.manifestPath)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, nil, nil, err
	}
	outScan := bufio.NewScanner(stdout)
	outScan.Buffer(make([]byte, 64*1024), 50*1024*1024)
	errScan := bufio.NewScanner(stderr)
	return cmd, stdin, outScan, errScan, nil
}

// --- Standard-level checks ---------------------------------------------

// canonicalMessage is the inbound `message` envelope the Standard checks
// drive the runtime with.
const canonicalMessage = `{"schemaVersion":1,"type":"message","id":"msg_01J9X0ZW1ZF7K8Q1V2T3M4N5P1","from":{"kind":"client","id":"client_alice"},"input":[{"schemaVersion":1,"type":"text","inline":"delegate this"}]}`

// readResponseLine drives the runtime with one message, reads the first
// stdout line, and returns it parsed. It reaps the child afterwards.
func driveOneMessage(fa *fakePlatformAdapter, binary string, in string) (map[string]any, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd, stdin, outScan, errScan, err := fa.spawn(ctx, binary)
	if err != nil {
		return nil, "", err
	}
	defer reap(cmd, stdin, 2*time.Second)

	if _, err := io.WriteString(stdin, in+"\n"); err != nil {
		return nil, "", fmt.Errorf("write message: %w", err)
	}

	type outResult struct {
		line string
		ok   bool
	}
	ch := make(chan outResult, 1)
	go func() {
		if outScan.Scan() {
			ch <- outResult{line: outScan.Text(), ok: true}
			return
		}
		ch <- outResult{}
	}()

	var line string
	select {
	case res := <-ch:
		if !res.ok {
			stderr := drainScanner(errScan)
			return nil, stderr, errors.New("runtime emitted no response on stdout")
		}
		line = res.line
	case <-time.After(10 * time.Second):
		return nil, drainScanner(errScan), errors.New("runtime did not emit a response within 10s")
	}

	var resp map[string]any
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		return nil, drainScanner(errScan), fmt.Errorf("response not JSON: %w (line %q)", err, line)
	}
	return resp, drainScanner(errScan), nil
}

// drainScanner reads whatever the scanner has buffered without blocking
// on more input. It is best-effort: the caller has already closed stdin.
func drainScanner(s *bufio.Scanner) string {
	var b []byte
	for s.Scan() {
		b = append(b, s.Bytes()...)
		b = append(b, '\n')
	}
	return string(b)
}

// checkMCPNonceHandshake verifies the §15.4.6 "MCP nonce handshake"
// category: on startup the runtime reads the manifest, connects to the
// platform MCP server, and presents `_lennyNonce` in the `initialize`
// params. The check also confirms the fake server rejects a connection
// that presents a bad nonce — the negative half of the category.
func checkMCPNonceHandshake(binary string, _ time.Duration, _ bool) (string, error) {
	fa, cleanup, err := newFakePlatformAdapter()
	if err != nil {
		return "", err
	}
	defer cleanup()

	resp, stderr, err := driveOneMessage(fa, binary, canonicalMessage)
	if err != nil {
		return "", withStderr(err, stderr)
	}
	if resp["type"] != "response" {
		return "", fmt.Errorf("expected response, got %v", resp)
	}
	// The runtime connected and at least one tool was dispatched, which
	// is only reachable past a valid nonce handshake (the fake server
	// closes a connection that fails the nonce check before dispatch).
	if fa.platform.callCount() == 0 {
		return "", errors.New("platform MCP server dispatched no tool — the runtime did not complete the nonce handshake")
	}
	if fa.platform.rejectedBad {
		return "", errors.New("platform MCP server rejected the runtime's connection — invalid or missing _lennyNonce")
	}

	// Negative half: a connection presenting a bad nonce must be
	// rejected with an immediate close (§15.4.3).
	if err := assertBadNonceRejected(fa.platform.socket); err != nil {
		return "", err
	}
	return "valid nonce accepted; bad nonce rejected", nil
}

// assertBadNonceRejected dials the fake platform MCP socket directly
// with a wrong nonce and confirms the server closes the connection
// without answering the initialize request.
func assertBadNonceRejected(socket string) error {
	conn, err := net.Dial("unix", socket)
	if err != nil {
		return fmt.Errorf("dial platform MCP socket for bad-nonce probe: %w", err)
	}
	defer conn.Close()
	enc := json.NewEncoder(conn)
	if err := enc.Encode(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"_lennyNonce":     "the-wrong-nonce",
			"protocolVersion": "2025-03-26",
		},
	}); err != nil {
		return fmt.Errorf("send bad-nonce initialize: %w", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var resp map[string]json.RawMessage
	if err := json.NewDecoder(conn).Decode(&resp); err == nil {
		return errors.New("platform MCP server answered an initialize carrying a bad nonce; the adapter must reject it")
	}
	return nil
}

// checkPlatformMCPToolInvocation verifies the §15.4.6 "platform MCP tool
// invocation" category: the runtime successfully calls at least
// `lenny/output` and `lenny/request_input` via the MCP client.
// delegation-echo also exercises `lenny/delegate_task` and
// `lenny/await_children` (§8.5) as its core behaviour; the check
// confirms the full delegation flow plus the two spec-named tools.
func checkPlatformMCPToolInvocation(binary string, _ time.Duration, _ bool) (string, error) {
	fa, cleanup, err := newFakePlatformAdapter()
	if err != nil {
		return "", err
	}
	defer cleanup()

	resp, stderr, err := driveOneMessage(fa, binary, canonicalMessage)
	if err != nil {
		return "", withStderr(err, stderr)
	}
	if resp["type"] != "response" {
		return "", fmt.Errorf("expected response, got %v", resp)
	}
	if errObj, ok := resp["error"].(map[string]any); ok {
		return "", fmt.Errorf("runtime reported a delegation error: %v", errObj)
	}

	called := fa.platform.toolsCalled()
	// §15.4.6 requires at least lenny/output and lenny/request_input.
	for _, required := range []string{"lenny/output", "lenny/request_input"} {
		if !called[required] {
			return "", fmt.Errorf("runtime did not call %s via the platform MCP server", required)
		}
	}
	// delegation-echo's defined behaviour additionally exercises the
	// §8.5 delegation tools.
	for _, delegationTool := range []string{"lenny/delegate_task", "lenny/await_children"} {
		if !called[delegationTool] {
			return "", fmt.Errorf("runtime did not call %s — the §8.5 delegation surface was not exercised", delegationTool)
		}
	}
	return fmt.Sprintf("invoked %d platform tools: delegate_task, await_children, request_input, output", fa.platform.callCount()), nil
}

// checkConnectorMCPReachability verifies the §15.4.6 "connector MCP
// server reachability" category: with a non-empty `connectorServers`
// array, the runtime connects to each connector with the same nonce and
// completes the `initialize` handshake. The harness uses two fake
// connector servers.
func checkConnectorMCPReachability(binary string, _ time.Duration, _ bool) (string, error) {
	fa, cleanup, err := newFakePlatformAdapter()
	if err != nil {
		return "", err
	}
	defer cleanup()
	if len(fa.connectors) < 2 {
		return "", errors.New("harness misconfigured: expected two fake connector servers")
	}

	resp, stderr, err := driveOneMessage(fa, binary, canonicalMessage)
	if err != nil {
		return "", withStderr(err, stderr)
	}
	if resp["type"] != "response" {
		return "", fmt.Errorf("expected response, got %v", resp)
	}
	// §15.4.6 requires the runtime to connect to each connector with
	// the manifest nonce and complete the initialize handshake. Each
	// fake connector server records a completed nonce-authenticated
	// initialize; the runtime opens its connector connections during
	// startup, so by the time it emits a response every handshake is
	// settled.
	for i, cs := range fa.connectors {
		if cs.rejectedBad {
			return "", fmt.Errorf("connector %d (%s) rejected the runtime's nonce", i, cs.name)
		}
		if cs.initializedCount() == 0 {
			return "", fmt.Errorf("connector %d (%s) saw no completed initialize handshake — the runtime did not connect to it", i, cs.name)
		}
	}
	return fmt.Sprintf("connected to %d connector MCP servers with the manifest nonce", len(fa.connectors)), nil
}

// checkToolResultCorrelation verifies the §15.4.6 "tool_call /
// tool_result correlation" category. For Standard-level runtimes the
// stdin/stdout `tool_call` channel carries adapter-local tools only —
// platform tools go over MCP (§15.4.1 tool access by level). A runtime
// that emits no `tool_call` (delegation-echo uses no adapter-local
// tools) satisfies the category vacuously; a runtime that does emit a
// `tool_call` must give it a unique `id` and read the matching
// `tool_result` from stdin before its final `response`. The check
// drives a message, supplies a `tool_result` for any emitted
// `tool_call`, and asserts every `tool_call` carries a non-empty `id`
// and precedes the `response`.
func checkToolResultCorrelation(binary string, _ time.Duration, _ bool) (string, error) {
	fa, cleanup, err := newFakePlatformAdapter()
	if err != nil {
		return "", err
	}
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd, stdin, outScan, errScan, err := fa.spawn(ctx, binary)
	if err != nil {
		return "", err
	}
	defer reap(cmd, stdin, 2*time.Second)

	if _, err := io.WriteString(stdin, canonicalMessage+"\n"); err != nil {
		return "", fmt.Errorf("write message: %w", err)
	}

	// Read stdout frames. For each `tool_call`, answer with a
	// correlated `tool_result` on stdin and continue. Stop at the
	// `response`. Every `tool_call.id` must be unique and non-empty.
	seenIDs := map[string]bool{}
	toolCalls := 0
	type frame struct {
		line string
		ok   bool
	}
	frames := make(chan frame, 1)
	readNext := func() {
		go func() {
			if outScan.Scan() {
				frames <- frame{line: outScan.Text(), ok: true}
				return
			}
			frames <- frame{}
		}()
	}
	readNext()
	for {
		var f frame
		select {
		case f = <-frames:
		case <-time.After(10 * time.Second):
			return "", withStderr(errors.New("runtime did not reach a response within 10s"), drainScanner(errScan))
		}
		if !f.ok {
			return "", withStderr(errors.New("runtime closed stdout before emitting a response"), drainScanner(errScan))
		}
		var env struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if err := json.Unmarshal([]byte(f.line), &env); err != nil {
			return "", fmt.Errorf("stdout frame not JSON: %w (line %q)", err, f.line)
		}
		switch env.Type {
		case "tool_call":
			if env.ID == "" {
				return "", fmt.Errorf("tool_call %q has an empty id; the adapter cannot correlate its tool_result", env.Name)
			}
			if seenIDs[env.ID] {
				return "", fmt.Errorf("tool_call id %q reused; ids must be unique within the session", env.ID)
			}
			seenIDs[env.ID] = true
			toolCalls++
			result := fmt.Sprintf(`{"type":"tool_result","id":%q,"content":[{"schemaVersion":1,"type":"text","inline":"ok"}],"isError":false}`, env.ID)
			if _, err := io.WriteString(stdin, result+"\n"); err != nil {
				return "", fmt.Errorf("write tool_result: %w", err)
			}
			readNext()
		case "response":
			if toolCalls == 0 {
				return "no adapter-local tool_call emitted; correlation satisfied vacuously (platform tools go over MCP)", nil
			}
			return fmt.Sprintf("%d tool_call/tool_result pair(s) correlated before response", toolCalls), nil
		default:
			// Forward-compat: ignore other frame kinds and keep reading.
			readNext()
		}
	}
}

// withStderr appends captured runtime stderr to an error when stderr is
// non-empty, so a failing check surfaces the runtime's own diagnosis.
func withStderr(err error, stderr string) error {
	if stderr == "" {
		return err
	}
	return fmt.Errorf("%w (runtime stderr: %s)", err, oneLineStderr(stderr))
}

// oneLineStderr collapses captured stderr to a single line for the
// check detail field.
func oneLineStderr(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r == '\n' || r == '\r' {
			r = ' '
		}
		out = append(out, r)
	}
	return string(out)
}
