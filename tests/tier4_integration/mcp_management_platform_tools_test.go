//go:build integration

// SPDX-License-Identifier: MIT

// Tier-4 integration scaffold for the §25.12 MCP Management Server's
// platform-management dispatch. §25.12 states that every admin-API
// endpoint with documented RBAC is exposed as an MCP tool, that the
// platform-management tools (tenant lifecycle, pool CRUD, credential
// pool management, runtime registration, quota) are routed to the
// gateway admin API via GatewayClient, and that "an MCP-first agent can
// do anything a REST caller can." This test walks that user journey: it
// drives a platform-management tool through /mcp/management's tools/call
// path and asserts the mutation lands in the gateway admin API.
//
// The dispatch path this test exercises does not exist yet. The
// /mcp/management invoker replays every tool's request against the
// lenny-ops mux only, which mounts no gateway-owned admin route, so a
// platform-management tool has nowhere to route. The gateway-proxy
// routing that would give these tools a dispatch target is unbuilt, and
// there is no per-tool marker (the generated tool carries only its
// method and path, with no ops-versus-gateway owner) to decide which
// side owns a given tool. The test is a phase-gated scaffold until that
// routing lands.
package tier4_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	"github.com/lennylabs/lenny/tests/testinfra/opsprocess"
)

// TestMCPManagementPlatformToolDispatchE2E boots a gateway and a
// lenny-ops pointed at it, then drives the admin.create_pool
// platform-management tool through /mcp/management's tools/call path and
// asserts the created pool is readable through the gateway admin API,
// proving the tool call reached the gateway rather than 404-ing against
// the local ops mux.
//
// spec: §25.12 (MCP Management Server — platform-management tools route
// to the gateway admin API via GatewayClient; "an MCP-first agent can do
// anything a REST caller can").
// diagnosis: a failure means a platform-management MCP tool invoked
// through /mcp/management did not reach the gateway admin API. Either
// the gateway-proxy dispatch routing did not forward the tool call to
// the gateway, the propagated identity was dropped, or the mutation did
// not land in the gateway's store — any of which shows the §25.12
// dual-routing model diverged from the spec when driven end to end.
func TestMCPManagementPlatformToolDispatchE2E(t *testing.T) {
	t.Skip("blocked: the §25.12 gateway-proxy dispatch routing is unbuilt — /mcp/management tools/call replays against the local lenny-ops mux only, so a gateway-owned platform-management tool (admin.create_pool) has no dispatch target; the ops-versus-gateway routing predicate is also undesigned (the generated tool carries no owner marker)")

	gateway.SkipUnlessAvailable(t)
	opsprocess.SkipUnlessAvailable(t)

	gw := gateway.StartWith(t, "--dev-mode")
	ops := opsprocess.StartWith(t, "--gateway-url="+gw.BaseURL())

	ctx := context.Background()
	const poolName = "mcp-created-pool"

	// Drive the platform-management tool through the MCP management
	// server's tools/call path. §25.12 routes admin.create_pool to the
	// gateway admin API POST /v1/admin/pools.
	callBody, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "admin.create_pool",
			"arguments": map[string]any{
				"name":      poolName,
				"warmCount": 1,
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal tools/call request: %v", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ops.BaseURL()+"/mcp/management", bytes.NewReader(callBody))
	if err != nil {
		t.Fatalf("build /mcp/management request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// Dev-mode identity headers stand in for a platform-admin JWT.
	req.Header.Set("X-Lenny-Role", "platform-admin")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /mcp/management: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tools/call admin.create_pool: status %d, body %s", resp.StatusCode, body)
	}
	var rpc struct {
		Error  *json.RawMessage `json:"error"`
		Result struct {
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &rpc); err != nil {
		t.Fatalf("decode tools/call response: %v (body %s)", err, body)
	}
	if rpc.Error != nil || rpc.Result.IsError {
		t.Fatalf("tools/call admin.create_pool reported an error: %s", body)
	}

	// Assert the mutation landed in the gateway admin API: the pool the
	// MCP tool created is readable through the gateway's own REST surface.
	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, gw.BaseURL()+"/v1/admin/pools/"+poolName, nil)
	if err != nil {
		t.Fatalf("build gateway pool GET: %v", err)
	}
	getReq.Header.Set("X-Lenny-Role", "platform-admin")
	getResp, err := http.DefaultClient.Do(getReq)
	if err != nil {
		t.Fatalf("GET gateway pool: %v", err)
	}
	defer func() { _ = getResp.Body.Close() }()
	if getResp.StatusCode != http.StatusOK {
		gb, _ := io.ReadAll(getResp.Body)
		t.Fatalf("gateway pool %q not found after MCP create: status %d, body %s", poolName, getResp.StatusCode, gb)
	}
}
