// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind test for the §25.12 MCP Management Server. §25.12
// places the ManagementMCPAdapter in lenny-ops at /mcp/management on
// port 8090 and has it serve Lenny's own management tools: an
// MCP-capable agent negotiates the handshake (initialize), discovers the
// tool inventory (tools/list), and invokes a tool (tools/call), whose
// result is the underlying ops endpoint's response.
//
// The §25.12 management server is unit-tested in-process
// (pkg/ops/mcp, pkg/ops/opsserver) and its generated tool inventory is
// pinned by a tier-0 drift test, but no test drives /mcp/management
// through the deployed lenny-ops binary — its real HTTP listener on port
// 8090, the §25.4 auth-middleware chain the request traverses, the MCP
// adapter, and the in-process ops handler the tool call replays against.
// This test closes that gap by reaching the deployed lenny-ops Service
// over a port-forward and driving the full handshake-list-invoke
// sequence against the live binary.
//
// The e2e chart install runs lenny-ops in its dev posture (no OIDC
// issuer or bearer-trust key is wired, so the §25.4 auth gate admits the
// request through its dev-header fallback rather than a verified bearer,
// and cluster-internal mTLS is disabled). The deployment-topology halves
// the finding also names — the Ingress front door and the
// lenny-ops-allow-ingress-from-ingress-controller NetworkPolicy — are
// opt-in (ops.ingress.enabled) and not rendered by the e2e overlay; the
// NetworkPolicy admission model is covered structurally by the tier-0
// render tests and by the gateway-facing ingress_test.go. This test
// pins the observable §25.12 contract: the management MCP server is
// reachable and functional on the deployed ops binary.

package tier5_e2e_kind_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// opsHTTPPort is the port the lenny-ops HTTP surface binds, matching the
// §25.12 architecture note ("at /mcp/management on port 8090") and the
// chart's ops.httpPort default.
const opsHTTPPort = 8090

// opsMCPToolCall posts a JSON-RPC 2.0 request to the deployed lenny-ops
// /mcp/management endpoint and returns the raw, still-encoded `result`
// field. The dev-mode identity headers (X-Lenny-Tenant-ID /
// X-Lenny-Roles / X-Lenny-User-ID) ride along; the e2e ops binary runs
// with no bearer verifier, so its §25.4 auth chain resolves the caller
// from these headers rather than a verified JWT. A JSON-RPC error or a
// non-200 HTTP status fails the test.
func opsMCPToolCall(t *testing.T, baseURL, method string, params map[string]any) json.RawMessage {
	t.Helper()
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
	}
	if params != nil {
		req["params"] = params
	}
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal %s request: %v", method, err)
	}
	httpReq, err := http.NewRequest(http.MethodPost, baseURL+"/mcp/management", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build %s request: %v", method, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Lenny-Tenant-ID", "platform")
	httpReq.Header.Set("X-Lenny-Roles", "platform-admin")
	httpReq.Header.Set("X-Lenny-User-ID", "alice")

	hc := &http.Client{Timeout: 30 * time.Second}
	res, err := hc.Do(httpReq)
	if err != nil {
		t.Fatalf("POST /mcp/management %s: %v", method, err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read /mcp/management %s response: %v", method, err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("§25.12 MCP %s on the deployed lenny-ops: HTTP status %d, body=%s", method, res.StatusCode, raw)
	}
	var rpc struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &rpc); err != nil {
		t.Fatalf("decode /mcp/management %s response: %v; body=%s", method, err, raw)
	}
	if rpc.Error != nil {
		t.Fatalf("§25.12 MCP %s returned a JSON-RPC error: %d %s", method, rpc.Error.Code, rpc.Error.Message)
	}
	if rpc.Result == nil {
		t.Fatalf("§25.12 MCP %s returned no result; body=%s", method, raw)
	}
	return rpc.Result
}

// spec: 25.12 ("MCP Management Server" — "The `ManagementMCPAdapter`
// lives in `lenny-ops` at `/mcp/management` on port 8090 ... serves
// Lenny's own management tools"; "The following tools are exposed via
// the MCP `tools/list` method"; Tool Inventory maps `lenny_ops_health_get`
// to `GET /v1/admin/ops/health`, "Get lenny-ops self-health").
// diagnosis: a failure here means the §25.12 management MCP server is
// unreachable or non-functional on the deployed lenny-ops binary. The
// path port 8090 -> lenny-ops HTTP listener -> §25.4 auth middleware ->
// MCP adapter -> ops handler is exercised end to end: the handshake
// (initialize), the tool inventory (tools/list), and a tool invocation
// (tools/call lenny_ops_health_get) that replays the mapped GET
// /v1/admin/ops/health against the ops mux and returns its response as
// the JSON-RPC result. The in-process pkg/ops/mcp and pkg/ops/opsserver
// suites pin the adapter's construction; this is the only test that
// drives the adapter as mounted on the real ops binary, reached by a
// live client over the port-forwarded Service. A broken listener, an
// auth-wiring regression, a missing tool, or a tool call that no longer
// reaches its ops handler surfaces here first.
func TestMCPManagementServerOnDeployedOps(t *testing.T) {
	c := kind.InstallLenny(t)

	if !t5DeploymentReady(t, c, "lenny-ops") {
		t.Skip("precondition not met: lenny-ops is not Ready; the §25.12 management MCP server is served by lenny-ops")
	}

	// Reach the deployed lenny-ops HTTP surface over a port-forward to
	// its Service. port-forward tunnels through the kube-apiserver to the
	// pod's loopback, so it exercises the real listener and auth chain
	// without needing the opt-in Ingress the e2e overlay does not render.
	baseURL, stop := c.PortForward(t, "svc/lenny-ops", t5SystemNS, opsHTTPPort)
	defer stop()

	// --- initialize: the §25.12 handshake reports the protocol version,
	// the management server's serverInfo, and the tools capability.
	initRaw := opsMCPToolCall(t, baseURL, "initialize", map[string]any{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "tier5-mcp-management-test", "version": "0.0.0"},
	})
	var initResult struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
		Capabilities struct {
			Tools *json.RawMessage `json:"tools"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(initRaw, &initResult); err != nil {
		t.Fatalf("decode initialize result: %v; raw=%s", err, initRaw)
	}
	if initResult.ProtocolVersion == "" {
		t.Errorf("§25.12 initialize handshake returned an empty protocolVersion; raw=%s", initRaw)
	}
	if initResult.ServerInfo.Name == "" {
		t.Errorf("§25.12 initialize handshake returned no serverInfo.name; the management MCP server must identify itself; raw=%s", initRaw)
	}
	if initResult.Capabilities.Tools == nil {
		t.Errorf("§25.12 initialize handshake did not advertise the tools capability; the management server exposes tools; raw=%s", initRaw)
	}
	t.Logf("§25.12 initialize: protocolVersion=%q serverInfo.name=%q version=%q",
		initResult.ProtocolVersion, initResult.ServerInfo.Name, initResult.ServerInfo.Version)

	// --- tools/list: the management server exposes its tool inventory.
	// Assert it is non-empty and includes lenny_ops_health_get, the
	// ops-owned self-health tool §25.12 lists. Each tool carries a name
	// and an inputSchema per the §25.12 tool descriptor.
	listRaw := opsMCPToolCall(t, baseURL, "tools/list", nil)
	var listing struct {
		Tools []struct {
			Name        string          `json:"name"`
			InputSchema json.RawMessage `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(listRaw, &listing); err != nil {
		t.Fatalf("decode tools/list result: %v; raw=%s", err, listRaw)
	}
	if len(listing.Tools) == 0 {
		t.Fatalf("§25.12 tools/list returned an empty inventory on the deployed lenny-ops; the management server exposes the operability tools")
	}
	const healthTool = "lenny_ops_health_get"
	var haveHealthTool bool
	for _, tool := range listing.Tools {
		if tool.Name == "" {
			t.Errorf("§25.12 tools/list returned a tool with no name; raw=%s", listRaw)
		}
		if len(tool.InputSchema) == 0 {
			t.Errorf("§25.12 tools/list tool %q carries no inputSchema; every §25.12 tool is defined with a JSON Schema inputSchema", tool.Name)
		}
		if tool.Name == healthTool {
			haveHealthTool = true
		}
	}
	if !haveHealthTool {
		t.Fatalf("§25.12 tools/list on the deployed lenny-ops does not expose %q; the Tool Inventory lists it (maps to GET /v1/admin/ops/health)", healthTool)
	}
	t.Logf("§25.12 tools/list: %d tools exposed, including %q", len(listing.Tools), healthTool)

	// --- tools/call: invoke lenny_ops_health_get. §25.12 translates the
	// invocation into GET /v1/admin/ops/health and replays it against the
	// ops mux; the handler's response is returned as the tool result. A
	// successful call is isError:false with the endpoint's JSON body in a
	// text content block.
	callRaw := opsMCPToolCall(t, baseURL, "tools/call", map[string]any{
		"name":      healthTool,
		"arguments": map[string]any{},
	})
	var callResult struct {
		IsError bool `json:"isError"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(callRaw, &callResult); err != nil {
		t.Fatalf("decode tools/call result: %v; raw=%s", err, callRaw)
	}
	if callResult.IsError {
		t.Fatalf("§25.12 tools/call %q returned isError:true on the deployed lenny-ops; the invocation did not reach its ops handler successfully; result=%s",
			healthTool, callRaw)
	}
	if len(callResult.Content) == 0 || callResult.Content[0].Text == "" {
		t.Fatalf("§25.12 tools/call %q returned no content; the ops self-health endpoint's response body is the tool result; result=%s",
			healthTool, callRaw)
	}
	// The tool result's text block is the GET /v1/admin/ops/health JSON
	// body. Confirm it decodes as a JSON object carrying the self-health
	// status field the endpoint reports, so the call reached the handler
	// rather than returning an empty or malformed envelope.
	var health struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(callResult.Content[0].Text), &health); err != nil {
		t.Fatalf("§25.12 tools/call %q content is not the ops self-health JSON body: %v; text=%s",
			healthTool, err, callResult.Content[0].Text)
	}
	if health.Status == "" {
		t.Errorf("§25.12 tools/call %q returned a self-health body with no status field; text=%s",
			healthTool, callResult.Content[0].Text)
	}
	t.Logf("§25.12 tools/call %q reached the ops self-health handler: status=%q", healthTool, health.Status)
}
