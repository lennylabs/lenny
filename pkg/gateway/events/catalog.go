// SPDX-License-Identifier: MIT

package events

import "strings"

// EventType is a §16.6 operational-event short name. The CloudEvents
// `type` attribute of an emitted event is "dev.lenny." + EventType.
type EventType string

// CloudEventsType returns the full CloudEvents `type` for the short
// name — the value the OperationalEvent.Type field carries.
func (t EventType) CloudEventsType() string { return cloudEventsPrefix + string(t) }

// cloudEventsPrefix is the §16.6 CloudEvents type prefix.
const cloudEventsPrefix = "dev.lenny."

// The §16.6 gateway-emitted operational-event short names.
const (
	EventAlertFired               EventType = "alert_fired"
	EventAlertResolved            EventType = "alert_resolved"
	EventUpgradeProgressed        EventType = "upgrade_progressed"
	EventPoolStateChanged         EventType = "pool_state_changed"
	EventCircuitBreakerOpened     EventType = "circuit_breaker_opened"
	EventCircuitBreakerClosed     EventType = "circuit_breaker_closed"
	EventCredentialRotated        EventType = "credential_rotated"
	EventCredentialPoolExhausted  EventType = "credential_pool_exhausted"
	EventSessionCompleted         EventType = "session_completed"
	EventSessionFailed            EventType = "session_failed"
	EventSessionTerminated        EventType = "session_terminated"
	EventSessionCancelled         EventType = "session_cancelled"
	EventSessionExpired           EventType = "session_expired"
	EventSessionAwaitingAction    EventType = "session_awaiting_action"
	EventDelegationCompleted      EventType = "delegation_completed"
	EventBackupCompleted          EventType = "backup_completed"
	EventBackupFailed             EventType = "backup_failed"
	EventPlatformUpgradeAvailable EventType = "platform_upgrade_available"
	EventHealthStatusChanged      EventType = "health_status_changed"
)

// The §16.6 ExperimentRouter operational-event short names.
const (
	EventExperimentUnknownVariantFromProvider EventType = "experiment.unknown_variant_from_provider"
	EventExperimentUnknownExternalID          EventType = "experiment.unknown_external_id"
	EventExperimentTargetingFailed            EventType = "experiment.targeting_failed"
	EventExperimentMultiEligibleSkipped       EventType = "experiment.multi_eligible_skipped"
	EventExperimentIsolationMismatch          EventType = "experiment.isolation_mismatch"
	EventExperimentVariantWeakerThanFloor     EventType = "experiment.variant_weaker_than_tenant_floor"
)

// The §16.6 §14 WorkspacePlan warning event short name. The
// CloudEvents type is `dev.lenny.workspace_plan_warning`; the payload
// carries the per-warning structured fields per §14 lines 100, 334,
// 338. spec: §14 line 100; §14 line 334; §14 line 338. F-14.1.17.
const EventWorkspacePlanWarning EventType = "workspace_plan_warning"

// gatewayEventCatalog is the §16.6 closed enumeration of the
// operational-event types the gateway emits. EventWorkspacePlanWarning
// is deliberately omitted: §14 lines 100/334/338 describe per-session
// warnings, not §16.6 catalogue events, and `TestGatewayCatalogIsCompleteAgainstSpec166`
// asserts the catalogue transcribes §16.6 exactly. The constant is
// exported for the §25.3 OpsEmitter Type field; subscribers that
// filter by suffix on `workspace_plan_warning` still receive it.
// F-14.1.17.
var gatewayEventCatalog = []EventType{
	EventAlertFired, EventAlertResolved, EventUpgradeProgressed, EventPoolStateChanged,
	EventCircuitBreakerOpened, EventCircuitBreakerClosed, EventCredentialRotated,
	EventCredentialPoolExhausted, EventSessionCompleted, EventSessionFailed,
	EventSessionTerminated, EventSessionCancelled, EventSessionExpired,
	EventSessionAwaitingAction, EventDelegationCompleted, EventBackupCompleted,
	EventBackupFailed, EventPlatformUpgradeAvailable, EventHealthStatusChanged,
	EventExperimentUnknownVariantFromProvider, EventExperimentUnknownExternalID,
	EventExperimentTargetingFailed, EventExperimentMultiEligibleSkipped,
	EventExperimentIsolationMismatch, EventExperimentVariantWeakerThanFloor,
}

// GatewayEventTypes returns the §16.6 catalogue of gateway-emitted
// operational-event types. The slice is fresh on every call.
func GatewayEventTypes() []EventType {
	return append([]EventType(nil), gatewayEventCatalog...)
}

// IsGatewayEventType reports whether t is a §16.6 gateway-emitted
// operational-event type.
func IsGatewayEventType(t EventType) bool {
	return inCatalog(t, gatewayEventCatalog)
}

// The §16.6 lenny-ops-emitted operational-event short names. The
// lenny-ops service writes these onto the same Redis stream and
// in-memory buffer as the gateway-emitted events.
const (
	EventOpsHealthStatusChanged            EventType = "ops_health_status_changed"
	EventEscalationCreated                 EventType = "escalation_created"
	EventRemediationLockAcquired           EventType = "remediation_lock_acquired"
	EventRemediationLockReleased           EventType = "remediation_lock_released"
	EventRemediationLockExpired            EventType = "remediation_lock_expired"
	EventRemediationLockStolen             EventType = "remediation_lock_stolen"
	EventRemediationLockSplitBrainDetected EventType = "remediation_lock_split_brain_detected"
	EventDriftDetected                     EventType = "drift_detected"
	EventPlatformUpgradeCompleted          EventType = "platform_upgrade_completed"
	EventPlatformUpgradeVerificationFailed EventType = "platform_upgrade_verification_failed"
	EventPlatformUpgradeImagePullFailed    EventType = "platform_upgrade_image_pull_failed"
	EventRestoreStarted                    EventType = "restore_started"
	EventRestoreShardCompleted             EventType = "restore_shard_completed"
	EventRestoreCompleted                  EventType = "restore_completed"
	EventRestoreFailed                     EventType = "restore_failed"
	EventEventDeliveryFailed               EventType = "event_delivery_failed"
	EventPrometheusQueryTimeout            EventType = "prometheus_query_timeout"
	EventLockSplitBrainDetected            EventType = "lock_split_brain_detected"
	EventOperationProgressed               EventType = "operation_progressed"
)

// opsServiceEventCatalog is the §16.6 closed enumeration of the
// operational-event types the lenny-ops service emits.
var opsServiceEventCatalog = []EventType{
	EventOpsHealthStatusChanged, EventEscalationCreated, EventRemediationLockAcquired,
	EventRemediationLockReleased, EventRemediationLockExpired, EventRemediationLockStolen,
	EventRemediationLockSplitBrainDetected, EventDriftDetected, EventPlatformUpgradeCompleted,
	EventPlatformUpgradeVerificationFailed, EventPlatformUpgradeImagePullFailed,
	EventRestoreStarted, EventRestoreShardCompleted, EventRestoreCompleted, EventRestoreFailed,
	EventEventDeliveryFailed, EventPrometheusQueryTimeout, EventLockSplitBrainDetected,
	EventOperationProgressed,
}

// OpsServiceEventTypes returns the §16.6 catalogue of lenny-ops-emitted
// operational-event types. The slice is fresh on every call.
func OpsServiceEventTypes() []EventType {
	return append([]EventType(nil), opsServiceEventCatalog...)
}

// IsOpsServiceEventType reports whether t is a §16.6 lenny-ops-emitted
// operational-event type.
func IsOpsServiceEventType(t EventType) bool {
	return inCatalog(t, opsServiceEventCatalog)
}

// IsKnownEventType reports whether t is in the §16.6 catalogue —
// emitted by either the gateway or the lenny-ops service.
func IsKnownEventType(t EventType) bool {
	return IsGatewayEventType(t) || IsOpsServiceEventType(t)
}

// inCatalog reports whether t is one of the entries in catalog.
func inCatalog(t EventType, catalog []EventType) bool {
	for _, v := range catalog {
		if t == v {
			return true
		}
	}
	return false
}

// knownSeverities is the §16.6 closed set of severity values an
// operational event carries. The §25.5 ?severity= filter is validated
// against it so an unrecognized token returns INVALID_EVENT_FILTER
// rather than silently matching nothing. spec: §16.6 catalogue severity
// column (Critical | Warning | Info); §25.5 line 2796.
var knownSeverities = map[string]bool{
	"critical": true,
	"warning":  true,
	"info":     true,
}

// IsKnownSeverity reports whether s is a §16.6 catalogue severity value.
func IsKnownSeverity(s string) bool { return knownSeverities[s] }

// normalizeEventTypeToken reduces a filter token to its §16.6 short
// name, stripping the dev.lenny. CloudEvents prefix when present so the
// token can be checked against the catalogue.
func normalizeEventTypeToken(tok string) EventType {
	return EventType(strings.TrimPrefix(tok, cloudEventsPrefix))
}

// ValidateFilterTokens reports the first unrecognized event-type or
// severity token in the CSV filter values, or nil when every token is
// in the §16.6 catalogue. Empty values impose no constraint and pass.
// It backs the §25.5 INVALID_EVENT_FILTER (400) error: an agent that
// mistypes an event type or severity gets a precise rejection instead
// of an empty page. spec: §25.5 lines 2795-2796.
func ValidateFilterTokens(eventType, severity string) error {
	for _, tok := range splitCSV(eventType) {
		if !IsKnownEventType(normalizeEventTypeToken(tok)) {
			return &FilterTokenError{Dimension: "eventType", Token: tok}
		}
	}
	for _, tok := range splitCSV(severity) {
		if !IsKnownSeverity(tok) {
			return &FilterTokenError{Dimension: "severity", Token: tok}
		}
	}
	return nil
}

// splitCSV splits a §25.2 CSV filter value into its non-empty,
// space-trimmed tokens.
func splitCSV(csv string) []string {
	if csv == "" {
		return nil
	}
	var out []string
	for _, tok := range strings.Split(csv, ",") {
		if tok = strings.TrimSpace(tok); tok != "" {
			out = append(out, tok)
		}
	}
	return out
}

// FilterTokenError names an unrecognized §25.5 filter token. The
// caller maps it to the INVALID_EVENT_FILTER (400) error code.
type FilterTokenError struct {
	Dimension string
	Token     string
}

func (e *FilterTokenError) Error() string {
	return "unrecognized " + e.Dimension + " filter token: " + e.Token
}
