// SPDX-License-Identifier: MIT

// Tests for the Standard-level delegation-echo reference runtime. They
// drive run() in-process — feeding stdin lines and reading the JSONL
// stdout — without spawning a subprocess. A fake platform MCP server
// over a Unix-socket pair stands in for the adapter so the §8.5
// delegation flow is exercised end to end.
package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- in-process fake platform MCP server -------------------------------

// fakeMCP is a JSON-RPC 2.0 MCP server for the runtime tests. It
// enforces the §15.4.3 nonce handshake and dispatches a fixed delegation
// tool set, recording every tool call.
type fakeMCP struct {
	socket   string
	nonce    string
	listener net.Listener

	mu           sync.Mutex
	calls        []toolCall
	failChildren bool // when set, await_children returns a failed child
}

type toolCall struct {
	name string
	args json.RawMessage
}

func newFakeMCP(t *testing.T, nonce string) *fakeMCP {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "p.sock")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen %s: %v", socket, err)
	}
	f := &fakeMCP{socket: socket, nonce: nonce, listener: l}
	go f.accept()
	t.Cleanup(func() { _ = l.Close() })
	return f
}

func (f *fakeMCP) accept() {
	for {
		conn, err := f.listener.Accept()
		if err != nil {
			return
		}
		go f.serve(conn)
	}
}

func (f *fakeMCP) serve(conn net.Conn) {
	defer conn.Close()
	dec := json.NewDecoder(conn)
	enc := json.NewEncoder(conn)

	var initReq struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			Nonce string `json:"_lennyNonce"`
		} `json:"params"`
	}
	if err := dec.Decode(&initReq); err != nil {
		return
	}
	if initReq.Method != "initialize" || initReq.Params.Nonce != f.nonce {
		// §15.4.3: reject a bad/missing nonce with an immediate close.
		return
	}
	_ = enc.Encode(map[string]any{
		"jsonrpc": "2.0", "id": initReq.ID,
		"result": map[string]any{
			"protocolVersion": mcpProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "fake", "version": "1"},
		},
	})

	for {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := dec.Decode(&req); err != nil {
			return
		}
		switch req.Method {
		case "tools/list":
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"tools": []any{}}})
		case "tools/call":
			var call struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &call)
			f.record(call.Name, call.Arguments)
			result, rpcErr := f.handle(call.Name, call.Arguments)
			if rpcErr != "" {
				_ = enc.Encode(map[string]any{
					"jsonrpc": "2.0", "id": req.ID,
					"error": map[string]any{"code": -32603, "message": rpcErr},
				})
				continue
			}
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
		default:
			_ = enc.Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"error": map[string]any{"code": -32601, "message": "unknown method"},
			})
		}
	}
}

func (f *fakeMCP) record(name string, args json.RawMessage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, toolCall{name: name, args: append(json.RawMessage(nil), args...)})
}

func (f *fakeMCP) callNames() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	names := make([]string, len(f.calls))
	for i, c := range f.calls {
		names[i] = c.name
	}
	return names
}

func (f *fakeMCP) argsFor(name string) json.RawMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c.name == name {
			return c.args
		}
	}
	return nil
}

// handle returns the canned result for each delegation tool. The
// delegate_task handler returns a §8.2 TaskHandle; await_children
// returns a list of §8.8 TaskResult.
func (f *fakeMCP) handle(name string, args json.RawMessage) (any, string) {
	switch name {
	case "lenny/delegate_task":
		return map[string]any{"taskId": "child_test_001", "state": "running"}, ""
	case "lenny/await_children":
		f.mu.Lock()
		fail := f.failChildren
		f.mu.Unlock()
		if fail {
			return []map[string]any{{
				"schemaVersion": 1,
				"taskId":        "child_test_001",
				"state":         "failed",
				"output":        nil,
				"error": map[string]any{
					"code":    "RUNTIME_CRASH",
					"message": "child process exited 137",
				},
			}}, ""
		}
		return []map[string]any{{
			"schemaVersion": 1,
			"taskId":        "child_test_001",
			"state":         "completed",
			"output": map[string]any{
				"parts": []map[string]any{
					{"schemaVersion": 1, "type": "text", "inline": "child result"},
				},
			},
			"error": nil,
		}}, ""
	case "lenny/request_input":
		return map[string]any{"parts": []map[string]any{{"schemaVersion": 1, "type": "text", "inline": "yes"}}}, ""
	case "lenny/output":
		return map[string]any{"accepted": 1}, ""
	default:
		return nil, "unknown tool " + name
	}
}

// writeManifest writes a Standard-level adapter manifest naming the fake
// MCP server, and returns its path. connectorSockets, when non-empty,
// adds connectorServers entries.
func writeManifest(t *testing.T, mcpSocket, nonce string, connectorSockets []string) string {
	t.Helper()
	connectors := make([]map[string]any, len(connectorSockets))
	for i, s := range connectorSockets {
		connectors[i] = map[string]any{"id": "conn", "socket": s}
	}
	m := map[string]any{
		"version":           1,
		"sessionId":         "sess_test",
		"taskId":            "task_test",
		"platformMcpServer": map[string]any{"socket": mcpSocket},
		"connectorServers":  connectors,
		"runtimeMcpServers": []any{},
		"adapterLocalTools": []any{},
		"mcpNonce":          nonce,
	}
	body, _ := json.Marshal(m)
	path := filepath.Join(t.TempDir(), "adapter-manifest.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return path
}

// runRuntime drives run() with the given stdin lines and manifest path,
// returning the parsed stdout JSONL frames. manifestPath may be empty to
// run without a manifest (Basic-level fallback).
func runRuntime(t *testing.T, manifestPath string, stdinLines []string) ([]map[string]any, string) {
	t.Helper()
	if manifestPath == "" {
		t.Setenv("LENNY_ADAPTER_MANIFEST", filepath.Join(t.TempDir(), "absent.json"))
	} else {
		t.Setenv("LENNY_ADAPTER_MANIFEST", manifestPath)
	}

	var stdin bytes.Buffer
	for _, l := range stdinLines {
		stdin.WriteString(l)
		stdin.WriteString("\n")
	}
	var stdout, stderr bytes.Buffer

	done := make(chan error, 1)
	go func() { done <- run(&stdin, &stdout, &stderr) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run returned error: %v\nstderr: %s", err, stderr.String())
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("run did not return within 15s\nstderr: %s", stderr.String())
	}

	var frames []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("stdout line not JSON: %v (%q)", err, line)
		}
		frames = append(frames, m)
	}
	return frames, stderr.String()
}

const testMessage = `{"schemaVersion":1,"type":"message","id":"msg_test_1","from":{"kind":"client","id":"client_alice"},"input":[{"schemaVersion":1,"type":"text","inline":"hello"}]}`

// --- Basic-level message handling --------------------------------------

func TestHeartbeatAck(t *testing.T) {
	frames, _ := runRuntime(t, "", []string{`{"type":"heartbeat","ts":1}`})
	if len(frames) != 1 {
		t.Fatalf("got %d frames, want 1", len(frames))
	}
	if frames[0]["type"] != "heartbeat_ack" {
		t.Errorf("frame type = %v, want heartbeat_ack", frames[0]["type"])
	}
}

func TestUnknownTypeIgnored(t *testing.T) {
	frames, _ := runRuntime(t, "", []string{
		`{"type":"some_future_kind","x":1}`,
		`{"type":"heartbeat","ts":2}`,
	})
	if len(frames) != 1 || frames[0]["type"] != "heartbeat_ack" {
		t.Fatalf("unknown type was not silently dropped; frames=%v", frames)
	}
}

func TestShutdownExitsClean(t *testing.T) {
	frames, _ := runRuntime(t, "", []string{`{"type":"shutdown","reason":"test","deadline_ms":250}`})
	if len(frames) != 0 {
		t.Errorf("shutdown produced stdout frames: %v", frames)
	}
}

func TestMalformedJSONLIsProtocolError(t *testing.T) {
	t.Setenv("LENNY_ADAPTER_MANIFEST", filepath.Join(t.TempDir(), "absent.json"))
	err := run(strings.NewReader("{not json\n"), io.Discard, io.Discard)
	if err == nil {
		t.Fatal("malformed JSONL did not produce an error")
	}
	var pe protocolError
	if !asProtocolError(err, &pe) {
		t.Errorf("error %v is not a protocolError", err)
	}
}

func asProtocolError(err error, target *protocolError) bool {
	pe, ok := err.(protocolError)
	if ok {
		*target = pe
	}
	return ok
}

// TestMessageBasicFallback verifies that without a manifest the runtime
// degrades to Basic-level echo: it echoes the inbound input parts
// directly with the seq-prefixed text.
func TestMessageBasicFallback(t *testing.T) {
	frames, _ := runRuntime(t, "", []string{testMessage})
	if len(frames) != 1 {
		t.Fatalf("got %d frames, want 1", len(frames))
	}
	if frames[0]["type"] != "response" {
		t.Fatalf("frame type = %v, want response", frames[0]["type"])
	}
	out, _ := frames[0]["output"].([]any)
	if len(out) != 1 {
		t.Fatalf("response.output len = %d, want 1", len(out))
	}
	part, _ := out[0].(map[string]any)
	inline, _ := part["inline"].(string)
	if !strings.Contains(inline, "hello") || !strings.Contains(inline, "delegation-echo seq=1") {
		t.Errorf("echoed text = %q, want it to contain the seq prefix and the input", inline)
	}
}

// --- Standard-level delegation flow ------------------------------------

// TestMessageDelegationFlow verifies the §8.5 delegation flow: on a
// message the runtime calls delegate_task, await_children,
// request_input, and output in order, and echoes the child's TaskResult
// parts back in the stdout response.
func TestMessageDelegationFlow(t *testing.T) {
	mcp := newFakeMCP(t, "nonce_abc123")
	manifest := writeManifest(t, mcp.socket, "nonce_abc123", nil)

	frames, stderr := runRuntime(t, manifest, []string{testMessage})
	if len(frames) != 1 {
		t.Fatalf("got %d frames, want 1\nstderr: %s", len(frames), stderr)
	}
	resp := frames[0]
	if resp["type"] != "response" {
		t.Fatalf("frame type = %v, want response", resp["type"])
	}
	if _, isErr := resp["error"]; isErr {
		t.Fatalf("response carried an error: %v", resp["error"])
	}

	// The §8.5 delegation tools were called in order.
	want := []string{"lenny/delegate_task", "lenny/await_children", "lenny/request_input", "lenny/output"}
	got := mcp.callNames()
	if len(got) != len(want) {
		t.Fatalf("tool calls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("tool call %d = %q, want %q", i, got[i], want[i])
		}
	}

	// The response echoes the child's TaskResult output parts.
	out, _ := resp["output"].([]any)
	if len(out) != 1 {
		t.Fatalf("response.output len = %d, want 1", len(out))
	}
	part, _ := out[0].(map[string]any)
	if part["inline"] != "child result" {
		t.Errorf("echoed part inline = %v, want %q", part["inline"], "child result")
	}
}

// TestDelegateTaskCarriesInput verifies the inbound message's input
// parts are forwarded into the delegate_task TaskSpec.
func TestDelegateTaskCarriesInput(t *testing.T) {
	mcp := newFakeMCP(t, "n1")
	manifest := writeManifest(t, mcp.socket, "n1", nil)
	runRuntime(t, manifest, []string{testMessage})

	raw := mcp.argsFor("lenny/delegate_task")
	if raw == nil {
		t.Fatal("delegate_task was not called")
	}
	var args struct {
		Target string `json:"target"`
		Task   struct {
			Input []map[string]any `json:"input"`
		} `json:"task"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		t.Fatalf("decode delegate_task args: %v", err)
	}
	if args.Target == "" {
		t.Error("delegate_task target is empty")
	}
	if len(args.Task.Input) != 1 || !strings.Contains(args.Task.Input[0]["inline"].(string), "hello") {
		t.Errorf("delegate_task task.input = %v, want the inbound text part", args.Task.Input)
	}
}

// TestAwaitChildrenArguments verifies await_children is called with the
// child id from the delegate_task TaskHandle and a valid mode.
func TestAwaitChildrenArguments(t *testing.T) {
	mcp := newFakeMCP(t, "n1")
	manifest := writeManifest(t, mcp.socket, "n1", nil)
	runRuntime(t, manifest, []string{testMessage})

	raw := mcp.argsFor("lenny/await_children")
	if raw == nil {
		t.Fatal("await_children was not called")
	}
	var args struct {
		ChildIDs []string `json:"child_ids"`
		Mode     string   `json:"mode"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		t.Fatalf("decode await_children args: %v", err)
	}
	if len(args.ChildIDs) != 1 || args.ChildIDs[0] != "child_test_001" {
		t.Errorf("await_children child_ids = %v, want [child_test_001]", args.ChildIDs)
	}
	if args.Mode != "all" && args.Mode != "settled" && args.Mode != "any" {
		t.Errorf("await_children mode = %q, want all|any|settled", args.Mode)
	}
}

// TestConnectorMCPConnection verifies the runtime connects to a
// connector MCP server listed in the manifest and completes the
// initialize handshake with the same nonce.
func TestConnectorMCPConnection(t *testing.T) {
	platform := newFakeMCP(t, "n1")
	connector := newFakeMCP(t, "n1")
	manifest := writeManifest(t, platform.socket, "n1", []string{connector.socket})

	frames, stderr := runRuntime(t, manifest, []string{testMessage})
	if len(frames) != 1 || frames[0]["type"] != "response" {
		t.Fatalf("expected one response frame, got %v\nstderr: %s", frames, stderr)
	}
	// The connector server records no tool calls (delegation-echo does
	// not invoke connector tools) but the runtime must have completed
	// the handshake: a failed connector dial is fatal in run(), so a
	// clean response proves the connector connection succeeded.
}

// TestBadNonceConnectionFails verifies that a manifest nonce that does
// not match the server's expected nonce causes the runtime to fail —
// the server rejects the handshake with an immediate close.
func TestBadNonceConnectionFails(t *testing.T) {
	mcp := newFakeMCP(t, "the-real-nonce")
	manifest := writeManifest(t, mcp.socket, "the-wrong-nonce", nil)
	t.Setenv("LENNY_ADAPTER_MANIFEST", manifest)

	err := run(strings.NewReader(testMessage+"\n"), io.Discard, io.Discard)
	if err == nil {
		t.Fatal("run succeeded despite a bad manifest nonce; the MCP handshake must fail")
	}
	if !strings.Contains(err.Error(), "platform MCP") {
		t.Errorf("error %v does not mention the platform MCP connection failure", err)
	}
}

// TestNewerManifestVersionRejected verifies the §4.7 forward-compat
// rule: a manifest version higher than supported is rejected.
func TestNewerManifestVersionRejected(t *testing.T) {
	body, _ := json.Marshal(map[string]any{
		"version":           2,
		"connectorServers":  []any{},
		"runtimeMcpServers": []any{},
		"adapterLocalTools": []any{},
	})
	path := filepath.Join(t.TempDir(), "adapter-manifest.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if _, err := loadManifest(path); err == nil {
		t.Error("loadManifest accepted a version-2 manifest; every version increment is breaking")
	}
}

// TestSequentialMessages verifies each message produces its own
// response and the seq counter advances.
func TestSequentialMessages(t *testing.T) {
	mcp := newFakeMCP(t, "n1")
	manifest := writeManifest(t, mcp.socket, "n1", nil)
	frames, stderr := runRuntime(t, manifest, []string{testMessage, testMessage, testMessage})
	if len(frames) != 3 {
		t.Fatalf("got %d frames, want 3\nstderr: %s", len(frames), stderr)
	}
	for i, f := range frames {
		if f["type"] != "response" {
			t.Errorf("frame %d type = %v, want response", i, f["type"])
		}
	}
}

// --- helpers under test ------------------------------------------------

func TestEchoPartsStampsSchemaVersion(t *testing.T) {
	in := []messagePart{{Type: "text", Inline: "x"}}
	out := echoParts(in, 7)
	if len(out) != 1 {
		t.Fatalf("echoParts len = %d, want 1", len(out))
	}
	if out[0].SchemaVersion != 1 {
		t.Errorf("schemaVersion = %d, want 1", out[0].SchemaVersion)
	}
	if !strings.Contains(out[0].Inline, "seq=7") {
		t.Errorf("inline = %q, want it to carry seq=7", out[0].Inline)
	}
}

func TestEchoPartsPassesThroughNonText(t *testing.T) {
	in := []messagePart{{Type: "file", Ref: "lenny-blob://x", SchemaVersion: 3}}
	out := echoParts(in, 1)
	if out[0].Ref != "lenny-blob://x" || out[0].Type != "file" {
		t.Errorf("non-text part not passed through verbatim: %+v", out[0])
	}
	if out[0].SchemaVersion != 3 {
		t.Errorf("schemaVersion = %d, want the original 3 preserved", out[0].SchemaVersion)
	}
}

func TestDecodeTaskResultsListAndSingle(t *testing.T) {
	list, err := decodeTaskResults(json.RawMessage(`[{"taskId":"a","state":"completed"}]`))
	if err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 || list[0].TaskID != "a" {
		t.Errorf("list decode = %+v, want one result with taskId a", list)
	}
	single, err := decodeTaskResults(json.RawMessage(`{"taskId":"b","state":"failed"}`))
	if err != nil {
		t.Fatalf("decode single: %v", err)
	}
	if len(single) != 1 || single[0].TaskID != "b" {
		t.Errorf("single decode = %+v, want one result with taskId b", single)
	}
}

// TestChildFailurePropagates verifies that when the child settles in a
// non-completed state the runtime emits a response carrying an error
// rather than echoing a result.
func TestChildFailurePropagates(t *testing.T) {
	mcp := newFakeMCP(t, "n1")
	mcp.mu.Lock()
	mcp.failChildren = true
	mcp.mu.Unlock()
	manifest := writeManifest(t, mcp.socket, "n1", nil)

	frames, _ := runRuntime(t, manifest, []string{testMessage})
	if len(frames) != 1 {
		t.Fatalf("got %d frames, want 1", len(frames))
	}
	errObj, ok := frames[0]["error"].(map[string]any)
	if !ok {
		t.Fatalf("response did not carry an error object: %v", frames[0])
	}
	if _, hasCode := errObj["code"]; !hasCode {
		t.Errorf("response error has no code: %v", errObj)
	}
}
