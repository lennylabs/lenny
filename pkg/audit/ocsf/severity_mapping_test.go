// SPDX-License-Identifier: MIT

package ocsf

import (
	"encoding/json"
	"testing"

	"github.com/lennylabs/lenny/pkg/audit"
)

// TestSeverityName_spec_25_9_3659 confirms the OCSF severity_id → name
// map the §25.9 ?severity= filter matches against. spec: §25.9 line
// 3659; OCSF v1.1.0 severity_id dictionary.
func TestSeverityName_spec_25_9_3659(t *testing.T) {
	cases := map[int]string{
		severityInformational: "informational",
		severityLow:           "low",
		severityMedium:        "medium",
		4:                     "high",
		severityCritical:      "critical",
		6:                     "fatal",
		0:                     "unknown",
		99:                    "unknown",
	}
	for id, want := range cases {
		if got := SeverityName(id); got != want {
			t.Errorf("SeverityName(%d) = %q, want %q", id, got, want)
		}
	}
}

// TestLookupClassArtifactReplication_spec_16_7_690 pins the OCSF class
// for the two §16.7 ArtifactStore cross-region replication audit
// events. Before F-16.7.3 neither resolved, so both dead-lettered at
// translation even when their emit sites were wired. spec: §16.7 line
// 690; §25.11. F-16.7.3.
func TestLookupClassArtifactReplication_spec_16_7_690(t *testing.T) {
	cases := map[string]ClassMapping{
		"artifact.cross_region_replication_verified": apiActivity(ActivityCreate),
		"artifact_replication.resumed":               apiActivity(ActivityUpdate),
	}
	for et, want := range cases {
		got, ok := LookupClass(et)
		if !ok {
			t.Errorf("%q has no OCSF class mapping; it would dead-letter at translation", et)
			continue
		}
		if got != want {
			t.Errorf("%q mapped to %+v, want %+v", et, got, want)
		}
	}
	// Forward-compat: an unrecognised artifact.* / artifact_replication.*
	// event still resolves via its namespace prefix rather than
	// dead-lettering. The "artifact." prefix must not absorb the
	// "artifact_replication." namespace (distinct eighth byte).
	for _, et := range []string{"artifact.future_event", "artifact_replication.future_event"} {
		if _, ok := LookupClass(et); !ok {
			t.Errorf("%q has no prefix mapping", et)
		}
	}
}

// TestTranslateSeverity_spec_16_7 verifies the §16.7 per-event OCSF
// severity_id table. Before F-16.7.9 every event was emitted at
// severity_id 1 (Informational), so a SIEM rule filtering on
// severity_id >= 5 (Critical) never matched a critical event such as a
// legal-hold override or a tamper detection. spec: §16.7 lines
// 670-694. F-16.7.9.
func TestTranslateSeverity_spec_16_7(t *testing.T) {
	cases := []struct {
		name      string
		eventType string
		payload   string
		want      int
	}{
		{"profile_decommissioned is Critical", "compliance.profile_decommissioned", `{}`, severityCritical},
		{"erasure_blocked_by_hold is Critical", "gdpr.erasure_blocked_by_hold", `{}`, severityCritical},
		{"legal_hold_overridden is Critical", "gdpr.legal_hold_overridden", `{}`, severityCritical},
		{"legal_hold_overridden_tenant is Critical", "gdpr.legal_hold_overridden_tenant", `{}`, severityCritical},
		{"self_recursion_allowed is Notice/Low", "delegation.self_recursion_allowed", `{}`, severityLow},
		{"cycle_warning is Warning/Medium", "delegation.cycle_warning", `{}`, severityMedium},
		{"cycle_detection_mode_changed is Notice/Low", "gateway.cycle_detection_mode_changed", `{}`, severityLow},
		{"feature_flag_downgrade is Notice/Low", "deployment.feature_flag_downgrade_acknowledged", `{}`, severityLow},
		{"escrow_region_resolved is INFO", "legal_hold.escrow_region_resolved", `{}`, severityInformational},
		{"node_drain_forced is Critical", "node.drain.forced", `{}`, severityCritical},
		{"tamper enforce is Critical", "elicitation.content_tamper_detected", `{"enforcement_mode":"enforce"}`, severityCritical},
		{"tamper detect-only is Warning/Medium", "elicitation.content_tamper_detected", `{"enforcement_mode":"detect-only"}`, severityMedium},
		{"tamper default (no mode) is Critical", "elicitation.content_tamper_detected", `{}`, severityCritical},
		{"unannotated event is Informational", "session.created", `{}`, severityInformational},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec, err := Translate(Input{
				ID:             "sev-" + c.eventType,
				EventType:      c.eventType,
				Payload:        json.RawMessage(c.payload),
				ChainIntegrity: audit.ChainUnchecked,
			})
			if err != nil {
				t.Fatalf("Translate(%q): %v", c.eventType, err)
			}
			if rec.SeverityID != c.want {
				t.Errorf("severity_id = %d, want %d", rec.SeverityID, c.want)
			}
		})
	}
}

// TestTranslateSeverityDenyFloor_spec_16_7_9 verifies the policy-deny
// floor still raises an otherwise-Informational event to Medium, and
// that an event whose §16.7 severity is already higher than Medium is
// not lowered by the floor. spec: §16.7; §11.7. F-16.7.9.
func TestTranslateSeverityDenyFloor_spec_16_7_9(t *testing.T) {
	// An unannotated event with a denial is raised to Medium.
	rec, err := Translate(Input{
		ID:             "floor-1",
		EventType:      "interceptor.rejected",
		Payload:        json.RawMessage(`{"policy_result":"deny"}`),
		ChainIntegrity: audit.ChainUnchecked,
	})
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if rec.SeverityID != severityMedium {
		t.Errorf("deny floor: severity_id = %d, want %d (Medium)", rec.SeverityID, severityMedium)
	}
	// A Critical §16.7 event with a denial keeps Critical; the floor
	// never lowers it.
	rec2, err := Translate(Input{
		ID:             "floor-2",
		EventType:      "gdpr.legal_hold_overridden",
		Payload:        json.RawMessage(`{"policy_result":"deny"}`),
		ChainIntegrity: audit.ChainUnchecked,
	})
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if rec2.SeverityID != severityCritical {
		t.Errorf("critical event with deny: severity_id = %d, want %d (Critical)", rec2.SeverityID, severityCritical)
	}
}
