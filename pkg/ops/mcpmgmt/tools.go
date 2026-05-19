// SPDX-License-Identifier: MIT

// Package mcpmgmt implements the §25.12 MCP Management Server: it
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
// This package carries the tool inventory (this file), the JSON-RPC
// surface (server.go), and the tool-to-handler routing. v1 exposes the
// §25.3–25.11 operability tools — health, diagnostics, runbooks,
// drift, locks, escalations. The build-time OpenAPI → MCP generation
// that §25.12 describes for the full admin surface reuses this
// registry's contract.
package mcpmgmt

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

// objectSchema builds a JSON Schema object with the given properties
// and required fields. A nil props map yields an empty properties
// object so the schema is a valid JSON Schema document for a no-argument
// tool.
func objectSchema(props map[string]any, required ...string) map[string]any {
	if props == nil {
		props = map[string]any{}
	}
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	} else {
		s["required"] = []string{}
	}
	return s
}

// strProp builds a JSON Schema string property with a description.
func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

// boolProp builds a JSON Schema boolean property with a description.
func boolProp(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}

// objProp builds a JSON Schema object property with a description.
func objProp(desc string) map[string]any {
	return map[string]any{"type": "object", "description": desc}
}

// operationIDProp is the §25.12 optional operationId property every
// tool schema includes for multi-step operation correlation.
var operationIDProp = map[string]any{
	"type":        "string",
	"format":      "uuid",
	"description": "Optional UUID for multi-step operation correlation.",
}

// observationTool builds a read-only §25.12 observation tool.
func observationTool(name, desc, method, path, scope string, props map[string]any) Tool {
	return Tool{
		Name: name, Description: desc, Method: method, Path: path,
		InputSchema:   objectSchema(props),
		Category:      CategoryObservation,
		RequiredRole:  RolePlatformAdmin,
		Scope:         scope,
		DryRunSupport: DryRunNone,
		ReadOnly:      true,
	}
}

// buildToolset returns the §25.12 v1 MCP management tool inventory: the
// operability tools spanning health, diagnostics, runbooks, drift,
// remediation locks, and escalations. The registry is keyed by tool
// name in NewRegistry.
func buildToolset() []Tool {
	return []Tool{
		// §25.12 observation tools — health and self-health.
		observationTool("lenny_health_get", "Get aggregate platform health.",
			"GET", "/v1/admin/health", "tools:health:read", nil),
		observationTool("lenny_health_component", "Get a component health deep-dive.",
			"GET", "/v1/admin/health/{component}", "tools:health:read",
			map[string]any{"component": strProp("Component name, e.g. 'warmPools'.")}),
		observationTool("lenny_health_summary", "Get the minimal health status.",
			"GET", "/v1/admin/health/summary", "tools:health:read", nil),
		observationTool("lenny_ops_health_get", "Get lenny-ops self-health.",
			"GET", "/v1/admin/ops/health", "tools:health:read", nil),
		observationTool("lenny_recommendations_get", "Get capacity recommendations.",
			"GET", "/v1/admin/recommendations", "tools:recommendations:read",
			map[string]any{"category": strProp("Optional recommendation-category filter.")}),

		// §25.12 observation tools — diagnostics.
		observationTool("lenny_diagnostics_session", "Diagnose a session: structured cause chain.",
			"GET", "/v1/admin/diagnostics/sessions/{id}", "tools:diagnostics:read",
			map[string]any{"id": strProp("Session id.")}),
		observationTool("lenny_diagnostics_pool", "Diagnose a warm pool: bottleneck analysis.",
			"GET", "/v1/admin/diagnostics/pools/{name}", "tools:diagnostics:read",
			map[string]any{"name": strProp("Pool name.")}),
		observationTool("lenny_diagnostics_connectivity", "Check dependency connectivity.",
			"GET", "/v1/admin/diagnostics/connectivity", "tools:diagnostics:read", nil),
		observationTool("lenny_diagnostics_credential_pool", "Diagnose a credential pool's health.",
			"GET", "/v1/admin/diagnostics/credential-pools/{name}", "tools:diagnostics:read",
			map[string]any{"name": strProp("Credential pool name.")}),

		// §25.12 observation tools — runbooks.
		observationTool("lenny_runbooks_list", "List operational runbooks with optional filters.",
			"GET", "/v1/admin/runbooks", "tools:runbooks:read",
			map[string]any{
				"alert":     strProp("Filter to runbooks triggered by this alert."),
				"component": strProp("Filter to runbooks for this health component."),
				"tag":       strProp("Filter to runbooks carrying this tag."),
			}),
		observationTool("lenny_runbooks_get", "Get a runbook's full markdown content.",
			"GET", "/v1/admin/runbooks/{name}", "tools:runbooks:read",
			map[string]any{"name": strProp("Runbook name.")}),

		// §25.12 observation tools — drift, audit, locks.
		observationTool("lenny_drift_report", "Get the configuration drift report.",
			"GET", "/v1/admin/drift", "tools:drift:read",
			map[string]any{
				"scope":   strProp("Comparison scope: all, pools, runtimes, tenants, credential-pools."),
				"against": strProp("Snapshot to compare against: live or target."),
			}),
		observationTool("lenny_audit_query", "Query the audit log.",
			"GET", "/v1/admin/audit-events", "tools:audit:read",
			map[string]any{
				"actorId":      strProp("Filter by caller identity."),
				"resourceType": strProp("Filter by resource type."),
			}),
		observationTool("lenny_locks_list", "List active remediation locks.",
			"GET", "/v1/admin/remediation-locks", "tools:locks:read", nil),
		observationTool("lenny_lock_get", "Get a single remediation lock's state.",
			"GET", "/v1/admin/remediation-locks/{id}", "tools:locks:read",
			map[string]any{"id": strProp("Lock id.")}),

		// §25.12 action tools — drift (coordination/destructive).
		{
			Name:        "lenny_drift_validate",
			Description: "Validate a caller-supplied desired state against the stored snapshot.",
			Method:      "POST", Path: "/v1/admin/drift/validate",
			InputSchema: objectSchema(map[string]any{
				"desired": objProp("The externally-supplied desired state to validate."),
			}, "desired"),
			Category: CategoryMutation, RequiredRole: RolePlatformAdmin,
			Scope: "tools:drift:write", DryRunSupport: DryRunNone,
		},
		{
			Name:        "lenny_drift_snapshot_refresh",
			Description: "Replace the stored desired-state snapshot. Requires confirm:true; omit for a dry-run preview.",
			Method:      "POST", Path: "/v1/admin/drift/snapshot/refresh",
			InputSchema: objectSchema(map[string]any{
				"desired":     objProp("The current desired state to store."),
				"confirm":     boolProp("Required to apply. Omit for a dry-run preview."),
				"operationId": operationIDProp,
			}, "desired"),
			Category: CategoryMutation, RequiredRole: RolePlatformAdmin,
			Scope: "tools:drift:write", DryRunSupport: DryRunConfirmBool,
		},

		// §25.12 action tools — remediation locks (coordination).
		{
			Name:        "lenny_lock_acquire",
			Description: "Acquire a remediation lock on a resource scope.",
			Method:      "POST", Path: "/v1/admin/remediation-locks",
			InputSchema: objectSchema(map[string]any{
				"scope":       strProp("Resource scope, e.g. 'pool:default-gvisor'."),
				"operation":   strProp("The remediation operation, e.g. 'scale'."),
				"ttlSeconds":  map[string]any{"type": "integer", "minimum": 1, "description": "Lock TTL in seconds."},
				"operationId": operationIDProp,
			}, "scope"),
			Category: CategoryCoordination, RequiredRole: RolePlatformAdmin,
			Scope: "tools:locks:write", DryRunSupport: DryRunNone,
		},
		{
			Name:        "lenny_lock_extend",
			Description: "Extend a held remediation lock's TTL.",
			Method:      "PATCH", Path: "/v1/admin/remediation-locks/{id}",
			InputSchema: objectSchema(map[string]any{
				"id":         strProp("Lock id."),
				"ttlSeconds": map[string]any{"type": "integer", "minimum": 1, "description": "New TTL in seconds."},
			}, "id"),
			Category: CategoryCoordination, RequiredRole: RolePlatformAdmin,
			Scope: "tools:locks:write", DryRunSupport: DryRunNone,
		},
		{
			Name:        "lenny_lock_release",
			Description: "Release a remediation lock the caller holds.",
			Method:      "DELETE", Path: "/v1/admin/remediation-locks/{id}",
			InputSchema: objectSchema(map[string]any{
				"id": strProp("Lock id."),
			}, "id"),
			Category: CategoryCoordination, RequiredRole: RolePlatformAdmin,
			Scope: "tools:locks:write", DryRunSupport: DryRunNone,
		},
		{
			Name:        "lenny_lock_steal",
			Description: "Take over an existing remediation lock (audited). Requires confirm:true and a reason.",
			Method:      "POST", Path: "/v1/admin/remediation-locks/{id}/steal",
			InputSchema: objectSchema(map[string]any{
				"id":         strProp("Lock id."),
				"confirm":    boolProp("Required to apply. Omit for a dry-run preview."),
				"reason":     strProp("Why the lock is being stolen."),
				"ttlSeconds": map[string]any{"type": "integer", "minimum": 1, "description": "New TTL in seconds."},
			}, "id"),
			Category: CategoryCoordination, RequiredRole: RolePlatformAdmin,
			Scope: "tools:locks:write", DryRunSupport: DryRunConfirmBool,
		},

		// §25.12 action tools — escalations (coordination).
		{
			Name:        "lenny_escalation_create",
			Description: "Create an escalation when a problem exceeds the agent's remediation capabilities.",
			Method:      "POST", Path: "/v1/admin/escalations",
			InputSchema: objectSchema(map[string]any{
				"severity":    map[string]any{"type": "string", "enum": []string{"critical", "warning", "info"}, "description": "Escalation severity."},
				"summary":     strProp("Human-readable escalation summary."),
				"alertName":   strProp("The triggering alert, if any."),
				"runbookName": strProp("The relevant runbook, if any."),
				"operationId": operationIDProp,
			}, "severity", "summary"),
			Category: CategoryCoordination, RequiredRole: RolePlatformAdmin,
			Scope: "tools:escalations:write", DryRunSupport: DryRunNone,
		},
	}
}
