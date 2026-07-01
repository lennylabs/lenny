// SPDX-License-Identifier: MIT

// Package mcptoolgen derives the §25.12 `/mcp/management` tool inventory
// from the gateway-served OpenAPI document.
//
// §25.12 mandates the inventory be generated rather than hand-maintained:
// "a single build-time `openapi-to-mcp` step produces the tool inventory
// from the OpenAPI" and "every admin-API endpoint with documented RBAC
// becomes an MCP tool automatically via the build-time OpenAPI → MCP
// generation". This package is the transform behind the `openapi-to-mcp`
// command: it reads `openapi.Document()`, emits one tool per documented
// operability endpoint that carries a non-null `x-lenny-mcp-tool`, and
// resolves each tool's wire fields (name, method, path, scope, required
// role) from the document plus its input schema from the operation's path
// parameters and request body (via the shared mcpschemagen machinery the
// §15.2 gateway generator reuses).
//
// The tool-taxonomy classification (§25.12 `x-lenny-category`:
// observation | coordination | mutation | destructive, and
// `x-lenny-dry-run-support`: confirm-bool | none) is not carried on the
// endpoint document — that document's `x-lenny-category` is the endpoint
// domain (`operability`) rather than the tool taxonomy the MCP descriptor
// reports. The classification lives in this generator as an explicit table
// keyed by tool name, so a destructive tool is classified deterministically
// rather than guessed from the HTTP method (a POST is coordination for a
// lock but destructive for a restore). An operability tool the document
// gains without a classification entry fails generation, so a new tool
// cannot silently ship misclassified.
//
// spec: §25.12 (build-time openapi-to-mcp generation, tool taxonomy),
// §15.1 (OpenAPI x-lenny-* extension contract), §18 (Phase 13
// openapi-to-mcp generator over the complete document).
package mcptoolgen

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcpschemagen"
)

// operabilityCategory is the §15.1 endpoint-domain `x-lenny-category` the
// §25.12 management server exposes as tools. It is the endpoint domain, not
// the tool taxonomy; see the package comment.
const operabilityCategory = "operability"

// GeneratedTool is the wire-and-metadata descriptor the generator emits for
// one §25.12 management tool. It mirrors the fields the mcp.Tool the runtime
// registry holds needs, kept as an independent type so the generator does
// not import the mcp package it writes into.
type GeneratedTool struct {
	Name          string
	Description   string
	InputSchema   json.RawMessage
	Method        string
	Path          string
	Category      string // §25.12 tool taxonomy
	RequiredRole  string
	Scope         string
	DryRunSupport string
	ReadOnly      bool
}

// classification is the §25.12 tool-taxonomy classification for one tool:
// its category and dry-run support. ReadOnly is derived from the category
// (observation ⇒ read-only) so the two cannot disagree.
type classification struct {
	category string
	dryRun   string
}

// Tool taxonomy values, matching the mcp package constants. Duplicated here
// so the generator does not import the package it generates into.
const (
	categoryObservation  = "observation"
	categoryCoordination = "coordination"
	categoryMutation     = "mutation"
	categoryDestructive  = "destructive"

	dryRunConfirmBool = "confirm-bool"
	dryRunNone        = "none"
)

// classifications maps each operability tool name to its §25.12 tool
// taxonomy. It is the explicit, reviewed classification the endpoint
// document cannot carry (its `x-lenny-category` is the endpoint domain).
// Generation fails when the document names an operability tool absent from
// this table, so a newly documented tool must be classified before it ships.
//
// Categories: observation (read-only), coordination (locks/escalations —
// mutating but low-risk), mutation (state changes), destructive (backup,
// restore, upgrade, config apply, quota/migration/preflight actions that
// change platform state irreversibly or with wide blast radius).
// Dry-run: confirm-bool for the §25.2 confirm-preview tools, none otherwise.
//
// spec: §25.12 (x-lenny-category tool taxonomy, x-lenny-dry-run-support).
var classifications = map[string]classification{
	// Identity + caller-self discovery (read-only).
	"admin.me":                  {categoryObservation, dryRunNone},
	"admin.me_authorized_tools": {categoryObservation, dryRunNone},

	// Health and self-health (read-only).
	"admin.health":              {categoryObservation, dryRunNone},
	"admin.health_component":    {categoryObservation, dryRunNone},
	"admin.health_summary":      {categoryObservation, dryRunNone},
	"lenny_ops_health_get":      {categoryObservation, dryRunNone},
	"admin.get_recommendations": {categoryObservation, dryRunNone},

	// Operations inventory (read-only).
	"lenny_operations_list": {categoryObservation, dryRunNone},
	"lenny_operation_get":   {categoryObservation, dryRunNone},

	// Diagnostics (read-only) and the doctor battery (mutation: it applies
	// safe idempotent fixes).
	"lenny_diagnostics_connectivity":    {categoryObservation, dryRunNone},
	"lenny_diagnostics_session":         {categoryObservation, dryRunNone},
	"lenny_diagnostics_pool":            {categoryObservation, dryRunNone},
	"lenny_diagnostics_credential_pool": {categoryObservation, dryRunNone},
	"lenny_doctor_run":                  {categoryMutation, dryRunNone},

	// Runbooks (read-only).
	"lenny_runbooks_list": {categoryObservation, dryRunNone},
	"lenny_runbooks_get":  {categoryObservation, dryRunNone},
	"lenny_runbook_steps": {categoryObservation, dryRunNone},

	// Configuration drift: report (read-only), validate (mutation, no state
	// change but a compute action), snapshot-refresh (mutation, confirm),
	// reconcile (mutation, changes resources toward the snapshot).
	"lenny_drift_report":           {categoryObservation, dryRunNone},
	"lenny_drift_validate":         {categoryMutation, dryRunNone},
	"lenny_drift_snapshot_refresh": {categoryMutation, dryRunConfirmBool},
	"lenny_drift_reconcile":        {categoryMutation, dryRunNone},

	// Remediation locks (coordination — mutating but low-risk).
	"lenny_locks_list":   {categoryObservation, dryRunNone},
	"lenny_lock_get":     {categoryObservation, dryRunNone},
	"lenny_lock_acquire": {categoryCoordination, dryRunNone},
	"lenny_lock_extend":  {categoryCoordination, dryRunNone},
	"lenny_lock_release": {categoryCoordination, dryRunNone},
	"lenny_lock_steal":   {categoryCoordination, dryRunConfirmBool},

	// Escalations (coordination).
	"lenny_escalations_list":  {categoryObservation, dryRunNone},
	"lenny_escalation_create": {categoryCoordination, dryRunNone},
	"lenny_escalation_update": {categoryCoordination, dryRunNone},

	// Webhook event subscriptions (mutation) and the event buffer (read-only).
	"admin.get_event_buffer":          {categoryObservation, dryRunNone},
	"lenny_event_subscriptions_list":  {categoryObservation, dryRunNone},
	"lenny_event_subscription_get":    {categoryObservation, dryRunNone},
	"lenny_event_subscription_create": {categoryMutation, dryRunNone},
	"lenny_event_subscription_update": {categoryMutation, dryRunNone},
	"lenny_event_subscription_delete": {categoryMutation, dryRunNone},

	// Pod-log proxy (read-only).
	"lenny_logs_pod": {categoryObservation, dryRunNone},

	// Backups (read-only reads; destructive writes — data-plane actions).
	"lenny_backups_list":        {categoryObservation, dryRunNone},
	"lenny_backup_get":          {categoryObservation, dryRunNone},
	"lenny_backup_job_get":      {categoryObservation, dryRunNone},
	"lenny_backup_policy_get":   {categoryObservation, dryRunNone},
	"lenny_backup_schedule_get": {categoryObservation, dryRunNone},
	"lenny_backup_create":       {categoryDestructive, dryRunNone},
	"lenny_backup_verify":       {categoryDestructive, dryRunNone},
	"lenny_backup_policy_set":   {categoryDestructive, dryRunNone},
	"lenny_backup_schedule_set": {categoryDestructive, dryRunNone},

	// Restore (read-only checks; destructive executes).
	"lenny_restore_safety_check":       {categoryObservation, dryRunNone},
	"lenny_restore_status":             {categoryObservation, dryRunNone},
	"lenny_restore_preview":            {categoryDestructive, dryRunNone},
	"lenny_restore_execute":            {categoryDestructive, dryRunConfirmBool},
	"lenny_restore_resume":             {categoryDestructive, dryRunNone},
	"lenny_restore_confirm_legal_hold": {categoryDestructive, dryRunNone},

	// Platform config, registry, version, upgrade (read-only reads;
	// destructive writes — platform-wide state changes).
	"admin.get_platform_config":            {categoryObservation, dryRunNone},
	"admin.get_platform_version":           {categoryObservation, dryRunNone},
	"lenny_platform_config_diff":           {categoryObservation, dryRunNone},
	"lenny_platform_registry_get":          {categoryObservation, dryRunNone},
	"lenny_platform_version_full":          {categoryObservation, dryRunNone},
	"lenny_platform_upgrade_check":         {categoryObservation, dryRunNone},
	"lenny_platform_upgrade_status":        {categoryObservation, dryRunNone},
	"admin.migration_status":               {categoryObservation, dryRunNone},
	"lenny_platform_config_apply":          {categoryDestructive, dryRunConfirmBool},
	"admin.apply_deployment_config_change": {categoryDestructive, dryRunNone},
	"lenny_platform_registry_set":          {categoryDestructive, dryRunNone},
	"lenny_platform_upgrade_preflight":     {categoryDestructive, dryRunNone},
	"lenny_platform_upgrade_start":         {categoryDestructive, dryRunConfirmBool},
	"lenny_platform_upgrade_proceed":       {categoryDestructive, dryRunNone},
	"lenny_platform_upgrade_pause":         {categoryDestructive, dryRunNone},
	"lenny_platform_upgrade_rollback":      {categoryDestructive, dryRunConfirmBool},
	"lenny_platform_upgrade_verify":        {categoryDestructive, dryRunNone},
	"admin.migration_down":                 {categoryDestructive, dryRunNone},
	"admin.reconcile_quota":                {categoryMutation, dryRunNone},
	"admin.run_preflight":                  {categoryMutation, dryRunNone},
}

// docOperation is the slice of one OpenAPI operation the generator reads.
type docOperation struct {
	OperationID  string  `json:"operationId"`
	Summary      string  `json:"summary"`
	MCPTool      *string `json:"x-lenny-mcp-tool"`
	Scope        string  `json:"x-lenny-scope"`
	RequiredRole string  `json:"x-lenny-required-role"`
	Category     string  `json:"x-lenny-category"`
}

var httpMethods = map[string]bool{
	"get": true, "post": true, "put": true, "delete": true, "patch": true,
}

// Generate derives the §25.12 operability tool inventory from the OpenAPI
// document bytes, sorted by tool name for a deterministic output. It emits
// one tool per operability endpoint (`x-lenny-category: operability`) that
// carries a non-null `x-lenny-mcp-tool`, resolving each tool's input schema
// from the operation's path parameters and request body.
//
// spec: §25.12 (one MCP tool per operability endpoint from the OpenAPI
// document), §15.1 (x-lenny-* contract).
func Generate(docBytes []byte) ([]GeneratedTool, error) {
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(docBytes, &doc); err != nil {
		return nil, fmt.Errorf("decode openapi document: %w", err)
	}

	var tools []GeneratedTool
	for path, methods := range doc.Paths {
		for method, raw := range methods {
			if !httpMethods[strings.ToLower(method)] {
				continue
			}
			var op docOperation
			if err := json.Unmarshal(raw, &op); err != nil {
				return nil, fmt.Errorf("decode %s %s: %w", method, path, err)
			}
			// §25.12: only operability endpoints with a non-null
			// x-lenny-mcp-tool become management tools. A null tool marks an
			// endpoint the spec deliberately does not expose as a tool.
			if op.Category != operabilityCategory || op.MCPTool == nil || *op.MCPTool == "" {
				continue
			}
			tool, err := buildTool(docBytes, op, strings.ToUpper(method), path)
			if err != nil {
				return nil, err
			}
			tools = append(tools, tool)
		}
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	return tools, nil
}

// buildTool assembles one GeneratedTool from the document operation plus its
// tool-taxonomy classification.
func buildTool(docBytes []byte, op docOperation, method, path string) (GeneratedTool, error) {
	name := *op.MCPTool
	cls, ok := classifications[name]
	if !ok {
		return GeneratedTool{}, fmt.Errorf(
			"operability tool %q (%s %s) has no §25.12 taxonomy classification; add it to mcptoolgen.classifications",
			name, method, path,
		)
	}

	schema, err := inputSchema(docBytes, op.OperationID, path, cls)
	if err != nil {
		return GeneratedTool{}, fmt.Errorf("input schema for %s (%s %s): %w", name, method, path, err)
	}

	return GeneratedTool{
		Name:          name,
		Description:   op.Summary,
		InputSchema:   schema,
		Method:        method,
		Path:          path,
		Category:      cls.category,
		RequiredRole:  op.RequiredRole,
		Scope:         op.Scope,
		DryRunSupport: cls.dryRun,
		ReadOnly:      cls.category == categoryObservation,
	}, nil
}

// inputSchema builds the tool's JSON Schema input. It reuses the §15.2
// mcpschemagen transform to resolve any documented request body and
// parameters, then adds the path-template parameters (which the merged
// operability routes do not carry as an explicit `parameters` array) and
// the optional `operationId` correlation property every §25.12 tool accepts.
func inputSchema(docBytes []byte, operationID, path string, cls classification) (json.RawMessage, error) {
	base, err := mcpschemagen.BuildToolInputSchema(docBytes, operationID, mcpschemagen.Options{})
	if err != nil {
		return nil, err
	}
	var schema map[string]any
	if err := json.Unmarshal(base, &schema); err != nil {
		return nil, fmt.Errorf("decode base schema: %w", err)
	}
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		props = map[string]any{}
		schema["properties"] = props
	}
	required := requiredSet(schema)

	// Path-template parameters ({id}, {name}, {component}, ...) become
	// required string inputs; the operability routes carry them in the path
	// template rather than a document `parameters` array.
	for _, p := range pathParams(path) {
		if _, present := props[p]; !present {
			props[p] = map[string]any{"type": "string", "description": "Path parameter " + p + "."}
		}
		required[p] = true
	}

	// §25.12: every tool accepts an optional operationId for multi-step
	// correlation. It is never required.
	if _, present := props["operationId"]; !present {
		props["operationId"] = map[string]any{
			"type":        "string",
			"format":      "uuid",
			"description": "Optional UUID for multi-step operation correlation.",
		}
	}

	if len(required) > 0 {
		names := make([]string, 0, len(required))
		for n := range required {
			names = append(names, n)
		}
		sort.Strings(names)
		schema["required"] = names
	} else {
		schema["required"] = []string{}
	}
	schema["type"] = "object"

	out, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("marshal schema: %w", err)
	}
	return out, nil
}

// requiredSet extracts the `required` array from a decoded schema as a set.
func requiredSet(schema map[string]any) map[string]bool {
	out := map[string]bool{}
	if reqList, ok := schema["required"].([]any); ok {
		for _, r := range reqList {
			if s, ok := r.(string); ok {
				out[s] = true
			}
		}
	}
	return out
}

// RenderSource renders the committed generated_tools.go source for the given
// tool inventory. The output declares the package-level generatedToolset the
// mcp registry consumes: each tool's static fields are Go literals, and its
// InputSchema is decoded once at package init from a canonical JSON string so
// the file is deterministic and byte-comparable across regenerations.
//
// spec: §25.12 (generated MCP tool inventory).
func RenderSource(tools []GeneratedTool) ([]byte, error) {
	var b strings.Builder
	b.WriteString(header)
	b.WriteString("\n// generatedToolset is the §25.12 operability MCP tool inventory the\n")
	b.WriteString("// build-time openapi-to-mcp step derives from the served OpenAPI document.\n")
	b.WriteString("func generatedToolset() []Tool {\n")
	b.WriteString("\treturn []Tool{\n")
	for _, t := range tools {
		if !json.Valid(t.InputSchema) {
			return nil, fmt.Errorf("tool %q input schema is not valid JSON", t.Name)
		}
		b.WriteString("\t\t{\n")
		fmt.Fprintf(&b, "\t\t\tName:          %q,\n", t.Name)
		fmt.Fprintf(&b, "\t\t\tDescription:   %q,\n", t.Description)
		fmt.Fprintf(&b, "\t\t\tInputSchema:   mustSchema(%s),\n", backtick(string(t.InputSchema)))
		fmt.Fprintf(&b, "\t\t\tMethod:        %q,\n", t.Method)
		fmt.Fprintf(&b, "\t\t\tPath:          %q,\n", t.Path)
		fmt.Fprintf(&b, "\t\t\tCategory:      %q,\n", t.Category)
		fmt.Fprintf(&b, "\t\t\tRequiredRole:  %q,\n", t.RequiredRole)
		fmt.Fprintf(&b, "\t\t\tScope:         %q,\n", t.Scope)
		fmt.Fprintf(&b, "\t\t\tDryRunSupport: %q,\n", t.DryRunSupport)
		fmt.Fprintf(&b, "\t\t\tReadOnly:      %t,\n", t.ReadOnly)
		b.WriteString("\t\t},\n")
	}
	b.WriteString("\t}\n}\n")
	return []byte(b.String()), nil
}

// backtick renders a JSON string as a Go raw string literal, falling back to
// a quoted literal if the JSON contains a backtick (it never does for the
// schemas here, but the fallback keeps the generator total).
func backtick(s string) string {
	if !strings.Contains(s, "`") {
		return "`" + s + "`"
	}
	return fmt.Sprintf("%q", s)
}

const header = `// SPDX-License-Identifier: MIT

// Code generated by openapi-to-mcp; DO NOT EDIT.
//
// The §25.12 ` + "`/mcp/management`" + ` tool inventory below is generated from the
// gateway-served OpenAPI document by the build-time openapi-to-mcp step.
// Regenerate with: go run ./cmd/openapi-to-mcp (or make generate).
//
// spec: §25.12 (build-time openapi-to-mcp generation), §15.1 (OpenAPI
// x-lenny-* contract).

package mcp

import "encoding/json"

// mustSchema decodes a generated JSON Schema string into the map form the
// Tool descriptor carries. The input is generator output, so a decode
// failure is a build-time invariant violation rather than a runtime error.
func mustSchema(raw string) map[string]any {
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		panic("mcp: generated tool schema is not valid JSON: " + err.Error())
	}
	return m
}
`

// pathParams returns the {param} names in a route template, in order.
func pathParams(path string) []string {
	var out []string
	for {
		open := strings.IndexByte(path, '{')
		if open < 0 {
			break
		}
		close := strings.IndexByte(path[open:], '}')
		if close < 0 {
			break
		}
		out = append(out, path[open+1:open+close])
		path = path[open+close+1:]
	}
	return out
}
