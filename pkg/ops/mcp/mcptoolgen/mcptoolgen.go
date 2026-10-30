// SPDX-License-Identifier: MIT

// Package mcptoolgen derives the §25.12 `/mcp/management` tool inventory
// from the gateway-served OpenAPI document.
//
// §25.12 mandates the inventory be generated rather than hand-maintained:
// "a single build-time `openapi-to-mcp` step produces the tool inventory
// from the OpenAPI" and "every admin-API endpoint with documented RBAC
// becomes an MCP tool automatically via the build-time OpenAPI → MCP
// generation". §25.12 further requires the inventory to cover the whole
// admin surface: "Every admin-API endpoint with documented RBAC is exposed
// as an MCP tool -- not only the operability endpoints." This package is the
// transform behind the `openapi-to-mcp` command: it reads
// `openapi.Document()`, emits one tool per documented endpoint that carries
// a non-null `x-lenny-mcp-tool` (every admin-API and lenny-ops operability
// route, not the operability subset alone), and resolves each tool's wire
// fields (name, method, path, scope, required role) from the document plus
// its input schema from the operation's path parameters and request body
// (via the shared mcpschemagen machinery the §15.2 gateway generator
// reuses).
//
// The §25.12 tool-taxonomy classification (`x-lenny-category`:
// observation | coordination | mutation | destructive, and
// `x-lenny-dry-run-support`: confirm-bool | none) is not carried on the
// served endpoint document in a form the generator can read directly: that
// document's `x-lenny-category` records the endpoint *domain*
// (`operability`, `tenant-management`, `security`, …) rather than the tool
// taxonomy the §25.12 MCP descriptor reports. The classification therefore
// lives in this generator as an explicit table keyed by tool name, so a
// destructive tool is classified deterministically rather than guessed from
// the HTTP method (a POST is coordination for a lock but destructive for a
// restore, and a DELETE is destructive for a tenant but coordination for a
// lock release). The table is complete for the whole documented tool
// inventory: a tool the document gains without a classification entry fails
// generation (fail-closed), so a new admin-API tool cannot silently ship
// misclassified or, worse, default into a non-destructive category the
// capability filter would expose to a nonDestructive-scoped agent.
//
// spec: §25.12 (build-time openapi-to-mcp generation over every admin-API
// endpoint, tool taxonomy), §15.1 (OpenAPI x-lenny-* extension contract,
// every admin-API endpoint MUST be an MCP tool on /mcp/management), §18
// (Phase 13 openapi-to-mcp generator over the complete document).
package mcptoolgen

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcpschemagen"
)

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

// classifications maps every documented tool name to its §25.12 tool
// taxonomy. It is the explicit, reviewed classification the served endpoint
// document cannot carry directly (its `x-lenny-category` is the endpoint
// domain, not the taxonomy). The table covers the whole documented tool
// inventory (§25.12 "Every admin-API endpoint with documented RBAC is
// exposed as an MCP tool -- not only the operability endpoints"): every
// gateway admin-API tool and every lenny-ops operability tool. Generation
// fails when the document names a tool absent from this table, so a newly
// documented tool must be classified before it ships, and a new destructive
// tool cannot silently default into a non-destructive category the §25.12
// nonDestructive capability filter would expose.
//
// Categories: observation (read-only, every GET), coordination
// (locks/escalations — mutating but low-risk), mutation (state changes),
// destructive (backup, restore, upgrade, config/registry apply, deletes,
// force-*, erase, credential/token/CA revocation, migration/partition and
// pool-upgrade actions that change platform state irreversibly or with wide
// blast radius). Dry-run: confirm-bool for the §25.2 confirm-preview tools,
// none otherwise.
//
// spec: §25.12 (x-lenny-category tool taxonomy, x-lenny-dry-run-support,
// every admin-API endpoint exposed as a tool), §15.1 (x-lenny-* contract).
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

	// Tenant management (§25.12 admin-API): tenants, custom roles, RBAC,
	// compliance, environments, experiments.
	"admin.create_custom_role":                 {categoryMutation, dryRunNone},
	"admin.create_tenant":                      {categoryMutation, dryRunNone},
	"admin.decommission_compliance_profile":    {categoryDestructive, dryRunNone},
	"admin.delete_custom_role":                 {categoryDestructive, dryRunNone},
	"admin.delete_runtime_capability_override": {categoryDestructive, dryRunNone},
	"admin.environment_usage":                  {categoryObservation, dryRunNone},
	"admin.force_delete_tenant":                {categoryDestructive, dryRunConfirmBool},
	"admin.get_custom_role":                    {categoryObservation, dryRunNone},
	"admin.get_elicitation_content_integrity":  {categoryObservation, dryRunNone},
	"admin.get_rbac_config":                    {categoryObservation, dryRunNone},
	"admin.get_runtime_capability_override":    {categoryObservation, dryRunNone},
	"admin.get_tenant":                         {categoryObservation, dryRunNone},
	"admin.list_custom_roles":                  {categoryObservation, dryRunNone},
	"admin.list_runtime_capability_overrides":  {categoryObservation, dryRunNone},
	"admin.list_tenants":                       {categoryObservation, dryRunNone},
	"admin.resume_tenant":                      {categoryMutation, dryRunNone},
	"admin.set_elicitation_content_integrity":  {categoryMutation, dryRunNone},
	"admin.set_runtime_capability_override":    {categoryMutation, dryRunNone},
	"admin.soft_delete_tenant":                 {categoryDestructive, dryRunConfirmBool},
	"admin.suspend_tenant":                     {categoryMutation, dryRunNone},
	"admin.tenant_access_report":               {categoryObservation, dryRunNone},
	"admin.update_custom_role":                 {categoryMutation, dryRunNone},
	"admin.update_rbac_config":                 {categoryMutation, dryRunNone},
	"admin.update_tenant":                      {categoryMutation, dryRunNone},

	// User management (§25.12 admin-API): users, erasure jobs, admin-token
	// rotation.
	"admin.assign_tenant_user_role": {categoryMutation, dryRunNone},
	"admin.create_user":             {categoryMutation, dryRunNone},
	"admin.erase_user":              {categoryDestructive, dryRunConfirmBool},
	"admin.get_erasure_job":         {categoryObservation, dryRunNone},
	"admin.get_user":                {categoryObservation, dryRunNone},
	"admin.invalidate_user":         {categoryMutation, dryRunNone},
	"admin.list_tenant_users":       {categoryObservation, dryRunNone},
	"admin.list_users":              {categoryObservation, dryRunNone},
	"admin.remove_tenant_user_role": {categoryDestructive, dryRunNone},
	"admin.soft_delete_user":        {categoryDestructive, dryRunConfirmBool},
	"admin.update_user":             {categoryMutation, dryRunNone},

	// Runtime management (§25.12 admin-API): runtimes, environments,
	// experiments, card regeneration.
	"admin.create_environment":           {categoryMutation, dryRunNone},
	"admin.create_experiment":            {categoryMutation, dryRunNone},
	"admin.create_runtime":               {categoryMutation, dryRunNone},
	"admin.delete_environment":           {categoryDestructive, dryRunNone},
	"admin.delete_experiment":            {categoryDestructive, dryRunNone},
	"admin.environment_runtime_exposure": {categoryObservation, dryRunNone},
	"admin.get_environment":              {categoryObservation, dryRunNone},
	"admin.get_experiment":               {categoryObservation, dryRunNone},
	"admin.get_experiment_results":       {categoryObservation, dryRunNone},
	"admin.get_runtime":                  {categoryObservation, dryRunNone},
	"admin.grant_runtime_access":         {categoryMutation, dryRunNone},
	"admin.list_environments":            {categoryObservation, dryRunNone},
	"admin.list_experiments":             {categoryObservation, dryRunNone},
	"admin.list_runtime_access":          {categoryObservation, dryRunNone},
	"admin.list_runtimes":                {categoryObservation, dryRunNone},
	"admin.patch_experiment":             {categoryMutation, dryRunNone},
	"admin.regenerate_runtime_cards":     {categoryMutation, dryRunNone},
	"admin.revoke_runtime_access":        {categoryDestructive, dryRunNone},
	"admin.soft_delete_runtime":          {categoryDestructive, dryRunConfirmBool},
	"admin.update_environment":           {categoryMutation, dryRunNone},
	"admin.update_experiment":            {categoryMutation, dryRunNone},
	"admin.update_runtime":               {categoryMutation, dryRunNone},

	// Warm-pool and credential-pool management (§25.12 admin-API).
	"admin.clear_pool_bootstrap_override": {categoryDestructive, dryRunNone},
	"admin.create_pool":                   {categoryMutation, dryRunNone},
	"admin.drain_pool":                    {categoryDestructive, dryRunNone},
	"admin.get_pool":                      {categoryObservation, dryRunNone},
	"admin.get_pool_sync_status":          {categoryObservation, dryRunNone},
	"admin.get_pool_upgrade_status":       {categoryObservation, dryRunNone},
	"admin.grant_pool_access":             {categoryMutation, dryRunNone},
	"admin.list_pool_access":              {categoryObservation, dryRunNone},
	"admin.list_pools":                    {categoryObservation, dryRunNone},
	"admin.pause_pool_upgrade":            {categoryDestructive, dryRunNone},
	"admin.proceed_pool_upgrade":          {categoryDestructive, dryRunNone},
	"admin.resume_pool_reconciliation":    {categoryMutation, dryRunNone},
	"admin.resume_pool_upgrade":           {categoryDestructive, dryRunNone},
	"admin.revoke_pool_access":            {categoryDestructive, dryRunNone},
	"admin.rollback_pool_upgrade":         {categoryDestructive, dryRunNone},
	"admin.set_pool_warm_count":           {categoryMutation, dryRunNone},
	"admin.soft_delete_pool":              {categoryDestructive, dryRunConfirmBool},
	"admin.start_pool_upgrade":            {categoryDestructive, dryRunNone},
	"admin.update_pool":                   {categoryMutation, dryRunNone},
	"admin.update_pool_circuit_breaker":   {categoryMutation, dryRunNone},

	// Policy, interceptor, delegation, circuit-breaker, and credential-pool
	// policy management (§25.12 admin-API).
	"admin.add_credential_to_pool":    {categoryMutation, dryRunNone},
	"admin.close_circuit_breaker":     {categoryMutation, dryRunNone},
	"admin.create_credential_pool":    {categoryMutation, dryRunNone},
	"admin.create_delegation_policy":  {categoryMutation, dryRunNone},
	"admin.create_interceptor":        {categoryMutation, dryRunNone},
	"admin.delete_credential_pool":    {categoryDestructive, dryRunConfirmBool},
	"admin.delete_delegation_policy":  {categoryDestructive, dryRunNone},
	"admin.delete_interceptor":        {categoryDestructive, dryRunNone},
	"admin.get_circuit_breaker":       {categoryObservation, dryRunNone},
	"admin.get_credential_pool":       {categoryObservation, dryRunNone},
	"admin.get_delegation_policy":     {categoryObservation, dryRunNone},
	"admin.get_interceptor":           {categoryObservation, dryRunNone},
	"admin.list_circuit_breakers":     {categoryObservation, dryRunNone},
	"admin.list_credential_pools":     {categoryObservation, dryRunNone},
	"admin.list_delegation_policies":  {categoryObservation, dryRunNone},
	"admin.list_interceptors":         {categoryObservation, dryRunNone},
	"admin.open_circuit_breaker":      {categoryDestructive, dryRunNone},
	"admin.re_enable_pool_credential": {categoryMutation, dryRunNone},
	"admin.remove_pool_credential":    {categoryDestructive, dryRunNone},
	"admin.revoke_credential_pool":    {categoryDestructive, dryRunNone},
	"admin.revoke_pool_credential":    {categoryDestructive, dryRunNone},
	"admin.update_credential_pool":    {categoryMutation, dryRunNone},
	"admin.update_delegation_policy":  {categoryMutation, dryRunNone},
	"admin.update_interceptor":        {categoryMutation, dryRunNone},
	"admin.update_pool_credential":    {categoryMutation, dryRunNone},

	// Security-critical admin-API (§25.12): impersonation, CA rotation,
	// credential rekey, legal hold, token/credential revocation, billing
	// corrections, erasure, artifact replication.
	"admin.approve_billing_correction":           {categoryMutation, dryRunNone},
	"admin.begin_ca_rotation":                    {categoryDestructive, dryRunNone},
	"admin.clear_erasure_processing_restriction": {categoryMutation, dryRunNone},
	"admin.clear_extension_denial":               {categoryDestructive, dryRunNone},
	"admin.create_billing_correction":            {categoryMutation, dryRunNone},
	"admin.credential_rekey":                     {categoryDestructive, dryRunNone},
	"admin.end_impersonation":                    {categoryDestructive, dryRunNone},
	"admin.force_terminate_session":              {categoryDestructive, dryRunNone},
	"admin.get_artifact_replication_status":      {categoryObservation, dryRunNone},
	"admin.get_billing_correction":               {categoryObservation, dryRunNone},
	"admin.get_ca_rotation":                      {categoryObservation, dryRunNone},
	"admin.get_credential_rekey":                 {categoryObservation, dryRunNone},
	"admin.get_session":                          {categoryObservation, dryRunNone},
	"admin.list_billing_corrections":             {categoryObservation, dryRunNone},
	"admin.list_impersonation":                   {categoryObservation, dryRunNone},
	"admin.list_legal_holds":                     {categoryObservation, dryRunNone},
	"admin.promote_ca_rotation":                  {categoryDestructive, dryRunNone},
	"admin.reject_billing_correction":            {categoryMutation, dryRunNone},
	"admin.resume_artifact_replication":          {categoryMutation, dryRunNone},
	"admin.retire_ca_rotation":                   {categoryDestructive, dryRunNone},
	"admin.retry_erasure_job":                    {categoryMutation, dryRunNone},
	"admin.revoke_token":                         {categoryDestructive, dryRunNone},
	"admin.rotate_erasure_salt":                  {categoryDestructive, dryRunNone},
	"admin.rotate_token":                         {categoryMutation, dryRunNone},
	"admin.set_legal_hold":                       {categoryDestructive, dryRunNone},
	"admin.start_impersonation":                  {categoryMutation, dryRunNone},

	// Connector management (§25.12 admin-API).
	"admin.authorize_connector_oauth": {categoryMutation, dryRunNone},
	"admin.connector_oauth_callback":  {categoryObservation, dryRunNone},
	"admin.create_connector":          {categoryMutation, dryRunNone},
	"admin.get_connector":             {categoryObservation, dryRunNone},
	"admin.list_connectors":           {categoryObservation, dryRunNone},
	"admin.refresh_connector":         {categoryMutation, dryRunNone},
	"admin.soft_delete_connector":     {categoryDestructive, dryRunConfirmBool},
	"admin.test_connector":            {categoryMutation, dryRunNone},
	"admin.update_connector":          {categoryMutation, dryRunNone},

	// External adapter management (§25.12 admin-API).
	"admin.delete_external_adapter":   {categoryDestructive, dryRunNone},
	"admin.get_external_adapter":      {categoryObservation, dryRunNone},
	"admin.list_external_adapters":    {categoryObservation, dryRunNone},
	"admin.register_external_adapter": {categoryMutation, dryRunNone},
	"admin.update_external_adapter":   {categoryMutation, dryRunNone},
	"admin.validate_external_adapter": {categoryMutation, dryRunNone},

	// Audit administration (§25.12 admin-API): event lookup, republish,
	// retranslate, partition drop.
	"admin.drop_audit_partition":    {categoryDestructive, dryRunNone},
	"admin.get_audit_event":         {categoryObservation, dryRunNone},
	"admin.list_audit_events":       {categoryObservation, dryRunNone},
	"admin.republish_audit_event":   {categoryMutation, dryRunNone},
	"admin.retranslate_audit_event": {categoryMutation, dryRunNone},
	"admin.summarize_audit_events":  {categoryObservation, dryRunNone},

	// Platform bootstrap (§25.12 admin-API).
	"admin.bootstrap": {categoryMutation, dryRunNone},
}

// docOperation is the slice of one OpenAPI operation the generator reads.
// x-lenny-category is deliberately not read: the served document records the
// endpoint domain there, not the §25.12 tool taxonomy the descriptor needs,
// so the taxonomy comes from the classifications table keyed by tool name.
type docOperation struct {
	OperationID  string  `json:"operationId"`
	Summary      string  `json:"summary"`
	MCPTool      *string `json:"x-lenny-mcp-tool"`
	Scope        string  `json:"x-lenny-scope"`
	RequiredRole string  `json:"x-lenny-required-role"`
}

var httpMethods = map[string]bool{
	"get": true, "post": true, "put": true, "delete": true, "patch": true,
}

// Generate derives the §25.12 management tool inventory from the OpenAPI
// document bytes, sorted by tool name for a deterministic output. It emits
// one tool per documented endpoint (every admin-API endpoint and every
// lenny-ops operability route, not the operability subset alone) that
// carries a non-null `x-lenny-mcp-tool`, resolving each tool's input schema
// from the operation's path parameters and request body.
//
// spec: §25.12 (one MCP tool per admin-API endpoint from the OpenAPI
// document, "not only the operability endpoints"), §15.1 (x-lenny-*
// contract; every admin-API endpoint MUST be an MCP tool on /mcp/management).
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
			// §25.12/§15.1: every documented admin-API endpoint with a
			// non-null x-lenny-mcp-tool becomes a management tool, not only
			// the operability endpoints ("Every admin-API endpoint with
			// documented RBAC is exposed as an MCP tool -- not only the
			// operability endpoints", spec/25 §25.12). A null tool marks an
			// endpoint the spec deliberately does not expose as a tool.
			if op.MCPTool == nil || *op.MCPTool == "" {
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
			"admin-API tool %q (%s %s) has no §25.12 taxonomy classification; add it to mcptoolgen.classifications",
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
