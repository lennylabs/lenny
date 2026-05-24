// SPDX-License-Identifier: MIT

package audit

import (
	"strings"
	"testing"
)

// spec167AuditEvents is the §16.7 "Section 25 Audit Events" catalog —
// every audit event type the section enumerates, transcribed in spec
// order. The companion events folded into the spec's prose (e.g.,
// admin.impersonation_ended, the gateway.* Helm-setting companions,
// the legal_hold.* escrow events) are listed explicitly because §16.7
// defines them by name even though they share a bullet.
var spec167AuditEvents = []string{
	"identity.discovered",
	"operations.inventory_queried",
	"token.exchanged",
	"token.revoked",
	"token.exchange_rate_limited",
	"security.audit_write_rejected",
	"admission.circuit_breaker_rejected",
	"delegation.self_recursion_allowed",
	"delegation.cycle_warning",
	"gateway.cycle_detection_mode_changed",
	"gateway.allow_self_recursion_changed",
	"gateway.default_max_depth_changed",
	"circuit_breaker.state_changed",
	"elicitation.content_tamper_detected",
	"tenant.elicitation_content_integrity_changed",
	"platform.elicitation_content_integrity_floor_changed",
	"tenant.elicitation_content_integrity_floor_clamp",
	"quota_failopen_started",
	"admission.circuit_breaker_cache_stale",
	"admin.impersonation_started",
	"admin.impersonation_ended",
	"compliance.profile_decommissioned",
	"deployment.feature_flag_downgrade_acknowledged",
	// §12.5 line 291 — drain-force override audit event.
	"node.drain.forced",
	"remediation.lock_acquired",
	"remediation.lock_extended",
	"remediation.lock_released",
	"remediation.lock_expired",
	"remediation.lock_stolen",
	"remediation.lock_split_brain_detected",
	"remediation.escalation_persisted",
	"ops_event.subscription_created",
	"ops_event.subscription_updated",
	"ops_event.subscription_secret_rotated",
	"ops_event.subscription_deleted",
	"diagnostics.session_diagnosed",
	"diagnostics.pool_diagnosed",
	"diagnostics.credential_pool_diagnosed",
	"diagnostics.connectivity_checked",
	"platform.version_checked",
	"platform.upgrade_started",
	"platform.upgrade_ops_rolled",
	"platform.upgrade_crds_updated",
	"platform.upgrade_schema_migrated",
	"platform.upgrade_gateway_rolled",
	"platform.upgrade_controllers_rolled",
	"platform.upgrade_phase_advanced",
	"platform.upgrade_paused",
	"platform.upgrade_rolled_back",
	"platform.upgrade_completed",
	"platform.upgrade_verified",
	"platform.config_changed",
	"platform.registry_updated",
	"platform.schema_migration_rolled_back",
	"audit.query_executed",
	"audit.chain_integrity_broken_detected",
	"audit.ocsf_retranslate_requested",
	"audit.partition_drop_forced",
	"eventbus.republish_requested",
	"drift.report_generated",
	"drift.reconciliation_started",
	"drift.resource_reconciled",
	"drift.reconciliation_completed",
	"drift.snapshot_refreshed",
	"experiment.status_changed",
	"backup.created",
	"backup.completed",
	"backup.failed",
	"backup.verified",
	"backup.deleted_by_retention",
	"backup.schedule_updated",
	"backup.policy_updated",
	"restore.preview_generated",
	"restore.started",
	"restore.shard_completed",
	"restore.resumed",
	"restore.completed",
	"restore.failed",
	"gdpr.backup_reconcile_completed",
	"gdpr.backup_reconcile_blocked",
	"gdpr.erasure_reconciled_suppressed_by_hold",
	"legal_hold.ledger_confirmed_current_at",
	"artifact.cross_region_replication_verified",
	"artifact_replication.resumed",
	"gdpr.erasure_deadletter_redacted",
	"gdpr.erasure_deadletter_downstream_notified",
	"gdpr.erasure_blocked_by_hold",
	"gdpr.legal_hold_overridden",
	"gdpr.legal_hold_overridden_tenant",
	"legal_hold.escrow_region_resolved",
	"legal_hold.escrowed",
	"legal_hold.escrow_released",
}

func TestCatalogIsNonEmpty(t *testing.T) {
	if len(Catalog()) == 0 {
		t.Fatal("the §16.7 audit-event catalog must not be empty")
	}
}

func TestCatalogHasNoDuplicatesOrEmpties(t *testing.T) {
	seen := map[EventType]bool{}
	for _, et := range Catalog() {
		if strings.TrimSpace(string(et)) == "" {
			t.Error("a catalog entry is empty")
		}
		if seen[et] {
			t.Errorf("duplicate catalog entry: %q", et)
		}
		seen[et] = true
	}
}

// TestCatalogIsCompleteAgainstSpec167 asserts the catalog contains
// every audit event §16.7 enumerates. This is the completion-gate
// check that the catalog is the §16.7 surface in code.
func TestCatalogIsCompleteAgainstSpec167(t *testing.T) {
	got := map[string]bool{}
	for _, et := range Catalog() {
		got[string(et)] = true
	}
	for _, name := range spec167AuditEvents {
		if !got[name] {
			t.Errorf("§16.7 audit event %q is missing from Catalog", name)
		}
	}
}

// TestCatalogHasNoUnspecifiedEvents asserts the catalog does not invent
// audit events §16.7 does not list.
func TestCatalogHasNoUnspecifiedEvents(t *testing.T) {
	allowed := map[string]bool{}
	for _, name := range spec167AuditEvents {
		allowed[name] = true
	}
	for _, et := range Catalog() {
		if !allowed[string(et)] {
			t.Errorf("Catalog event %q is not a §16.7 audit event — the catalog must transcribe §16.7, not invent events", et)
		}
	}
}

func TestIsKnownEventType(t *testing.T) {
	if !IsKnownEventType(EventTokenExchanged) {
		t.Error("a catalogued event type must be reported as known")
	}
	if !IsKnownEventType(EventGDPRLegalHoldOverriddenTenant) {
		t.Error("a catalogued event type must be reported as known")
	}
	if IsKnownEventType("not.a_real_event") {
		t.Error("an uncatalogued event type must not be reported as known")
	}
}

// TestEventTypesAreNonEmptyIdentifiers asserts every §16.7 event type
// is a non-empty lower-case identifier. Almost every §16.7 event is
// dot-namespaced; quota_failopen_started is the one spec exception,
// carried verbatim.
func TestEventTypesAreNonEmptyIdentifiers(t *testing.T) {
	for _, et := range Catalog() {
		s := string(et)
		if s == "" {
			t.Error("an audit event type is empty")
			continue
		}
		if s != strings.ToLower(s) {
			t.Errorf("audit event type %q is not lower-case", et)
		}
	}
}
