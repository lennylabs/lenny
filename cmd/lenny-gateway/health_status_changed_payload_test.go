// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/operability/health"
)

// spec: §25.3 (event types) — the event catalog documents the
// health_status_changed payload as "Old status, new status, triggering
// component". A transition attributable to a single component's status
// change must name that component in the emitted payload, alongside the
// previous and current aggregate status.
func TestHealthStatusChangedPayloadNamesTriggeringComponent_spec_25_3(t *testing.T) {
	prevComponents := []health.Component{
		{Name: "cache", Status: health.StatusHealthy},
		{Name: "postgres", Status: health.StatusHealthy},
	}
	currComponents := []health.Component{
		{Name: "cache", Status: health.StatusHealthy},
		{Name: "postgres", Status: health.StatusDegraded, Detail: "active connections > 80%"},
	}

	fields := healthStatusChangedPayload(health.StatusHealthy, health.StatusDegraded, prevComponents, currComponents)

	// The payload must round-trip as JSON, since it is marshaled onto the
	// event stream.
	if _, err := json.Marshal(fields); err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	if got := fields["oldStatus"]; got != "healthy" {
		t.Errorf("oldStatus = %v, want healthy", got)
	}
	if got := fields["newStatus"]; got != "degraded" {
		t.Errorf("newStatus = %v, want degraded", got)
	}

	trig, ok := fields["triggeringComponents"].([]string)
	if !ok {
		t.Fatalf("payload has no triggeringComponents field identifying the triggering component; got fields %v", fields)
	}
	if want := []string{"postgres"}; !reflect.DeepEqual(trig, want) {
		t.Errorf("triggeringComponents = %v, want %v", trig, want)
	}
}

// spec: §25.3 — the triggering component is the component whose status
// changed, including on a recovery transition back to healthy (the
// component that recovered), so a subscriber can attribute a
// return-to-healthy signal to the component that cleared.
func TestHealthStatusChangedPayloadNamesRecoveredComponent_spec_25_3(t *testing.T) {
	prevComponents := []health.Component{
		{Name: "redis", Status: health.StatusUnhealthy},
	}
	currComponents := []health.Component{
		{Name: "redis", Status: health.StatusHealthy},
	}

	trig, ok := healthStatusChangedPayload(health.StatusUnhealthy, health.StatusHealthy, prevComponents, currComponents)["triggeringComponents"].([]string)
	if !ok {
		t.Fatal("payload has no triggeringComponents field on a recovery transition")
	}
	if want := []string{"redis"}; !reflect.DeepEqual(trig, want) {
		t.Errorf("triggeringComponents = %v, want %v", trig, want)
	}
}
