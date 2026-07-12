//go:build contract

// SPDX-License-Identifier: MIT

// Tier-3 contract test for the §4.4 audit-egress OCSF translator's
// recompute property. The translator is pure and deterministic, so an
// external auditor can recompute each row's chain hash from the raw
// canonical tuple and cross-check it against the OCSF wire form: the
// successor record's `unmapped.lenny_chain.prev_hash` (parsed from the
// marshaled OCSF JSON) MUST equal the recomputed predecessor hash. This
// goes beyond the presence-only checks in admin_audit_envelope_test.go:
// it verifies that the value carried in the lenny_chain extension is the
// same hash `pkg/audit.ComputeHash` produces over the canonical tuple,
// so a translator change that drops or mis-serializes a lenny_chain
// field is caught as a contract violation.
//
// spec: §4.4 — "The translator is pure ... and deterministic ... so the
// same chain hash can be recomputed by an external auditor from either
// the OCSF record plus unmapped.lenny_chain extension or the raw
// canonical tuple." §11.7 (audit hash-chain).
package ocsf_audit_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/audit/ocsf"
)

// translateRowToOCSFJSON renders one committed audit row through the
// production translator and returns the marshaled OCSF wire object as
// the external auditor would parse it off the egress boundary. It
// mirrors the admin audit-query Row→Input mapping so the test exercises
// the same projection an auditor receives.
func translateRowToOCSFJSON(t *testing.T, row audit.Row, integrity audit.ChainIntegrity) map[string]any {
	t.Helper()
	rec, err := ocsf.Translate(ocsf.Input{
		ID:                 row.ID,
		Sequence:           row.Seq,
		TenantID:           row.TenantID,
		EventType:          row.EventType,
		EventSchemaVersion: row.EventSchemaVersion,
		CreatedAtUnixMs:    row.Timestamp.UTC().UnixMilli(),
		Payload:            row.Payload,
		PrevHash:           row.PrevHash,
		ChainIntegrity:     integrity,
	})
	if err != nil {
		t.Fatalf("Translate seq %d (%s): %v", row.Seq, row.EventType, err)
	}
	b, err := ocsf.MarshalRecord(rec)
	if err != nil {
		t.Fatalf("MarshalRecord seq %d: %v", row.Seq, err)
	}
	var obj map[string]any
	if err := json.Unmarshal(b, &obj); err != nil {
		t.Fatalf("OCSF record for seq %d is not a JSON object: %v", row.Seq, err)
	}
	return obj
}

// lennyChainField pulls one string field out of a parsed OCSF record's
// unmapped.lenny_chain extension, failing when the extension or the
// field is absent so a dropped field is a hard failure rather than a
// silent empty string.
func lennyChainField(t *testing.T, rec map[string]any, field string) string {
	t.Helper()
	unmapped, ok := rec["unmapped"].(map[string]any)
	if !ok {
		t.Fatalf("OCSF record has no unmapped extension object")
	}
	chain, ok := unmapped["lenny_chain"].(map[string]any)
	if !ok {
		t.Fatalf("OCSF record has no unmapped.lenny_chain extension object")
	}
	v, ok := chain[field].(string)
	if !ok {
		t.Fatalf("unmapped.lenny_chain.%s missing or not a string", field)
	}
	return v
}

// TestOCSFChainHashRecomputableFromCanonicalTuple pins the §4.4
// recompute property: for a real per-tenant chain, recomputing each
// row's hash from the raw canonical tuple via pkg/audit.ComputeHash
// reproduces the value the OCSF wire form carries to link the next row
// (unmapped.lenny_chain.prev_hash), and the genesis row's prev_hash is
// the §11.7 sentinel. An external auditor holding only the OCSF records
// and the canonical tuples can therefore reconstruct and verify the
// chain link end to end.
//
// spec: §4.4 (recompute the chain hash from the raw canonical tuple and
// the OCSF record plus unmapped.lenny_chain extension), §11.7.
// diagnosis: a failure means the OCSF projection no longer carries the
// recomputable chain link — either the translator dropped or
// mis-serialized unmapped.lenny_chain.prev_hash, or the hash an auditor
// recomputes from the canonical tuple diverges from the wire link — so
// an external auditor cannot reconstruct the §11.7 hash chain from the
// egress records and the audit-chain integrity guarantee is unverifiable
// off the authoritative store.
func TestOCSFChainHashRecomputableFromCanonicalTuple(t *testing.T) {
	chains := audit.NewChainSet()
	const tenant = "acme"
	now := func(sec int) time.Time {
		// Millisecond-granular timestamps so the canonical tuple the
		// auditor recomputes over is the same instant the row sealed.
		return time.Date(2026, 1, 1, 0, 0, sec, 0, time.UTC)
	}
	// A chain whose payloads exercise fully-unmapped keys, mapped keys
	// (caller_kind, policy_result), and mixed payloads, so the cross-
	// check is not specific to one translation path.
	chains.Append(tenant, "admin.tenant.created", json.RawMessage(`{"tenant_name":"acme"}`), now(1))
	chains.Append(tenant, "credential.leased", json.RawMessage(`{"caller_kind":"service","secret_ref":"kv/prod"}`), now(2))
	chains.Append(tenant, "session.created", json.RawMessage(`{"user_id":"alice","policy_result":"allow"}`), now(3))

	rows := chains.Chain(tenant).Rows()
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}

	for i, row := range rows {
		// The raw-canonical-tuple recompute path: ComputeHash over the
		// canonical tuple reproduces the sealed chain hash. This is the
		// value the successor row links to.
		recomputed := audit.ComputeHash(row)
		if recomputed != row.Hash {
			t.Fatalf("seq %d: ComputeHash over canonical tuple = %s, want sealed hash %s",
				row.Seq, recomputed, row.Hash)
		}

		rec := translateRowToOCSFJSON(t, row, audit.ChainVerified)

		// The OCSF wire form must carry the identity fields the auditor
		// uses to correlate the record with the canonical row.
		meta, ok := rec["metadata"].(map[string]any)
		if !ok {
			t.Fatalf("seq %d: OCSF record has no metadata object", row.Seq)
		}
		if got := meta["uid"]; got != row.ID {
			t.Errorf("seq %d: metadata.uid = %v, want row id %q", row.Seq, got, row.ID)
		}
		if got := meta["tenant_uid"]; got != tenant {
			t.Errorf("seq %d: metadata.tenant_uid = %v, want %q", row.Seq, got, tenant)
		}

		// The recompute cross-check: the prev_hash the OCSF record
		// carries must equal the hash recomputed from the predecessor's
		// canonical tuple (the genesis sentinel for the first row).
		wirePrev := lennyChainField(t, rec, "prev_hash")
		var wantPrev string
		if i == 0 {
			wantPrev = audit.GenesisPrevHash
		} else {
			wantPrev = audit.ComputeHash(rows[i-1])
		}
		if wirePrev != wantPrev {
			t.Errorf("seq %d: unmapped.lenny_chain.prev_hash = %s, want recomputed predecessor hash %s",
				row.Seq, wirePrev, wantPrev)
		}

		// The integrity verdict the auditor was given must round-trip
		// through the wire form unchanged.
		if got := lennyChainField(t, rec, "integrity"); got != string(audit.ChainVerified) {
			t.Errorf("seq %d: unmapped.lenny_chain.integrity = %q, want %q",
				row.Seq, got, audit.ChainVerified)
		}
	}
}
