// SPDX-License-Identifier: MIT

package mcptoolgen

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestGenerateEmitsToolPerAdminEndpointNotOnlyOperability asserts the
// generator emits one tool per documented endpoint carrying a non-null
// x-lenny-mcp-tool -- every admin-API endpoint AND every operability
// endpoint, not the operability subset alone -- skipping only null-tool
// endpoints. This pins the §25.12 invariant "Every admin-API endpoint with
// documented RBAC is exposed as an MCP tool -- not only the operability
// endpoints": the tenant-management endpoint below MUST become a tool, and a
// generator that filters to x-lenny-category=="operability" (the pre-fix
// behavior) fails this test because it drops admin.create_tenant.
// spec: 25.12 (one MCP tool per admin-API endpoint, not only operability),
// 15.1 (every admin-API endpoint MUST be an MCP tool on /mcp/management).
func TestGenerateEmitsToolPerAdminEndpointNotOnlyOperability(t *testing.T) {
	doc := []byte(`{
      "paths": {
        "/v1/admin/runbooks": {"get": {"operationId":"getRunbooks","summary":"List runbooks","x-lenny-mcp-tool":"lenny_runbooks_list","x-lenny-scope":"tools:runbooks:read","x-lenny-required-role":"platform-admin","x-lenny-category":"operability"}},
        "/v1/admin/tenants": {"post": {"operationId":"postTenants","summary":"Create tenant","x-lenny-mcp-tool":"admin.create_tenant","x-lenny-scope":"tools:tenant:write","x-lenny-required-role":"platform-admin","x-lenny-category":"tenant-management"}},
        "/v1/admin/tenants/{id}": {"delete": {"operationId":"deleteTenant","summary":"Delete tenant","x-lenny-mcp-tool":"admin.soft_delete_tenant","x-lenny-scope":"tools:tenant:write","x-lenny-required-role":"platform-admin","x-lenny-category":"tenant-management"}},
        "/v1/admin/events/stream": {"get": {"operationId":"getStream","summary":"Event stream","x-lenny-mcp-tool":null,"x-lenny-scope":"tools:events:read","x-lenny-required-role":"platform-admin","x-lenny-category":"operability"}}
      }
    }`)
	tools, err := Generate(doc)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	byName := map[string]GeneratedTool{}
	for _, tl := range tools {
		byName[tl.Name] = tl
	}
	// The null-tool stream endpoint is skipped; the other three become tools.
	if len(tools) != 3 {
		t.Fatalf("emitted %d tools, want 3 (operability + admin-API, null skipped): %v", len(tools), byName)
	}
	// The admin-API tenant endpoints MUST be present: the pre-fix
	// operability-only filter dropped exactly these.
	if _, ok := byName["admin.create_tenant"]; !ok {
		t.Error("admin.create_tenant (tenant-management admin-API endpoint) was not emitted; §25.12 requires every admin-API endpoint to become a tool, not only operability")
	}
	// The operability endpoint is still an observation read-only tool.
	rb, ok := byName["lenny_runbooks_list"]
	if !ok {
		t.Fatal("lenny_runbooks_list operability tool was not emitted")
	}
	if rb.Category != categoryObservation || !rb.ReadOnly {
		t.Errorf("lenny_runbooks_list = %q readonly=%v, want observation read-only", rb.Category, rb.ReadOnly)
	}
	// A DELETE admin-API endpoint classifies destructive (fail-closed), so
	// the §25.12 nonDestructive capability filter excludes it.
	del, ok := byName["admin.soft_delete_tenant"]
	if !ok {
		t.Fatal("admin.soft_delete_tenant admin-API tool was not emitted")
	}
	if del.Category != categoryDestructive {
		t.Errorf("admin.soft_delete_tenant category = %q, want destructive (a tenant delete must be nonDestructive-filterable)", del.Category)
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
// admin-API tool cannot silently ship without a §25.12 taxonomy (fail-closed:
// a new destructive tool must be classified before it can reach the tool
// surface, rather than defaulting into a non-destructive category the
// nonDestructive capability filter would expose). spec: 25.12 (tool taxonomy
// classification).
func TestGenerateFailsOnUnclassifiedTool(t *testing.T) {
	doc := []byte(`{
      "paths": {
        "/v1/admin/frobnicate": {"post": {"operationId":"postFrob","summary":"Frobnicate","x-lenny-mcp-tool":"lenny_frobnicate","x-lenny-scope":"tools:frob:write","x-lenny-required-role":"platform-admin","x-lenny-category":"tenant-management"}}
      }
    }`)
	_, err := Generate(doc)
	if err == nil {
		t.Fatal("expected an error for an unclassified admin-API tool, got nil")
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
