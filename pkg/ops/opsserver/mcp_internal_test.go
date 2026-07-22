// SPDX-License-Identifier: MIT

package opsserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/observability/correlation"
	"github.com/lennylabs/lenny/pkg/ops/gateway"
	"github.com/lennylabs/lenny/pkg/ops/mcp"
)

// TestOpsOwnedToolNamesRoutingPredicate pins the §25.12 ops-vs-gateway
// routing predicate: the ops-owned set built from RouteSchemas() marks the
// lenny-ops-resident tools ops-owned and every other generated tool
// gateway-owned. Asserted as membership rather than cardinality, since the
// 60/162 split is already drift-guarded by TestRouteSchemasMatchLiveMux.
//
// spec: §25.12 (transparent dual routing; ops-vs-gateway routing predicate).
func TestOpsOwnedToolNamesRoutingPredicate_spec_25_12(t *testing.T) {
	owned := opsOwnedToolNames()
	for _, name := range []string{"admin.me", "admin.me_authorized_tools", "lenny_lock_acquire"} {
		if !owned[name] {
			t.Errorf("tool %q must be ops-owned (present in RouteSchemas()) so it routes to local replay", name)
		}
	}
	for _, name := range []string{"admin.create_pool", "admin.set_pool_warm_count", "admin.create_tenant"} {
		if owned[name] {
			t.Errorf("tool %q must be gateway-owned (absent from RouteSchemas()) so it proxies to the gateway", name)
		}
	}
}

// gatewayClientTo builds a gateway.Client whose transport points at srv.
// The client sends no service-account bearer, matching the §25.12
// identity-forwarding proxy posture where the caller identity rides on the
// forwarded headers.
func gatewayClientTo(t *testing.T, url string) *gateway.Client {
	t.Helper()
	c, err := gateway.NewClient(gateway.Config{BaseURL: url, PerRequestTimeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("build gateway client: %v", err)
	}
	return c
}

// TestOpsInvokerGatewayOwnedToolForwardsIdentityAndReemitsStatus confirms
// the §25.12 gateway-proxy dispatch: a gateway-owned tool routes through
// ProxyAdminCall with the caller identity and correlation headers
// forwarded, and the gateway's raw status and body re-emit verbatim into
// the tool result. This is the corrected outcome; before the routing
// branch existed the tool was replayed locally against the lenny-ops mux
// (a 404, never the gateway), so the caller identity never reached the
// gateway.
//
// spec: §25.12 (the authenticated identity from the MCP session maps to
// the Authorization header on gateway calls; the gateway response
// re-emits verbatim).
func TestOpsInvokerGatewayOwnedToolForwardsIdentity_spec_25_12(t *testing.T) {
	var gotAuth, gotCaller, gotOp, gotAgent, gotMethod, gotPath string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCaller = r.Header.Get("X-Lenny-Caller")
		gotOp = r.Header.Get(correlation.HeaderOperationID)
		gotAgent = r.Header.Get(correlation.HeaderAgentName)
		gotMethod, gotPath = r.Method, r.URL.Path
		gotBody, _ = readAll(r)
		// The gateway re-authorizes the forwarded identity and denies it.
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":"RBAC_DENIED"}}`))
	}))
	defer srv.Close()

	inv := &opsInvoker{opsOwned: opsOwnedToolNames(), gateway: gatewayClientTo(t, srv.URL)}
	ctx := withMCPCallerIdentity(context.Background(), http.Header{"Authorization": []string{"Bearer caller-token"}})
	ctx = correlation.With(ctx, correlation.Fields{OperationID: "op-42", AgentName: "remediator"})

	tool := mcp.Tool{Name: "admin.create_pool", Method: http.MethodPost, Path: "/v1/admin/pools"}
	res, err := inv.Invoke(ctx, tool, json.RawMessage(`{"name":"gvisor-a","runtime":"gvisor"}`))
	if err != nil {
		t.Fatalf("Invoke returned a transport error, want a verbatim gateway result: %v", err)
	}
	if res.Status != http.StatusForbidden {
		t.Errorf("ToolResult.Status = %d, want the gateway's verbatim 403", res.Status)
	}
	if string(res.Body) != `{"error":{"code":"RBAC_DENIED"}}` {
		t.Errorf("ToolResult.Body = %q, want the gateway body re-emitted verbatim", res.Body)
	}
	if res.Unavailable {
		t.Error("ToolResult.Unavailable = true on a completed gateway response; a 4xx must not map to ENDPOINT_UNAVAILABLE")
	}
	if gotAuth != "Bearer caller-token" {
		t.Errorf("gateway saw Authorization %q, want the forwarded caller bearer", gotAuth)
	}
	if gotCaller != "mcp-management" {
		t.Errorf("gateway saw X-Lenny-Caller %q, want mcp-management", gotCaller)
	}
	if gotOp != "op-42" || gotAgent != "remediator" {
		t.Errorf("gateway saw correlation (op=%q agent=%q), want the §25.12 correlation headers forwarded", gotOp, gotAgent)
	}
	if gotMethod != http.MethodPost || gotPath != "/v1/admin/pools" {
		t.Errorf("gateway saw %s %s, want POST /v1/admin/pools from buildToolRequest", gotMethod, gotPath)
	}
	if string(gotBody) != `{"name":"gvisor-a","runtime":"gvisor"}` {
		t.Errorf("gateway saw body %q, want the tool arguments as the request body", gotBody)
	}
}

// TestOpsInvokerGatewayTransportFailureMapsToUnavailable confirms a
// §25.12 transport failure on the gateway proxy path (the request never
// completes) maps to ToolResult.Unavailable so handleToolsCall returns
// -32000 ENDPOINT_UNAVAILABLE and the tool stays listed, rather than
// surfacing as a tool error.
//
// spec: §25.12 (a transport failure maps to ENDPOINT_UNAVAILABLE).
func TestOpsInvokerGatewayTransportFailureMapsToUnavailable_spec_25_12(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closedURL := srv.URL
	srv.Close() // the gateway is now unreachable; the request cannot complete.

	inv := &opsInvoker{opsOwned: opsOwnedToolNames(), gateway: gatewayClientTo(t, closedURL)}
	tool := mcp.Tool{Name: "admin.create_pool", Method: http.MethodPost, Path: "/v1/admin/pools"}
	res, err := inv.Invoke(context.Background(), tool, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Invoke = %v, want a nil error with Unavailable set", err)
	}
	if !res.Unavailable {
		t.Error("ToolResult.Unavailable = false on a gateway transport failure, want true so the tool maps to -32000 and stays listed")
	}
}

// TestOpsInvokerNilGatewayReportsUnavailable confirms the local-only dev
// posture: with no gateway client wired, a gateway-owned tool reports
// ENDPOINT_UNAVAILABLE rather than dispatching. This is the corrected
// outcome; before the routing branch the tool was replayed locally and
// 404'd against the lenny-ops mux.
//
// spec: §25.12 (a nil gateway makes every gateway-owned tool report
// ENDPOINT_UNAVAILABLE).
func TestOpsInvokerNilGatewayReportsUnavailable_spec_25_12(t *testing.T) {
	inv := &opsInvoker{opsOwned: opsOwnedToolNames(), gateway: nil}
	tool := mcp.Tool{Name: "admin.create_pool", Method: http.MethodPost, Path: "/v1/admin/pools"}
	res, err := inv.Invoke(context.Background(), tool, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Invoke = %v, want a nil error with Unavailable set", err)
	}
	if !res.Unavailable {
		t.Error("ToolResult.Unavailable = false for a gateway-owned tool with a nil gateway, want true (ENDPOINT_UNAVAILABLE)")
	}
	if res.Status != 0 {
		t.Errorf("ToolResult.Status = %d, want 0; a nil-gateway dev posture never reaches the gateway", res.Status)
	}
}

// TestOpsInvokerGatewayDryRunPreviewSurfaced confirms the §25.2 dry-run
// probe is applied on the gateway-proxied path as on the local replay: a
// 200 carrying dryRun:true lifts the flag and preview onto the tool
// result.
//
// spec: §25.2 (dry-run preview), §25.12 (dry-run mapping on the gateway
// path).
func TestOpsInvokerGatewayDryRunPreviewSurfaced_spec_25_12(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"dryRun":true,"preview":{"deleted":3}}`))
	}))
	defer srv.Close()

	inv := &opsInvoker{opsOwned: opsOwnedToolNames(), gateway: gatewayClientTo(t, srv.URL)}
	tool := mcp.Tool{Name: "admin.soft_delete_tenant", Method: http.MethodPost, Path: "/v1/admin/tenants/acme/soft-delete"}
	res, err := inv.Invoke(context.Background(), tool, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if !res.DryRun {
		t.Error("ToolResult.DryRun = false on a gateway dryRun:true response, want the §25.2 preview surfaced")
	}
	if string(res.Preview) != `{"deleted":3}` {
		t.Errorf("ToolResult.Preview = %q, want the gateway preview object", res.Preview)
	}
}

// TestOpsInvokerGatewayGetToolAppendsQueryString confirms the §25.12
// gateway proxy carries a GET tool's non-path arguments as the request
// query string: buildToolRequest encodes the unconsumed arguments and
// invokeGateway appends "?"+query to the proxied path, so the gateway
// admin API sees the filter the caller supplied. A GET gateway-owned tool
// whose query was dropped would reach the gateway unfiltered and return a
// different result set than the caller asked for.
//
// spec: §25.12 (a gateway-owned tool proxies to the gateway admin API via
// GatewayClient; a GET tool's arguments map to the request query string).
func TestOpsInvokerGatewayGetToolAppendsQueryString_spec_25_12(t *testing.T) {
	var gotMethod, gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"events":[]}`))
	}))
	defer srv.Close()

	inv := &opsInvoker{opsOwned: opsOwnedToolNames(), gateway: gatewayClientTo(t, srv.URL)}
	tool := mcp.Tool{Name: "admin.list_audit_events", Method: http.MethodGet, Path: "/v1/admin/audit"}
	res, err := inv.invokeGateway(context.Background(), tool, json.RawMessage(`{"actor":"alice"}`))
	if err != nil {
		t.Fatalf("invokeGateway returned an error for a GET tool with a query argument: %v", err)
	}
	if res.Status != http.StatusOK {
		t.Errorf("ToolResult.Status = %d, want 200", res.Status)
	}
	if gotMethod != http.MethodGet || gotPath != "/v1/admin/audit" {
		t.Errorf("gateway saw %s %s, want GET /v1/admin/audit", gotMethod, gotPath)
	}
	if gotQuery != "actor=alice" {
		t.Errorf("gateway saw query %q, want actor=alice appended from the GET arguments", gotQuery)
	}
}

// TestOpsInvokerGatewayMalformedArgsReturnsError confirms invokeGateway
// surfaces a buildToolRequest failure: arguments that are not valid JSON
// cannot be translated into a request, so the proxy returns the error
// rather than dispatching a malformed call to the gateway. The gateway
// must never be reached with a half-built request.
//
// spec: §25.12 (a gateway-owned tool proxies its mapped admin-API call; an
// untranslatable request is an error rather than a silent dispatch).
func TestOpsInvokerGatewayMalformedArgsReturnsError_spec_25_12(t *testing.T) {
	reached := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		reached = true
	}))
	defer srv.Close()

	inv := &opsInvoker{opsOwned: opsOwnedToolNames(), gateway: gatewayClientTo(t, srv.URL)}
	tool := mcp.Tool{Name: "admin.create_pool", Method: http.MethodPost, Path: "/v1/admin/pools"}
	_, err := inv.invokeGateway(context.Background(), tool, json.RawMessage(`{not valid json`))
	if err == nil {
		t.Fatal("invokeGateway returned a nil error for unparseable arguments, want the buildToolRequest error surfaced")
	}
	if reached {
		t.Error("the gateway was dispatched a request built from unparseable arguments; the error must short-circuit before ProxyAdminCall")
	}
}

// readAll drains an http.Request body for assertion.
func readAll(r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	return io.ReadAll(r.Body)
}
