// SPDX-License-Identifier: MIT

// Package mcp implements the §25.12 MCP Management Server: it
// exposes the §25 operability surface as an MCP tool server so any
// MCP-capable agent can manage Lenny natively rather than only observe
// it. An agent that speaks MCP discovers every tool, inspects its
// schema, and invokes it without REST-API knowledge.
//
// §25.12 serves the management MCP server at /mcp/management. It is a
// separate MCP server from /mcp/runtimes/{name} (which proxies tool
// calls to agent pods) with its own capability negotiation and
// authentication scope.
//
// This file carries the tool descriptor type and the §25.12 taxonomy
// vocabulary. The tool inventory itself is generated: §25.12 mandates a
// build-time openapi-to-mcp step that derives one tool per documented
// operability endpoint from the served OpenAPI document, so the inventory
// lives in the generated generated_tools.go (via generatedToolset) rather
// than a hand-maintained literal, and cannot drift from the REST surface it
// mirrors. The JSON-RPC surface is in server.go and the registry in
// registry.go. v1 exposes the operability tools — health, diagnostics,
// runbooks, drift, locks, escalations, backup/restore, upgrade, events.
//
//go:generate go run ../../../cmd/openapi-to-mcp
package mcp

// §25.12 x-lenny-category tool classifications.
const (
	// CategoryObservation is a read-only tool.
	CategoryObservation = "observation"
	// CategoryCoordination is a mutating but low-risk tool (locks,
	// escalations).
	CategoryCoordination = "coordination"
	// CategoryMutation is a state-changing tool.
	CategoryMutation = "mutation"
	// CategoryDestructive is a high-risk tool (backup, restore, upgrade).
	CategoryDestructive = "destructive"
)

// §25.12 x-lenny-required-role values.
const (
	RolePlatformAdmin = "platform-admin"
	RoleTenantAdmin   = "tenant-admin"
)

// §25.12 x-lenny-dry-run-support values.
const (
	// DryRunConfirmBool marks a tool that follows the §25.2 dry-run/
	// confirm pattern: invoking without confirm:true yields a preview.
	DryRunConfirmBool = "confirm-bool"
	// DryRunNone marks a tool with no dry-run pattern.
	DryRunNone = "none"
)

// Tool is one §25.12 MCP management tool: its name, description, JSON
// Schema input, the REST endpoint it maps to, and the x-lenny-*
// metadata that drives capability filtering and the security model.
type Tool struct {
	// Name is the tool name; §25.12 names follow lenny_{domain}_{action}.
	Name string `json:"name"`
	// Description is the human-readable tool description.
	Description string `json:"description"`
	// InputSchema is the JSON Schema Draft 2020-12 document for the
	// tool's arguments.
	InputSchema map[string]any `json:"inputSchema"`

	// Method and Path are the admin-API endpoint the tool maps to.
	Method string `json:"-"`
	Path   string `json:"-"`

	// Category is the §25.12 x-lenny-category classification.
	Category string `json:"-"`
	// RequiredRole is the §25.12 x-lenny-required-role.
	RequiredRole string `json:"-"`
	// Scope is the §25.12 x-lenny-scope identifier the scope-enforcement
	// layer checks against the caller's JWT scope claim.
	Scope string `json:"-"`
	// DryRunSupport is the §25.12 x-lenny-dry-run-support value.
	DryRunSupport string `json:"-"`
	// ReadOnly reports whether the tool is in the observation category
	// (used by the readOnly capability filter).
	ReadOnly bool `json:"-"`
}
