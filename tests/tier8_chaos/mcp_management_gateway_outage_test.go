// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos test for the §25.12 MCP Management Server
// "Unhealthy-endpoint behavior" during a real gateway outage.
//
// §25.12 states: "When an underlying endpoint is unreachable (e.g.,
// gateway is down and the tool maps to a gateway endpoint), the MCP
// adapter returns -32000 (generic server error) with data.code:
// "ENDPOINT_UNAVAILABLE" and the standard retryable: true. It does not
// remove the tool from tools/list during the outage — removing tools
// would cause confusing client behavior (tools "disappearing" from the
// inventory)."
//
// The existing coverage of this behavior
// (pkg/ops/mcp/server_test.go::TestToolsCallEndpointUnavailable and
// pkg/ops/opsserver/mcp_test.go::TestMCPManagementUnavailableEndpointMapsTo32000)
// simulates the outage with a nil/unconfigured local ops invoker and
// exercises only lenny_health_get, an ops-owned tool. Neither drives the
// spec's own example: a platform-management tool whose backend is the
// gateway admin API, invoked through /mcp/management while the gateway
// itself is down. That path requires the management server to dispatch a
// gateway-owned tool (the generated admin.* tools, e.g.
// admin.get_ca_rotation -> GET /v1/admin/ca-rotation) through
// GatewayClient rather than by replaying the request against the
// lenny-ops mux.
//
// spec: 25.12
package tier8_chaos_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// gatewayOwnedTool is the §25.12-generated tool this test invokes. It
// maps to a gateway admin-API endpoint (GET /v1/admin/ca-rotation) with
// no path parameters, so a single fixed request body exercises it.
const gatewayOwnedTool = "admin.get_ca_rotation"

// mcpManagementHTTPPort is the port the lenny-ops HTTP surface binds,
// matching the §25.12 architecture note ("at /mcp/management on port
// 8090") and the chart's ops.httpPort default.
const mcpManagementHTTPPort = 8090

// mcpManagementRPC posts a JSON-RPC 2.0 request to the deployed
// lenny-ops /mcp/management endpoint and returns the HTTP status and the
// decoded envelope (result and/or error), without failing the test on a
// JSON-RPC error — the outage case this test drives expects one.
func mcpManagementRPC(t *testing.T, baseURL, method string, params map[string]any) (int, map[string]any) {
	t.Helper()
	req := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method}
	if params != nil {
		req["params"] = params
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal %s request: %v", method, err)
	}
	httpReq, err := http.NewRequest(http.MethodPost, baseURL+"/mcp/management", bytes.NewReader(raw))
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
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read /mcp/management %s response: %v", method, err)
	}
	var out map[string]any
	if len(body) > 0 {
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("decode /mcp/management %s response: %v; body=%s", method, err, body)
		}
	}
	return res.StatusCode, out
}

// spec: 25.12
// diagnosis: a failure here means the §25.12 "Unhealthy-endpoint
// behavior" contract does not hold for a gateway-owned tool during a
// genuine gateway outage: either the MCP adapter did not map the failed
// dispatch to -32000 ENDPOINT_UNAVAILABLE with retryable:true, or the
// tool was removed from tools/list while the gateway was down (§25.12
// requires it to remain listed so a client's inventory does not appear
// to change mid-outage).
func TestMCPManagementGatewayOwnedToolDuringGatewayOutage(t *testing.T) {
	t.Skip("the §25.12 gateway-proxy tool-call dispatch (routing a generated admin.* tool through GatewayClient to the " +
		"gateway admin API) is unbuilt: pkg/ops/mcp.Invoker has a single implementation (opsserver's opsInvoker), which " +
		"only replays a tool's mapped request against the lenny-ops mux, so a gateway-owned tool call has nowhere to " +
		"route to gateway or otherwise; this test documents the intended chaos exercise for when that dispatch exists")

	c := kind.InstallLenny(t)

	if !deploymentReady(t, c, gatewayDeployment) {
		t.Skipf("precondition not met: %s Deployment is not fully Ready (%s) before the chaos injection",
			gatewayDeployment, deploymentReadyState(t, c, gatewayDeployment))
	}
	if !deploymentReady(t, c, opsDeployment) {
		t.Skipf("precondition not met: %s Deployment is not fully Ready (%s) before the chaos injection",
			opsDeployment, deploymentReadyState(t, c, opsDeployment))
	}

	opsBase, _ := c.PortForward(t, "svc/"+opsDeployment, lennySystemNamespace, mcpManagementHTTPPort)

	// --- Preconditions (gateway UP): the tool call succeeds and the tool
	// is present in tools/list.
	if st, resp := mcpManagementRPC(t, opsBase, "tools/call", map[string]any{
		"name": gatewayOwnedTool, "arguments": map[string]any{},
	}); st != http.StatusOK || resp["error"] != nil {
		t.Skipf("precondition not met: %s tools/call did not succeed before the injection (status %d, resp %v)",
			gatewayOwnedTool, st, resp)
	}

	// --- Inject: scale the gateway to zero (a genuine outage — the
	// Service loses its endpoints).
	gatewayReplicas := scaleDownAndRestore(t, c, gatewayDeployment)
	if !waitDeploymentScaledDown(t, c, gatewayDeployment, storeRecoveryBound) {
		t.Fatalf("%s did not scale down to zero replicas after the scale command", gatewayDeployment)
	}
	if !pollUntil(storeRecoveryBound, 2*time.Second, func() bool { return endpointCount(t, c, gatewayDeployment) == 0 }) {
		t.Fatalf("Service %s still has endpoints after the Deployment scaled to zero; "+
			"the gateway is not genuinely unreachable", gatewayDeployment)
	}

	// --- Assert: the §25.12 unhealthy-endpoint mapping.
	_, callResp := mcpManagementRPC(t, opsBase, "tools/call", map[string]any{
		"name": gatewayOwnedTool, "arguments": map[string]any{},
	})
	rpcErr, _ := callResp["error"].(map[string]any)
	if rpcErr == nil {
		t.Fatalf("§25.12 tools/call for %s during a gateway outage returned no JSON-RPC error; want -32000 "+
			"ENDPOINT_UNAVAILABLE; resp=%v", gatewayOwnedTool, callResp)
	}
	if code, _ := rpcErr["code"].(float64); int(code) != -32000 {
		t.Errorf("§25.12 tools/call error code = %v, want -32000 (ENDPOINT_UNAVAILABLE) for %s during a gateway outage",
			rpcErr["code"], gatewayOwnedTool)
	}
	data, _ := rpcErr["data"].(map[string]any)
	if data == nil || data["code"] != "ENDPOINT_UNAVAILABLE" {
		t.Errorf("§25.12 tools/call error data.code = %v, want ENDPOINT_UNAVAILABLE for %s during a gateway outage",
			data, gatewayOwnedTool)
	}
	if data != nil {
		if retryable, ok := data["retryable"].(bool); !ok || !retryable {
			t.Errorf("§25.12 tools/call error data.retryable = %v, want true for %s during a gateway outage",
				data["retryable"], gatewayOwnedTool)
		}
	}

	// --- Assert: the tool is not removed from tools/list during the
	// outage.
	_, listResp := mcpManagementRPC(t, opsBase, "tools/list", nil)
	result, _ := listResp["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	var stillListed bool
	for _, raw := range tools {
		tool, _ := raw.(map[string]any)
		if tool["name"] == gatewayOwnedTool {
			stillListed = true
			break
		}
	}
	if !stillListed {
		t.Errorf("§25.12 tools/list omitted %s during the gateway outage; the spec requires the tool to remain "+
			"listed rather than disappear from the inventory", gatewayOwnedTool)
	}

	restoreDeployment(t, c, gatewayDeployment, gatewayReplicas)
}
