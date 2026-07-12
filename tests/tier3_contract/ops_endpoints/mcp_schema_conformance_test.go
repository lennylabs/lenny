// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract tests that pin the §25.12 MCP management server's
// wire envelopes to JSON Schemas and golden files. The other MCP
// contract tests in this package check individual fields; these
// validate the whole JSON-RPC 2.0 response envelope, the tools/list and
// tools/call result objects, and the canonical dry-run and
// SCOPE_FORBIDDEN payloads against schema and golden fixtures so a wire
// drift (a missing jsonrpc:2.0, a broken id echo, a tool descriptor
// missing its inputSchema, a changed error data object) fails a test.
package ops_endpoints_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/mcp"
	"github.com/lennylabs/lenny/tests/testinfra/golden"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// schemaRel is a path, relative to the repository root, to a schema
// fixture this suite validates the MCP envelopes against.
const (
	rpcEnvelopeSchema     = "tests/tier3_contract/ops_endpoints/testdata/schema/jsonrpc_response.json"
	toolsListResultSchema = "tests/tier3_contract/ops_endpoints/testdata/schema/tools_list_result.json"
	toolsCallResultSchema = "tests/tier3_contract/ops_endpoints/testdata/schema/tools_call_result.json"
)

// validateEnvelope validates a decoded MCP response against the
// JSON-RPC 2.0 envelope schema and confirms the response echoes the
// request id, the two properties §25.12's JSON-RPC transport requires
// that field-level assertions do not currently pin.
func validateEnvelope(t *testing.T, body map[string]any, wantID float64) {
	t.Helper()
	schema := schematest.Compile(t, rpcEnvelopeSchema)
	if err := schema.Validate(body); err != nil {
		t.Fatalf("response is not a valid JSON-RPC 2.0 envelope: %v\nbody=%v", err, body)
	}
	if body["jsonrpc"] != "2.0" {
		t.Errorf("jsonrpc = %v, want \"2.0\"", body["jsonrpc"])
	}
	if id, _ := body["id"].(float64); id != wantID {
		t.Errorf("response id = %v, want the request id %v (id echo)", body["id"], wantID)
	}
}

// TestMCPInitializeEnvelopeConformance pins the §25.12 initialize
// response to the JSON-RPC 2.0 envelope schema: it declares jsonrpc:2.0,
// echoes the request id, and carries a result object.
//
// spec: 25.12 (MCP management server — initialize handshake; the
// JSON-RPC transport at /mcp/management)
// diagnosis: The initialize response drifted from the JSON-RPC 2.0
// envelope — a missing jsonrpc:2.0, a dropped or non-echoed id, or both
// result and error present. An MCP client rejects a malformed handshake
// envelope before it can negotiate a session.
func TestMCPInitializeEnvelopeConformance(t *testing.T) {
	srv := opsServer(t)
	_, body := request(t, srv, http.MethodPost, "/mcp/management", nil, map[string]any{
		"jsonrpc": "2.0", "id": 7, "method": "initialize",
	})
	validateEnvelope(t, body, 7)
	if _, ok := body["result"].(map[string]any); !ok {
		t.Fatalf("initialize response has no result object: %v", body)
	}
}

// TestMCPToolsListResultConformance validates every §25.12 tools/list
// tool descriptor against the tools/list result schema: each carries a
// name, a description, a JSON Schema inputSchema, and the x-lenny-*
// extension metadata.
//
// spec: 25.12 (MCP tool inventory — tools/list; "Each tool is defined
// with a JSON Schema inputSchema"; the x-lenny-* extension metadata)
// diagnosis: A tools/list descriptor is missing its inputSchema or an
// x-lenny-* extension, or the result is not the {tools:[...]} object.
// Field-level checks in mcp_test.go probe only the first descriptor;
// this validates the whole inventory so one malformed tool fails.
func TestMCPToolsListResultConformance(t *testing.T) {
	srv := opsServer(t)
	_, body := request(t, srv, http.MethodPost, "/mcp/management", nil, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/list",
	})
	validateEnvelope(t, body, 3)
	result, ok := body["result"].(map[string]any)
	if !ok {
		t.Fatalf("tools/list response has no result object: %v", body)
	}
	schema := schematest.Compile(t, toolsListResultSchema)
	if err := schema.Validate(result); err != nil {
		t.Fatalf("tools/list result violates the tool-descriptor schema: %v", err)
	}
}

// TestMCPToolsCallSuccessResultConformance validates a §25.12
// tools/call success against the JSON-RPC envelope schema and the MCP
// content-envelope schema: isError:false with a text content array.
//
// spec: 25.12 (MCP tools/call — routing to the underlying endpoint; the
// MCP content envelope)
// diagnosis: A successful tools/call drifted from the MCP content
// envelope — a missing content array, a content item without type:text,
// or isError of the wrong type. An MCP client cannot render a tool
// result that does not match the content envelope.
func TestMCPToolsCallSuccessResultConformance(t *testing.T) {
	srv := opsServer(t)
	_, body := request(t, srv, http.MethodPost, "/mcp/management", nil, map[string]any{
		"jsonrpc": "2.0", "id": 4, "method": "tools/call",
		"params": map[string]any{
			"name":      "lenny_diagnostics_pool",
			"arguments": map[string]any{"name": "default-gvisor"},
		},
	})
	validateEnvelope(t, body, 4)
	result, ok := body["result"].(map[string]any)
	if !ok {
		t.Fatalf("tools/call response has no result object: %v", body)
	}
	schema := schematest.Compile(t, toolsCallResultSchema)
	if err := schema.Validate(result); err != nil {
		t.Fatalf("tools/call result violates the content-envelope schema: %v", err)
	}
	if result["isError"] != false {
		t.Errorf("isError = %v, want false on a successful pool diagnosis", result["isError"])
	}
}

// TestMCPToolsCallDryRunEnvelopeConformance pins the §25.12 dry-run
// result mapping: a preview is isError:false, validates against the
// content-envelope schema, and carries the canonical _meta.lenny.dryRun
// signal. The _meta payload is pinned to a golden file so a drift in the
// canonical dry-run signal an MCP client checks fails the test.
//
// spec: 25.12 (Dry-Run Result Mapping — "The structured
// _meta.lenny.dryRun: true field is the canonical signal"; isError:false)
// diagnosis: A dry-run tools/call dropped the _meta.lenny.dryRun signal,
// reported isError:true, or changed the _meta payload MCP clients read
// programmatically to distinguish a preview from an applied mutation.
func TestMCPToolsCallDryRunEnvelopeConformance(t *testing.T) {
	srv := opsServer(t)
	_, body := request(t, srv, http.MethodPost, "/mcp/management", nil, map[string]any{
		"jsonrpc": "2.0", "id": 5, "method": "tools/call",
		"params": map[string]any{
			"name":      "lenny_drift_snapshot_refresh",
			"arguments": map[string]any{"desired": map[string]any{"pools": map[string]any{}}},
		},
	})
	validateEnvelope(t, body, 5)
	result, ok := body["result"].(map[string]any)
	if !ok {
		t.Fatalf("tools/call response has no result object: %v", body)
	}
	schema := schematest.Compile(t, toolsCallResultSchema)
	if err := schema.Validate(result); err != nil {
		t.Fatalf("dry-run tools/call result violates the content-envelope schema: %v", err)
	}
	if result["isError"] != false {
		t.Errorf("isError = %v, want false for a dry-run preview", result["isError"])
	}
	meta, ok := result["_meta"].(map[string]any)
	if !ok || meta["lenny.dryRun"] != true {
		t.Fatalf("_meta = %v, want the canonical lenny.dryRun:true signal", result["_meta"])
	}
	golden.AssertJSON(t, "mcp_dry_run_meta.json", mustJSON(t, meta))
}

// TestMCPScopeForbiddenDataObjectConformance pins the §25.12
// SCOPE_FORBIDDEN error to the JSON-RPC envelope schema and its data
// object to a golden file. §25.12 specifies the exact data fields a
// caller inspects to understand a scope restriction.
//
// spec: 25.12 (Security Model — Scope-forbidden behavior: "-32001 with
// data.code: SCOPE_FORBIDDEN, data.retryable: false, data.requiredScope,
// and data.activeScope")
// diagnosis: The SCOPE_FORBIDDEN error drifted from the JSON-RPC error
// envelope or its data object changed. A caller that inspects
// requiredScope/activeScope to recover from a forbidden call breaks when
// these fields move.
func TestMCPScopeForbiddenDataObjectConformance(t *testing.T) {
	srv := opsServer(t)
	_, body := request(t, srv, http.MethodPost, "/mcp/management",
		map[string]string{"X-Lenny-Scope": "tools:health:read"},
		map[string]any{
			"jsonrpc": "2.0", "id": 6, "method": "tools/call",
			"params": map[string]any{
				"name":      "lenny_lock_acquire",
				"arguments": map[string]any{"scope": "pool:p"},
			},
		})
	validateEnvelope(t, body, 6)
	rpcErr, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected a JSON-RPC error, got: %v", body)
	}
	if code, _ := rpcErr["code"].(float64); code != -32001 {
		t.Errorf("error code = %v, want -32001 SCOPE_FORBIDDEN", rpcErr["code"])
	}
	data, ok := rpcErr["data"].(map[string]any)
	if !ok {
		t.Fatalf("SCOPE_FORBIDDEN error has no data object: %v", rpcErr)
	}
	golden.AssertJSON(t, "mcp_scope_forbidden_data.json", mustJSON(t, data))
}

// TestMCPEndpointUnavailableEnvelopeConformance pins the §25.12
// ENDPOINT_UNAVAILABLE error to the JSON-RPC envelope schema and the
// documented data object. A management server with no reachable backing
// endpoint (a nil invoker) maps every tools/call to -32000 with
// data.code ENDPOINT_UNAVAILABLE and retryable:true.
//
// spec: 25.12 (Security Model — Unhealthy-endpoint behavior: "-32000
// (generic server error) with data.code: ENDPOINT_UNAVAILABLE and the
// standard retryable: true")
// diagnosis: An unreachable-endpoint tools/call drifted from the -32000
// ENDPOINT_UNAVAILABLE envelope or dropped retryable:true. A client that
// keys retry behavior on the retryable flag mishandles the outage.
func TestMCPEndpointUnavailableEnvelopeConformance(t *testing.T) {
	// A nil invoker reports every tool as unavailable, the §25.12
	// unhealthy-endpoint condition, without needing an actual outage.
	srv := mcp.NewServer(nil)
	body := rpcRoundTrip(t, srv, map[string]any{
		"jsonrpc": "2.0", "id": 8, "method": "tools/call",
		"params": map[string]any{
			"name":      "lenny_lock_acquire",
			"arguments": map[string]any{"scope": "pool:p"},
		},
	})
	validateEnvelope(t, body, 8)
	rpcErr, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected a JSON-RPC error, got: %v", body)
	}
	if code, _ := rpcErr["code"].(float64); code != -32000 {
		t.Errorf("error code = %v, want -32000 ENDPOINT_UNAVAILABLE", rpcErr["code"])
	}
	data, _ := rpcErr["data"].(map[string]any)
	if data["code"] != "ENDPOINT_UNAVAILABLE" {
		t.Errorf("data.code = %v, want ENDPOINT_UNAVAILABLE", data["code"])
	}
	if data["retryable"] != true {
		t.Errorf("data.retryable = %v, want true", data["retryable"])
	}
}

// rpcRoundTrip serves one JSON-RPC request against a bare §25.12 MCP
// server and decodes the response body, for cases the opsServer helper's
// full wiring does not exercise (a nil invoker).
func rpcRoundTrip(t *testing.T, srv *mcp.Server, payload map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/mcp/management", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode response: %v", err)
		}
	}
	return out
}

// mustJSON marshals v with sorted, indented keys for a golden
// comparison, failing the test on a marshal error.
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal golden payload: %v", err)
	}
	return raw
}
