// SPDX-License-Identifier: MIT

package admin_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
)

// testReceiptKeyID names the ES256 key the audit-query tests sign receipts
// with.
const testReceiptKeyID = "admin-test-audit-key"

// testReceiptKey and testReceiptKeys are the signing key and the key set
// the crafted routers trust.
var (
	testReceiptKey  = mustReceiptKey()
	testReceiptKeys = mustReceiptKeySet(testReceiptKey)
)

func mustReceiptKey() *ecdsa.PrivateKey {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	return k
}

func mustReceiptKeySet(k *ecdsa.PrivateKey) *audit.ReceiptKeySet {
	set, err := audit.NewReceiptKeySet(audit.ReceiptKey{KeyID: testReceiptKeyID, PublicKey: &k.PublicKey})
	if err != nil {
		panic(err)
	}
	return set
}

// signedTestReceipt returns a receipt for the redacted row, bound to its
// preserved hash and to its current content hash as new_hash,
// signed under the test key, as the erasure job writes one.
func signedTestReceipt(row audit.Row) audit.RedactionReceipt {
	r := audit.RedactionReceipt{
		ReceiptID: "00000000-0000-4000-8000-0000000000a1", AuditEventID: row.ID, TenantID: row.TenantID,
		RedactedSeq: row.Seq, OriginalHash: row.Hash, NewHash: audit.ComputeHash(row),
		ErasureJobID: "00000000-0000-4000-8000-0000000000b1", LegalBasis: "gdpr_art17",
		RedactorIdentity: "erasure-job:test", Timestamp: time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC),
		KeyID: testReceiptKeyID,
	}
	input, err := audit.ReceiptSigningInput(r)
	if err != nil {
		panic(err)
	}
	digest := sha256.Sum256(input)
	rInt, sInt, err := ecdsa.Sign(rand.Reader, testReceiptKey, digest[:])
	if err != nil {
		panic(err)
	}
	sig := make([]byte, 64)
	rInt.FillBytes(sig[:32])
	sInt.FillBytes(sig[32:])
	r.Signature = sig
	return r
}
