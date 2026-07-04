// SPDX-License-Identifier: MIT

package events

import (
	"encoding/json"
	"strings"
	"testing"
)

// spec: §25.5 line 2556 — an audit-bearing operational event sets
// datacontenttype application/ocsf+json; a non-audit event sets
// application/json.
func TestOperationalEventIsAuditBearing_spec_25_5(t *testing.T) {
	cases := []struct {
		name            string
		dataContentType string
		want            bool
	}{
		{"ocsf", ContentTypeOCSF, true},
		{"json", ContentTypeJSON, false},
		{"empty", "", false},
		{"other", "text/plain", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := OperationalEvent{DataContentType: tc.dataContentType}
			if got := e.IsAuditBearing(); got != tc.want {
				t.Fatalf("IsAuditBearing()=%v, want %v for %q", got, tc.want, tc.dataContentType)
			}
		})
	}
}

// spec: §25.5 line 2556 — the OCSF content type string is the canonical
// application/ocsf+json the §11.7 wire format mandates; the audit-bearing
// data field survives the CloudEvents structured-content round trip.
func TestAuditBearingEventRoundTrips_spec_25_5(t *testing.T) {
	ocsfBody := json.RawMessage(`{"class_uid":3002,"metadata":{"version":"1.1.0"}}`)
	in := OperationalEvent{
		Type:            "dev.lenny.delegation.self_recursion_allowed",
		Source:          "//lenny.dev/gateway/gw-abcde",
		DataContentType: ContentTypeOCSF,
		Data:            ocsfBody,
		Extensions:      map[string]string{"lennytenantid": "acme"},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out OperationalEvent
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !out.IsAuditBearing() {
		t.Fatalf("round-tripped event lost its audit-bearing discriminator: %s", b)
	}
	if out.Extensions["lennytenantid"] != "acme" {
		t.Fatalf("lennytenantid extension lost: %v", out.Extensions)
	}
}

// TestOperationalEventInlineOCSFWireForm asserts §25.3's single-envelope
// model at the byte level: an audit-bearing OperationalEvent emits the OCSF
// record inline under the top-level `data` key as a JSON object, so nothing
// is double-wrapped. The spec-named-failure path is the SDK-alias
// serialization the native struct exists to avoid, which writes
// application/ocsf+json data as an escaped JSON string and surfaces `data`
// as a quoted string rather than an object.
//
// spec: 25.3 (single-envelope inline model), 12.6 (envelope contract)
func TestOperationalEventInlineOCSFWireForm(t *testing.T) {
	ocsfBody := json.RawMessage(`{"class_uid":3002,"metadata":{"version":"1.1.0"}}`)
	in := OperationalEvent{
		Type:            "dev.lenny.delegation.self_recursion_allowed",
		Source:          "//lenny.dev/gateway/gw-abcde",
		DataContentType: ContentTypeOCSF,
		Data:            ocsfBody,
		Extensions:      map[string]string{"lennytenantid": "acme"},
	}
	if !in.IsAuditBearing() {
		t.Fatalf("test precondition: datacontenttype = %q, want application/ocsf+json", in.DataContentType)
	}

	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// The wire form is a single flat CloudEvents object; `data` must
	// carry the OCSF record inline. Parsing into json.RawMessage keeps the
	// byte form of `data` intact so the object-versus-string discriminator
	// below is exact.
	var flat map[string]json.RawMessage
	if err := json.Unmarshal(b, &flat); err != nil {
		t.Fatalf("unmarshal flat: %v", err)
	}
	raw, ok := flat["data"]
	if !ok {
		t.Fatalf("wire JSON has no top-level `data` key: %s", b)
	}

	// A double-wrapped payload surfaces `data` as a quoted JSON string; the
	// single-envelope inline model requires a JSON object. Decoding into
	// map[string]any succeeds only when `data` is an object, so it fails
	// against a string-wrapped regression.
	var asObject map[string]any
	if err := json.Unmarshal(raw, &asObject); err != nil {
		t.Fatalf("top-level `data` is not a JSON object (double-wrapped OCSF record): %v; data=%s", err, raw)
	}
	if asObject["class_uid"] == nil {
		t.Errorf("inline OCSF record lost its class_uid field on the wire: %s", raw)
	}
}

// TestOperationalEventEmptyExtensionsRoundTrip covers the empty-extension
// boundary of the CloudEvents structured-content marshaler: an event with a
// nil (unset) Extensions map marshals and round-trips without loss,
// complementing the populated-extension round-trip in types_test.go. The
// non-happy path is a marshaler that emits a nested "extensions" key or
// mishandles the empty map, which the assertions below reject.
//
// spec: 25.3 (operational event)
func TestOperationalEventEmptyExtensionsRoundTrip(t *testing.T) {
	in := OperationalEvent{
		ID:          "evt-empty-ext",
		SpecVersion: "1.0.2",
		Type:        "dev.lenny.pool_state_changed",
		Subject:     "pool/default-gvisor",
		// Extensions deliberately nil — the empty-extension boundary.
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// The extension map must never serialize under a nested key, even when
	// empty; the flattening path is a no-op here but must not leak the
	// json:"-" field.
	if strings.Contains(string(b), `"extensions"`) {
		t.Errorf("empty Extensions must not serialize under a nested key: %s", b)
	}
	var out OperationalEvent
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Extensions) != 0 {
		t.Errorf("empty Extensions must round-trip empty, got %v", out.Extensions)
	}
	if out.ID != in.ID || out.SpecVersion != in.SpecVersion || out.Type != in.Type || out.Subject != in.Subject {
		t.Errorf("context attributes lost on round trip: got %+v, want %+v", out, in)
	}
}
