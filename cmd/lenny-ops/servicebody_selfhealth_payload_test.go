// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/opsservice"
)

// spec: §25.3 (event types) — the event catalog documents the
// ops_health_status_changed payload as "Old status, new status,
// triggering check". A transition attributable to a single named check
// must name that check in the emitted payload, alongside the previous and
// current aggregate status.
func TestSelfHealthChangePayloadNamesTriggeringCheck_spec_25_3(t *testing.T) {
	prev := opsservice.SelfHealthReport{
		Status:     opsservice.StatusHealthy,
		StatusText: opsservice.StatusHealthy.String(),
		ReplicaID:  "ops-0",
		Checks: []opsservice.CheckResult{
			{Name: "k8s_api", Status: opsservice.StatusHealthy, StatusText: "healthy"},
			{Name: "postgres_pool", Status: opsservice.StatusHealthy, StatusText: "healthy"},
		},
	}
	next := opsservice.SelfHealthReport{
		Status:     opsservice.StatusDegraded,
		StatusText: opsservice.StatusDegraded.String(),
		ReplicaID:  "ops-0",
		Checks: []opsservice.CheckResult{
			{Name: "k8s_api", Status: opsservice.StatusHealthy, StatusText: "healthy"},
			{Name: "postgres_pool", Status: opsservice.StatusDegraded, StatusText: "degraded", Detail: "active connections > 80%"},
		},
	}

	fields := selfHealthChangePayload("ops-0", prev, next)

	// The payload must round-trip as JSON, since it is marshaled onto the
	// event stream.
	if _, err := json.Marshal(fields); err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	if got := fields["previous"]; got != "healthy" {
		t.Errorf("previous = %v, want healthy", got)
	}
	if got := fields["current"]; got != "degraded" {
		t.Errorf("current = %v, want degraded", got)
	}
	if got := fields["replicaId"]; got != "ops-0" {
		t.Errorf("replicaId = %v, want ops-0", got)
	}

	trig, ok := fields["triggeringChecks"].([]string)
	if !ok {
		t.Fatalf("payload has no triggeringChecks field identifying the triggering check; got fields %v", fields)
	}
	if want := []string{"postgres_pool"}; !reflect.DeepEqual(trig, want) {
		t.Errorf("triggeringChecks = %v, want %v", trig, want)
	}
}

// spec: §25.3 — the triggering check is the check whose status changed,
// including on a recovery transition back to healthy (the check that
// recovered), so a subscriber can attribute a return-to-healthy signal to
// the dimension that cleared.
func TestSelfHealthChangePayloadNamesRecoveredCheck_spec_25_3(t *testing.T) {
	prev := opsservice.SelfHealthReport{
		Status:     opsservice.StatusUnhealthy,
		StatusText: opsservice.StatusUnhealthy.String(),
		ReplicaID:  "ops-0",
		Checks: []opsservice.CheckResult{
			{Name: "redis_consumer_lag", Status: opsservice.StatusUnhealthy, StatusText: "unhealthy"},
		},
	}
	next := opsservice.SelfHealthReport{
		Status:     opsservice.StatusHealthy,
		StatusText: opsservice.StatusHealthy.String(),
		ReplicaID:  "ops-0",
		Checks: []opsservice.CheckResult{
			{Name: "redis_consumer_lag", Status: opsservice.StatusHealthy, StatusText: "healthy"},
		},
	}

	trig, ok := selfHealthChangePayload("ops-0", prev, next)["triggeringChecks"].([]string)
	if !ok {
		t.Fatal("payload has no triggeringChecks field on a recovery transition")
	}
	if want := []string{"redis_consumer_lag"}; !reflect.DeepEqual(trig, want) {
		t.Errorf("triggeringChecks = %v, want %v", trig, want)
	}
}
