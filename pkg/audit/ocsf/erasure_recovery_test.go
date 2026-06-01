// SPDX-License-Identifier: MIT

package ocsf

import (
	"encoding/json"
	"testing"

	"github.com/lennylabs/lenny/pkg/audit"
)

// TestErasureRecoveryEventsMapToEntityManagement asserts the §24.12
// erasure-job operator-recovery audit events resolve to OCSF Entity
// Management with the expected activity and Notice (severityLow)
// severity. spec: §24.12 lines 143-144. F-24.12.4.
func TestErasureRecoveryEventsMapToEntityManagement_spec_24_12(t *testing.T) {
	cases := []struct {
		eventType string
		activity  int
	}{
		{"gdpr.erasure_job_retried", ActivityDelete},
		{"gdpr.processing_restriction_cleared", ActivityUpdate},
	}
	for _, tc := range cases {
		rec, err := Translate(Input{
			ID:             "11111111-1111-1111-1111-111111111111",
			TenantID:       "platform",
			EventType:      tc.eventType,
			Payload:        json.RawMessage(`{}`),
			ChainIntegrity: audit.ChainVerified,
		})
		if err != nil {
			t.Fatalf("Translate(%q): %v", tc.eventType, err)
		}
		if rec.ClassUID != ClassEntityManagement {
			t.Errorf("%q class = %d, want %d (Entity Management)", tc.eventType, rec.ClassUID, ClassEntityManagement)
		}
		if rec.ActivityID != tc.activity {
			t.Errorf("%q activity = %d, want %d", tc.eventType, rec.ActivityID, tc.activity)
		}
		if rec.SeverityID != severityLow {
			t.Errorf("%q severity = %d, want %d (Notice)", tc.eventType, rec.SeverityID, severityLow)
		}
	}
}
