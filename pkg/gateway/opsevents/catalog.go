// SPDX-License-Identifier: MIT

package opsevents

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

// gatewayEventCatalog is the §16.6 closed enumeration of the
// operational-event types the gateway emits.
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
	for _, v := range gatewayEventCatalog {
		if t == v {
			return true
		}
	}
	return false
}
