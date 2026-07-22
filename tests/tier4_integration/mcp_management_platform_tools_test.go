//go:build integration

// SPDX-License-Identifier: MIT

// Tier-4 integration test for the §25.12 MCP Management Server's
// platform-management dispatch. §25.12 states that every admin-API
// endpoint with documented RBAC is exposed as an MCP tool, that the
// platform-management tools (tenant lifecycle, pool CRUD, credential
// pool management, runtime registration, quota) are routed to the
// gateway admin API via GatewayClient, and that "an MCP-first agent can
// do anything a REST caller can." This test walks that user journey: it
// drives the platform-management tools through /mcp/management's
// tools/call path and asserts each mutation lands in the gateway admin
// API, while an ops-owned tool stays on the local lenny-ops handler.
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

// mcpRPCResult is the decoded JSON-RPC envelope of a /mcp/management
// tools/call response: the transport-level error (if any) and the tool
// result with the §25.12 content and dry-run mapping.
type mcpRPCResult struct {
	Error  *json.RawMessage `json:"error"`
	Result struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		Meta map[string]any `json:"_meta"`
	} `json:"result"`
}

// callManagementTool drives one §25.12 tools/call against the deployed
// lenny-ops /mcp/management endpoint with the dev-mode platform-admin
// identity headers the §25.12 bridge forwards to the gateway
// (X-Lenny-Tenant-ID and X-Lenny-Roles plural, the headers the gateway
// auth layer consumes). It returns the decoded JSON-RPC envelope.
func callManagementTool(t *testing.T, ctx context.Context, opsBase, tool string, args map[string]any) mcpRPCResult {
	t.Helper()
	callBody, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": tool, "arguments": args},
	})
	if err != nil {
		t.Fatalf("marshal tools/call request: %v", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, opsBase+"/mcp/management", bytes.NewReader(callBody))
	if err != nil {
		t.Fatalf("build /mcp/management request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// Dev-mode identity headers stand in for a platform-admin JWT. The
	// §25.12 bridge forwards X-Lenny-Tenant-ID and X-Lenny-Roles to the
	// gateway under AllowDevHeaders, so the gateway re-authorizes the
	// forwarded platform-admin identity for a gateway-owned tool.
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set("X-Lenny-User-ID", "alice@acme.com")
	req.Header.Set("X-Lenny-Roles", "platform-admin")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /mcp/management %s: %v", tool, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tools/call %s: status %d, body %s", tool, resp.StatusCode, body)
	}
	var rpc mcpRPCResult
	if err := json.Unmarshal(body, &rpc); err != nil {
		t.Fatalf("decode %s response: %v (body %s)", tool, err, body)
	}
	return rpc
}

// gatewayAdminGET reads a gateway admin-API resource with the dev-mode
// platform-admin identity and returns the status and decoded body, so a
// test can confirm a mutation an MCP tool made landed in the gateway's
// own store.
func gatewayAdminGET(t *testing.T, ctx context.Context, gwBase, path string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, gwBase+path, nil)
	if err != nil {
		t.Fatalf("build gateway GET %s: %v", path, err)
	}
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set("X-Lenny-User-ID", "alice@acme.com")
	req.Header.Set("X-Lenny-Roles", "platform-admin")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET gateway %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return resp.StatusCode, out
}

// gatewayErrorCode extracts the §25.2 canonical error code from a tool
// result's content text, so a test can confirm a gateway-generated
// validation envelope (rather than a local 404) reached the agent
// verbatim.
func gatewayErrorCode(t *testing.T, text string) string {
	t.Helper()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(text), &env); err != nil {
		t.Fatalf("decode gateway error envelope %q: %v", text, err)
	}
	return env.Error.Code
}

// poolWarmCount reads the warmCount off a gateway admin-pool GET body so a
// test can confirm a scale mutation an MCP tool made landed (or, on the
// §25.2 preview path, did not land) in the gateway's own store.
func poolWarmCount(t *testing.T, ctx context.Context, gwBase, name string) int {
	t.Helper()
	code, body := gatewayAdminGET(t, ctx, gwBase, "/v1/admin/pools/"+name)
	if code != http.StatusOK {
		t.Fatalf("gateway pool %q not readable: status %d", name, code)
	}
	// warmCount is a JSON number; encoding/json decodes it into float64.
	wc, _ := body["warmCount"].(float64)
	return int(wc)
}

// TestMCPManagementPlatformToolDispatchE2E boots a gateway and a
// lenny-ops pointed at it, then drives the platform-management tools
// (pool CRUD, the operability scale path, and tenant lifecycle) through
// /mcp/management's tools/call path and asserts each mutation is readable
// through the gateway admin API, proving the tool call reached the gateway
// rather than 404-ing against the local ops mux. It also confirms an
// ops-owned tool (lenny_lock_acquire) still dispatches locally, so the
// dual-routing predicate keeps ops-owned tools on the lenny-ops handler.
//
// The confirm-gated non-happy path is the §25.17 scale mutation
// admin.set_pool_warm_count: invoked without confirm the gateway returns a
// §25.2 dry-run preview (isError:false, _meta.lenny.dryRun) and does not
// scale the pool, and only a confirm:true call lands the warm count. This
// is the tool whose gateway endpoint enforces the §25.2 confirm gate; the
// destructive DELETE tenant tool has no confirm precondition on its
// gateway handler, so its dispatch is proven by the state transition
// alone.
//
// spec: §25.12 (MCP Management Server — platform-management tools route
// to the gateway admin API via GatewayClient; "an MCP-first agent can do
// anything a REST caller can"); §25.2 (dry-run/confirm preview);
// §25.17 (the operability warm-count scale path).
// diagnosis: a failure means a platform-management MCP tool invoked
// through /mcp/management did not reach the gateway admin API. Either the
// gateway-proxy dispatch routing did not forward the tool call to the
// gateway, the propagated identity was dropped, the mutation did not land
// in the gateway's store, a confirm-less scale mutation applied without
// its §25.2 preview gate, or an ops-owned tool was misrouted to the
// gateway — any of which shows the §25.12 dual-routing model diverged
// from the spec when driven end to end.
func TestMCPManagementPlatformToolDispatchE2E(t *testing.T) {
	gateway.SkipUnlessAvailable(t)
	opsprocess.SkipUnlessAvailable(t)

	gw := gateway.StartWith(t, "--dev-mode")
	ops := opsprocess.StartWith(t, "--gateway-url="+gw.BaseURL())
	ctx := context.Background()

	// ---- pool create: admin.create_pool routes to POST /v1/admin/pools ----
	const poolName = "mcp-created-pool"
	rpc := callManagementTool(t, ctx, ops.BaseURL(), "admin.create_pool", map[string]any{
		"name":      poolName,
		"warmCount": 1,
	})
	if rpc.Error != nil || rpc.Result.IsError {
		t.Fatalf("admin.create_pool reported an error: %+v", rpc)
	}
	if code, _ := gatewayAdminGET(t, ctx, gw.BaseURL(), "/v1/admin/pools/"+poolName); code != http.StatusOK {
		t.Fatalf("gateway pool %q not found after MCP create: status %d", poolName, code)
	}

	// ---- pool update: admin.update_pool routes to PUT /v1/admin/pools/{name}.
	// The gateway enforces §15.1 optimistic concurrency (If-Match) on the
	// PUT, a header the MCP arg-to-request mapping does not carry, so the
	// gateway answers with its own ETAG_REQUIRED validation envelope. That
	// envelope re-emitting verbatim through the tool result proves the
	// gateway-owned PUT reached the gateway admin API rather than 404-ing
	// against the local ops mux ----
	rpc = callManagementTool(t, ctx, ops.BaseURL(), "admin.update_pool", map[string]any{
		"name":      poolName,
		"warmCount": 3,
	})
	if rpc.Error != nil {
		t.Fatalf("admin.update_pool returned a transport error: %v", *rpc.Error)
	}
	if len(rpc.Result.Content) == 0 {
		t.Fatalf("admin.update_pool returned no content: %+v", rpc)
	}
	if code := gatewayErrorCode(t, rpc.Result.Content[0].Text); code != "ETAG_REQUIRED" {
		t.Errorf("admin.update_pool gateway error code = %q, want ETAG_REQUIRED (the gateway's own PUT validation, proving gateway dispatch)", code)
	}

	// ---- scale without confirm: admin.set_pool_warm_count routes to PUT
	// /v1/admin/pools/{name}/warm-count. The gateway warm-count handler
	// enforces the §25.2 dry-run/confirm gate: without confirm:true it
	// returns a 200 preview (dryRun:true) and does NOT scale the pool, which
	// the invoker surfaces as isError:false with _meta.lenny.dryRun. The
	// pool's warmCount must remain at its created value so a retried
	// watchdog call cannot scale on an exploratory (confirm-less) invocation.
	// This is the non-happy path the destructive scale mutation must gate on ----
	rpc = callManagementTool(t, ctx, ops.BaseURL(), "admin.set_pool_warm_count", map[string]any{
		"name":    poolName,
		"minWarm": 3,
	})
	if rpc.Error != nil {
		t.Fatalf("admin.set_pool_warm_count (no confirm) returned a transport error: %v", *rpc.Error)
	}
	if rpc.Result.IsError {
		t.Fatalf("admin.set_pool_warm_count (no confirm) reported isError; a confirm-less scale must be a §25.2 preview, not a failure: %+v", rpc)
	}
	if dry, _ := rpc.Result.Meta["lenny.dryRun"].(bool); !dry {
		t.Errorf("admin.set_pool_warm_count (no confirm) _meta.lenny.dryRun = %v, want true (the §25.2 confirm gate must return a preview)", rpc.Result.Meta["lenny.dryRun"])
	}
	if wc := poolWarmCount(t, ctx, gw.BaseURL(), poolName); wc != 1 {
		t.Errorf("pool warmCount after confirm-less scale = %d, want 1 (a preview must not mutate the gateway store)", wc)
	}

	// ---- scale with confirm: the same tool with confirm:true applies the
	// mutation, and the new warm count lands in the gateway store, proving an
	// MCP-first agent can drive the §25.17 scale path a REST caller can ----
	rpc = callManagementTool(t, ctx, ops.BaseURL(), "admin.set_pool_warm_count", map[string]any{
		"name":    poolName,
		"minWarm": 3,
		"confirm": true,
	})
	if rpc.Error != nil || rpc.Result.IsError {
		t.Fatalf("admin.set_pool_warm_count (confirm:true) reported an error: %+v", rpc)
	}
	if dry, _ := rpc.Result.Meta["lenny.dryRun"].(bool); dry {
		t.Errorf("admin.set_pool_warm_count (confirm:true) _meta.lenny.dryRun = true, want a real mutation, not a preview")
	}
	if wc := poolWarmCount(t, ctx, gw.BaseURL(), poolName); wc != 3 {
		t.Errorf("pool warmCount after confirmed scale = %d, want 3 (the confirmed scale mutation did not land in the gateway store)", wc)
	}

	// ---- tenant create: admin.create_tenant routes to POST /v1/admin/tenants ----
	const tenantID = "mcp-created-tenant"
	rpc = callManagementTool(t, ctx, ops.BaseURL(), "admin.create_tenant", map[string]any{
		"id":          tenantID,
		"displayName": "MCP Created Tenant",
	})
	if rpc.Error != nil || rpc.Result.IsError {
		t.Fatalf("admin.create_tenant reported an error: %+v", rpc)
	}
	if code, _ := gatewayAdminGET(t, ctx, gw.BaseURL(), "/v1/admin/tenants/"+tenantID); code != http.StatusOK {
		t.Fatalf("gateway tenant %q not found after MCP create: status %d", tenantID, code)
	}

	// ---- tenant delete: admin.soft_delete_tenant routes to DELETE
	// /v1/admin/tenants/{id}, which initiates the §12.8 deletion lifecycle
	// (the tenant transitions active -> disabling). The destructive tool
	// reaching the gateway and landing the state transition proves the
	// gateway-owned delete dispatched to the gateway admin API. The gateway
	// DELETE handler carries no confirm precondition of its own (its input
	// schema exposes only id), so the §25.2 confirm gate is exercised above
	// on admin.set_pool_warm_count, whose gateway endpoint enforces it ----
	rpc = callManagementTool(t, ctx, ops.BaseURL(), "admin.soft_delete_tenant", map[string]any{
		"id": tenantID,
	})
	if rpc.Error != nil || rpc.Result.IsError {
		t.Fatalf("admin.soft_delete_tenant reported an error: %+v", rpc)
	}
	code, deleted := gatewayAdminGET(t, ctx, gw.BaseURL(), "/v1/admin/tenants/"+tenantID)
	if code != http.StatusOK {
		t.Fatalf("gateway tenant %q not readable after MCP delete: status %d", tenantID, code)
	}
	if state, _ := deleted["state"].(string); state != "disabling" {
		t.Errorf("tenant state after MCP delete = %q, want disabling (the deletion lifecycle did not land in the gateway store)", state)
	}

	// ---- ops-owned tool stays local: lenny_lock_acquire dispatches to the
	// lenny-ops handler, not the gateway (which has no such route) ----
	rpc = callManagementTool(t, ctx, ops.BaseURL(), "lenny_lock_acquire", map[string]any{
		"scope": "pool:mcp-created-pool",
	})
	if rpc.Error != nil || rpc.Result.IsError {
		t.Fatalf("ops-owned lenny_lock_acquire did not dispatch locally: %+v", rpc)
	}
}
