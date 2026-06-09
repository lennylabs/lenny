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

	// §8.2 / §11.7 line 62 — `delegation.spawned` is recorded when a
	// child session is created via recursive delegation. The §11.7 row
	// carries `parent_session_id`, `child_session_id`, `delegation_depth`,
	// `runtime_ref`, `pool_ref`, `isolation_profile`, and the tuple
	// SIEM consumers use to attribute billing and lineage. spec: §8.2 /
	// §11.7 line 62; F-8.5.8.
	EventDelegationSpawned EventType = "delegation.spawned"

	// §8.2 / §8.3 — Helm-driven cycle-detection setting changes.
	EventGatewayCycleDetectionModeChanged EventType = "gateway.cycle_detection_mode_changed"
	EventGatewayAllowSelfRecursionChanged EventType = "gateway.allow_self_recursion_changed"
	EventGatewayDefaultMaxDepthChanged    EventType = "gateway.default_max_depth_changed"

	// §11.6 — operator-managed circuit-breaker state changes.
	EventCircuitBreakerStateChanged EventType = "circuit_breaker.state_changed"

	// §15.1 lines 818-819 — operator tenant suspend/resume actions.
	EventTenantSuspended EventType = "tenant.suspended"
	EventTenantResumed   EventType = "tenant.resumed"

	// §9.2 — elicitation content integrity.
	EventElicitationContentTamperDetected                EventType = "elicitation.content_tamper_detected"
	EventTenantElicitationContentIntegrityChanged        EventType = "tenant.elicitation_content_integrity_changed"
	EventPlatformElicitationContentIntegrityFloorChanged EventType = "platform.elicitation_content_integrity_floor_changed"
	EventTenantElicitationContentIntegrityFloorClamp     EventType = "tenant.elicitation_content_integrity_floor_clamp"

	// §7.2 / §11.7 / §16.7 — interaction-resolution audit events. Every
	// state-changing user decision (tool-use approve/deny, elicitation
	// respond/dismiss) writes a §11.7 hash-chained audit row so a
	// post-incident investigation can reconstruct who approved or denied
	// which tool call or elicitation. F-7.2.8.
	EventToolUseApproved      EventType = "tool_use.approved"
	EventToolUseDenied        EventType = "tool_use.denied"
	EventElicitationResponded EventType = "elicitation.responded"
	EventElicitationDismissed EventType = "elicitation.dismissed"

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

	// §7.3 retry/resume lifecycle audit events. The catalog already
	// covers the regular session-lifecycle terminals through the
	// session.created/completed/failed/cancelled/expired strings emitted
	// by sessionserver lifecycle.go; the events below cover the §7.3
	// auto-retry, resume, awaiting-client-action, and cascade transitions
	// that sit between create and terminal. F-7.3.25.
	EventSessionResumed                 EventType = "session.resumed"
	EventSessionRetryAttempted          EventType = "session.retry_attempted"
	EventSessionAwaitingActionEntered   EventType = "session.awaiting_action_entered"
	EventSessionExpiredInAwaitingAction EventType = "session.expired_in_awaiting_action"
	EventSessionCascadeApplied          EventType = "session.cascade_applied"
	// EventSessionSetupCommandFailed is the §7.5 / §7.3 line 387
	// non-retryable failure category, emitted by the gateway when a
	// setup command exits non-zero or is killed by the per-command /
	// aggregate cap. The audit Detail carries `cmd`, `exitCode`,
	// `stderr_excerpt`, `index`, and `duration_ms` so an operator can
	// reconstruct what happened without parsing the gRPC error string.
	// spec: §7.5 line 475, §7.3 line 387 — F-7.5.9.
	EventSessionSetupCommandFailed EventType = "session.setup_command_failed"

	// EventSessionForceTerminated is the §24.11 operator force-terminate
	// action: a platform-admin terminates a stuck or unresponsive session
	// through `POST /v1/admin/sessions/{id}/force-terminate`. The Detail
	// carries `operator_sub`, `operator_tenant_id`, `session_id`,
	// `previous_state`, and the optional `reason`. §16.7 does not
	// enumerate this operability action (it is a §24.11 CLI surface, not a
	// §25 lifecycle event), so it is recognized by IsKnownEventType via
	// auxKnownEventTypes but excluded from Catalog(). spec: §24.11 line
	// 136 — F-24.11.3.
	EventSessionForceTerminated EventType = "session.force_terminated"

	// §9.3 connector lifecycle audit events. The §15.1 admin connector
	// CRUD endpoints (`POST /v1/admin/connectors`, `PUT`, `DELETE`) and
	// the §9.3 OAuth-flow endpoints (authorize / callback) emit these
	// strings through the standard hash-chain audit pipeline. The
	// §11.7 audit-log row records who registered, updated, soft-deleted,
	// initiated, or completed a connector OAuth flow against which
	// connector_id; the §16.7 catalog enumerates each so audit-sink
	// validators (IsKnownEventType) do not discard the rows as unknown
	// event types. spec: §9.3 line 116-164 — F-9.3.9.
	EventAdminConnectorCreated                EventType = "admin.connector.created"
	EventAdminConnectorUpdated                EventType = "admin.connector.updated"
	EventAdminConnectorSoftDeleted            EventType = "admin.connector.soft_deleted"
	EventConnectorOAuthAuthorizationInitiated EventType = "connector.oauth.authorization_initiated"
	EventConnectorOAuthCredentialStored       EventType = "connector.oauth.credential_stored"
)

// §15.1 / §24.8 external-protocol adapter registry lifecycle. The admin
// CRUD endpoints at `/v1/admin/external-adapters` and the validate
// registration gate emit these through the §11.7 audit path. §16.7 does
// not enumerate them (it predates the registry-gate audit surface), so
// they are recognized by IsKnownEventType via auxKnownEventTypes but
// excluded from Catalog(). spec: §15 line 1414 (machine-enforceable
// registration gate); §24.8 line 113. F-24.8.2.
const (
	EventAdminExternalAdapterRegistered       EventType = "admin.external_adapter.registered"
	EventAdminExternalAdapterUpdated          EventType = "admin.external_adapter.updated"
	EventAdminExternalAdapterDeleted          EventType = "admin.external_adapter.deleted"
	EventAdminExternalAdapterValidated        EventType = "admin.external_adapter.validated"
	EventAdminExternalAdapterValidationFailed EventType = "admin.external_adapter.validation_failed"
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

	// §25.6 `fix=true` auto-remediation lifecycle. The doctor
	// orchestrator emits these around a `POST /v1/admin/diagnostics/run?-
	// fix=true` run: one fix_started/fix_completed pair bounds the run,
	// and one fix_applied / fix_skipped / fix_failed per finding. §16.7
	// line 685 enumerates only the four read-side diagnostics events, so
	// these are recognized by IsKnownEventType (see auxKnownEventTypes)
	// but excluded from Catalog(). spec: §25.6 lines 2975-2982 — F-25.6.2.
	EventDiagnosticsFixStarted   EventType = "diagnostics.fix_started"
	EventDiagnosticsFixApplied   EventType = "diagnostics.fix_applied"
	EventDiagnosticsFixSkipped   EventType = "diagnostics.fix_skipped"
	EventDiagnosticsFixFailed    EventType = "diagnostics.fix_failed"
	EventDiagnosticsFixCompleted EventType = "diagnostics.fix_completed"
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
	// EventDataResidencyViolationAttempt is the §12.8 cross-border
	// violation event emitted on a fail-closed region-resolution abort.
	// The operation field distinguishes the surface: "write" (runtime
	// StorageRouter), "backup" (BACKUP_REGION_UNRESOLVABLE backup
	// dispatch), "artifact_replication" (ArtifactStore replication
	// preflight), "legal_hold_escrow" (Phase 3.5 escrow), and
	// "platform_audit_write" (CMP-058). It shares the
	// lenny_data_residency_violation_total counter across surfaces.
	// spec: §12.8 lines 921-923, line 936.
	EventDataResidencyViolationAttempt EventType = "DataResidencyViolationAttempt"
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
	// §12.8 line 739 — emitted by the legal-hold reconciler when a
	// session under legal_hold=true is observed with a checkpoint
	// sequence gap (one or more checkpoints already rotated).
	EventLegalHoldCheckpointGapDetected EventType = "legal_hold.checkpoint_gap_detected"
)

// §11.7-path audit event types that are written through the standard
// hash-chained audit log but are not §16.7 / §25-additions. §16.7
// enumerates only the events §25 adds on top of the standard §11.7
// path, so these core §11.7 events are deliberately absent from
// catalog (and from Catalog()) yet must still be recognized by
// IsKnownEventType so audit-sink validators do not discard the rows as
// unknown event types.
const (
	// EventInterceptorRejected is the §4.8 interceptor-chain REJECT
	// audit event. §11.7 line 331 names it in the per-tenant
	// audit-write traffic list ("session.created, token.exchanged,
	// interceptor.rejected"); §4.8 / §16.7 mention it only to contrast
	// it with admission.circuit_breaker_rejected (the pre-chain gate,
	// which is not an interceptor). It is emitted in production by
	// pkg/gateway/policy.RecordRejection on every chain REJECT.
	// spec: §11.7 line 331; §16.7 line 669 (contrast). F-11.7.18.
	EventInterceptorRejected EventType = "interceptor.rejected"

	// EventInterceptorFailPolicyWeakened is the §4.8 line 1034 audit
	// event emitted when an admin flips an external interceptor's
	// failPolicy from fail-closed to fail-open. It carries interceptor_ref,
	// old_fail_policy, new_fail_policy, the server-minted transition_ts,
	// the cooldown_seconds in force at the transition, and the
	// affected_policy_count / affected_policy_names of the active
	// DelegationPolicy resources whose contentPolicy.interceptorRef names
	// the interceptor. It is the producer the §8.3 rule-5 weakening
	// cooldown is paired with. spec: §4.8 line 1034; §8.3 line 224
	// (SEC-013). F-4.8.17.
	EventInterceptorFailPolicyWeakened EventType = "interceptor.fail_policy_weakened"

	// EventInterceptorFailPolicyStrengthened is the §4.8 line 1034
	// audit event emitted on the reverse fail-open to fail-closed
	// transition, with the same fields. The strengthen takes effect
	// immediately and arms no cooldown. spec: §4.8 line 1034. F-4.8.17.
	EventInterceptorFailPolicyStrengthened EventType = "interceptor.fail_policy_strengthened"

	// EventInterceptorWeakeningCooldownActive is the §8.3 line 218
	// audit event emitted once per cooldown-window entry (not per
	// rejected request) when a fail-closed to fail-open transition arms
	// the weakening cooldown. It carries interceptor_ref, transition_ts,
	// cooldown_seconds, and the affected_policy_count /
	// affected_policy_names. spec: §8.3 line 218 (SEC-013). F-4.8.17.
	EventInterceptorWeakeningCooldownActive EventType = "interceptor.weakening_cooldown_active"

	// EventGDPRErasureJobRetried is the §24.12 line 143 / §12.8 line 766
	// audit row for an operator retry of a failed erasure job. Emitted by
	// pkg/gateway/admin handleRetryErasureJob.
	EventGDPRErasureJobRetried EventType = "gdpr.erasure_job_retried"

	// EventGDPRProcessingRestrictionCleared is the §24.12 line 144 / §12.8
	// line 764 audit row for an operator manually clearing a user's GDPR
	// Article 18 processing-restriction flag. Emitted by
	// pkg/gateway/admin handleClearErasureRestriction.
	EventGDPRProcessingRestrictionCleared EventType = "gdpr.processing_restriction_cleared"
)

// catalog is the §16.7 closed enumeration of §25 audit event types.
// The slice is declared in §16.7 catalog order.
var catalog = []EventType{
	EventIdentityDiscovered, EventOperationsInventoryQueried,
	EventTokenExchanged, EventTokenRevoked, EventTokenExchangeRateLimited,
	EventSecurityAuditWriteRejected,
	EventAdmissionCircuitBreakerRejected, EventAdmissionCircuitBreakerCacheStale,
	EventDelegationSelfRecursionAllowed, EventDelegationCycleWarning,
	EventDelegationSpawned,
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

	// §7.3 retry/resume lifecycle. F-7.3.25.
	EventSessionResumed, EventSessionRetryAttempted, EventSessionAwaitingActionEntered,
	EventSessionExpiredInAwaitingAction, EventSessionCascadeApplied,

	// §9.3 connector lifecycle and OAuth flow. F-9.3.9.
	EventAdminConnectorCreated, EventAdminConnectorUpdated, EventAdminConnectorSoftDeleted,
	EventConnectorOAuthAuthorizationInitiated, EventConnectorOAuthCredentialStored,

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
	EventLegalHoldCheckpointGapDetected,
}

// auxKnownEventTypes are §11.7-path audit event types emitted through
// the standard hash-chained audit log that are not §16.7 / §25
// additions. They are recognized by IsKnownEventType so audit-sink
// validators accept the rows, but are intentionally excluded from
// Catalog() because Catalog() transcribes only the §16.7 enumeration.
// spec: §11.7 line 331. F-11.7.18.
var auxKnownEventTypes = []EventType{
	EventInterceptorRejected,
	// §4.8 line 1034 / §8.3 SEC-013 interceptor failPolicy-transition
	// events. Emitted through the §11.7 audit path by the admin
	// interceptor-registry PUT handler; §16.7 does not enumerate them, so
	// they are recognized by IsKnownEventType (audit-sink validators must
	// not discard them) yet excluded from Catalog(). F-4.8.17.
	EventInterceptorFailPolicyWeakened,
	EventInterceptorFailPolicyStrengthened,
	EventInterceptorWeakeningCooldownActive,
	// §24.12 / §12.8 erasure-job operator actions. §16.7 enumerates the
	// gdpr.* erasure-receipt and legal-hold rows but not these two
	// operator-recovery events, so they are recognized by IsKnownEventType
	// (audit-sink validators must not discard them) yet excluded from
	// Catalog(), which transcribes only the §16.7 enumeration. F-24.12.4.
	EventGDPRErasureJobRetried,
	EventGDPRProcessingRestrictionCleared,
	// §24.11 session force-terminate operator action. §16.7 enumerates the
	// session lifecycle terminals but not this operator-driven force, so it
	// is recognized by IsKnownEventType yet excluded from Catalog(). F-24.11.3.
	EventSessionForceTerminated,
	// §15.1 lines 818-819 tenant suspend/resume operator actions. §16.7
	// does not enumerate them, so they are recognized by IsKnownEventType
	// (audit-sink validators must not discard them) yet excluded from
	// Catalog(), which transcribes only the §16.7 enumeration. F-15.1.3.
	EventTenantSuspended,
	EventTenantResumed,
	// §15.1 / §24.8 external-adapter registry lifecycle. Emitted through the
	// §11.7 audit path by the admin CRUD endpoints and the validate gate;
	// §16.7 does not enumerate them, so they are recognized by
	// IsKnownEventType yet excluded from Catalog(). F-24.8.2.
	EventAdminExternalAdapterRegistered,
	EventAdminExternalAdapterUpdated,
	EventAdminExternalAdapterDeleted,
	EventAdminExternalAdapterValidated,
	EventAdminExternalAdapterValidationFailed,
	// §25.6 doctor `fix=true` auto-remediation lifecycle. §16.7 line 685
	// enumerates only the read-side diagnostics events, so these are
	// recognized by IsKnownEventType yet excluded from Catalog(). F-25.6.2.
	EventDiagnosticsFixStarted,
	EventDiagnosticsFixApplied,
	EventDiagnosticsFixSkipped,
	EventDiagnosticsFixFailed,
	EventDiagnosticsFixCompleted,
}

// Catalog returns the §16.7 catalog of §25 audit event types. The
// slice is fresh on every call so callers may sort or filter it
// freely. §11.7-path core events (see auxKnownEventTypes) are not
// included — Catalog() is the §16.7 / §25-additions surface only.
func Catalog() []EventType {
	return append([]EventType(nil), catalog...)
}

// IsKnownEventType reports whether t is a recognized audit event type:
// either a §16.7 / §25-additions catalogue entry or a §11.7-path core
// event (auxKnownEventTypes). It is the discard gate for audit-sink
// validators, so it must accept every event the gateway emits through
// the standard §11.7 audit path, not only the §16.7 subset.
// spec: §11.7 line 331. F-11.7.18.
func IsKnownEventType(t EventType) bool {
	for _, v := range catalog {
		if t == v {
			return true
		}
	}
	for _, v := range auxKnownEventTypes {
		if t == v {
			return true
		}
	}
	return false
}

// operationalStreamEscalations is the §16.7 subset of audit event types
// that are additionally routed onto the §25.5 operational event stream,
// beyond the §11.7 append-only audit log. §16.7 records this routing per
// event ("Written through the standard append-only audit path ... and
// also routed to the operational event stream so on-call operators can
// ... in real time"). When such an event is emitted on the stream the
// envelope is CloudEvents with datacontenttype application/ocsf+json and
// the OCSF v1.1.0 record in the data field. spec: §16.7 lines 661, 670,
// 674, 682, 687; §25.5 line 2556.
var operationalStreamEscalations = map[EventType]bool{
	EventDelegationSelfRecursionAllowed:             true, // spec: §16.7 line 670
	EventElicitationContentTamperDetected:           true, // spec: §16.7 line 674
	EventDeploymentFeatureFlagDowngradeAcknowledged: true, // spec: §16.7 line 682
	EventAuditOcsfRetranslateRequested:              true, // spec: §16.7 line 687
	EventAuditPartitionDropForced:                   true, // spec: §16.7 line 687
	EventEventBusRepublishRequested:                 true, // spec: §16.7 line 687
}

// EscalatesToOperationalStream reports whether an audit event of type t
// is routed onto the §25.5 operational event stream in addition to the
// §11.7 audit log. The escalation set is the §16.7 events flagged for
// real-time operator visibility (self-recursion admission, elicitation
// content tampering, feature-flag downgrade acknowledgement, and the
// §25.9 audit-maintenance operations). spec: §16.7 line 661.
func EscalatesToOperationalStream(t EventType) bool {
	return operationalStreamEscalations[t]
}
