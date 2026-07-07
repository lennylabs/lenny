// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind conformance test for the built-in MCPAdapter (§15
// "Built-in adapter inventory": MCPAdapter at /mcp, protocol "MCP
// Streamable HTTP", status V1, "Always available"). The tier-3 contract
// suite (tests/tier3_contract/rest_mcp_consistency) already validates
// the `initialize` result and the `tools/list` catalog against the
// published MCP protocol schema, but only against an in-process
// httptest server wired directly to a memstore and an EchoExecutor — it
// never drives a deployed gateway binary through its real HTTP
// listener, its auth middleware, or its adapter-registry mounting. This
// file closes that gap by driving the MCP handshake and tool catalog
// through the port-forwarded lenny-gateway Service on the e2e Kind
// cluster and validating each response against the same vendored MCP
// schema the tier-3 suite uses.

package tier5_e2e_kind_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
	"github.com/lennylabs/lenny/tests/testinfra/sessiondriver"
)

// mcpConformanceTenant is the synthetic tenant the MCP conformance test
// bootstraps. sessiondriver.Close removes it in best-effort cleanup.
const mcpConformanceTenant = "scaffold-mcp-conformance-tenant"

// t5MCPSchemaFile maps a negotiated MCP protocol version to its
// vendored published schema file, mirroring
// tests/tier3_contract/rest_mcp_consistency's mcpSchemaFile helper.
func t5MCPSchemaFile(version string) string {
	return "tests/testdata/mcp/schema/" + version + ".schema.json"
}

// t5MCPCall posts a JSON-RPC 2.0 request to the deployed gateway's /mcp
// endpoint with the dev-mode identity headers and returns the raw,
// still-encoded `result` field.
func t5MCPCall(t *testing.T, baseURL, tenant, method string, params map[string]any) json.RawMessage {
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
	status, raw := t5PostJSON(t, baseURL, "/mcp", tenant, body)
	if status != http.StatusOK {
		t.Fatalf("MCP %s on the deployed gateway: status %d, body=%s", method, status, raw)
	}
	var rpc struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &rpc); err != nil {
		t.Fatalf("decode %s response: %v; body=%s", method, err, raw)
	}
	if rpc.Error != nil {
		t.Fatalf("MCP %s returned a JSON-RPC error: %d %s", method, rpc.Error.Code, rpc.Error.Message)
	}
	if rpc.Result == nil {
		t.Fatalf("MCP %s returned no result; body=%s", method, raw)
	}
	return rpc.Result
}

// t5MCPValidateAgainst compiles the named definition out of the
// vendored schema file and validates raw against it.
func t5MCPValidateAgainst(t *testing.T, schemaFile, definition string, raw json.RawMessage) {
	t.Helper()
	sch := schematest.Compile(t, schemaFile+"#/definitions/"+definition)
	var instance any
	if err := json.Unmarshal(raw, &instance); err != nil {
		t.Fatalf("decode instance for %s: %v", definition, err)
	}
	if err := sch.Validate(instance); err != nil {
		t.Errorf("does not validate against the published MCP %s schema (%s): %v", definition, schemaFile, err)
	}
}

// spec: 15 ("Built-in adapter inventory": MCPAdapter at `/mcp`,
// protocol "MCP Streamable HTTP", status V1; "Built-in (compiled in):
// MCP, OpenAI Completions, Open Responses. Always available,
// configurable via admin API."); 15.2 ("Version negotiation": Lenny
// concurrently serves the current (2025-03-26) and previous (2024-11-05)
// MCP spec versions per the vendored schema README next to
// tests/testdata/mcp).
// diagnosis: a failure here means the deployed gateway's /mcp adapter
// is unreachable through its real HTTP listener and auth middleware, it
// fails to negotiate the requested protocol version, or its
// `initialize` result no longer validates against the MCP
// specification's own InitializeResult schema for that version. The
// tier-3 contract suite (tests/tier3_contract/rest_mcp_consistency)
// pins the same schema but only against an in-process httptest server;
// this test is the only one that exercises the handshake as mounted on
// the real gateway binary, reached by a live client over the
// port-forwarded Service.
func TestMCPInitializeConformsToPublishedSchemaOnDeployedGateway(t *testing.T) {
	d := sessiondriver.New(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := d.BootstrapTenant(ctx, mcpConformanceTenant); err != nil {
		t.Fatalf("bootstrap tenant: %v", err)
	}

	for _, version := range []string{"2025-03-26", "2024-11-05"} {
		t.Run(version, func(t *testing.T) {
			raw := t5MCPCall(t, d.BaseURL(), mcpConformanceTenant, "initialize", map[string]any{
				"protocolVersion": version,
				"capabilities":    map[string]any{},
				"clientInfo":      map[string]any{"name": "tier5-conformance-test", "version": "0.0.0"},
			})

			var negotiated struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			if err := json.Unmarshal(raw, &negotiated); err != nil {
				t.Fatalf("decode negotiated protocolVersion: %v", err)
			}
			if negotiated.ProtocolVersion != version {
				t.Fatalf("requested version %s, deployed gateway negotiated %s", version, negotiated.ProtocolVersion)
			}

			t5MCPValidateAgainst(t, t5MCPSchemaFile(version), "InitializeResult", raw)
		})
	}
}

// spec: 15.2 ("Version negotiation" — "The MCPAdapter dispatches to
// version-specific serialization logic internally — tool schemas,
// error formats, and streaming behavior conform to the negotiated
// version.")
// diagnosis: a failure here means the deployed gateway's `tools/list`
// catalog no longer satisfies the MCP specification's own Tool /
// ListToolsResult schema for the current negotiated version — most
// commonly an inputSchema missing the literal "type": "object" the MCP
// schema requires, or a tool missing name or inputSchema. Lenny's own
// tools/list tests (pkg/gateway/mcpfabric/mcptools) and the tier-3
// contract suite only exercise the in-process construction; this test
// is the only one that validates the catalog a live client receives
// from the deployed gateway.
func TestMCPToolsListConformsToPublishedSchemaOnDeployedGateway(t *testing.T) {
	d := sessiondriver.New(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := d.BootstrapTenant(ctx, mcpConformanceTenant); err != nil {
		t.Fatalf("bootstrap tenant: %v", err)
	}

	raw := t5MCPCall(t, d.BaseURL(), mcpConformanceTenant, "tools/list", nil)

	var listing struct {
		Tools []json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(raw, &listing); err != nil {
		t.Fatalf("decode tools/list result: %v", err)
	}
	if len(listing.Tools) == 0 {
		t.Fatal("tools/list returned no tools; nothing to validate")
	}

	t5MCPValidateAgainst(t, t5MCPSchemaFile("2025-03-26"), "ListToolsResult", raw)
	for _, tool := range listing.Tools {
		t5MCPValidateAgainst(t, t5MCPSchemaFile("2025-03-26"), "Tool", tool)
	}
}
