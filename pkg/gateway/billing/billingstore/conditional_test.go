// SPDX-License-Identifier: MIT

package billingstore_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore"
)

// spec: §11.2.1 — Event schema (all events): the event-type-specific
// conditional fields ride the Event through the ledger. The in-memory
// store round-trips a credential.leased event's Conditional block
// unchanged. F-11.2.12.
func TestMemoryRoundTripsConditional_spec_11_2_1(t *testing.T) {
	m := billingstore.NewMemory()
	ctx := context.Background()
	want := billingstore.Event{
		TenantID:  "acme",
		UserID:    "alice@acme",
		SessionID: "sess-1",
		EventType: billingstore.EventType("credential.leased"),
		Conditional: &billingstore.Conditional{
			CredentialPoolID: "pool-a",
			CredentialID:     "cred-7",
			DeliveryMode:     "direct",
		},
	}
	if _, err := m.Append(ctx, want); err != nil {
		t.Fatalf("Append: %v", err)
	}
	got, err := m.Since(ctx, "acme", 0, 0)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Since: got %d events, want 1", len(got))
	}
	c := got[0].Conditional
	if c == nil {
		t.Fatal("Conditional was dropped on round-trip")
	}
	if c.CredentialPoolID != "pool-a" || c.CredentialID != "cred-7" || c.DeliveryMode != "direct" {
		t.Errorf("Conditional round-trip mismatch: got %+v", c)
	}
}

// spec: §11.2.1 — null/absent field contract: a field annotated "(for X
// events only)" MUST be omitted from the JSON payload for every other
// event type, and consumers MUST treat an absent field as "not
// applicable" rather than zero. The Conditional marshals only the
// populated fields. F-11.2.12.
func TestConditionalJSONOmitsUnpopulatedFields_spec_11_2_1(t *testing.T) {
	c := billingstore.Conditional{
		InterceptorRef: "scanner-1",
		OldFailPolicy:  "fail-closed",
		NewFailPolicy:  "fail-open",
	}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	js := string(b)
	for _, present := range []string{"interceptorRef", "oldFailPolicy", "newFailPolicy"} {
		if !strings.Contains(js, present) {
			t.Errorf("expected %q in %s", present, js)
		}
	}
	// Fields for unrelated event types must not surface.
	for _, absent := range []string{"credentialPoolId", "filePath", "revokedBy", "poolName"} {
		if strings.Contains(js, absent) {
			t.Errorf("unpopulated field %q leaked into %s", absent, js)
		}
	}
}

// spec: §11.2.1 — the export-scan boolean transitions
// (old_scanExportedFiles / new_scanExportedFiles) carry a meaningful
// false value, so they are pointer fields: false MUST serialize as
// `false`, not be omitted as a zero value. F-11.2.12.
func TestConditionalBooleanTransitionsSerializeFalse_spec_11_2_1(t *testing.T) {
	f := false
	tr := true
	c := billingstore.Conditional{
		PolicyName:           "export-policy",
		OldScanExportedFiles: &tr,
		NewScanExportedFiles: &f,
	}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	js := string(b)
	if !strings.Contains(js, `"oldScanExportedFiles":true`) {
		t.Errorf("oldScanExportedFiles true missing: %s", js)
	}
	if !strings.Contains(js, `"newScanExportedFiles":false`) {
		t.Errorf("newScanExportedFiles false must serialize, not be omitted: %s", js)
	}
}
