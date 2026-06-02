// SPDX-License-Identifier: MIT

package sessionserver

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
)

// spec: §11.2.1 — the metering wire shape must surface the correction
// and cost dimensions a consumer needs to reconstruct the accurate
// ledger (pod_minutes, corrects_sequence, correction_reason_code,
// correction_detail). The pre-fix wire shape dropped all four. F-11.2.12.
func TestToMeteringEvent_exposesCorrectionFields_spec_11_2_1(t *testing.T) {
	e := billingstore.Event{
		TenantID:             "acme",
		SequenceNumber:       9,
		EventType:            billingstore.EventBillingCorrection,
		PodMinutes:           12.5,
		CorrectsSequence:     4,
		CorrectionReasonCode: billingstore.ReasonMeteringBug,
		CorrectionDetail:     "double-count removed",
		CreatedAt:            time.Unix(1700000000, 0).UTC(),
	}
	b, err := json.Marshal(toMeteringEvent(e))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	js := string(b)
	for _, want := range []string{
		`"podMinutes":12.5`,
		`"correctsSequence":4`,
		`"correctionReasonCode":"METERING_BUG"`,
		`"correctionDetail":"double-count removed"`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("metering wire missing %q in %s", want, js)
		}
	}
}

// spec: §11.2.1 — the event-type-specific conditional block is promoted
// to the top level of the event payload (the embedded *Conditional),
// so a credential.leased event surfaces credentialPoolId / credentialId
// / deliveryMode at the top level rather than nested. F-11.2.12.
func TestToMeteringEvent_flattensConditional_spec_11_2_1(t *testing.T) {
	e := billingstore.Event{
		TenantID:       "acme",
		SequenceNumber: 3,
		EventType:      billingstore.EventType("credential.leased"),
		CreatedAt:      time.Unix(1700000000, 0).UTC(),
		Conditional: &billingstore.Conditional{
			CredentialPoolID: "pool-a",
			CredentialID:     "cred-7",
			DeliveryMode:     "direct",
		},
	}
	b, err := json.Marshal(toMeteringEvent(e))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	js := string(b)
	for _, want := range []string{
		`"credentialPoolId":"pool-a"`,
		`"credentialId":"cred-7"`,
		`"deliveryMode":"direct"`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("flattened conditional missing %q in %s", want, js)
		}
	}
	// The block must be flat, not nested under a "Conditional" key.
	if strings.Contains(js, `"Conditional"`) {
		t.Errorf("conditional block was not flattened: %s", js)
	}
}

// spec: §11.2.1 null/absent field contract — a session.created event
// carries no correction fields and no event-type-specific block, so the
// wire payload omits them entirely (a consumer reads absence as "not
// applicable", never as a misleading zero). F-11.2.12.
func TestToMeteringEvent_omitsAbsentFields_spec_11_2_1(t *testing.T) {
	e := billingstore.Event{
		TenantID:       "acme",
		SequenceNumber: 1,
		EventType:      billingstore.EventSessionCreated,
		TokensInput:    100,
		CreatedAt:      time.Unix(1700000000, 0).UTC(),
	}
	b, err := json.Marshal(toMeteringEvent(e))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	js := string(b)
	for _, absent := range []string{
		"podMinutes", "correctsSequence", "correctionReasonCode",
		"correctionDetail", "credentialPoolId", "interceptorRef", "filePath",
	} {
		if strings.Contains(js, absent) {
			t.Errorf("absent field %q leaked into session.created wire: %s", absent, js)
		}
	}
	// The fields that DO apply are present.
	if !strings.Contains(js, `"tokensInput":100`) {
		t.Errorf("tokensInput missing: %s", js)
	}
}
