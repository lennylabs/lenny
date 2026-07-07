// SPDX-License-Identifier: MIT

//go:build contract

// Pins the gateway-edge `/mcp` `initialize` result and `tools/list`
// catalog against the published MCP protocol schema for the
// negotiated revision, vendored at
// tests/testdata/mcp/schema/{2025-03-26,2024-11-05}.schema.json (see
// the README next to it for provenance). pkg/gateway/mcpfabric/mcp's
// own unit tests check field presence against Lenny's ad hoc
// map[string]any construction; neither those nor the scaffold suite
// in this package catch a response that satisfies Lenny's own
// expectations but violates the real MCP `InitializeResult` / `Tool`
// contract (a wrong key casing, a missing required field, an
// `inputSchema` whose `type` is not the literal `"object"` the MCP
// schema requires). This file closes that gap.
package rest_mcp_consistency_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// mcpSchemaFile maps a negotiated MCP protocol version to its vendored
// published schema file, per the §15.2 "Version negotiation" supported
// set (current 2025-03-26, previous 2024-11-05).
func mcpSchemaFile(version string) string {
	return "tests/testdata/mcp/schema/" + version + ".schema.json"
}

// rpcResult posts a JSON-RPC 2.0 request to the MCP endpoint and
// returns the raw `result` field, still-encoded, so it can be
// unmarshalled into `any` for schema validation without going through
// the tools/call content-block envelope mcpToolPayload expects.
func rpcResult(t *testing.T, url, tenant, method string, params map[string]any) json.RawMessage {
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
	resp, raw := postJSON(t, url, tenant, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("MCP %s status: %d, body=%s", method, resp.StatusCode, raw)
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

// validateAgainst compiles the named definition out of the vendored
// schema file and validates raw against it.
func validateAgainst(t *testing.T, schemaFile, definition string, raw json.RawMessage) {
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

// spec: §15.2 ("Version negotiation": "Once negotiated, the connection
// is pinned to that version for its lifetime. The MCPAdapter dispatches
// to version-specific serialization logic internally — tool schemas,
// error formats, and streaming behavior conform to the negotiated
// version."; spec/15_external-api-surface.md:1315)
// diagnosis: a failure here means the gateway-edge `/mcp` `initialize`
// result no longer satisfies the MCP specification's own
// `InitializeResult` schema for the negotiated protocol version — a
// conforming MCP client (which validates the handshake against the
// published schema before proceeding) would reject the connection
// even though Lenny's own map[string]any construction round-trips
// through its unit tests.
func TestMCPInitializeMatchesPublishedSchema(t *testing.T) {
	for _, version := range []string{"2025-03-26", "2024-11-05"} {
		t.Run(version, func(t *testing.T) {
			tsREST, tsMCP, _ := newConsistencyServers(t, "acme")
			_ = tsREST

			raw := rpcResult(t, tsMCP.URL+"/mcp", "acme", "initialize", map[string]any{
				"protocolVersion": version,
				"capabilities":    map[string]any{},
				"clientInfo":      map[string]any{"name": "contract-test", "version": "0.0.0"},
			})

			var negotiated struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			if err := json.Unmarshal(raw, &negotiated); err != nil {
				t.Fatalf("decode negotiated protocolVersion: %v", err)
			}
			if negotiated.ProtocolVersion != version {
				t.Fatalf("requested version %s, gateway negotiated %s", version, negotiated.ProtocolVersion)
			}

			validateAgainst(t, mcpSchemaFile(version), "InitializeResult", raw)
		})
	}
}

// spec: §15.2 ("Version negotiation" line 1315, quoted above) — the
// negotiated version governs "tool schemas" as well as the
// `initialize` handshake, so the `tools/list` catalog is checked
// against the same per-revision published schema.
// diagnosis: a failure here means the `tools/list` catalog (the
// §15.2 MCP tools table) no longer satisfies the MCP specification's
// own `Tool` / `ListToolsResult` schema — most commonly an
// `inputSchema` missing the literal `"type": "object"` the MCP schema
// requires, or a tool missing `name` or `inputSchema`. Lenny's own
// tools/list tests (pkg/gateway/mcpfabric/mcptools) only check that
// specific tool names are present; they do not validate every catalog
// entry against the external MCP `Tool` contract.
func TestMCPToolsListMatchesPublishedSchema(t *testing.T) {
	_, tsMCP, _ := newConsistencyServers(t, "acme")

	raw := rpcResult(t, tsMCP.URL+"/mcp", "acme", "tools/list", nil)

	var listing struct {
		Tools []json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(raw, &listing); err != nil {
		t.Fatalf("decode tools/list result: %v", err)
	}
	if len(listing.Tools) == 0 {
		t.Fatal("tools/list returned no tools; nothing to validate")
	}

	// Every tool the gateway advertises must satisfy the negotiated
	// version's Tool schema; the negotiated version at connection time
	// here is the default (current, 2025-03-26).
	validateAgainst(t, mcpSchemaFile("2025-03-26"), "ListToolsResult", raw)
	for _, tool := range listing.Tools {
		validateAgainst(t, mcpSchemaFile("2025-03-26"), "Tool", tool)
	}
}
