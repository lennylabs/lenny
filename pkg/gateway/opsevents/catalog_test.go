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
