// SPDX-License-Identifier: MIT

package audit

import (
	"encoding/json"
	"testing"
	"time"
)

// spec: §12.8 line 810 — the redacted payload drops every PII-bearing
// field and carries only the redaction flags plus the preserved event
// type and OCSF error class.
func TestBuildRedactedPayload_spec_12_8_line810(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	got := BuildRedactedPayload("session.created", "class_mapping_missing", at)

	var fields map[string]any
	if err := json.Unmarshal(got, &fields); err != nil {
		t.Fatalf("redacted payload is not valid JSON: %v", err)
	}
	if fields["redacted"] != true {
		t.Errorf("redacted = %v, want true", fields["redacted"])
	}
	if fields["user_id_redacted_from_row"] != true {
		t.Errorf("user_id_redacted_from_row = %v, want true", fields["user_id_redacted_from_row"])
	}
	if fields["event_type"] != "session.created" {
		t.Errorf("event_type = %v, want session.created", fields["event_type"])
	}
	if fields["error_class"] != "class_mapping_missing" {
		t.Errorf("error_class = %v, want class_mapping_missing", fields["error_class"])
	}
	if fields["redacted_at"] != at.Format(time.RFC3339Nano) {
		t.Errorf("redacted_at = %v, want %v", fields["redacted_at"], at.Format(time.RFC3339Nano))
	}
	// No PII field may survive. The redacted payload has exactly the five
	// spec-named keys.
	if len(fields) != 5 {
		t.Errorf("redacted payload has %d keys, want 5: %v", len(fields), fields)
	}
}

// spec: §12.8 line 810 — the chain verifier and the §11.7 integrity check
// key on the `"redacted": true` marker to tell a lawful redaction apart
// from a tamper.
func TestIsRedactedPayload_spec_12_8_line810(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		payload json.RawMessage
		want    bool
	}{
		{"redacted", BuildRedactedPayload("x", "y", time.Now()), true},
		{"normal", json.RawMessage(`{"user_id":"alice@acme.com"}`), false},
		{"explicit false", json.RawMessage(`{"redacted":false}`), false},
		{"empty", json.RawMessage(``), false},
		{"nil", nil, false},
		{"malformed", json.RawMessage(`not json`), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsRedactedPayload(tc.payload); got != tc.want {
				t.Errorf("IsRedactedPayload(%s) = %v, want %v", tc.payload, got, tc.want)
			}
		})
	}
}

// spec: §12.8 line 810 — a redaction preserves the chain link by leaving
// the row's pre-redaction hash as the recorded value while the recomputed
// hash over the redacted payload differs; the receipt pins the boundary.
func TestRedactedPayloadChangesContentHash_spec_12_8_line810(t *testing.T) {
	t.Parallel()
	original := Row{
		ID:        "11111111-1111-1111-1111-111111111111",
		Seq:       1,
		TenantID:  "acme",
		EventType: "session.created",
		Payload:   json.RawMessage(`{"user_id":"alice@acme.com","secret":"x"}`),
		Timestamp: time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC),
		PrevHash:  GenesisPrevHash,
	}
	original.Hash = ComputeHash(original)

	redacted := original
	redacted.Payload = BuildRedactedPayload(original.EventType, "schema_violation", original.Timestamp)
	newHash := ComputeHash(redacted)
	if newHash == original.Hash {
		t.Fatalf("redaction did not change the content hash; PII may survive in the canonical tuple")
	}
}
