// SPDX-License-Identifier: MIT

package audit

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"sync"
	"testing"
	"time"
)

// testReceiptKeyID is the key identifier the test signing key carries.
const testReceiptKeyID = "test-audit-signing-v1"

var (
	testReceiptKeyOnce sync.Once
	testReceiptKey     *ecdsa.PrivateKey
	testReceiptKeyErr  error
)

// receiptTestKey returns the package's shared ES256 test signing key.
func receiptTestKey(t testing.TB) *ecdsa.PrivateKey {
	t.Helper()
	testReceiptKeyOnce.Do(func() {
		testReceiptKey, testReceiptKeyErr = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	})
	if testReceiptKeyErr != nil {
		t.Fatalf("generate test receipt key: %v", testReceiptKeyErr)
	}
	return testReceiptKey
}

// receiptTestKeySet returns a key set trusting the shared test key.
func receiptTestKeySet(t testing.TB) *ReceiptKeySet {
	t.Helper()
	set, err := NewReceiptKeySet(ReceiptKey{KeyID: testReceiptKeyID, PublicKey: &receiptTestKey(t).PublicKey})
	if err != nil {
		t.Fatalf("NewReceiptKeySet: %v", err)
	}
	return set
}

// signReceiptWith signs r under key and stamps keyID, as the erasure job's
// KMS signer does.
func signReceiptWith(t testing.TB, key *ecdsa.PrivateKey, keyID string, r RedactionReceipt) RedactionReceipt {
	t.Helper()
	r.KeyID = keyID
	input, err := ReceiptSigningInput(r)
	if err != nil {
		t.Fatalf("ReceiptSigningInput: %v", err)
	}
	digest := sha256.Sum256(input)
	rInt, sInt, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatalf("ecdsa.Sign: %v", err)
	}
	sig := make([]byte, receiptSignatureSize)
	rInt.FillBytes(sig[:receiptSignatureSize/2])
	sInt.FillBytes(sig[receiptSignatureSize/2:])
	r.Signature = sig
	return r
}

// lawfulReceipt returns a receipt for row signed under the shared test
// key, binding the row's tenant, id, sequence, and preserved hash.
func lawfulReceipt(t testing.TB, row Row) RedactionReceipt {
	t.Helper()
	return signReceiptWith(t, receiptTestKey(t), testReceiptKeyID, RedactionReceipt{
		ReceiptID:        "00000000-0000-4000-8000-0000000000aa",
		AuditEventID:     row.ID,
		TenantID:         row.TenantID,
		RedactedSeq:      row.Seq,
		OriginalHash:     row.Hash,
		NewHash:          "ab",
		ErasureJobID:     "00000000-0000-4000-8000-0000000000bb",
		LegalBasis:       "gdpr_art17",
		RedactorIdentity: "erasure-job:test",
		Timestamp:        time.Date(2026, 11, 26, 10, 0, 0, 123456789, time.UTC),
	})
}
