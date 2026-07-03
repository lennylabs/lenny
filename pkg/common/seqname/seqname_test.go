// SPDX-License-Identifier: MIT

package seqname

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"testing"
)

// hexDigest40 matches a §10.2 derived sequence name: a literal prefix
// followed by exactly 40 lowercase-hex characters and nothing else.
var hexDigest40 = regexp.MustCompile(`^(billing_seq_|audit_seq_)[0-9a-f]{40}$`)

// maxTenantID is a 128-character tenant_id, the longest the §10.2 format
// (`^[a-zA-Z0-9_-]{1,128}$`) admits. The raw `billing_seq_{tenant_id}`
// name for this input is 140 bytes, far past the 63-byte Postgres limit,
// which is the hazard the derivation removes.
var maxTenantID = strings.Repeat("a", 128)

// TestSequenceName_Derivation pins the exact §10.2 derivation against an
// independent SHA-256 computation: the name is the ledger prefix plus
// the lowercase hex of the first 20 bytes of SHA-256(tenant_id).
//
// spec: §10.2
func TestSequenceName_Derivation(t *testing.T) {
	const tenantID = "acme"
	sum := sha256.Sum256([]byte(tenantID))
	wantDigest := hex.EncodeToString(sum[:20])

	cases := []struct {
		name       string
		got        string
		wantPrefix string
	}{
		{"billing", BillingSequenceName(tenantID), "billing_seq_"},
		{"audit", AuditSequenceName(tenantID), "audit_seq_"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := tc.wantPrefix + wantDigest
			if tc.got != want {
				t.Fatalf("derived name = %q, want %q", tc.got, want)
			}
			if len(wantDigest) != 40 {
				t.Fatalf("digest length = %d, want 40", len(wantDigest))
			}
		})
	}
}

// TestSequenceName_Deterministic confirms the derivation is a pure
// function of the tenant_id: repeated calls return the same name, so a
// tenant's CREATE SEQUENCE and its later nextval resolve to one object.
//
// spec: §10.2
func TestSequenceName_Deterministic(t *testing.T) {
	const tenantID = "bob"
	first := BillingSequenceName(tenantID)
	for i := 0; i < 100; i++ {
		if got := BillingSequenceName(tenantID); got != first {
			t.Fatalf("call %d returned %q, want %q", i, got, first)
		}
	}
}

// TestSequenceName_LengthAndFormat asserts every derived name is a valid
// Postgres identifier ≤ 63 bytes with the exact per-ledger byte length
// the spec states, across a short tenant_id, the 128-character maximum,
// and the format's punctuation characters. This is the core safety
// property S1 introduces: the raw name overruns 63 bytes, the derived
// name never does.
//
// spec: §10.2
func TestSequenceName_LengthAndFormat(t *testing.T) {
	tenants := []string{
		"a",
		"acme",
		"tenant_with-hyphens_and_underscores",
		maxTenantID,
		strings.Repeat("Z9_-", 32), // 128 chars mixing every allowed class
	}
	for _, tenantID := range tenants {
		billing := BillingSequenceName(tenantID)
		audit := AuditSequenceName(tenantID)

		if len(billing) != 52 {
			t.Errorf("billing name %q for tenant %q = %d bytes, want 52", billing, tenantID, len(billing))
		}
		if len(audit) != 50 {
			t.Errorf("audit name %q for tenant %q = %d bytes, want 50", audit, tenantID, len(audit))
		}
		for _, name := range []string{billing, audit} {
			if len(name) > 63 {
				t.Errorf("name %q exceeds 63-byte Postgres identifier limit (%d)", name, len(name))
			}
			if !hexDigest40.MatchString(name) {
				t.Errorf("name %q is not a prefix + 40-hex identifier", name)
			}
		}
	}
}

// TestSequenceName_LedgersDistinct confirms billing and audit derive
// different sequence objects for the same tenant, so neither ledger's
// nextval consumes the other's numbers on the shared billing/audit
// instance.
//
// spec: §10.2, §11.7
func TestSequenceName_LedgersDistinct(t *testing.T) {
	const tenantID = "carol"
	billing := BillingSequenceName(tenantID)
	audit := AuditSequenceName(tenantID)
	if billing == audit {
		t.Fatalf("billing and audit names collide for tenant %q: %q", tenantID, billing)
	}
	if !strings.HasPrefix(billing, "billing_seq_") {
		t.Errorf("billing name %q missing billing_seq_ prefix", billing)
	}
	if !strings.HasPrefix(audit, "audit_seq_") {
		t.Errorf("audit name %q missing audit_seq_ prefix", audit)
	}
	// The digest half is identical; only the prefix differs.
	if strings.TrimPrefix(billing, "billing_seq_") != strings.TrimPrefix(audit, "audit_seq_") {
		t.Errorf("digest halves differ: billing=%q audit=%q", billing, audit)
	}
}

// TestSequenceName_NoTruncationCollision is the regression test for the
// §10.2 hazard the derivation resolves: two tenants sharing a
// 51-character prefix collapse onto the same 63-byte-truncated raw
// `billing_seq_{tenant_id}` object. The derived name distinguishes them
// because the whole tenant_id feeds the digest. This fails against any
// implementation that truncates or that keys the name on a prefix.
//
// spec: §10.2
func TestSequenceName_NoTruncationCollision(t *testing.T) {
	shared := strings.Repeat("t", 51)
	a := shared + "AAAAAAAAAAAAAAAAAAAAAAAAAAA" // 78 chars, first 51 shared
	b := shared + "BBBBBBBBBBBBBBBBBBBBBBBBBBB"

	// The raw literal names collide once Postgres truncates to 63 bytes.
	rawA := ("billing_seq_" + a)[:63]
	rawB := ("billing_seq_" + b)[:63]
	if rawA != rawB {
		t.Fatalf("test premise broken: raw truncated names differ (%q vs %q)", rawA, rawB)
	}

	derivedA := BillingSequenceName(a)
	derivedB := BillingSequenceName(b)
	if derivedA == derivedB {
		t.Fatalf("derived names collide for distinct tenants: %q", derivedA)
	}
}

// TestSequenceName_DistinctTenantsDistinctNames confirms different
// tenants map to different names on the same ledger.
//
// spec: §10.2
func TestSequenceName_DistinctTenantsDistinctNames(t *testing.T) {
	names := map[string]string{}
	for _, tenantID := range []string{"alice", "bob", "carol", "dave", "default", ""} {
		name := AuditSequenceName(tenantID)
		if prior, ok := names[name]; ok {
			t.Fatalf("tenants %q and %q collide on %q", prior, tenantID, name)
		}
		names[name] = tenantID
	}
}

// TestSequenceName_EmptyTenant confirms the derivation handles the empty
// string without panicking and still yields a well-formed 40-hex name,
// because SHA-256 is defined on the empty input.
//
// spec: §10.2
func TestSequenceName_EmptyTenant(t *testing.T) {
	name := BillingSequenceName("")
	if !hexDigest40.MatchString(name) {
		t.Fatalf("empty-tenant name %q is not a prefix + 40-hex identifier", name)
	}
	if len(name) != 52 {
		t.Fatalf("empty-tenant billing name %q = %d bytes, want 52", name, len(name))
	}
}

// TestLedgerSequenceName_MatchesConvenienceFuncs confirms the exported
// Ledger values agree with the convenience functions, so call sites can
// use either surface interchangeably.
//
// spec: §10.2
func TestLedgerSequenceName_MatchesConvenienceFuncs(t *testing.T) {
	const tenantID = "dave"
	if got, want := Billing.SequenceName(tenantID), BillingSequenceName(tenantID); got != want {
		t.Errorf("Billing.SequenceName = %q, BillingSequenceName = %q", got, want)
	}
	if got, want := Audit.SequenceName(tenantID), AuditSequenceName(tenantID); got != want {
		t.Errorf("Audit.SequenceName = %q, AuditSequenceName = %q", got, want)
	}
}
