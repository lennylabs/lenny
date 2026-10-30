// SPDX-License-Identifier: MIT

package mcp_test

import (
	"encoding/json"
	"go/format"
	"os"
	"testing"

	"github.com/lennylabs/lenny/pkg/common/scopes"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/openapi"
	"github.com/lennylabs/lenny/pkg/ops/mcp"
	"github.com/lennylabs/lenny/pkg/ops/mcp/mcptoolgen"
)

// TestGeneratedToolsMatchOpenAPI is the §25.12 build-time drift guard: it
// re-runs the openapi-to-mcp generation from the live OpenAPI document and
// asserts the committed generated_tools.go is byte-identical to a fresh
// generation. A diff means openapi.json changed (or the tool classification
// table changed) without re-running the generator; regenerate with
// `go run ./cmd/openapi-to-mcp` (or `make generate`) to fix. This is the
// §25.12 "auto-generated from the canonical OpenAPI spec at build time"
// guarantee: the management tool inventory cannot silently drift from the
// REST surface it mirrors.
//
// spec: 25.12 (build-time openapi-to-mcp generation), 15.1 (OpenAPI x-lenny-*
// contract).
// diagnosis: The committed pkg/ops/mcp/generated_tools.go diverges from a
// fresh generation over the served openapi.json. An OpenAPI edit or a tool
// classification change landed without regenerating the inventory. Run
// `go run ./cmd/openapi-to-mcp` and commit the result.
func TestGeneratedToolsMatchOpenAPI_spec_25_12(t *testing.T) {
	tools, err := mcptoolgen.Generate(openapi.Document())
	if err != nil {
		t.Fatalf("generate tool inventory: %v", err)
	}
	src, err := mcptoolgen.RenderSource(tools)
	if err != nil {
		t.Fatalf("render generated source: %v", err)
	}
	fresh, err := format.Source(src)
	if err != nil {
		t.Fatalf("gofmt fresh source: %v", err)
	}
	committed, err := os.ReadFile("generated_tools.go")
	if err != nil {
		t.Fatalf("read committed generated_tools.go: %v", err)
	}
	if string(fresh) != string(committed) {
		t.Errorf("committed generated_tools.go drifted from a fresh openapi-to-mcp generation.\n" +
			"Run: go run ./cmd/openapi-to-mcp (or make generate) and commit the result.")
	}
}

// TestGeneratedInventoryCoversOperabilitySurface asserts the generated
// inventory includes the lenny-ops operability tools the §25.12 "entire
// operability surface" claim depends on — the runbooks, drift, backups,
// restore, upgrade, diagnostics, and event-subscription tools the F-COV-3
// document merge added — not only the pre-generator hand-maintained subset.
// It pins the coverage the generator produces so a future openapi.json edit
// that drops an operability route from the document is caught here as a
// missing tool rather than a silent inventory shrink.
//
// spec: 25.12 (entire operability surface — one MCP tool per operability
// endpoint), 15.1 (OpenAPI completeness).
// diagnosis: The generated MCP inventory is missing an operability tool the
// served document documents. The openapi.json lost the route's
// x-lenny-mcp-tool (or its operability category), so the generator no longer
// emits a tool for it. Restore the route's extensions and regenerate.
func TestGeneratedInventoryCoversOperabilitySurface_spec_25_12(t *testing.T) {
	reg := mcp.NewRegistry()
	have := map[string]bool{}
	for _, tool := range reg.All() {
		have[tool.Name] = true
	}
	// A representative slice across the operability families the F-COV-3
	// merge completed. These are absent from the pre-generator buildToolset
	// literal, so their presence proves the inventory now derives from the
	// complete document rather than the old hand-maintained subset.
	for _, want := range []string{
		"lenny_runbooks_list", "lenny_runbook_steps",
		"lenny_backups_list", "lenny_backup_create",
		"lenny_restore_preview", "lenny_restore_execute",
		"lenny_platform_upgrade_start", "lenny_platform_upgrade_status",
		"lenny_drift_reconcile", "lenny_escalations_list",
		"lenny_event_subscriptions_list", "lenny_logs_pod",
		"lenny_diagnostics_connectivity",
	} {
		if !have[want] {
			t.Errorf("generated MCP inventory is missing the operability tool %q", want)
		}
	}
}

// TestGeneratedInventoryCoversEveryDocumentedEndpoint is the F-MCP-1 /
// §25.12 completeness contract: the generated inventory has exactly one tool
// per documented endpoint that carries a non-null x-lenny-mcp-tool -- every
// admin-API endpoint AND every operability endpoint -- and skips only the
// null-tool endpoints. It pins the §25.12 invariant "Every admin-API endpoint
// with documented RBAC is exposed as an MCP tool -- not only the operability
// endpoints" and the §15.1 rule that /mcp/management is these tools' only MCP
// home. A generator that filters to the operability subset (the pre-fix
// behavior) fails this test because it drops the ~150 gateway admin-API tools
// (tenant, user, runtime, pool, policy, security, connector, adapter, audit),
// leaving those management endpoints with no MCP surface at all.
//
// spec: 25.12 (every admin-API endpoint exposed as a tool, not only
// operability), 15.1 (every admin-API endpoint MUST be an MCP tool on
// /mcp/management; neither MCP server proxies to the other).
// diagnosis: The served openapi.json documents an endpoint with a non-null
// x-lenny-mcp-tool that the generated MCP inventory does not carry (or carries
// an extra tool no endpoint documents). The generator dropped or invented a
// tool relative to the document. Regenerate and reconcile the classification
// table.
func TestGeneratedInventoryCoversEveryDocumentedEndpoint_spec_25_12(t *testing.T) {
	// The set of non-null x-lenny-mcp-tool names the served document carries.
	var doc struct {
		Paths map[string]map[string]struct {
			MCPTool *string `json:"x-lenny-mcp-tool"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(openapi.Document(), &doc); err != nil {
		t.Fatalf("decode openapi document: %v", err)
	}
	httpMethods := map[string]bool{"get": true, "post": true, "put": true, "delete": true, "patch": true}
	wantTools := map[string]bool{}
	for _, methods := range doc.Paths {
		for method, op := range methods {
			if !httpMethods[method] {
				continue
			}
			if op.MCPTool != nil && *op.MCPTool != "" {
				wantTools[*op.MCPTool] = true
			}
		}
	}
	if len(wantTools) == 0 {
		t.Fatal("served document carries no x-lenny-mcp-tool endpoints; the fixture is wrong")
	}

	haveTools := map[string]bool{}
	for _, tool := range mcp.NewRegistry().All() {
		haveTools[tool.Name] = true
	}

	for name := range wantTools {
		if !haveTools[name] {
			t.Errorf("documented endpoint tool %q is absent from the generated /mcp/management inventory", name)
		}
	}
	for name := range haveTools {
		if !wantTools[name] {
			t.Errorf("generated inventory carries tool %q that no documented endpoint declares", name)
		}
	}
	if len(haveTools) != len(wantTools) {
		t.Errorf("inventory has %d tools, document declares %d non-null x-lenny-mcp-tool endpoints", len(haveTools), len(wantTools))
	}
	// The admin-API management families (dropped by the pre-fix
	// operability-only filter) must be present; assert a representative slice
	// so the regression is explicit even if the counts happen to line up.
	for _, name := range []string{
		"admin.create_tenant", "admin.create_runtime", "admin.list_users",
		"admin.create_pool", "admin.create_connector", "admin.list_circuit_breakers",
	} {
		if !haveTools[name] {
			t.Errorf("admin-API management tool %q is missing; §25.12 requires every admin-API endpoint to be a tool, not only operability", name)
		}
	}
}

// TestGeneratedDestructiveToolsClassified asserts the generator classifies
// the data-plane and platform-wide write tools as destructive so the §25.12
// nonDestructive capability filter excludes them. A misclassification (e.g. a
// restore-execute tool labelled mutation) would leak a destructive tool into
// a nonDestructive agent's view, so this pins the taxonomy the generator
// assigns for the highest-risk tools.
//
// spec: 25.12 (x-lenny-category tool taxonomy — destructive; nonDestructive
// capability filter).
// diagnosis: A destructive tool is classified below destructive in the
// generator classification table, so the nonDestructive filter would show it.
// Fix its entry in mcptoolgen.classifications and regenerate.
func TestGeneratedDestructiveToolsClassified_spec_25_12(t *testing.T) {
	reg := mcp.NewRegistry()
	byName := map[string]mcp.Tool{}
	for _, tool := range reg.All() {
		byName[tool.Name] = tool
	}
	for _, name := range []string{
		"lenny_backup_create", "lenny_restore_execute",
		"lenny_platform_upgrade_start", "lenny_platform_config_apply",
	} {
		tool, ok := byName[name]
		if !ok {
			t.Errorf("expected destructive tool %q in the inventory", name)
			continue
		}
		if tool.Category != mcp.CategoryDestructive {
			t.Errorf("tool %q category = %q, want %q", name, tool.Category, mcp.CategoryDestructive)
		}
	}
	// The nonDestructive filter drops every destructive tool.
	// An absent scope claim (empty Set) permits every scope, so this
	// exercises the capability filter alone.
	filtered := reg.FilterForList(mcp.Capabilities{NonDestructive: true}, scopes.Set{})
	for _, tool := range filtered {
		if tool.Category == mcp.CategoryDestructive {
			t.Errorf("nonDestructive filter kept the destructive tool %q", tool.Name)
		}
	}
}
