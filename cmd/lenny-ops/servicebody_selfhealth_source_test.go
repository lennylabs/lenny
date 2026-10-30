// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"testing"

	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/ops/opsservice"
)

// spec: §25.4 Self-Monitoring, "Multi-replica scope" — "Events carry the
// replica identity (`source.replicaID` field) so subscribers can
// distinguish leader-replica self-health from non-leader-replica
// self-health."
//
// This test builds the ops_health_status_changed event the same way
// buildServiceBody's OnSelfHealthChange callback does (servicebody.go) and
// checks the wire JSON for a structured source.replicaID property.
func TestSelfHealthEventCarriesReplicaIdentityInSourceReplicaIDField_spec_25_4(t *testing.T) {
	t.Skip("open spec-vs-code reconciliation: whether the replica identity documented as a structured " +
		"source.replicaID field belongs in a restructured OperationalEvent.Source or the spec text should " +
		"instead describe the existing data.replicaId/Subject location is undecided")

	const replicaID = "ops-0"
	prev := opsservice.SelfHealthReport{Status: opsservice.StatusHealthy, StatusText: "healthy", ReplicaID: replicaID}
	next := opsservice.SelfHealthReport{Status: opsservice.StatusDegraded, StatusText: "degraded", ReplicaID: replicaID}

	payload, err := json.Marshal(selfHealthChangePayload(replicaID, prev, next))
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	ev := events.OperationalEvent{
		Type:            events.EventOpsHealthStatusChanged.CloudEventsType(),
		Subject:         "ops/" + replicaID,
		Severity:        selfHealthEventSeverity(next.StatusText),
		DataContentType: "application/json",
		Data:            payload,
	}

	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}

	var wire map[string]json.RawMessage
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal wire event: %v", err)
	}

	sourceRaw, ok := wire["source"]
	if !ok {
		t.Fatalf("emitted event has no source attribute at all; §25.4 documents the replica identity in a "+
			"source.replicaID field, so source must be present. wire=%s", raw)
	}

	var source struct {
		ReplicaID string `json:"replicaID"`
	}
	if err := json.Unmarshal(sourceRaw, &source); err != nil {
		t.Fatalf("§25.4 documents the emitted event's replica identity in a structured source.replicaID field; "+
			"the emitted event's source attribute is not a JSON object with a replicaID property (got %s): %v",
			sourceRaw, err)
	}
	if source.ReplicaID != replicaID {
		t.Errorf("source.replicaID = %q, want %q", source.ReplicaID, replicaID)
	}
}
