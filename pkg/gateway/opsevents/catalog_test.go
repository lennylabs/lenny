// SPDX-License-Identifier: MIT

package opsevents

import (
	"strings"
	"testing"
)

func TestCloudEventsType(t *testing.T) {
	if got := EventCircuitBreakerOpened.CloudEventsType(); got != "dev.lenny.circuit_breaker_opened" {
		t.Errorf("CloudEventsType = %q, want dev.lenny.circuit_breaker_opened", got)
	}
	// An experiment short name keeps its dotted suffix.
	if got := EventExperimentIsolationMismatch.CloudEventsType(); got != "dev.lenny.experiment.isolation_mismatch" {
		t.Errorf("experiment CloudEventsType = %q", got)
	}
}

func TestGatewayEventCatalog(t *testing.T) {
	cat := GatewayEventTypes()
	if len(cat) == 0 {
		t.Fatal("the §16.6 catalogue must not be empty")
	}
	seen := map[EventType]bool{}
	for _, et := range cat {
		if et == "" {
			t.Error("a catalogue entry is empty")
		}
		if seen[et] {
			t.Errorf("duplicate catalogue entry: %q", et)
		}
		seen[et] = true
		if !IsGatewayEventType(et) {
			t.Errorf("IsGatewayEventType(%q) = false for a catalogued type", et)
		}
		// Every short name resolves to a dev.lenny.* CloudEvents type.
		if !strings.HasPrefix(et.CloudEventsType(), "dev.lenny.") {
			t.Errorf("%q does not map to a dev.lenny.* type", et)
		}
	}
}

func TestIsGatewayEventTypeRejectsUnknown(t *testing.T) {
	if IsGatewayEventType("not_a_real_event") {
		t.Error("an unknown short name must not be reported as a gateway event type")
	}
}

func TestOpsServiceEventCatalog(t *testing.T) {
	cat := OpsServiceEventTypes()
	if len(cat) == 0 {
		t.Fatal("the §16.6 lenny-ops catalogue must not be empty")
	}
	seen := map[EventType]bool{}
	for _, et := range cat {
		if et == "" {
			t.Error("a catalogue entry is empty")
		}
		if seen[et] {
			t.Errorf("duplicate catalogue entry: %q", et)
		}
		seen[et] = true
		if !IsOpsServiceEventType(et) {
			t.Errorf("IsOpsServiceEventType(%q) = false for a catalogued type", et)
		}
		if !strings.HasPrefix(et.CloudEventsType(), "dev.lenny.") {
			t.Errorf("%q does not map to a dev.lenny.* type", et)
		}
	}
}

func TestGatewayAndOpsCatalogsAreDisjoint(t *testing.T) {
	gw := map[EventType]bool{}
	for _, et := range GatewayEventTypes() {
		gw[et] = true
	}
	for _, et := range OpsServiceEventTypes() {
		if gw[et] {
			t.Errorf("%q appears in both the gateway and lenny-ops catalogues", et)
		}
	}
}

func TestIsKnownEventType(t *testing.T) {
	if !IsKnownEventType(EventCircuitBreakerOpened) {
		t.Error("a gateway event type must be reported as known")
	}
	if !IsKnownEventType(EventDriftDetected) {
		t.Error("a lenny-ops event type must be reported as known")
	}
	if IsKnownEventType("not_a_real_event") {
		t.Error("an uncatalogued short name must not be reported as known")
	}
}

// spec166GatewayEvents is the §16.6 "Gateway-emitted" list plus the
// §16.6 "Experiment events (gateway-emitted, operational)" sub-list,
// transcribed in spec order.
var spec166GatewayEvents = []EventType{
	"alert_fired", "alert_resolved", "upgrade_progressed", "pool_state_changed",
	"circuit_breaker_opened", "circuit_breaker_closed", "credential_rotated",
	"credential_pool_exhausted", "session_completed", "session_failed",
	"session_terminated", "session_cancelled", "session_expired",
	"session_awaiting_action", "delegation_completed", "backup_completed",
	"backup_failed", "platform_upgrade_available", "health_status_changed",
	"experiment.unknown_variant_from_provider", "experiment.unknown_external_id",
	"experiment.targeting_failed", "experiment.multi_eligible_skipped",
	"experiment.isolation_mismatch", "experiment.variant_weaker_than_tenant_floor",
}

// spec166OpsEvents is the §16.6 "lenny-ops-emitted" list, transcribed
// in spec order.
var spec166OpsEvents = []EventType{
	"ops_health_status_changed", "escalation_created", "remediation_lock_acquired",
	"remediation_lock_released", "remediation_lock_expired", "remediation_lock_stolen",
	"remediation_lock_split_brain_detected", "drift_detected", "platform_upgrade_completed",
	"platform_upgrade_verification_failed", "platform_upgrade_image_pull_failed",
	"restore_started", "restore_shard_completed", "restore_completed", "restore_failed",
	"event_delivery_failed", "prometheus_query_timeout", "lock_split_brain_detected",
	"operation_progressed",
}

// TestGatewayCatalogIsCompleteAgainstSpec166 asserts the gateway
// operational-event catalog covers every §16.6 gateway-emitted event.
func TestGatewayCatalogIsCompleteAgainstSpec166(t *testing.T) {
	got := map[EventType]bool{}
	for _, et := range GatewayEventTypes() {
		got[et] = true
	}
	for _, want := range spec166GatewayEvents {
		if !got[want] {
			t.Errorf("§16.6 gateway-emitted event %q is missing from the catalog", want)
		}
	}
	if len(GatewayEventTypes()) != len(spec166GatewayEvents) {
		t.Errorf("gateway catalog has %d entries, §16.6 lists %d — the catalog must transcribe §16.6 exactly",
			len(GatewayEventTypes()), len(spec166GatewayEvents))
	}
}

// TestOpsCatalogIsCompleteAgainstSpec166 asserts the lenny-ops
// operational-event catalog covers every §16.6 lenny-ops-emitted event.
func TestOpsCatalogIsCompleteAgainstSpec166(t *testing.T) {
	got := map[EventType]bool{}
	for _, et := range OpsServiceEventTypes() {
		got[et] = true
	}
	for _, want := range spec166OpsEvents {
		if !got[want] {
			t.Errorf("§16.6 lenny-ops-emitted event %q is missing from the catalog", want)
		}
	}
	if len(OpsServiceEventTypes()) != len(spec166OpsEvents) {
		t.Errorf("lenny-ops catalog has %d entries, §16.6 lists %d — the catalog must transcribe §16.6 exactly",
			len(OpsServiceEventTypes()), len(spec166OpsEvents))
	}
}
