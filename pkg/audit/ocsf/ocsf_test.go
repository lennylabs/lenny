// SPDX-License-Identifier: MIT

package ocsf

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/audit"
)

// spec: 11.7
// diagnosis: the §11.7 OCSF field mapping must place each Lenny
// audit-row column on the OCSF attribute the spec table names — id →
// metadata.uid, sequence_number → metadata.sequence, tenant_id →
// metadata.tenant_uid, created_at → time (epoch ms), event_type →
// class_uid + activity_id, with the version pinned at metadata.version.
func TestTranslateMapsCanonicalColumns(t *testing.T) {
	in := Input{
		ID:              "11111111-1111-1111-1111-111111111111",
		Sequence:        7,
		TenantID:        "acme",
		EventType:       "admin.tenant.created",
		CreatedAtUnixMs: 1_700_000_000_000,
		Payload:         json.RawMessage(`{}`),
		PrevHash:        "abcd",
		ChainIntegrity:  audit.ChainVerified,
	}
	rec, err := Translate(in)
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if rec.Metadata.UID != in.ID {
		t.Errorf("metadata.uid = %q, want %q", rec.Metadata.UID, in.ID)
	}
	if rec.Metadata.Sequence != 7 {
		t.Errorf("metadata.sequence = %d, want 7", rec.Metadata.Sequence)
	}
	if rec.Metadata.TenantUID != "acme" {
		t.Errorf("metadata.tenant_uid = %q, want acme", rec.Metadata.TenantUID)
	}
	if rec.Time != 1_700_000_000_000 {
		t.Errorf("time = %d, want 1700000000000", rec.Time)
	}
	if rec.Metadata.Version != Version {
		t.Errorf("metadata.version = %q, want %q", rec.Metadata.Version, Version)
	}
	// admin.tenant.created → Entity Management create.
	if rec.ClassUID != ClassEntityManagement || rec.ActivityID != ActivityCreate {
		t.Errorf("class/activity = %d/%d, want %d/%d",
			rec.ClassUID, rec.ActivityID, ClassEntityManagement, ActivityCreate)
	}
	if rec.TypeUID != ClassEntityManagement*100+ActivityCreate {
		t.Errorf("type_uid = %d, want %d", rec.TypeUID, ClassEntityManagement*100+ActivityCreate)
	}
}

// spec: 11.7
// diagnosis: the §11.7 mapping routes prev_hash and chainIntegrity
// into unmapped.lenny_chain.* so external tools can verify the hash
// chain without being OCSF-aware, and the genesis_nonce onto the
// first-entry-only field.
func TestTranslateSurfacesChainExtension(t *testing.T) {
	rec, err := Translate(Input{
		ID:             "id-1",
		Sequence:       1,
		TenantID:       "acme",
		EventType:      "session.created",
		PrevHash:       "0000",
		ChainIntegrity: audit.ChainVerified,
		GenesisNonce:   "deadbeef",
	})
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if rec.Unmapped.Chain.PrevHash != "0000" {
		t.Errorf("unmapped.lenny_chain.prev_hash = %q, want 0000", rec.Unmapped.Chain.PrevHash)
	}
	if rec.Unmapped.Chain.Integrity != string(audit.ChainVerified) {
		t.Errorf("unmapped.lenny_chain.integrity = %q, want verified", rec.Unmapped.Chain.Integrity)
	}
	if rec.Unmapped.Chain.GenesisNonce != "deadbeef" {
		t.Errorf("unmapped.lenny_chain.genesis_nonce = %q, want deadbeef", rec.Unmapped.Chain.GenesisNonce)
	}
}

// spec: 11.7
// diagnosis: the §11.7 mapping projects payload.policy_result onto
// disposition/disposition_id (allow → Allowed 1, deny → Denied 2),
// caller_kind onto actor.user.type_id (human 1, service 3, agent 99),
// and routes every unmapped payload field verbatim into unmapped.lenny.*.
func TestTranslateMapsPayloadFields(t *testing.T) {
	rec, err := Translate(Input{
		ID:        "id-2",
		Sequence:  2,
		TenantID:  "acme",
		EventType: "interceptor.rejected",
		Payload: json.RawMessage(`{
			"policy_result":"deny","denial_reason":"quota exceeded",
			"caller_kind":"agent","user_id":"alice@acme.com",
			"operation_id":"op-9","session_id":"sess-3",
			"resource_type":"session","resource_id":"r-1",
			"source_ip":"203.0.113.7","user_agent":"curl/8",
			"custom_lenny_field":"keepme"}`),
		ChainIntegrity: audit.ChainUnchecked,
	})
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if rec.DispositionID != dispositionDenied {
		t.Errorf("disposition_id = %d, want %d (Denied)", rec.DispositionID, dispositionDenied)
	}
	if rec.SeverityID != 3 {
		t.Errorf("a denial must raise severity to 3, got %d", rec.SeverityID)
	}
	if rec.StatusDetail != "quota exceeded" {
		t.Errorf("status_detail = %q, want the denial reason", rec.StatusDetail)
	}
	if rec.Actor == nil || rec.Actor.User.TypeID != userTypeOther || rec.Actor.User.Type != "Agent" {
		t.Errorf("agent caller_kind must map to Other(99)/Agent, got %+v", rec.Actor)
	}
	if rec.Actor.User.UID != "alice@acme.com" {
		t.Errorf("actor.user.uid = %q, want alice@acme.com", rec.Actor.User.UID)
	}
	if rec.Metadata.CorrelationUID != "op-9" {
		t.Errorf("metadata.correlation_uid = %q, want op-9", rec.Metadata.CorrelationUID)
	}
	if rec.Metadata.Labels["lenny.session_id"] != "sess-3" {
		t.Errorf("metadata.labels[lenny.session_id] = %q, want sess-3", rec.Metadata.Labels["lenny.session_id"])
	}
	if len(rec.Resources) != 1 || rec.Resources[0].Type != "session" || rec.Resources[0].UID != "r-1" {
		t.Errorf("resources = %+v, want one {session,r-1}", rec.Resources)
	}
	if rec.SrcEndpoint == nil || rec.SrcEndpoint.IP != "203.0.113.7" {
		t.Errorf("src_endpoint.ip not mapped: %+v", rec.SrcEndpoint)
	}
	if rec.HTTPRequest == nil || rec.HTTPRequest.UserAgent != "curl/8" {
		t.Errorf("http_request.user_agent not mapped: %+v", rec.HTTPRequest)
	}
	// An unmapped payload field is preserved verbatim under unmapped.lenny.
	if rec.Unmapped.Lenny["custom_lenny_field"] != "keepme" {
		t.Errorf("unmapped.lenny.custom_lenny_field = %v, want keepme", rec.Unmapped.Lenny["custom_lenny_field"])
	}
	// A mapped field must NOT also leak into unmapped.lenny.
	if _, leaked := rec.Unmapped.Lenny["policy_result"]; leaked {
		t.Error("policy_result has an explicit OCSF mapping; it must not also appear in unmapped.lenny")
	}
}

// spec: 11.7
// diagnosis: §11.7 says a translation failure does not roll the
// canonical row back. Translate must return a *TranslateError carrying
// the error_class — class_mapping_missing for an unknown event type,
// schema_violation for a non-object payload — so the caller can drive
// the retry / dead-letter state machine.
func TestTranslateErrorClasses(t *testing.T) {
	t.Run("unknown event type → class_mapping_missing", func(t *testing.T) {
		_, err := Translate(Input{EventType: "totally.unknown.type", Payload: json.RawMessage(`{}`)})
		var te *TranslateError
		if !errors.As(err, &te) {
			t.Fatalf("want *TranslateError, got %v", err)
		}
		if te.Class != ErrClassMappingMissing {
			t.Errorf("error class = %q, want class_mapping_missing", te.Class)
		}
	})
	t.Run("non-object payload → schema_violation", func(t *testing.T) {
		_, err := Translate(Input{EventType: "session.created", Payload: json.RawMessage(`["not","an","object"]`)})
		var te *TranslateError
		if !errors.As(err, &te) {
			t.Fatalf("want *TranslateError, got %v", err)
		}
		if te.Class != ErrSchemaViolation {
			t.Errorf("error class = %q, want schema_violation", te.Class)
		}
	})
}

// spec: 11.7
// diagnosis: §11.7 dead-letter handling requires the translator to
// emit, in place of an untranslatable event, an OCSF Application
// Security Finding (class 2004) with the raw payload as an opaque
// base64 unmapped.lenny.raw_canonical_b64 field — so a single failing
// event does not halt the per-tenant SIEM stream and is re-translatable.
func TestDeadLetterReceiptIsSchemaValidFinding(t *testing.T) {
	in := Input{
		ID:              "id-dl",
		Sequence:        5,
		TenantID:        "acme",
		EventType:       "broken.event",
		CreatedAtUnixMs: 1_700_000_000_000,
		Payload:         json.RawMessage(`{"pii":"alice@acme.com"}`),
		PrevHash:        "ff00",
		ChainIntegrity:  audit.ChainVerified,
	}
	te := &TranslateError{Class: ErrClassMappingMissing, EventType: "broken.event", Detail: "no mapping"}
	rec := DeadLetterReceipt(in, te)
	if rec.ClassUID != ClassAppSecurityFinding {
		t.Errorf("dead-letter receipt class = %d, want 2004 (Application Security Finding)", rec.ClassUID)
	}
	if rec.Finding == nil || rec.Finding.Title == "" {
		t.Fatal("dead-letter receipt must carry a finding with a title")
	}
	rawB64, ok := rec.Unmapped.Lenny["raw_canonical_b64"].(string)
	if !ok {
		t.Fatal("dead-letter receipt must carry raw_canonical_b64")
	}
	decoded, err := base64.StdEncoding.DecodeString(rawB64)
	if err != nil {
		t.Fatalf("raw_canonical_b64 is not valid base64: %v", err)
	}
	if string(decoded) != `{"pii":"alice@acme.com"}` {
		t.Errorf("raw_canonical_b64 decoded = %q, want the original payload", decoded)
	}
	// The receipt itself must be schema-compliant: it marshals cleanly.
	if _, err := MarshalRecord(rec); err != nil {
		t.Fatalf("dead-letter receipt does not marshal: %v", err)
	}
}

// spec: 11.7
// diagnosis: the §11.7 wire version is pinned at OCSF v1.1.0 and must
// be advertised in every record via metadata.version. A drift in the
// constant is a deployer-observable change and the test pins it.
func TestVersionPinnedAt110(t *testing.T) {
	if Version != "1.1.0" {
		t.Fatalf("OCSF wire version = %q, want 1.1.0 (§11.7 pin)", Version)
	}
}

// spec: 11.7
// diagnosis: §11.7 says the OCSF mapping is deterministic. Translating
// the same canonical tuple twice must yield byte-identical wire bytes.
func TestTranslateIsDeterministic(t *testing.T) {
	in := Input{
		ID:        "id-det",
		Sequence:  3,
		TenantID:  "acme",
		EventType: "credential.leased",
		Payload:   json.RawMessage(`{"a":1,"b":2,"caller_kind":"service"}`),
	}
	first, err := Translate(in)
	if err != nil {
		t.Fatalf("Translate first: %v", err)
	}
	second, err := Translate(in)
	if err != nil {
		t.Fatalf("Translate second: %v", err)
	}
	b1, _ := MarshalRecord(first)
	b2, _ := MarshalRecord(second)
	if string(b1) != string(b2) {
		t.Errorf("translation is not deterministic:\n %s\n %s", b1, b2)
	}
}

// spec: 11.7
// diagnosis: the §11.7 event-type catalog must cover the credential
// (§4.9.2) and §16.7 audit event families. Every catalog entry must
// resolve to a class via LookupClass, and the prefix fallback must
// catch the broader namespaces.
func TestLookupClassCoversCatalog(t *testing.T) {
	for _, et := range CatalogEventTypes() {
		if _, ok := LookupClass(et); !ok {
			t.Errorf("catalog event type %q has no class mapping", et)
		}
	}
	// Prefix fallback: an unlisted admin.* type still resolves.
	if m, ok := LookupClass("admin.something.new"); !ok || m.ClassUID == 0 {
		t.Errorf("admin.* prefix fallback failed: %+v ok=%v", m, ok)
	}
	// The longest prefix wins: admin.tenant.created is more specific
	// than admin.
	m, _ := LookupClass("admin.tenant.created")
	if m.ClassUID != ClassEntityManagement || m.ActivityID != ActivityCreate {
		t.Errorf("admin.tenant.created resolved to %+v, want EntityManagement/create", m)
	}
}

// spec: §11.4 — admin.user.invalidated (soft_disable / hard_disable /
// full_revoke fan-out emitter) must resolve to OCSF AccountChange with
// ActivityDisable so SIEM consumers see a distinguished "Disable
// Account" event rather than the generic admin.user prefix
// (AccountChange/Unknown).
func TestAdminUserInvalidatedMapsToDisable_spec_11_4(t *testing.T) {
	m, ok := LookupClass("admin.user.invalidated")
	if !ok {
		t.Fatal("admin.user.invalidated did not resolve to any class")
	}
	if m.ClassUID != ClassAccountChange {
		t.Errorf("admin.user.invalidated class_uid = %d, want %d (AccountChange)", m.ClassUID, ClassAccountChange)
	}
	if m.ActivityID != ActivityDisable {
		t.Errorf("admin.user.invalidated activity_id = %d, want %d (Disable)", m.ActivityID, ActivityDisable)
	}
}

// spec: §9.3 line 116-164 — F-9.3.9. The §9.3 connector lifecycle
// audit events must resolve to a distinguished OCSF class so SIEM
// consumers see registration, update, soft-delete, and OAuth flow
// rows under the right semantic class instead of the generic admin.*
// prefix fallback. Connector CRUD maps to EntityManagement and the
// OAuth flow maps to Authentication.
func TestConnectorEventsResolveToTypedClasses_spec_9_3_116(t *testing.T) {
	for _, tc := range []struct {
		eventType string
		classUID  int
		activity  int
	}{
		{"admin.connector.created", ClassEntityManagement, ActivityCreate},
		{"admin.connector.updated", ClassEntityManagement, ActivityUpdate},
		{"admin.connector.soft_deleted", ClassEntityManagement, ActivityDelete},
		{"connector.oauth.authorization_initiated", ClassAuthentication, ActivityCreate},
		{"connector.oauth.credential_stored", ClassAuthentication, ActivityCreate},
	} {
		m, ok := LookupClass(tc.eventType)
		if !ok {
			t.Errorf("%s: no class mapping", tc.eventType)
			continue
		}
		if m.ClassUID != tc.classUID {
			t.Errorf("%s class_uid = %d, want %d", tc.eventType, m.ClassUID, tc.classUID)
		}
		if m.ActivityID != tc.activity {
			t.Errorf("%s activity_id = %d, want %d", tc.eventType, m.ActivityID, tc.activity)
		}
	}
}
