// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract test asserting that the OCSF records Lenny emits for a
// class carry that class's unconditionally-required attributes as the
// externally published OCSF v1.1.0 per-class schemas define them, rather
// than only the base_event envelope attributes that
// ocsf_schema_conformance_test.go already checks.
//
// §11.7 pins OCSF v1.1.0 as the single canonical wire format and links
// https://schema.ocsf.io/1.1.0/. The published Authentication [3002]
// class marks `user` (the authenticated subject) as a required
// attribute, and the published API Activity [6003] class marks `actor`
// as a required attribute. Lenny's §11.7 Lenny -> OCSF field-mapping
// table maps `user_id` (when present) onto `actor.user.uid` and never
// populates a top-level `user`, and populates `actor` only when the
// payload carries `user_id` or `caller_kind`. Many 3002/6003-mapped
// event types (token.exchanged, session.created, delegation.spawned,
// circuit_breaker.state_changed) therefore produce records that satisfy
// the base_event envelope but omit the per-class required attribute.
//
// This is distinct from TestOCSFClassUIDsMatchPublishedRegistry, which
// checks that the class_uid numbers themselves match the published
// registry. This test takes the class_uid as correct and checks that the
// record additionally carries the attributes the published class at that
// number requires.
package ocsf_audit_test

import (
	"encoding/json"
	"testing"

	"github.com/lennylabs/lenny/pkg/audit/ocsf"
)

// spec: 11.7
// diagnosis: §11.7 makes OCSF v1.1.0 the single canonical wire format
// (https://schema.ocsf.io/1.1.0/). The published Authentication [3002]
// class requires a top-level `user` attribute and the published API
// Activity [6003] class requires an `actor` attribute. A failure means a
// record Lenny maps to one of these classes omits that class's required
// attribute, so an OCSF consumer validating against the per-class schema
// (not only base_event) rejects the record. Whether Lenny must satisfy
// the per-class required attributes — and, given §11.7's "when present"
// conditional mapping and its "deterministic and reversible" mandate,
// with what value when the source payload carries no identity — is an
// open spec-mapping question, so this test is skipped pending a change
// proposal.
func TestOCSFPerClassRequiredAttributesPresent(t *testing.T) {
	t.Skip("§11.7 maps identity onto actor.user only when the payload carries it and " +
		"defines no top-level `user` attribute; whether Lenny must satisfy the published " +
		"OCSF v1.1.0 per-class required attributes (top-level user for Authentication 3002, " +
		"actor for API Activity 6003) when the source payload carries no identity is an open " +
		"spec-mapping question pending a change proposal")

	cases := []struct {
		name      string
		eventType string
		classUID  int
		// requiredAttr is the top-level OCSF attribute the published
		// v1.1.0 class at classUID marks unconditionally required.
		requiredAttr string
	}{
		// Authentication [3002]: the published class requires `user`.
		{"authentication requires user", "token.exchanged", ocsf.ClassAuthentication, "user"},
		// API Activity [6003]: the published class requires `actor`.
		{"api-activity requires actor", "session.created", ocsf.ClassAPIActivity, "actor"},
		{"delegation-spawned requires actor", "delegation.spawned", ocsf.ClassAPIActivity, "actor"},
		{"circuit-breaker requires actor", "circuit_breaker.state_changed", ocsf.ClassAPIActivity, "actor"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A payload with no identity fields (no user_id, no
			// caller_kind, no source_ip): the record must still satisfy
			// the per-class required attribute.
			in := ocsf.Input{
				ID:              "id-" + tc.eventType,
				Sequence:        1,
				TenantID:        "acme",
				EventType:       tc.eventType,
				CreatedAtUnixMs: 1_700_000_000_000,
				Payload:         json.RawMessage(`{}`),
				PrevHash:        "abcd",
			}
			rec, err := ocsf.Translate(in)
			if err != nil {
				t.Fatalf("translate %q: %v", tc.eventType, err)
			}
			if rec.ClassUID != tc.classUID {
				t.Fatalf("%q mapped to class_uid %d, want %d", tc.eventType, rec.ClassUID, tc.classUID)
			}
			b, err := ocsf.MarshalRecord(rec)
			if err != nil {
				t.Fatalf("marshal %q: %v", tc.eventType, err)
			}
			var obj map[string]any
			if err := json.Unmarshal(b, &obj); err != nil {
				t.Fatalf("decode %q: %v", tc.eventType, err)
			}
			v, ok := obj[tc.requiredAttr]
			if !ok || v == nil {
				t.Errorf("record for %q (class_uid %d) omits the published OCSF v1.1.0 "+
					"required attribute %q\nrecord: %s", tc.eventType, tc.classUID, tc.requiredAttr, b)
			}
		})
	}
}
