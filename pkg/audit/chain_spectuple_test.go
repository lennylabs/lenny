// SPDX-License-Identifier: MIT

package audit

import (
	"encoding/json"
	"testing"
	"time"
)

// The §11.7 item 3 hash input is the canonical tuple (id, prev_hash,
// tenant_id, sequence_number, event_type, event_schema_version,
// payload_canonical_json, created_at). These tests pin the columns the
// finding F-11.7.4 reported missing: id, event_schema_version, and the
// RFC 8785 canonical payload.

// baseRow returns a sealed row with the spec-tuple fields set so a test
// can perturb one field and observe the hash change.
func baseRow() Row {
	r := Row{
		ID:                 "11111111-1111-4111-8111-111111111111",
		Seq:                1,
		TenantID:           "acme",
		EventType:          "credential.leased",
		EventSchemaVersion: "v1",
		Payload:            json.RawMessage(`{"user":"alice","n":1}`),
		Timestamp:          time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		PrevHash:           GenesisPrevHash,
	}
	r.Hash = ComputeHash(r)
	return r
}

// TestHashIncludesID_spec_11_7_361 — the row UUID is part of the hash
// input, so two rows identical except for id hash differently. Before
// F-11.7.4 the id was omitted. spec: §11.7 item 3 line 361.
func TestHashIncludesID_spec_11_7_361(t *testing.T) {
	t.Parallel()
	a := baseRow()
	b := a
	b.ID = "22222222-2222-4222-8222-222222222222"
	if ComputeHash(a) == ComputeHash(b) {
		t.Fatal("hash must depend on the row id")
	}
}

// TestHashIncludesEventSchemaVersion_spec_11_7_365 — a payload shape
// change for the same event_type, modeled as a schema-version bump,
// produces a distinct hash. spec: §11.7 item 3 line 365.
func TestHashIncludesEventSchemaVersion_spec_11_7_365(t *testing.T) {
	t.Parallel()
	a := baseRow()
	b := a
	b.EventSchemaVersion = "v2"
	if ComputeHash(a) == ComputeHash(b) {
		t.Fatal("hash must depend on event_schema_version so two schema versions of one event_type do not collide")
	}
}

// TestSchemaVersionEmptyDefaultsToV1InHash_spec_11_7_365 — an unset
// EventSchemaVersion hashes identically to the explicit default "v1",
// matching the audit_log column default so an in-memory row and a
// Postgres-scanned row agree. spec: §11.7 item 3 line 365.
func TestSchemaVersionEmptyDefaultsToV1InHash_spec_11_7_365(t *testing.T) {
	t.Parallel()
	a := baseRow()
	a.EventSchemaVersion = ""
	b := baseRow()
	b.EventSchemaVersion = DefaultEventSchemaVersion
	if ComputeHash(a) != ComputeHash(b) {
		t.Fatal("empty event_schema_version must hash as the v1 default")
	}
}

// TestHashStableUnderPayloadKeyReorder_spec_11_7_364 — the hash input
// canonicalizes the payload, so a key reordering (exactly what a Postgres
// jsonb round trip produces) does not change the hash. This is what makes
// the durable chain verify clean after the payload column is read back.
// spec: §11.7 item 3 line 364.
func TestHashStableUnderPayloadKeyReorder_spec_11_7_364(t *testing.T) {
	t.Parallel()
	a := baseRow()
	b := a
	b.Payload = json.RawMessage(`{"n":1,"user":"alice"}`) // keys swapped
	if ComputeHash(a) != ComputeHash(b) {
		t.Fatal("hash must be stable under payload key reordering (JCS canonicalization)")
	}
}

// TestHashStableUnderPayloadNumberForm_spec_11_7_364 — the same numeric
// value written with a trailing zero or exponent canonicalizes to one
// form, so a jsonb numeric round trip does not break the chain.
// spec: §11.7 item 3 line 364.
func TestHashStableUnderPayloadNumberForm_spec_11_7_364(t *testing.T) {
	t.Parallel()
	a := baseRow()
	a.Payload = json.RawMessage(`{"amount":1.50}`)
	a.Hash = ComputeHash(a)
	b := a
	b.Payload = json.RawMessage(`{"amount":1.5}`)
	if ComputeHash(a) != ComputeHash(b) {
		t.Fatal("hash must be stable under equivalent numeric spellings")
	}
}

// TestHashChangesWhenPayloadValueChanges — a real payload change still
// changes the hash; canonicalization must not collapse distinct values.
func TestHashChangesWhenPayloadValueChanges(t *testing.T) {
	t.Parallel()
	a := baseRow()
	b := a
	b.Payload = json.RawMessage(`{"user":"bob","n":1}`)
	if ComputeHash(a) == ComputeHash(b) {
		t.Fatal("a genuine payload change must change the hash")
	}
}

// TestLinkHashIsPredecessorContentHash_spec_11_7_361 — the prev_hash a
// successor carries is the SHA-256 of the predecessor's canonical tuple,
// which equals the predecessor's content hash. spec: §11.7 item 3 line 361.
func TestLinkHashIsPredecessorContentHash_spec_11_7_361(t *testing.T) {
	t.Parallel()
	prev := baseRow()
	if LinkHash(prev) != prev.Hash {
		t.Fatalf("LinkHash(prev) = %q, want prev.Hash %q", LinkHash(prev), prev.Hash)
	}
	if LinkHash(prev) != ComputeHash(prev) {
		t.Fatal("LinkHash(prev) must equal ComputeHash(prev)")
	}
}

// TestCanonicalPayloadEmptyIsNull — an empty payload canonicalizes to the
// JSON null literal so the hash input is always well-formed JSON.
func TestCanonicalPayloadEmptyIsNull(t *testing.T) {
	t.Parallel()
	if got := string(CanonicalPayload(nil)); got != "null" {
		t.Errorf("CanonicalPayload(nil) = %q, want null", got)
	}
	if got := string(CanonicalPayload(json.RawMessage(``))); got != "null" {
		t.Errorf("CanonicalPayload(empty) = %q, want null", got)
	}
}

// TestCanonicalPayloadSortsKeys — the canonical payload sorts object keys,
// the value stored in payload_canonical_json for external verifiers.
func TestCanonicalPayloadSortsKeys(t *testing.T) {
	t.Parallel()
	got := string(CanonicalPayload(json.RawMessage(`{"b":1,"a":2}`)))
	if got != `{"a":2,"b":1}` {
		t.Errorf("CanonicalPayload = %q, want sorted", got)
	}
}

// TestAppendStampsIDAndSchemaVersion_spec_11_7_361 — the in-memory chain
// stamps a unique id and the default schema version on every appended
// row, so the spec-tuple hash inputs are populated end to end.
// spec: §11.7 item 3 lines 361-365.
func TestAppendStampsIDAndSchemaVersion_spec_11_7_361(t *testing.T) {
	t.Parallel()
	c := NewChain("acme")
	r1 := c.Append("e1", json.RawMessage(`{}`), ts())
	r2 := c.Append("e2", json.RawMessage(`{}`), ts())
	if r1.ID == "" || r2.ID == "" {
		t.Fatal("Append must stamp a row id")
	}
	if r1.ID == r2.ID {
		t.Fatal("each appended row must get a distinct id")
	}
	if r1.EventSchemaVersion != DefaultEventSchemaVersion {
		t.Errorf("EventSchemaVersion = %q, want %q", r1.EventSchemaVersion, DefaultEventSchemaVersion)
	}
	if res := c.Verify(); res.Integrity != ChainVerified {
		t.Errorf("chain with stamped ids should verify: %q", res.Integrity)
	}
}
