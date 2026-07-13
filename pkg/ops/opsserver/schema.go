// SPDX-License-Identifier: MIT

package opsserver

import (
	"net/http"
	"reflect"
	"sort"
	"strings"
)

// RouteSchema is the OpenAPI-emission descriptor for one §25 lenny-ops
// operability route. It carries the method, path template, human summary,
// and the four §15.1 / §25.12 `x-lenny-*` extensions the served OpenAPI
// document requires on every documented admin path (x-lenny-mcp-tool,
// x-lenny-scope, x-lenny-required-role, x-lenny-category).
//
// The lenny-ops routes are served by a separate Deployment from the
// gateway (pkg/ops/opsserver), so they are absent from the gateway-built
// openapi.json by construction. F-COV-3 merges them into the single served
// document; this registry is the schema-emission surface that merge reads,
// so the merge is regenerable rather than hand-drifting from the served
// routes. The §25.12 build-time openapi-to-mcp generator then emits one MCP
// tool per operability route from the completed document.
//
// spec: §15.1 (OpenAPI x-lenny-* extension contract), §25.12 (operability
// surface + build-time OpenAPI→MCP generation), §18 (build ordering).
type RouteSchema struct {
	// Method is the HTTP verb (GET, POST, PUT, PATCH, DELETE).
	Method string
	// Path is the route template as registered on the opsserver mux, e.g.
	// /v1/admin/runbooks/{name}. Path parameters use the same {name} form the
	// mux registers and the OpenAPI document documents.
	Path string
	// Summary is the human-readable operation summary emitted as
	// paths.<path>.<method>.summary.
	Summary string
	// MCPTool is the §25.12 x-lenny-mcp-tool value: the management MCP tool
	// name the build-time generator emits for this route, or the empty
	// string for a route that is not exposed as a tool (serialised as the
	// JSON null the per-path invariant permits).
	MCPTool string
	// Scope is the §15.1 x-lenny-scope in canonical tools:<domain>:<action>
	// form. Every scope's domain is in the closed scopes.Domains taxonomy.
	Scope string
	// RequiredRole is the §25.12 x-lenny-required-role. lenny-ops gates every
	// route on platform-admin or tenant-admin (pkg/ops/opsserver/auth.go);
	// the caller-self /me routes admit the user role.
	RequiredRole string
	// Category is the §25.12 x-lenny-category. Every operability route is
	// classified "operability", matching the two /me routes already in the
	// served document.
	RequiredCategory string
	// SuccessStatus is the primary 2xx response code the operation returns
	// (200 for reads and idempotent updates, 201 for creates, 202 for
	// accepted-async, 204 for no-content). It is emitted as the sole
	// documented response so the merge produces a valid operation object.
	SuccessStatus string
}

// operabilityCategory is the §25.12 x-lenny-category every lenny-ops
// operability route carries, matching the /me and /platform/config entries
// already in the served document.
const operabilityCategory = "operability"

// RouteSchemas returns the §25 lenny-ops operability route inventory the
// F-COV-3 document merge emits into the gateway-served openapi.json, keyed
// by the routes the opsserver mux registers. The returned slice is sorted
// by (path, method) so the merge and the drift guard are deterministic.
//
// The registry is the single source of the lenny-ops route→schema mapping.
// TestRouteSchemasMatchLiveMux (schema_test.go) walks a fully-wired
// opsserver mux and asserts the registry lists exactly the /v1/admin/*
// routes the server registers (both directions), so the registry cannot
// silently drift from the served surface. The gateway-side
// completeness_test.go then asserts the served document covers every route
// this registry names (lenny-ops-mux ⊆ doc).
//
// spec: §25.12 (operability surface), §15.1 (OpenAPI completeness).
func RouteSchemas() []RouteSchema {
	out := append([]RouteSchema(nil), opsRouteSchemas...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Method < out[j].Method
	})
	return out
}

// adminRoutePatterns walks the opsserver mux and returns the set of
// "METHOD /path" operability routes it registers under /v1/admin/. The
// probe/scrape/MCP routes and the §25.8 GET /v1/latest release-channel route
// sit outside the /v1/admin operability surface the OpenAPI document
// documents, so they are excluded. The drift guard compares this against
// RouteSchemas.
//
// The walk is the same http.ServeMux routing-tree reflection the gateway
// completeness test uses; it is the source of truth the RouteSchemas
// registry is guarded against. spec: §25.12.
func adminRoutePatterns(mux *http.ServeMux) map[string]bool {
	out := map[string]bool{}
	for _, pat := range walkMuxPatterns(mux) {
		// pat is "METHOD /path"; keep only the /v1/admin operability surface.
		sp := strings.SplitN(pat, " ", 2)
		if len(sp) != 2 || !strings.HasPrefix(sp[1], "/v1/admin/") {
			continue
		}
		out[pat] = true
	}
	return out
}

// walkMuxPatterns extracts the registered "METHOD /path" patterns from an
// *http.ServeMux by reflecting over its unexported routing tree. The Go
// standard library does not expose the registered patterns, so this reaches
// into the tree the same way the gateway completeness test does. It is used
// by the drift guard (schema_test.go) and by LiveAdminRoutePatterns; a
// future Go release that renames the tree fields yields an empty result,
// which the guard surfaces as a failure rather than a silent miss.
func walkMuxPatterns(mux *http.ServeMux) []string {
	patterns := map[string]bool{}
	tree := reflect.ValueOf(mux).Elem().FieldByName("tree")
	if tree.IsValid() {
		walkRoutingNode(tree, patterns)
	}
	out := make([]string, 0, len(patterns))
	for p := range patterns {
		out = append(out, p)
	}
	return out
}

// walkRoutingNode recurses the http.routingNode tree, collecting the full
// "METHOD /path" string of every node that carries a registered pattern.
func walkRoutingNode(node reflect.Value, out map[string]bool) {
	if node.Kind() == reflect.Ptr {
		if node.IsNil() {
			return
		}
		node = node.Elem()
	}
	if pat := node.FieldByName("pattern"); pat.IsValid() && !pat.IsNil() {
		if str := pat.Elem().FieldByName("str"); str.IsValid() {
			out[str.String()] = true
		}
	}
	if children := node.FieldByName("children"); children.IsValid() {
		if s := children.FieldByName("s"); s.IsValid() {
			for i := 0; i < s.Len(); i++ {
				walkRoutingNode(s.Index(i).FieldByName("value"), out)
			}
		}
		if m := children.FieldByName("m"); m.IsValid() && !m.IsNil() {
			for _, k := range m.MapKeys() {
				walkRoutingNode(m.MapIndex(k), out)
			}
		}
	}
	for _, child := range []string{"multiChild", "emptyChild"} {
		if c := node.FieldByName(child); c.IsValid() {
			walkRoutingNode(c, out)
		}
	}
}

// opsRouteSchemas is the flat §25 operability route inventory. Each entry
// maps a served opsserver route to its OpenAPI schema. Grouped by §25
// subsection for readability; RouteSchemas sorts the slice for the merge.
//
// x-lenny-mcp-tool is the §25.12 management tool name for a route the
// build-time openapi-to-mcp generator exposes as a tool, or "" (JSON null)
// for a route that is not a tool: the §25.5 SSE/poll event stream, the
// caller-self /me routes (identity discovery rather than a management
// action), and the §25.5 subscription-delivery/rotate-secret sub-resources.
var opsRouteSchemas = []RouteSchema{
	// §25.4 caller identity + capabilities.
	{"GET", "/v1/admin/me", "Identity + role grants for the calling principal", "admin.me", "tools:me:read", "user", operabilityCategory, "200"},
	{"GET", "/v1/admin/me/authorized-tools", "Admin tools the caller's roles authorize", "admin.me_authorized_tools", "tools:me:read", "user", operabilityCategory, "200"},
	{"GET", "/v1/admin/me/operations", "The caller's in-flight operations", "", "tools:me:read", "user", operabilityCategory, "200"},

	// §25.4 lenny-ops self-health.
	{"GET", "/v1/admin/ops/health", "lenny-ops self-health report", "lenny_ops_health_get", "tools:health:read", "platform-admin", operabilityCategory, "200"},

	// §25.4 Operations Inventory.
	{"GET", "/v1/admin/operations", "List in-flight operations across subsystems", "lenny_operations_list", "tools:operations:read", "platform-admin", operabilityCategory, "200"},
	{"GET", "/v1/admin/operations/{id}", "Get a single operation by canonical id", "lenny_operation_get", "tools:operations:read", "platform-admin", operabilityCategory, "200"},

	// §25.6 diagnostics.
	{"GET", "/v1/admin/diagnostics/connectivity", "Check dependency connectivity", "lenny_diagnostics_connectivity", "tools:diagnostics:read", "platform-admin", operabilityCategory, "200"},
	{"GET", "/v1/admin/diagnostics/sessions/{id}", "Diagnose a session: structured cause chain", "lenny_diagnostics_session", "tools:diagnostics:read", "platform-admin", operabilityCategory, "200"},
	{"GET", "/v1/admin/diagnostics/pools/{name}", "Diagnose a warm pool: bottleneck analysis", "lenny_diagnostics_pool", "tools:diagnostics:read", "platform-admin", operabilityCategory, "200"},
	{"GET", "/v1/admin/diagnostics/credential-pools/{name}", "Diagnose a credential pool's health", "lenny_diagnostics_credential_pool", "tools:diagnostics:read", "platform-admin", operabilityCategory, "200"},
	{"POST", "/v1/admin/diagnostics/run", "Run the doctor auto-remediation battery", "lenny_doctor_run", "tools:diagnostics:write", "platform-admin", operabilityCategory, "200"},

	// §25.7 runbooks.
	{"GET", "/v1/admin/runbooks", "List operational runbooks", "lenny_runbooks_list", "tools:runbooks:read", "platform-admin", operabilityCategory, "200"},
	{"GET", "/v1/admin/runbooks/{name}", "Get a runbook's full markdown content", "lenny_runbooks_get", "tools:runbooks:read", "platform-admin", operabilityCategory, "200"},
	{"GET", "/v1/admin/runbooks/{name}/steps", "Get a runbook's structured steps", "lenny_runbook_steps", "tools:runbooks:read", "platform-admin", operabilityCategory, "200"},

	// §25.10 configuration drift.
	{"GET", "/v1/admin/drift", "Get the configuration drift report", "lenny_drift_report", "tools:drift:read", "platform-admin", operabilityCategory, "200"},
	{"POST", "/v1/admin/drift/validate", "Validate a desired state against the snapshot", "lenny_drift_validate", "tools:drift:write", "platform-admin", operabilityCategory, "200"},
	{"POST", "/v1/admin/drift/snapshot/refresh", "Replace the stored desired-state snapshot", "lenny_drift_snapshot_refresh", "tools:drift:write", "platform-admin", operabilityCategory, "200"},
	{"POST", "/v1/admin/drift/reconcile", "Reconcile drifted resources toward the snapshot", "lenny_drift_reconcile", "tools:drift:write", "platform-admin", operabilityCategory, "200"},

	// §25.4 remediation locks.
	{"GET", "/v1/admin/remediation-locks", "List active remediation locks", "lenny_locks_list", "tools:locks:read", "platform-admin", operabilityCategory, "200"},
	{"POST", "/v1/admin/remediation-locks", "Acquire a remediation lock", "lenny_lock_acquire", "tools:locks:write", "platform-admin", operabilityCategory, "201"},
	{"GET", "/v1/admin/remediation-locks/{id}", "Get a single remediation lock's state", "lenny_lock_get", "tools:locks:read", "platform-admin", operabilityCategory, "200"},
	{"PATCH", "/v1/admin/remediation-locks/{id}", "Extend a held remediation lock", "lenny_lock_extend", "tools:locks:write", "platform-admin", operabilityCategory, "200"},
	{"DELETE", "/v1/admin/remediation-locks/{id}", "Release a remediation lock", "lenny_lock_release", "tools:locks:write", "platform-admin", operabilityCategory, "204"},
	{"POST", "/v1/admin/remediation-locks/{id}/steal", "Steal an expired remediation lock", "lenny_lock_steal", "tools:locks:write", "platform-admin", operabilityCategory, "200"},

	// §25.4 escalations.
	{"GET", "/v1/admin/escalations", "List escalations", "lenny_escalations_list", "tools:escalation:read", "platform-admin", operabilityCategory, "200"},
	{"POST", "/v1/admin/escalations", "Open an escalation", "lenny_escalation_create", "tools:escalation:write", "platform-admin", operabilityCategory, "201"},
	{"PUT", "/v1/admin/escalations/{id}", "Update an escalation's state", "lenny_escalation_update", "tools:escalation:write", "platform-admin", operabilityCategory, "200"},

	// §25.5 operational event stream (SSE + poll) and webhook subscriptions.
	{"GET", "/v1/admin/events", "Poll the operational event stream", "", "tools:events:read", "platform-admin", operabilityCategory, "200"},
	{"GET", "/v1/admin/events/stream", "Server-sent operational event stream", "", "tools:events:read", "platform-admin", operabilityCategory, "200"},
	// spec: §25.5 — a tenant-admin may create, list, get, update, and delete
	// subscriptions scoped to its own tenant, so the tool's required-role
	// ceiling is tenant-admin (the value callerHasToolRole reads as "admits
	// platform-admin and tenant-admin"), keeping the §25.4 authorized-tools
	// discovery surface accurate for a tenant-admin caller.
	{"GET", "/v1/admin/event-subscriptions", "List webhook event subscriptions", "lenny_event_subscriptions_list", "tools:events:read", "tenant-admin", operabilityCategory, "200"},
	{"POST", "/v1/admin/event-subscriptions", "Create a webhook event subscription", "lenny_event_subscription_create", "tools:events:write", "tenant-admin", operabilityCategory, "201"},
	{"GET", "/v1/admin/event-subscriptions/{id}", "Get a webhook event subscription", "lenny_event_subscription_get", "tools:events:read", "tenant-admin", operabilityCategory, "200"},
	{"PUT", "/v1/admin/event-subscriptions/{id}", "Update a webhook event subscription", "lenny_event_subscription_update", "tools:events:write", "tenant-admin", operabilityCategory, "200"},
	{"DELETE", "/v1/admin/event-subscriptions/{id}", "Delete a webhook event subscription", "lenny_event_subscription_delete", "tools:events:write", "tenant-admin", operabilityCategory, "204"},
	{"GET", "/v1/admin/event-subscriptions/{id}/deliveries", "List a subscription's webhook deliveries", "", "tools:events:read", "platform-admin", operabilityCategory, "200"},
	{"POST", "/v1/admin/event-subscriptions/{id}/rotate-secret", "Rotate a subscription's signing secret", "", "tools:events:write", "platform-admin", operabilityCategory, "200"},

	// §25.4 pod-log proxy.
	{"GET", "/v1/admin/logs/pods/{namespace}/{name}", "Proxy a pod's logs from the Kubernetes API", "lenny_logs_pod", "tools:logs:read", "platform-admin", operabilityCategory, "200"},

	// §25.11 backup and restore.
	{"GET", "/v1/admin/backups", "List platform backups", "lenny_backups_list", "tools:backup:read", "platform-admin", operabilityCategory, "200"},
	{"POST", "/v1/admin/backups", "Create a platform backup", "lenny_backup_create", "tools:backup:write", "platform-admin", operabilityCategory, "202"},
	{"GET", "/v1/admin/backups/{id}", "Get a backup's metadata", "lenny_backup_get", "tools:backup:read", "platform-admin", operabilityCategory, "200"},
	{"POST", "/v1/admin/backups/{id}/verify", "Verify a backup's integrity", "lenny_backup_verify", "tools:backup:write", "platform-admin", operabilityCategory, "202"},
	{"GET", "/v1/admin/backups/policy", "Get the backup retention policy", "lenny_backup_policy_get", "tools:backup:read", "platform-admin", operabilityCategory, "200"},
	{"PUT", "/v1/admin/backups/policy", "Update the backup retention policy", "lenny_backup_policy_set", "tools:backup:write", "platform-admin", operabilityCategory, "200"},
	{"GET", "/v1/admin/backups/schedule", "Get the backup schedule", "lenny_backup_schedule_get", "tools:backup:read", "platform-admin", operabilityCategory, "200"},
	{"PUT", "/v1/admin/backups/schedule", "Update the backup schedule", "lenny_backup_schedule_set", "tools:backup:write", "platform-admin", operabilityCategory, "200"},
	{"GET", "/v1/admin/backup-jobs/{id}", "Get an async backup job's status", "lenny_backup_job_get", "tools:backup:read", "platform-admin", operabilityCategory, "200"},
	{"POST", "/v1/admin/restore/preview", "Preview a restore plan (dry-run)", "lenny_restore_preview", "tools:restore:write", "platform-admin", operabilityCategory, "200"},
	{"GET", "/v1/admin/restore/safety-check", "Run the pre-restore safety check", "lenny_restore_safety_check", "tools:restore:read", "platform-admin", operabilityCategory, "200"},
	{"POST", "/v1/admin/restore/execute", "Execute a restore", "lenny_restore_execute", "tools:restore:write", "platform-admin", operabilityCategory, "202"},
	{"GET", "/v1/admin/restore/{id}/status", "Get a restore operation's status", "lenny_restore_status", "tools:restore:read", "platform-admin", operabilityCategory, "200"},
	{"POST", "/v1/admin/restore/resume", "Resume a paused restore", "lenny_restore_resume", "tools:restore:write", "platform-admin", operabilityCategory, "202"},
	{"POST", "/v1/admin/restore/{id}/confirm-legal-hold-ledger", "Confirm the legal-hold ledger before restore", "lenny_restore_confirm_legal_hold", "tools:restore:write", "platform-admin", operabilityCategory, "200"},

	// §25.8 platform upgrade, config, and registry.
	{"GET", "/v1/admin/platform/config/diff", "Diff the running config against the desired config", "lenny_platform_config_diff", "tools:config:read", "platform-admin", operabilityCategory, "200"},
	{"PUT", "/v1/admin/platform/config", "Apply a platform configuration change", "lenny_platform_config_apply", "tools:config:write", "platform-admin", operabilityCategory, "200"},
	{"GET", "/v1/admin/platform/registry", "Get the runtime registry", "lenny_platform_registry_get", "tools:config:read", "platform-admin", operabilityCategory, "200"},
	{"PUT", "/v1/admin/platform/registry", "Update the runtime registry", "lenny_platform_registry_set", "tools:config:write", "platform-admin", operabilityCategory, "200"},
	{"GET", "/v1/admin/platform/upgrade-check", "Check the release channel for an available upgrade", "lenny_platform_upgrade_check", "tools:upgrade:read", "platform-admin", operabilityCategory, "200"},
	{"GET", "/v1/admin/platform/version/full", "Report full component versions and drift", "lenny_platform_version_full", "tools:upgrade:read", "platform-admin", operabilityCategory, "200"},
	{"POST", "/v1/admin/platform/upgrade/preflight", "Run upgrade preflight and return the plan", "lenny_platform_upgrade_preflight", "tools:upgrade:write", "platform-admin", operabilityCategory, "200"},
	{"POST", "/v1/admin/platform/upgrade/start", "Start a platform upgrade", "lenny_platform_upgrade_start", "tools:upgrade:write", "platform-admin", operabilityCategory, "202"},
	{"POST", "/v1/admin/platform/upgrade/proceed", "Proceed a paused upgrade to the next phase", "lenny_platform_upgrade_proceed", "tools:upgrade:write", "platform-admin", operabilityCategory, "202"},
	{"POST", "/v1/admin/platform/upgrade/pause", "Pause an in-progress upgrade", "lenny_platform_upgrade_pause", "tools:upgrade:write", "platform-admin", operabilityCategory, "202"},
	{"POST", "/v1/admin/platform/upgrade/rollback", "Roll back an upgrade", "lenny_platform_upgrade_rollback", "tools:upgrade:write", "platform-admin", operabilityCategory, "202"},
	{"POST", "/v1/admin/platform/upgrade/verify", "Verify a completed upgrade phase", "lenny_platform_upgrade_verify", "tools:upgrade:write", "platform-admin", operabilityCategory, "200"},
	{"GET", "/v1/admin/platform/upgrade/status", "Get the current upgrade status", "lenny_platform_upgrade_status", "tools:upgrade:read", "platform-admin", operabilityCategory, "200"},
}
