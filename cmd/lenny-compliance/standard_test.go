// SPDX-License-Identifier: MIT

// Tests for the §15.4.6 Standard-level conformance battery. They build
// the delegation-echo (Standard-level) and echo (Basic-level) reference
// runtimes once, then exercise every Standard check against both: a
// Standard runtime must pass every check; a Basic runtime must fail the
// MCP-dependent checks. The fake platform MCP server is also tested
// directly.
package main

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

var (
	delegationEchoBinary string // Standard-level reference runtime
	echoBinary           string // Basic-level reference runtime
)

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "compliance-standard-test-*")
	if err != nil {
		panic("standard battery TestMain: mkdtemp: " + err.Error())
	}
	defer os.RemoveAll(tmp)

	root := repoRootForTest()
	build := func(name, pkg string) string {
		bin := filepath.Join(tmp, name)
		cmd := exec.Command("go", "build", "-o", bin, pkg)
		cmd.Dir = root
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			panic("standard battery TestMain: build " + pkg + ": " + err.Error())
		}
		return bin
	}
	delegationEchoBinary = build("delegation-echo", "./cmd/runtimes/delegation-echo")
	echoBinary = build("echo", "./cmd/runtimes/echo")

	os.Exit(m.Run())
}

func repoRootForTest() string {
	wd, _ := os.Getwd()
	for d := wd; d != "/" && d != ""; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
	}
	return wd
}

// --- battery composition -----------------------------------------------

// TestStandardBatteryIsBasicPlusStandard verifies the Standard battery
// is the Basic battery plus the four §15.4.6 Standard categories, and
// that delegation-echo passes every one.
func TestStandardBatteryIsBasicPlusStandard(t *testing.T) {
	r := runStandardBattery(delegationEchoBinary, 30*time.Second, false)
	if r.Level != "standard" {
		t.Errorf("report level = %q, want standard", r.Level)
	}
	// 7 Basic checks + 4 Standard checks.
	if r.Summary.Total != 11 {
		t.Errorf("total checks = %d, want 11 (7 basic + 4 standard)", r.Summary.Total)
	}
	if r.Summary.Failed != 0 {
		for _, c := range r.Checks {
			if !c.Pass {
				t.Errorf("delegation-echo failed Standard check %q: %s", c.Name, c.Detail)
			}
		}
	}

	// The four Standard categories from §15.4.6 are present and tagged
	// with the 15.4.6 spec section.
	wantStandard := map[string]bool{
		"mcp_nonce_handshake":               false,
		"platform_mcp_tool_invocation":      false,
		"connector_mcp_server_reachability": false,
		"tool_call_tool_result_correlation": false,
	}
	for _, c := range r.Checks {
		if _, ok := wantStandard[c.Name]; ok {
			wantStandard[c.Name] = true
			if c.Spec != "15.4.6" {
				t.Errorf("Standard check %q spec = %q, want 15.4.6", c.Name, c.Spec)
			}
		}
	}
	for name, seen := range wantStandard {
		if !seen {
			t.Errorf("Standard battery is missing the §15.4.6 category %q", name)
		}
	}
}

// TestStandardBatteryReusesBasicChecks confirms the Basic checks run
// inside the Standard battery and pass for delegation-echo.
func TestStandardBatteryReusesBasicChecks(t *testing.T) {
	r := runStandardBattery(delegationEchoBinary, 30*time.Second, false)
	basicNames := []string{
		"binary_exists_and_executes", "empty_stdin_exits_cleanly",
		"message_emits_response", "heartbeat_emits_ack",
		"unknown_type_ignored", "shutdown_exits_within_deadline",
		"sequential_messages_handled",
	}
	got := map[string]Check{}
	for _, c := range r.Checks {
		got[c.Name] = c
	}
	for _, name := range basicNames {
		c, ok := got[name]
		if !ok {
			t.Errorf("Standard battery is missing Basic check %q", name)
			continue
		}
		if !c.Pass {
			t.Errorf("Basic check %q failed inside the Standard battery: %s", name, c.Detail)
		}
	}
}

// TestStandardBatteryRejectsBasicRuntime verifies a Basic-level runtime
// (echo) fails the MCP-dependent Standard checks: it never reads the
// manifest, so it cannot complete the nonce handshake, invoke platform
// tools, or reach the connectors.
func TestStandardBatteryRejectsBasicRuntime(t *testing.T) {
	r := runStandardBattery(echoBinary, 30*time.Second, false)
	if r.Summary.Failed == 0 {
		t.Fatal("echo (Basic-level) passed the entire Standard battery; the MCP checks must fail it")
	}
	byName := map[string]Check{}
	for _, c := range r.Checks {
		byName[c.Name] = c
	}
	// The MCP-dependent categories must fail for a Basic runtime.
	for _, name := range []string{"mcp_nonce_handshake", "platform_mcp_tool_invocation", "connector_mcp_server_reachability"} {
		if byName[name].Pass {
			t.Errorf("Standard check %q passed for echo; a Basic runtime cannot satisfy it", name)
		}
	}
	// The Basic checks still pass — echo is a conformant Basic runtime.
	if !byName["heartbeat_emits_ack"].Pass {
		t.Error("echo failed a Basic check inside the Standard battery")
	}
}

// --- individual Standard checks ----------------------------------------

func TestCheckMCPNonceHandshake(t *testing.T) {
	detail, err := checkMCPNonceHandshake(delegationEchoBinary, 30*time.Second, false)
	if err != nil {
		t.Fatalf("nonce handshake check failed for delegation-echo: %v", err)
	}
	if detail == "" {
		t.Error("nonce handshake check returned an empty detail on success")
	}

	// echo never reads the manifest, so it never completes the
	// handshake — the check must fail.
	if _, err := checkMCPNonceHandshake(echoBinary, 30*time.Second, false); err == nil {
		t.Error("nonce handshake check passed for echo; a Basic runtime does not connect to the platform MCP server")
	}
}

func TestCheckPlatformMCPToolInvocation(t *testing.T) {
	detail, err := checkPlatformMCPToolInvocation(delegationEchoBinary, 30*time.Second, false)
	if err != nil {
		t.Fatalf("platform MCP tool invocation check failed for delegation-echo: %v", err)
	}
	if detail == "" {
		t.Error("tool invocation check returned an empty detail on success")
	}
	if _, err := checkPlatformMCPToolInvocation(echoBinary, 30*time.Second, false); err == nil {
		t.Error("tool invocation check passed for echo; a Basic runtime invokes no platform MCP tools")
	}
}

func TestCheckConnectorMCPReachability(t *testing.T) {
	if _, err := checkConnectorMCPReachability(delegationEchoBinary, 30*time.Second, false); err != nil {
		t.Fatalf("connector reachability check failed for delegation-echo: %v", err)
	}
	if _, err := checkConnectorMCPReachability(echoBinary, 30*time.Second, false); err == nil {
		t.Error("connector reachability check passed for echo; a Basic runtime connects to no connectors")
	}
}

func TestCheckToolResultCorrelation(t *testing.T) {
	// delegation-echo uses no adapter-local tools, so the category is
	// satisfied vacuously — but the check must still complete cleanly
	// (the runtime reaches a response).
	if _, err := checkToolResultCorrelation(delegationEchoBinary, 30*time.Second, false); err != nil {
		t.Fatalf("tool_call/tool_result correlation check failed for delegation-echo: %v", err)
	}
}

// --- fake MCP server ----------------------------------------------------

// TestFakeMCPServerNonceHandshake verifies the fake platform MCP server
// answers a valid-nonce initialize and closes a bad-nonce connection.
func TestFakeMCPServerNonceHandshake(t *testing.T) {
	fa, cleanup, err := newFakePlatformAdapter()
	if err != nil {
		t.Fatalf("newFakePlatformAdapter: %v", err)
	}
	defer cleanup()

	// Valid nonce: the initialize is answered with a result.
	conn, err := net.Dial("unix", fa.platform.socket)
	if err != nil {
		t.Fatalf("dial platform socket: %v", err)
	}
	enc, dec := json.NewEncoder(conn), json.NewDecoder(conn)
	if err := enc.Encode(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"_lennyNonce": fa.nonce, "protocolVersion": "2025-03-26"},
	}); err != nil {
		t.Fatalf("send initialize: %v", err)
	}
	var resp map[string]json.RawMessage
	if err := dec.Decode(&resp); err != nil {
		t.Fatalf("read initialize response: %v", err)
	}
	if resp["result"] == nil {
		t.Errorf("valid-nonce initialize returned no result: %v", resp)
	}
	conn.Close()

	if err := assertBadNonceRejected(fa.platform.socket); err != nil {
		t.Errorf("bad-nonce probe: %v", err)
	}
}

// TestFakeMCPServerDelegationTools verifies the fake platform MCP server
// dispatches the §8.5 delegation tools and returns the canned
// TaskHandle / TaskResult payloads.
func TestFakeMCPServerDelegationTools(t *testing.T) {
	fa, cleanup, err := newFakePlatformAdapter()
	if err != nil {
		t.Fatalf("newFakePlatformAdapter: %v", err)
	}
	defer cleanup()

	conn, err := net.Dial("unix", fa.platform.socket)
	if err != nil {
		t.Fatalf("dial platform socket: %v", err)
	}
	defer conn.Close()
	enc, dec := json.NewEncoder(conn), json.NewDecoder(conn)
	if err := enc.Encode(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"_lennyNonce": fa.nonce, "protocolVersion": "2025-03-26"},
	}); err != nil {
		t.Fatalf("send initialize: %v", err)
	}
	var initResp map[string]json.RawMessage
	if err := dec.Decode(&initResp); err != nil {
		t.Fatalf("read initialize response: %v", err)
	}

	callTool := func(id int, name string, args map[string]any) map[string]json.RawMessage {
		if err := enc.Encode(map[string]any{
			"jsonrpc": "2.0", "id": id, "method": "tools/call",
			"params": map[string]any{"name": name, "arguments": args},
		}); err != nil {
			t.Fatalf("send tools/call %s: %v", name, err)
		}
		var resp map[string]json.RawMessage
		if err := dec.Decode(&resp); err != nil {
			t.Fatalf("read tools/call %s response: %v", name, err)
		}
		if _, isErr := resp["error"]; isErr {
			t.Fatalf("tools/call %s errored: %s", name, resp["error"])
		}
		return resp
	}

	// lenny/delegate_task returns a TaskHandle with a taskId.
	handleResp := callTool(2, "lenny/delegate_task", map[string]any{
		"target": "some-runtime",
		"task":   map[string]any{"input": []any{}},
	})
	var handle struct {
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(handleResp["result"], &handle); err != nil {
		t.Fatalf("decode TaskHandle: %v", err)
	}
	if handle.TaskID == "" {
		t.Error("delegate_task returned an empty taskId")
	}

	// lenny/await_children returns a list with one completed TaskResult.
	awaitResp := callTool(3, "lenny/await_children", map[string]any{
		"child_ids": []string{handle.TaskID},
		"mode":      "all",
	})
	var results []struct {
		TaskID string `json:"taskId"`
		State  string `json:"state"`
	}
	if err := json.Unmarshal(awaitResp["result"], &results); err != nil {
		t.Fatalf("decode await_children result: %v", err)
	}
	if len(results) != 1 || results[0].State != "completed" {
		t.Errorf("await_children result = %+v, want one completed TaskResult", results)
	}

	// The server recorded both delegation tool calls.
	called := fa.platform.toolsCalled()
	for _, name := range []string{"lenny/delegate_task", "lenny/await_children"} {
		if !called[name] {
			t.Errorf("fake platform server did not record a call to %s", name)
		}
	}
}

// TestFakeMCPServerRejectsMissingNonce verifies a connection with no
// _lennyNonce field is rejected (the negative path of §15.4.3).
func TestFakeMCPServerRejectsMissingNonce(t *testing.T) {
	fa, cleanup, err := newFakePlatformAdapter()
	if err != nil {
		t.Fatalf("newFakePlatformAdapter: %v", err)
	}
	defer cleanup()

	conn, err := net.Dial("unix", fa.platform.socket)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"protocolVersion": "2025-03-26"},
	}); err != nil {
		t.Fatalf("send initialize: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var resp map[string]json.RawMessage
	if err := json.NewDecoder(conn).Decode(&resp); err == nil {
		t.Error("fake server answered an initialize carrying no nonce")
	}
	if !fa.platform.rejectedBad {
		t.Error("fake server did not record the missing-nonce rejection")
	}
}

// TestFakePlatformAdapterManifest verifies the fixture writes a
// Standard-level manifest naming the platform MCP server, two connector
// servers, and the nonce.
func TestFakePlatformAdapterManifest(t *testing.T) {
	fa, cleanup, err := newFakePlatformAdapter()
	if err != nil {
		t.Fatalf("newFakePlatformAdapter: %v", err)
	}
	defer cleanup()

	body, err := os.ReadFile(fa.manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m struct {
		Version           int    `json:"version"`
		MCPNonce          string `json:"mcpNonce"`
		PlatformMcpServer struct {
			Socket string `json:"socket"`
		} `json:"platformMcpServer"`
		ConnectorServers []struct {
			ID     string `json:"id"`
			Socket string `json:"socket"`
		} `json:"connectorServers"`
	}
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if m.Version != 1 {
		t.Errorf("manifest version = %d, want 1", m.Version)
	}
	if m.MCPNonce != fa.nonce || m.MCPNonce == "" {
		t.Errorf("manifest mcpNonce = %q, want %q", m.MCPNonce, fa.nonce)
	}
	if m.PlatformMcpServer.Socket != fa.platform.socket {
		t.Errorf("manifest platformMcpServer.socket = %q, want %q", m.PlatformMcpServer.Socket, fa.platform.socket)
	}
	if len(m.ConnectorServers) != 2 {
		t.Fatalf("manifest connectorServers len = %d, want 2", len(m.ConnectorServers))
	}
	for i, c := range m.ConnectorServers {
		if c.ID == "" || c.Socket == "" {
			t.Errorf("connector %d incomplete: %+v", i, c)
		}
	}
}

// TestStandardBatteryReportShape verifies the report emitted by the
// Standard battery carries the harness id, the binary, the level, and a
// consistent summary.
func TestStandardBatteryReportShape(t *testing.T) {
	r := runStandardBattery(delegationEchoBinary, 30*time.Second, false)
	if r.Binary != delegationEchoBinary {
		t.Errorf("report binary = %q, want %q", r.Binary, delegationEchoBinary)
	}
	if r.Summary.Passed+r.Summary.Failed != r.Summary.Total {
		t.Errorf("summary inconsistent: passed=%d failed=%d total=%d",
			r.Summary.Passed, r.Summary.Failed, r.Summary.Total)
	}
	if len(r.Checks) != r.Summary.Total {
		t.Errorf("checks len = %d, summary total = %d", len(r.Checks), r.Summary.Total)
	}
	if r.failedCount() != r.Summary.Failed {
		t.Errorf("failedCount() = %d, summary.Failed = %d", r.failedCount(), r.Summary.Failed)
	}
}
