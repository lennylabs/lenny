// SPDX-License-Identifier: MIT

package events

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOperationalEventSubjectJSONRoundTrip_spec_25_3_19(t *testing.T) {
	// spec: §16.6 — the CloudEvents subject attribute round-trips
	// through the §25.3 wire format and is omitted when empty.
	e := OperationalEvent{
		ID:          "evt-1",
		SpecVersion: "1.0.2",
		Type:        "dev.lenny.alert_fired",
		Subject:     "credential_pool/openai-prod",
	}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), `"subject":"credential_pool/openai-prod"`) {
		t.Errorf("marshalled JSON missing subject: %s", data)
	}
	empty := OperationalEvent{ID: "evt-2", SpecVersion: "1.0.2", Type: "dev.lenny.alert_resolved"}
	emptyData, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("marshal empty: %v", err)
	}
	if strings.Contains(string(emptyData), `"subject"`) {
		t.Errorf("empty Subject must be omitted from JSON: %s", emptyData)
	}
}

func TestOperationalEventExtensionsRoundTrip_spec_25_3_13(t *testing.T) {
	// spec: §25.3 line 650 / §12.6 — CloudEvents extension attributes
	// flatten into the top-level object and round-trip back into
	// Extensions, mirroring pkg/gateway/eventbus.Event.
	e := OperationalEvent{
		ID: "evt-1", SpecVersion: "1.0.2", Type: "dev.lenny.session_failed",
		Subject: "session/abc123",
		Extensions: map[string]string{
			"lennytenantid":      "acme",
			"lennyrootsessionid": "root-1",
		},
	}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("unmarshal root: %v", err)
	}
	if _, ok := root["lennytenantid"]; !ok {
		t.Error("extension lennytenantid must flatten to the top-level CloudEvents object")
	}
	if _, ok := root["extensions"]; ok {
		t.Error("extensions must not serialize under a nested key")
	}
	var got OperationalEvent
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Extensions["lennytenantid"] != "acme" || got.Extensions["lennyrootsessionid"] != "root-1" {
		t.Errorf("extensions did not round-trip: %+v", got.Extensions)
	}
	// Structured attributes are never captured as extensions.
	if _, leaked := got.Extensions["subject"]; leaked {
		t.Error("subject is a structured attribute and must not appear in Extensions")
	}
	if got.Subject != "session/abc123" {
		t.Errorf("subject = %q, want session/abc123", got.Subject)
	}
}

func TestOperationalEventExtensionNeverClobbersKnownAttribute_spec_25_3_13(t *testing.T) {
	// A stray extension keyed on a structured attribute name must not
	// overwrite the first-class field on the wire.
	e := OperationalEvent{
		ID: "evt-2", SpecVersion: "1.0.2", Type: "dev.lenny.alert_fired",
		Severity:   "critical",
		Extensions: map[string]string{"severity": "info"},
	}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got OperationalEvent
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Severity != "critical" {
		t.Errorf("structured severity = %q, want critical (extension must not clobber)", got.Severity)
	}
}

// TestEventFilterMatchesDirectly_spec_25_3_15 asserts EventFilter.Matches
// applies the §25.2 CSV union and intersection rules directly on the
// value type, independent of the ring buffer.
func TestEventFilterMatchesDirectly_spec_25_3_15(t *testing.T) {
	// spec: §25.2 lines 210-211 — eventType and severity accept the CSV
	// form; a query matches the union of the comma-separated tokens.
	critical := OperationalEvent{Type: "dev.lenny.alert_fired", Severity: "critical"}
	warning := OperationalEvent{Type: "dev.lenny.session_failed", Severity: "warning"}
	info := OperationalEvent{Type: "dev.lenny.pool_state_changed", Severity: "info"}

	if !(EventFilter{Severity: "critical,warning"}).Matches(critical) {
		t.Error("severity CSV union must match critical")
	}
	if (EventFilter{Severity: "critical,warning"}).Matches(info) {
		t.Error("severity CSV union must not match info")
	}
	if !(EventFilter{EventType: "alert_fired, pool_state_changed"}).Matches(info) {
		t.Error("eventType CSV union (whitespace tolerated) must match pool_state_changed")
	}
	// The two CSV dimensions intersect (severity AND type).
	if (EventFilter{EventType: "alert_fired,session_failed", Severity: "warning"}).Matches(critical) {
		t.Error("combined CSV intersection must reject a non-matching severity")
	}
	if !(EventFilter{EventType: "alert_fired,session_failed", Severity: "warning"}).Matches(warning) {
		t.Error("combined CSV intersection must match session_failed/warning")
	}
	// A CSV of only empty tokens imposes no constraint.
	if !(EventFilter{Severity: " , "}).Matches(info) {
		t.Error("all-empty CSV must not filter")
	}
	// A single value still works (no regression).
	if !(EventFilter{EventType: "alert_fired"}).Matches(critical) {
		t.Error("single eventType must match")
	}
}
