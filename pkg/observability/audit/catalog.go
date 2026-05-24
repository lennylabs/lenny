// SPDX-License-Identifier: MIT

// Package audit declares the §16.7 catalog of §25 audit event types as
// a typed surface. §16.7 enumerates the audit event types §25 adds to
// the standard §11.7 audit path; each constant here transcribes one
// entry of that enumeration.
//
// Audit events are distinct from the §16.6 operational events in the
// pkg/gateway/opsevents catalog: an operational event describes a live
// platform state transition, an audit event is an append-only §11.7
// record. Some audit events are additionally routed onto the
// operational event stream (a high-severity auth denial operators want
// to see in real time); §16.7 records that routing per event. This
// package is the canonical type-level enumeration, not the payload
// schema — Section 25 remains the source of truth for payload shapes.
package audit

// EventType is a §16.7 audit event type identifier. The value is the
// audit_log row's event_type field — the same string §16.7 lists in
// backticks (e.g., "token.exchanged", "gdpr.legal_hold_overridden").
type EventType string

// String returns the event type identifier.
func (t EventType) String() string { return string(t) }

// The §16.7 lenny-ops and Token Service audit events grouped by their
// §25 origin. Constants are declared in §16.7 catalog order so the
// file reads as a transcription of the spec section.
const (
	// §25.4 — caller discovery and the lenny-ops service.
	EventIdentityDiscovered         EventType = "identity.discovered"
	EventOperationsInventoryQueried EventType = "operations.inventory_queried"

	// §4.3 / §13.3 — Token Service exchange and revocation.
	EventTokenExchanged           EventType = "token.exchanged"
	EventTokenRevoked             EventType = "token.revoked"
	EventTokenExchangeRateLimited EventType = "token.exchange_rate_limited"

	// §11.7 — platform-tenant audit-write rejection.
	EventSecurityAuditWriteRejected EventType = "security.audit_write_rejected"

	// §11.6 / §4.8 — AdmissionController circuit-breaker gate.
	EventAdmissionCircuitBreakerRejected   EventType = "admission.circuit_breaker_rejected"
	EventAdmissionCircuitBreakerCacheStale EventType = "admission.circuit_breaker_cache_stale"

	// §8.2 — delegation cycle-detection decision matrix.
	EventDelegationSelfRecursionAllowed EventType = "delegation.self_recursion_allowed"
	EventDelegationCycleWarning         EventType = "delegation.cycle_warning"

	// §8.2 / §8.3 — Helm-driven cycle-detection setting changes.
	EventGatewayCycleDetectionModeChanged EventType = "gateway.cycle_detection_mode_changed"
	EventGatewayAllowSelfRecursionChanged EventType = "gateway.allow_self_recursion_changed"
	EventGatewayDefaultMaxDepthChanged    EventType = "gateway.default_max_depth_changed"

	// §11.6 — operator-managed circuit-breaker state changes.
	EventCircuitBreakerStateChanged EventType = "circuit_breaker.state_changed"

	// §9.2 — elicitation content integrity.
	EventElicitationContentTamperDetected                EventType = "elicitation.content_tamper_detected"
	EventTenantElicitationContentIntegrityChanged        EventType = "tenant.elicitation_content_integrity_changed"
	EventPlatformElicitationContentIntegrityFloorChanged EventType = "platform.elicitation_content_integrity_floor_changed"
	EventTenantElicitationContentIntegrityFloorClamp     EventType = "tenant.elicitation_content_integrity_floor_clamp"

	// §12.4 — quota fail-open entry edge.
	EventQuotaFailOpenStarted EventType = "quota_failopen_started"

	// §13.3 — platform-admin impersonation.
	EventAdminImpersonationStarted EventType = "admin.impersonation_started"
	EventAdminImpersonationEnded   EventType = "admin.impersonation_ended"

	// §11.7 — compliance profile downgrade ratchet.
	EventComplianceProfileDecommissioned EventType = "compliance.profile_decommissioned"

	// §17.2 — admission-plane feature-flag downgrade acknowledgement.
	EventDeploymentFeatureFlagDowngradeAcknowledged EventType = "deployment.feature_flag_downgrade_acknowledged"

	// §12.5 line 291 / §16.7 — drain-force override audit. The
	// lenny-drain-readiness webhook emits this event when a node
	// carries the lenny.dev/drain-force: "true" override and the
	// eviction is admitted despite a degraded artifact store.
	EventNodeDrainForced EventType = "node.drain.forced"
)

// The §16.7 / §25.4 lenny-ops remediation-lock and escalation audit
// events.
const (
	EventRemediationLockAcquired           EventType = "remediation.lock_acquired"
	EventRemediationLockExtended           EventType = "remediation.lock_extended"
	EventRemediationLockReleased           EventType = "remediation.lock_released"
	EventRemediationLockExpired            EventType = "remediation.lock_expired"
	EventRemediationLockStolen             EventType = "remediation.lock_stolen"
	EventRemediationLockSplitBrainDetected EventType = "remediation.lock_split_brain_detected"
	EventRemediationEscalationPersisted    EventType = "remediation.escalation_persisted"
)

// The §16.7 / §25.5 operational-event subscription audit events.
const (
	EventOpsEventSubscriptionCreated       EventType = "ops_event.subscription_created"
	EventOpsEventSubscriptionUpdated       EventType = "ops_event.subscription_updated"
	EventOpsEventSubscriptionSecretRotated EventType = "ops_event.subscription_secret_rotated"
	EventOpsEventSubscriptionDeleted       EventType = "ops_event.subscription_deleted"
)

// The §16.7 / §25.6 diagnostic-endpoint audit events.
const (
	EventDiagnosticsSessionDiagnosed        EventType = "diagnostics.session_diagnosed"
	EventDiagnosticsPoolDiagnosed           EventType = "diagnostics.pool_diagnosed"
	EventDiagnosticsCredentialPoolDiagnosed EventType = "diagnostics.credential_pool_diagnosed"
	EventDiagnosticsConnectivityChecked     EventType = "diagnostics.connectivity_checked"
)

// The §16.7 / §25.8 platform-lifecycle-management audit events.
const (
	EventPlatformVersionChecked            EventType = "platform.version_checked"
	EventPlatformUpgradeStarted            EventType = "platform.upgrade_started"
	EventPlatformUpgradeOpsRolled          EventType = "platform.upgrade_ops_rolled"
	EventPlatformUpgradeCrdsUpdated        EventType = "platform.upgrade_crds_updated"
	EventPlatformUpgradeSchemaMigrated     EventType = "platform.upgrade_schema_migrated"
	EventPlatformUpgradeGatewayRolled      EventType = "platform.upgrade_gateway_rolled"
	EventPlatformUpgradeControllersRolled  EventType = "platform.upgrade_controllers_rolled"
	EventPlatformUpgradePhaseAdvanced      EventType = "platform.upgrade_phase_advanced"
	EventPlatformUpgradePaused             EventType = "platform.upgrade_paused"
	EventPlatformUpgradeRolledBack         EventType = "platform.upgrade_rolled_back"
	EventPlatformUpgradeCompleted          EventType = "platform.upgrade_completed"
	EventPlatformUpgradeVerified           EventType = "platform.upgrade_verified"
	EventPlatformConfigChanged             EventType = "platform.config_changed"
	EventPlatformRegistryUpdated           EventType = "platform.registry_updated"
	EventPlatformSchemaMigrationRolledBack EventType = "platform.schema_migration_rolled_back"
)

// The §16.7 / §25.9 audit-log query-API audit events.
const (
	EventAuditQueryExecuted                EventType = "audit.query_executed"
	EventAuditChainIntegrityBrokenDetected EventType = "audit.chain_integrity_broken_detected"
	EventAuditOcsfRetranslateRequested     EventType = "audit.ocsf_retranslate_requested"
	EventAuditPartitionDropForced          EventType = "audit.partition_drop_forced"
	EventEventBusRepublishRequested        EventType = "eventbus.republish_requested"
)

// The §16.7 / §25.10 configuration-drift-detection audit events.
const (
	EventDriftReportGenerated         EventType = "drift.report_generated"
	EventDriftReconciliationStarted   EventType = "drift.reconciliation_started"
	EventDriftResourceReconciled      EventType = "drift.resource_reconciled"
	EventDriftReconciliationCompleted EventType = "drift.reconciliation_completed"
	EventDriftSnapshotRefreshed       EventType = "drift.snapshot_refreshed"
)

// The §16.7 / §10.7 experiment-status audit event.
const (
	EventExperimentStatusChanged EventType = "experiment.status_changed"
)

// The §16.7 / §25.11 backup-and-restore audit events.
const (
	EventBackupCreated            EventType = "backup.created"
	EventBackupCompleted          EventType = "backup.completed"
	EventBackupFailed             EventType = "backup.failed"
	EventBackupVerified           EventType = "backup.verified"
	EventBackupDeletedByRetention EventType = "backup.deleted_by_retention"
	EventBackupScheduleUpdated    EventType = "backup.schedule_updated"
	EventBackupPolicyUpdated      EventType = "backup.policy_updated"
	EventRestorePreviewGenerated  EventType = "restore.preview_generated"
	EventRestoreStarted           EventType = "restore.started"
	EventRestoreShardCompleted    EventType = "restore.shard_completed"
	EventRestoreResumed           EventType = "restore.resumed"
	EventRestoreCompleted         EventType = "restore.completed"
	EventRestoreFailed            EventType = "restore.failed"
)

// The §16.7 / §12.8 post-restore GDPR-reconciler and cross-region
// replication audit events.
const (
	EventGDPRBackupReconcileCompleted           EventType = "gdpr.backup_reconcile_completed"
	EventGDPRBackupReconcileBlocked             EventType = "gdpr.backup_reconcile_blocked"
	EventGDPRErasureReconciledSuppressedByHold  EventType = "gdpr.erasure_reconciled_suppressed_by_hold"
	EventLegalHoldLedgerConfirmedCurrentAt      EventType = "legal_hold.ledger_confirmed_current_at"
	EventArtifactCrossRegionReplicationVerified EventType = "artifact.cross_region_replication_verified"
	EventArtifactReplicationResumed             EventType = "artifact_replication.resumed"
)

// The §16.7 / §12.8 GDPR erasure dead-letter and legal-hold audit
// events.
const (
	EventGDPRErasureDeadletterRedacted           EventType = "gdpr.erasure_deadletter_redacted"
	EventGDPRErasureDeadletterDownstreamNotified EventType = "gdpr.erasure_deadletter_downstream_notified"
	EventGDPRErasureBlockedByHold                EventType = "gdpr.erasure_blocked_by_hold"
	EventGDPRLegalHoldOverridden                 EventType = "gdpr.legal_hold_overridden"
	EventGDPRLegalHoldOverriddenTenant           EventType = "gdpr.legal_hold_overridden_tenant"
	EventLegalHoldEscrowRegionResolved           EventType = "legal_hold.escrow_region_resolved"
	EventLegalHoldEscrowed                       EventType = "legal_hold.escrowed"
	EventLegalHoldEscrowReleased                 EventType = "legal_hold.escrow_released"
)

// catalog is the §16.7 closed enumeration of §25 audit event types.
// The slice is declared in §16.7 catalog order.
var catalog = []EventType{
	EventIdentityDiscovered, EventOperationsInventoryQueried,
	EventTokenExchanged, EventTokenRevoked, EventTokenExchangeRateLimited,
	EventSecurityAuditWriteRejected,
	EventAdmissionCircuitBreakerRejected, EventAdmissionCircuitBreakerCacheStale,
	EventDelegationSelfRecursionAllowed, EventDelegationCycleWarning,
	EventGatewayCycleDetectionModeChanged, EventGatewayAllowSelfRecursionChanged,
	EventGatewayDefaultMaxDepthChanged,
	EventCircuitBreakerStateChanged,
	EventElicitationContentTamperDetected, EventTenantElicitationContentIntegrityChanged,
	EventPlatformElicitationContentIntegrityFloorChanged,
	EventTenantElicitationContentIntegrityFloorClamp,
	EventQuotaFailOpenStarted,
	EventAdminImpersonationStarted, EventAdminImpersonationEnded,
	EventComplianceProfileDecommissioned,
	EventDeploymentFeatureFlagDowngradeAcknowledged,
	EventNodeDrainForced,

	EventRemediationLockAcquired, EventRemediationLockExtended, EventRemediationLockReleased,
	EventRemediationLockExpired, EventRemediationLockStolen, EventRemediationLockSplitBrainDetected,
	EventRemediationEscalationPersisted,

	EventOpsEventSubscriptionCreated, EventOpsEventSubscriptionUpdated,
	EventOpsEventSubscriptionSecretRotated, EventOpsEventSubscriptionDeleted,

	EventDiagnosticsSessionDiagnosed, EventDiagnosticsPoolDiagnosed,
	EventDiagnosticsCredentialPoolDiagnosed, EventDiagnosticsConnectivityChecked,

	EventPlatformVersionChecked, EventPlatformUpgradeStarted, EventPlatformUpgradeOpsRolled,
	EventPlatformUpgradeCrdsUpdated, EventPlatformUpgradeSchemaMigrated, EventPlatformUpgradeGatewayRolled,
	EventPlatformUpgradeControllersRolled, EventPlatformUpgradePhaseAdvanced, EventPlatformUpgradePaused,
	EventPlatformUpgradeRolledBack, EventPlatformUpgradeCompleted, EventPlatformUpgradeVerified,
	EventPlatformConfigChanged, EventPlatformRegistryUpdated, EventPlatformSchemaMigrationRolledBack,

	EventAuditQueryExecuted, EventAuditChainIntegrityBrokenDetected, EventAuditOcsfRetranslateRequested,
	EventAuditPartitionDropForced, EventEventBusRepublishRequested,

	EventDriftReportGenerated, EventDriftReconciliationStarted, EventDriftResourceReconciled,
	EventDriftReconciliationCompleted, EventDriftSnapshotRefreshed,

	EventExperimentStatusChanged,

	EventBackupCreated, EventBackupCompleted, EventBackupFailed, EventBackupVerified,
	EventBackupDeletedByRetention, EventBackupScheduleUpdated, EventBackupPolicyUpdated,
	EventRestorePreviewGenerated, EventRestoreStarted, EventRestoreShardCompleted,
	EventRestoreResumed, EventRestoreCompleted, EventRestoreFailed,
	EventGDPRBackupReconcileCompleted, EventGDPRBackupReconcileBlocked,
	EventGDPRErasureReconciledSuppressedByHold, EventLegalHoldLedgerConfirmedCurrentAt,
	EventArtifactCrossRegionReplicationVerified, EventArtifactReplicationResumed,

	EventGDPRErasureDeadletterRedacted, EventGDPRErasureDeadletterDownstreamNotified,
	EventGDPRErasureBlockedByHold, EventGDPRLegalHoldOverridden,
	EventGDPRLegalHoldOverriddenTenant, EventLegalHoldEscrowRegionResolved,
	EventLegalHoldEscrowed, EventLegalHoldEscrowReleased,
}

// Catalog returns the §16.7 catalog of §25 audit event types. The
// slice is fresh on every call so callers may sort or filter it
// freely.
func Catalog() []EventType {
	return append([]EventType(nil), catalog...)
}

// IsKnownEventType reports whether t is a §16.7 audit event type.
func IsKnownEventType(t EventType) bool {
	for _, v := range catalog {
		if t == v {
			return true
		}
	}
	return false
}
