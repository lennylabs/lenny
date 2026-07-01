// SPDX-License-Identifier: MIT

package mcptoolgen

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestGenerateEmitsOneToolPerOperabilityEndpoint asserts the generator emits
// exactly one tool per documented operability endpoint that carries a
// non-null x-lenny-mcp-tool, skipping non-operability endpoints and null-tool
// endpoints. spec: 25.12 (one MCP tool per operability endpoint).
func TestGenerateEmitsOneToolPerOperabilityEndpoint(t *testing.T) {
	doc := []byte(`{
      "paths": {
        "/v1/admin/runbooks": {"get": {"operationId":"getRunbooks","summary":"List runbooks","x-lenny-mcp-tool":"lenny_runbooks_list","x-lenny-scope":"tools:runbooks:read","x-lenny-required-role":"platform-admin","x-lenny-category":"operability"}},
        "/v1/admin/tenants": {"post": {"operationId":"postTenants","summary":"Create tenant","x-lenny-mcp-tool":"admin.create_tenant","x-lenny-scope":"tools:tenant:write","x-lenny-required-role":"platform-admin","x-lenny-category":"tenant-management"}},
        "/v1/admin/events/stream": {"get": {"operationId":"getStream","summary":"Event stream","x-lenny-mcp-tool":null,"x-lenny-scope":"tools:events:read","x-lenny-required-role":"platform-admin","x-lenny-category":"operability"}}
      }
    }`)
	tools, err := Generate(doc)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("emitted %d tools, want 1 (operability + non-null only)", len(tools))
	}
	if tools[0].Name != "lenny_runbooks_list" {
		t.Errorf("tool name = %q, want lenny_runbooks_list", tools[0].Name)
	}
	if tools[0].Category != categoryObservation || !tools[0].ReadOnly {
		t.Errorf("classification = %q readonly=%v, want observation read-only", tools[0].Category, tools[0].ReadOnly)
	}
}

// TestGenerateAddsPathParametersAsRequired asserts a route with a {param}
// template gains that parameter as a required input property, since the
// merged operability routes carry path parameters in the template rather than
// an explicit document parameters array. spec: 25.12 (tool input schema).
func TestGenerateAddsPathParametersAsRequired(t *testing.T) {
	doc := []byte(`{
      "paths": {
        "/v1/admin/runbooks/{name}": {"get": {"operationId":"getRunbook","summary":"Get runbook","x-lenny-mcp-tool":"lenny_runbooks_get","x-lenny-scope":"tools:runbooks:read","x-lenny-required-role":"platform-admin","x-lenny-category":"operability"}}
      }
    }`)
	tools, err := Generate(doc)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("emitted %d tools, want 1", len(tools))
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(tools[0].InputSchema, &schema); err != nil {
		t.Fatalf("decode input schema: %v", err)
	}
	if _, ok := schema.Properties["name"]; !ok {
		t.Error("input schema is missing the {name} path parameter property")
	}
	if _, ok := schema.Properties["operationId"]; !ok {
		t.Error("input schema is missing the optional operationId property")
	}
	found := false
	for _, r := range schema.Required {
		if r == "name" {
			found = true
		}
		if r == "operationId" {
			t.Error("operationId must be optional, not required")
		}
	}
	if !found {
		t.Error("path parameter {name} must be required")
	}
}

// TestGenerateFailsOnUnclassifiedTool asserts the generator refuses to emit a
// tool the classification table does not cover, so a newly documented
// operability tool cannot silently ship without a §25.12 taxonomy. spec:
// 25.12 (tool taxonomy classification).
func TestGenerateFailsOnUnclassifiedTool(t *testing.T) {
	doc := []byte(`{
      "paths": {
        "/v1/admin/frobnicate": {"post": {"operationId":"postFrob","summary":"Frobnicate","x-lenny-mcp-tool":"lenny_frobnicate","x-lenny-scope":"tools:frob:write","x-lenny-required-role":"platform-admin","x-lenny-category":"operability"}}
      }
    }`)
	_, err := Generate(doc)
	if err == nil {
		t.Fatal("expected an error for an unclassified operability tool, got nil")
	}
	if !strings.Contains(err.Error(), "classification") {
		t.Errorf("error = %v, want a classification-missing error", err)
	}
}

// TestRenderSourceIsDeterministic asserts two renders of the same inventory
// produce byte-identical source, which the drift guard depends on. spec:
// 25.12 (build-time generation, byte-equal drift guard).
func TestRenderSourceIsDeterministic(t *testing.T) {
	tools := []GeneratedTool{
		{
			Name: "lenny_x", Description: "X", InputSchema: json.RawMessage(`{"type":"object"}`),
			Method: "GET", Path: "/v1/admin/x", Category: categoryObservation,
			RequiredRole: "platform-admin", Scope: "tools:x:read", DryRunSupport: dryRunNone, ReadOnly: true,
		},
	}
	a, err := RenderSource(tools)
	if err != nil {
		t.Fatalf("RenderSource: %v", err)
	}
	b, err := RenderSource(tools)
	if err != nil {
		t.Fatalf("RenderSource: %v", err)
	}
	if string(a) != string(b) {
		t.Error("RenderSource is not deterministic")
	}
	if !strings.Contains(string(a), "Code generated by openapi-to-mcp; DO NOT EDIT.") {
		t.Error("rendered source is missing the generated-code header")
	}
}
