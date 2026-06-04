// SPDX-License-Identifier: MIT

package events

import (
	"encoding/json"
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
