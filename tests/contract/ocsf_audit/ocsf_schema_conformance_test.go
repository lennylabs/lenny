// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract test validating that the OCSF records the §25.9
// audit-query API emits satisfy the externally published OCSF v1.1.0
// JSON Schema, rather than only the hand-picked key list
// admin_audit_envelope_test.go checks. A record with a well-formed key
// set but a type or enum violation (a string severity_id, a
// non-integer time, a metadata block missing its required product
// object) passes the key-presence check in admin_audit_envelope_test.go
// but fails this schema validation.
//
// The vendored schema (tests/testdata/ocsf/schema/v1.1.0-base-event.schema.json)
// covers the base_event envelope attributes OCSF v1.1.0 requires with
// no profile applied: class_uid, category_uid, activity_id, type_uid,
// time, severity_id, and metadata (itself requiring version and
// product). This is exactly the set §11.7's Lenny -> OCSF field-mapping
// table promises the translator populates on every record; §11.7 does
// not mention the class-specific, profile-gated attributes (user,
// actor, cloud, device, api, finding_info, ...) that the real OCSF
// per-class schemas additionally require only when a profile is
// active, and those are already tracked as a separate, known gap by
// the skipped TestOCSFClassUIDsMatchPublishedRegistry test in
// ocsf_audit_test.go pending a change proposal.
//
// spec: §25.9 — "all endpoints in this subsection return audit records
// as OCSF v1.1.0 JSON objects per the Wire Format in §11.7".
// spec: §11.7 — the Lenny -> OCSF field-mapping table (id ->
// metadata.uid, sequence_number -> metadata.sequence, tenant_id ->
// metadata.tenant_uid, event_type -> class_uid + activity_id, ...).
package ocsf_audit_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"

	"github.com/lennylabs/lenny/pkg/audit/ocsf"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// ocsfBaseEventSchema compiles the vendored OCSF v1.1.0 base_event
// envelope schema once per test.
func ocsfBaseEventSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	return schematest.Compile(t, "tests/testdata/ocsf/schema/v1.1.0-base-event.schema.json")
}

// validateOCSFEnvelope decodes raw OCSF record bytes and asserts they
// satisfy the vendored base_event schema, failing the test with the
// record's identifying context on a mismatch.
func validateOCSFEnvelope(t *testing.T, schema *jsonschema.Schema, label string, raw []byte) {
	t.Helper()
	var instance any
	if err := json.Unmarshal(raw, &instance); err != nil {
		t.Fatalf("%s: decode OCSF record: %v", label, err)
	}
	if err := schema.Validate(instance); err != nil {
		t.Errorf("%s: record does not validate against the published OCSF v1.1.0 base_event schema: %v\nrecord: %s",
			label, err, raw)
	}
}

// spec: §11.7, §25.9
// diagnosis: a failure means a translated record for some catalog event
// type violates the published OCSF v1.1.0 base_event envelope contract
// (a wrong type, a missing required metadata.product/version, or a
// severity_id outside the OCSF dictionary enum), so a SIEM or any other
// externally schema-validating OCSF consumer would reject or
// misinterpret the record even though it carries every key
// admin_audit_envelope_test.go checks for.
func TestOCSFTranslatedRecordsSatisfyPublishedBaseEventSchema(t *testing.T) {
	schema := ocsfBaseEventSchema(t)
	catalog := ocsf.CatalogEventTypes()
	if len(catalog) == 0 {
		t.Fatal("the OCSF event-type catalog is empty")
	}
	for i, et := range catalog {
		in := ocsf.Input{
			ID:                 "id-" + et,
			Sequence:           uint64(i + 1),
			TenantID:           "acme",
			EventType:          et,
			EventSchemaVersion: "v1",
			CreatedAtUnixMs:    1_700_000_000_000,
			Payload:            json.RawMessage(`{"user_id":"alice@acme.com","caller_kind":"service"}`),
			PrevHash:           "abcd",
		}
		rec, err := ocsf.Translate(in)
		if err != nil {
			t.Errorf("catalog event %q failed translation: %v", et, err)
			continue
		}
		b, err := ocsf.MarshalRecord(rec)
		if err != nil {
			t.Fatalf("%s: MarshalRecord: %v", et, err)
		}
		validateOCSFEnvelope(t, schema, et, b)
	}
}

// spec: §11.7 (dead-letter handling: "the translator achieves this by
// emitting ... a translation-failure receipt -- an OCSF Application
// Security Finding (class 2004) record ... The receipt itself flows
// through the normal translator (it is schema-compliant by
// construction)")
// diagnosis: a failure means the §11.7 dead-letter receipt (the record
// substituted for an untranslatable row so the SIEM pointer can
// advance) is not actually schema-compliant, contradicting the spec's
// explicit "schema-compliant by construction" claim for this record.
func TestOCSFDeadLetterReceiptSatisfiesPublishedBaseEventSchema(t *testing.T) {
	schema := ocsfBaseEventSchema(t)
	in := ocsf.Input{
		ID: "id-dl", Sequence: 7, TenantID: "acme",
		EventType:       "permanently.unmapped",
		CreatedAtUnixMs: 1_700_000_000_000,
		Payload:         json.RawMessage(`{}`),
		PrevHash:        "abcd",
	}
	te := &ocsf.TranslateError{Class: ocsf.ErrClassMappingMissing, EventType: in.EventType, Detail: "no class mapping"}
	rec := ocsf.DeadLetterReceipt(in, te)
	b, err := ocsf.MarshalRecord(rec)
	if err != nil {
		t.Fatalf("MarshalRecord: %v", err)
	}
	validateOCSFEnvelope(t, schema, "dead-letter receipt", b)
}

// spec: §25.9 — "all endpoints in this subsection return audit records
// as OCSF v1.1.0 JSON objects per the Wire Format in §11.7".
// diagnosis: a failure means a live GET /v1/admin/audit-events response
// carries an items[] record that fails external OCSF v1.1.0 schema
// validation, so the production admin-audit egress boundary itself (not
// just the translator in isolation) emits a non-conformant record.
func TestAdminAuditEventsListItemsSatisfyPublishedBaseEventSchema(t *testing.T) {
	schema := ocsfBaseEventSchema(t)
	router := newRouter(t)

	body, _ := json.Marshal(admin.TenantPayload{ID: "acme"})
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, adminPrincipal(httptest.NewRequest(
		http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body),
	)))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create tenant: %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, adminPrincipal(httptest.NewRequest(
		http.MethodGet, "/v1/admin/audit-events?tenantId=platform", nil,
	)))
	if rr.Code != http.StatusOK {
		t.Fatalf("list: %d body=%s", rr.Code, rr.Body.String())
	}

	var env admin.AuditEventEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if len(env.Items) == 0 {
		t.Fatal("expected at least one item in the audit-events list response")
	}
	for i, item := range env.Items {
		validateOCSFEnvelope(t, schema, "GET /v1/admin/audit-events items["+string(rune('0'+i))+"]", item)
	}
}
