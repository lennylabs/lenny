// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract suite for the §4.1 dedicated MCP endpoint for
// type: mcp runtimes: "Serve dedicated MCP endpoints for `type: mcp`
// runtimes at `/mcp/runtimes/{name}`."
//
// A type: mcp runtime's agent is itself an MCP server (§5.1); the
// gateway relays a client's raw JSON-RPC body to that runtime's MCP
// server and returns its response verbatim. The handler's own unit
// tests (pkg/gateway/mcpfabric/mcpruntimes) cover the error envelopes
// (404 unknown runtime, 400 non-mcp, 503 no client) against a stub
// dispatcher, but they never drive a live type: mcp runtime, so the
// initialize / tools/list / tools/call exchanges that actually flow
// across the endpoint are unpinned against the MCP protocol schema. A
// conforming MCP client validates each frame against the published MCP
// schema before proceeding; a response that satisfies Lenny's own
// expectations but violates the real MCP contract (a wrong key casing,
// a missing required field, an inputSchema whose type is not the literal
// "object") would break such a client while passing every unit test.
// This suite closes that gap: it mounts the real §4.1 handler in front
// of the reference type: mcp runtime (cmd/runtimes/mcp-reference) and
// asserts each response frame validates against the vendored MCP schema.
package mcp_runtimes_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcpruntimes"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/tests/testinfra/mcpschema"
)

// referenceRuntimeName is the type: mcp runtime the endpoint is mounted
// against. It matches the reference runtime binary the suite spawns.
const referenceRuntimeName = "mcp-reference"

// stdioRelayDispatcher is a mcpruntimes.Dispatcher that relays the raw
// JSON-RPC request body to a running MCP-server subprocess over its
// stdin/stdout pipes and returns the raw response frame. It models the
// production per-runtime MCP client: the §4.1 endpoint hands the handler
// the request body and expects the runtime's raw JSON-RPC response back.
//
// The endpoint is request/response only, and the reference runtime
// answers one line per request line, so a mutex serialises access and
// each Dispatch writes one framed request and reads one framed response.
type stdioRelayDispatcher struct {
	mu     sync.Mutex
	stdin  io.Writer
	stdout *bufio.Reader
}

func (d *stdioRelayDispatcher) Dispatch(_ context.Context, _ string, body []byte) ([]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	// The reference MCP server reads newline-delimited JSON-RPC. The
	// handler passes the raw body through verbatim; trim any trailing
	// newline and re-frame so exactly one message is written.
	line := append([]byte(strings.TrimRight(string(body), "\n")), '\n')
	if _, err := d.stdin.Write(line); err != nil {
		return nil, err
	}
	resp, err := d.stdout.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// startReferenceRuntime builds and spawns cmd/runtimes/mcp-reference and
// returns a dispatcher relaying to its stdio pipes. The subprocess is
// terminated on test cleanup by closing its stdin (the reference
// runtime's clean-shutdown signal).
func startReferenceRuntime(t *testing.T) *stdioRelayDispatcher {
	t.Helper()
	root := repoRoot(t)
	bin := filepath.Join(t.TempDir(), "mcp-reference")
	build := exec.Command("go", "build", "-o", bin, "./cmd/runtimes/mcp-reference")
	build.Dir = root
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build mcp-reference: %v", err)
	}

	cmd := exec.Command(bin)
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start mcp-reference: %v", err)
	}
	t.Cleanup(func() {
		// Closing stdin is the reference runtime's clean-exit signal.
		_ = stdin.Close()
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	})
	return &stdioRelayDispatcher{stdin: stdin, stdout: bufio.NewReader(stdout)}
}

// newEndpoint mounts the real §4.1 mcpruntimes.Handler for a single
// type: mcp runtime backed by the reference runtime dispatcher and
// returns an httptest server plus the endpoint URL for that runtime.
func newEndpoint(t *testing.T, disp mcpruntimes.Dispatcher) string {
	t.Helper()
	store := runtimestore.NewMemory()
	if err := store.Create(context.Background(), runtimestore.Runtime{
		Name: referenceRuntimeName,
		Type: runtimestore.TypeMCP,
	}); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	mux := http.NewServeMux()
	mux.Handle("POST /mcp/runtimes/{name}", mcpruntimes.New(store, disp))
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts.URL + "/mcp/runtimes/" + referenceRuntimeName
}

// postFrame sends a JSON-RPC request to the endpoint and returns the raw
// response frame. It fails the test on a non-200 status so a schema
// assertion runs only on a real success frame.
func postFrame(t *testing.T, url, method string, params any) []byte {
	t.Helper()
	req := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method}
	if params != nil {
		req["params"] = params
	}
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal %s request: %v", method, err)
	}
	resp, err := http.Post(url, "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("POST %s: %v", method, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s response: %v", method, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s status = %d, want 200; body=%s", method, resp.StatusCode, raw)
	}
	return raw
}

// spec: §4.1 ("Serve dedicated MCP endpoints for `type: mcp` runtimes at
// `/mcp/runtimes/{name}`"; spec/04_system-components.md); §15.2 (the
// negotiated MCP version pins tool schemas and error formats)
// diagnosis: the §4.1 /mcp/runtimes/{name} surface relayed an MCP
// initialize / tools/list / tools/call exchange whose response frame no
// longer satisfies the published MCP JSON-RPC schema — a conforming MCP
// client would reject the frame even though Lenny's own unit tests round
// -trip it. Most likely the handler corrupted the runtime's raw response
// (re-encoding, truncation, or a wrong Content-Type) or the reference
// runtime's frame drifted from the MCP contract.
func TestMCPRuntimeEndpointExchangeConformsToSchema(t *testing.T) {
	disp := startReferenceRuntime(t)
	url := newEndpoint(t, disp)
	v := mcpschema.New(t, mcpschema.CurrentVersion)

	// initialize: the handshake result must satisfy MCP InitializeResult.
	initFrame := postFrame(t, url, "initialize", map[string]any{
		"protocolVersion": mcpschema.CurrentVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "contract-test", "version": "0.0.0"},
	})
	if err := v.ValidateResponseFrame(initFrame, mcpschema.DefInitializeResult); err != nil {
		t.Fatalf("initialize frame: %v", err)
	}

	// tools/list: the catalog result must satisfy ListToolsResult and
	// every advertised tool must satisfy the MCP Tool contract.
	listFrame := postFrame(t, url, "tools/list", map[string]any{})
	if err := v.ValidateResponseFrame(listFrame, mcpschema.DefListToolsResult); err != nil {
		t.Fatalf("tools/list frame: %v", err)
	}
	tools, err := v.Tools(resultOf(t, listFrame))
	if err != nil {
		t.Fatalf("tools/list catalog: %v", err)
	}
	if len(tools) == 0 {
		t.Fatal("tools/list returned no tools; nothing to validate against the MCP Tool schema")
	}

	// tools/call: the reference `echo` tool result must satisfy
	// CallToolResult and echo the input back in a text content block.
	callFrame := postFrame(t, url, "tools/call", map[string]any{
		"name":      "echo",
		"arguments": map[string]any{"text": "contract ok"},
	})
	if err := v.ValidateResponseFrame(callFrame, mcpschema.DefCallToolResult); err != nil {
		t.Fatalf("tools/call frame: %v", err)
	}
	if !strings.Contains(string(callFrame), "contract ok") {
		t.Errorf("tools/call result did not echo the input; frame=%s", callFrame)
	}
}

// spec: §4.1 (dedicated MCP endpoints for type: mcp runtimes); §15.2
// (negotiated MCP version pins result schemas)
// diagnosis: the MCP JSON-RPC schema validator no longer rejects frames
// that violate the published MCP contract, so the conformance assertion
// in TestMCPRuntimeEndpointExchangeConformsToSchema would pass
// vacuously. This guard proves the validator has teeth: a frame missing
// the required InitializeResult fields, and a frame that is not a
// JSON-RPC 2.0 success envelope, must both be rejected.
func TestMCPSchemaValidatorRejectsNonConformingFrames(t *testing.T) {
	v := mcpschema.New(t, mcpschema.CurrentVersion)

	// A well-formed envelope whose result omits the required
	// InitializeResult fields (protocolVersion, capabilities, serverInfo)
	// must fail the result-definition check.
	badResult := []byte(`{"jsonrpc":"2.0","id":1,"result":{"unexpected":"payload"}}`)
	if err := v.ValidateResponseFrame(badResult, mcpschema.DefInitializeResult); err == nil {
		t.Error("validator accepted a result missing every required InitializeResult field")
	}

	// A frame with the wrong jsonrpc version is not a valid JSON-RPC 2.0
	// envelope and must fail the envelope check.
	badEnvelope := []byte(`{"jsonrpc":"1.0","id":1,"result":{}}`)
	if err := v.ValidateResponseFrame(badEnvelope, mcpschema.DefInitializeResult); err == nil {
		t.Error("validator accepted a frame whose jsonrpc version is not 2.0")
	}
}

// resultOf extracts the still-encoded `result` field of a JSON-RPC frame.
func resultOf(t *testing.T, frame []byte) json.RawMessage {
	t.Helper()
	var env struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(frame, &env); err != nil {
		t.Fatalf("decode result: %v; frame=%s", err, frame)
	}
	return env.Result
}

// repoRoot walks up to the module root so `go build ./cmd/...` resolves.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, _ := os.Getwd()
	for d := wd; d != "/" && d != ""; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
	}
	t.Fatal("could not locate module root")
	return ""
}
