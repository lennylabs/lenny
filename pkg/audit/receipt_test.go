// SPDX-License-Identifier: MIT

package audit

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// redactedChain returns a two-row chain whose first row a GDPR erasure
// rewrote in place, with no receipt attached yet.
func redactedChain(t *testing.T) (*Chain, Row) {
	t.Helper()
	c := NewChain("acme")
	c.Append("e1", json.RawMessage(`{"pii":"alice@acme.com"}`), ts())
	c.Append("e2", json.RawMessage(`{"n":2}`), ts())
	return c, c.Rows()[0]
}

// TestReceiptVerifiesUnderTheTrustedKey pins that a receipt signed under a
// key the set trusts verifies, and that a chain whose verifier holds that
// key reports the redaction as lawful.
//
// spec: §12.8 (Compliance Interfaces)
func TestReceiptVerifiesUnderTheTrustedKey(t *testing.T) {
	t.Parallel()
	c, row := redactedChain(t)
	rcpt := lawfulReceipt(t, row)
	if err := receiptTestKeySet(t).VerifyReceipt(rcpt); err != nil {
		t.Fatalf("VerifyReceipt: %v", err)
	}
	c.WithReceiptVerifier(receiptTestKeySet(t))
	if err := c.Redact(1, json.RawMessage(`{"redacted":true}`), rcpt); err != nil {
		t.Fatalf("Redact: %v", err)
	}
	if got := c.Verify().Integrity; got != ChainRedactedGDPR {
		t.Errorf("Verify = %q, want redacted_gdpr", got)
	}
}

// TestReceiptThatDoesNotVerifyLeavesTheRowBroken pins the fail-closed
// receipt check: a receipt with a changed field, a forged signature, an
// untrusted key, no signature, or a chain with no verifier does not make
// the redaction lawful, so the row reads broken.
//
// spec: §12.8 (Compliance Interfaces), §11.7 (Audit Logging)
func TestReceiptThatDoesNotVerifyLeavesTheRowBroken(t *testing.T) {
	t.Parallel()
	other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		mutate   func(r RedactionReceipt, row Row) RedactionReceipt
		verifier func(t *testing.T) ReceiptVerifier
		wantErr  error
	}{
		{
			name: "a field changed after signing",
			mutate: func(r RedactionReceipt, _ Row) RedactionReceipt {
				r.LegalBasis = "operator_acknowledged_override"
				return r
			},
			wantErr: ErrReceiptSignature,
		},
		{
			name: "the timestamp moved by a microsecond",
			mutate: func(r RedactionReceipt, _ Row) RedactionReceipt {
				r.Timestamp = r.Timestamp.Add(time.Microsecond)
				return r
			},
			wantErr: ErrReceiptSignature,
		},
		{
			name: "a signature by a key the set does not hold under a trusted identifier",
			mutate: func(r RedactionReceipt, _ Row) RedactionReceipt {
				return signReceiptWith(t, other, testReceiptKeyID, r)
			},
			wantErr: ErrReceiptSignature,
		},
		{
			name: "a key identifier the set does not trust",
			mutate: func(r RedactionReceipt, _ Row) RedactionReceipt {
				return signReceiptWith(t, other, "attacker-key", r)
			},
			wantErr: ErrReceiptKeyUnknown,
		},
		{
			name: "no signature",
			mutate: func(r RedactionReceipt, _ Row) RedactionReceipt {
				r.Signature = nil
				return r
			},
			wantErr: ErrReceiptUnsigned,
		},
		{
			name: "a truncated signature",
			mutate: func(r RedactionReceipt, _ Row) RedactionReceipt {
				r.Signature = r.Signature[:10]
				return r
			},
			wantErr: ErrReceiptSignature,
		},
		{
			name:     "a chain with no verifier",
			mutate:   func(r RedactionReceipt, _ Row) RedactionReceipt { return r },
			verifier: func(*testing.T) ReceiptVerifier { return nil },
		},
		{
			name: "a receipt for another row of the tenant",
			mutate: func(r RedactionReceipt, row Row) RedactionReceipt {
				r.AuditEventID = "00000000-0000-4000-8000-00000000ffff"
				return signReceiptWith(t, receiptTestKey(t), testReceiptKeyID, r)
			},
		},
		{
			name: "a receipt for another tenant",
			mutate: func(r RedactionReceipt, _ Row) RedactionReceipt {
				r.TenantID = "globex"
				return signReceiptWith(t, receiptTestKey(t), testReceiptKeyID, r)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, row := redactedChain(t)
			rcpt := tc.mutate(lawfulReceipt(t, row), row)
			var v ReceiptVerifier = receiptTestKeySet(t)
			if tc.verifier != nil {
				v = tc.verifier(t)
			}
			if tc.wantErr != nil {
				if err := receiptTestKeySet(t).VerifyReceipt(rcpt); !errors.Is(err, tc.wantErr) {
					t.Errorf("VerifyReceipt = %v, want %v", err, tc.wantErr)
				}
			}
			c.WithReceiptVerifier(v)
			if err := c.Redact(1, json.RawMessage(`{"redacted":true}`), rcpt); err != nil {
				t.Fatalf("Redact: %v", err)
			}
			if got := c.Verify(); got.Integrity != ChainBroken || got.BreakSeq != 1 {
				t.Errorf("Verify = %q at %d (%s), want broken at 1", got.Integrity, got.BreakSeq, got.Detail)
			}
			if got := c.VerifyRows()[1]; got != ChainBroken {
				t.Errorf("VerifyRows[1] = %q, want broken", got)
			}
		})
	}
}

// TestReceiptSigningInputNormalizesTheTimestamp pins the serialization
// that external verifiers reproduce: a nanosecond instant signs as its
// microsecond truncation in UTC, so the value a timestamptz column returns
// verifies, and the hashes and the sequence number keep their encodings.
//
// spec: §12.8 (Compliance Interfaces)
func TestReceiptSigningInputNormalizesTheTimestamp(t *testing.T) {
	t.Parallel()
	base := RedactionReceipt{
		ReceiptID: "r", AuditEventID: "a", TenantID: "acme", RedactedSeq: 42,
		OriginalHash: "0a", NewHash: "0b", ErasureJobID: "j", LegalBasis: "gdpr_art17",
		RedactorIdentity: "erasure-job:j",
	}
	nanos := base
	nanos.Timestamp = time.Date(2026, 11, 26, 10, 0, 0, 123456789, time.FixedZone("x", 3600))
	micros := base
	micros.Timestamp = time.Date(2026, 11, 26, 9, 0, 0, 123456000, time.UTC)
	a, err := ReceiptSigningInput(nanos)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ReceiptSigningInput(micros)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatalf("signing inputs differ:\n%s\n%s", a, b)
	}
	want := `{"audit_event_id":"a","erasure_job_id":"j","legal_basis":"gdpr_art17","new_hash":"0b","original_hash":"0a","receipt_id":"r","redactor_identity":"erasure-job:j","sequence_number":42,"tenant_id":"acme","timestamp":"2026-11-26T09:00:00.123456Z"}`
	if string(a) != want {
		t.Errorf("signing input = %s, want %s", a, want)
	}
	whole := base
	whole.Timestamp = time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
	c, _ := ReceiptSigningInput(whole)
	if !strings.Contains(string(c), `"timestamp":"2026-11-26T09:00:00Z"`) {
		t.Errorf("whole-second timestamp serialized with fractional digits: %s", c)
	}
	if got := ReceiptTimestamp(nanos.Timestamp); !got.Equal(micros.Timestamp) || got.Location() != time.UTC {
		t.Errorf("ReceiptTimestamp = %v, want %v in UTC", got, micros.Timestamp)
	}
}

// TestReceiptKeySetRefusesAnIncompleteKey covers the constructor guard and
// the sorted listing the admin route serves. It verifies no requirement.
func TestReceiptKeySetRefusesAnIncompleteKey(t *testing.T) {
	t.Parallel()
	if _, err := NewReceiptKeySet(ReceiptKey{KeyID: "", PublicKey: &receiptTestKey(t).PublicKey}); err == nil {
		t.Error("empty key id accepted")
	}
	if _, err := NewReceiptKeySet(ReceiptKey{KeyID: "k"}); err == nil {
		t.Error("nil public key accepted")
	}
	pub := &receiptTestKey(t).PublicKey
	set, err := NewReceiptKeySet(ReceiptKey{KeyID: "b", PublicKey: pub}, ReceiptKey{KeyID: "a", PublicKey: pub})
	if err != nil {
		t.Fatal(err)
	}
	keys := set.Keys()
	if len(keys) != 2 || keys[0].KeyID != "a" || keys[1].KeyID != "b" {
		t.Errorf("Keys = %+v, want a then b", keys)
	}
	var nilSet *ReceiptKeySet
	if nilSet.Keys() != nil {
		t.Error("nil set lists keys")
	}
	if err := nilSet.VerifyReceipt(RedactionReceipt{Signature: []byte{1}, KeyID: "a"}); !errors.Is(err, ErrReceiptKeyUnknown) {
		t.Errorf("nil set VerifyReceipt = %v, want ErrReceiptKeyUnknown", err)
	}
}
