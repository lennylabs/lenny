// SPDX-License-Identifier: MIT

package audit

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"time"

	"github.com/lennylabs/lenny/pkg/audit/jcs"
)

// receiptSignatureSize is the byte length of an ES256 receipt signature in
// the r||s encoding: two 32-byte big-endian integers.
const receiptSignatureSize = 64

// Sentinel errors a ReceiptVerifier returns. A chain verifier treats each
// one as a receipt that does not verify, so the redacted row reads broken.
var (
	// ErrReceiptUnsigned is returned for a receipt with no signature or no
	// key identifier.
	ErrReceiptUnsigned = errors.New("audit: redaction receipt carries no signature")

	// ErrReceiptKeyUnknown is returned for a receipt whose key identifier
	// names no key the verifier trusts.
	ErrReceiptKeyUnknown = errors.New("audit: redaction receipt signed under an untrusted key")

	// ErrReceiptSignature is returned for a receipt whose signature does
	// not verify over its canonical tuple.
	ErrReceiptSignature = errors.New("audit: redaction receipt signature does not verify")
)

// RedactionReceipt is the signed record attached to a row that a GDPR
// erasure rewrote in place. Its fields are the columns of
// audit_redaction_receipts. The signature covers the canonical
// serialization ReceiptSigningInput returns, and KeyID names the KMS key
// version that produced it.
type RedactionReceipt struct {
	// ReceiptID is the receipt's UUID (receipt_id).
	ReceiptID string `json:"receipt_id"`

	// AuditEventID is the id of the redacted audit_log row.
	AuditEventID string `json:"audit_event_id"`

	// TenantID is the redacted row's tenant.
	TenantID string `json:"tenant_id"`

	// RedactedSeq is the sequence number of the rewritten row.
	RedactedSeq uint64 `json:"redacted_seq"`

	// OriginalHash is the row's content hash before redaction, as
	// lowercase hex. The verifier uses it to confirm the receipt matches
	// the break it observes.
	OriginalHash string `json:"original_hash"`

	// NewHash is the row's content hash after redaction, as lowercase hex.
	NewHash string `json:"new_hash"`

	// ErasureJobID is the erasure job that performed the redaction.
	ErasureJobID string `json:"erasure_job_id"`

	// LegalBasis is the receipt's legal_basis enum value.
	LegalBasis string `json:"legal_basis"`

	// RedactorIdentity is the identity that authorized the redaction.
	RedactorIdentity string `json:"redactor_identity"`

	// Timestamp is the instant the payload rewrite committed. Signers
	// normalize it with ReceiptTimestamp so the value survives a
	// timestamptz round trip.
	Timestamp time.Time `json:"timestamp"`

	// Signature is the 64-byte ES256 r||s signature over the SHA-256 of
	// ReceiptSigningInput.
	Signature []byte `json:"signature"`

	// KeyID is signature_kms_key_id: the KMS key-version reference that
	// signed the receipt.
	KeyID string `json:"signature_kms_key_id"`
}

// ReceiptTimestamp returns t in UTC truncated to microseconds, the
// precision of a Postgres timestamptz. The erasure job stamps every
// receipt with a normalized instant, so the value a verifier reads back
// from audit_redaction_receipts serializes to the bytes that were signed.
//
// spec: §12.8 (Compliance Interfaces)
func ReceiptTimestamp(t time.Time) time.Time {
	return t.UTC().Truncate(time.Microsecond)
}

// ReceiptSigningInput returns the canonical serialization a receipt
// signature covers: the RFC 8785 JCS form of an object holding the
// receipt's ten signed fields, keyed by their column names. The hashes
// are lowercase hex strings, sequence_number is a number, and timestamp is
// RFC 3339 UTC with at most six fractional digits and no trailing zeros.
// The signer and every verifier use this one function.
//
// spec: §12.8 (Compliance Interfaces)
func ReceiptSigningInput(r RedactionReceipt) ([]byte, error) {
	tuple, err := json.Marshal(map[string]any{
		"receipt_id":        r.ReceiptID,
		"audit_event_id":    r.AuditEventID,
		"tenant_id":         r.TenantID,
		"sequence_number":   r.RedactedSeq,
		"original_hash":     r.OriginalHash,
		"new_hash":          r.NewHash,
		"erasure_job_id":    r.ErasureJobID,
		"legal_basis":       r.LegalBasis,
		"redactor_identity": r.RedactorIdentity,
		"timestamp":         ReceiptTimestamp(r.Timestamp).Format(time.RFC3339Nano),
	})
	if err != nil {
		return nil, fmt.Errorf("audit: encode receipt tuple: %w", err)
	}
	canon, err := jcs.Canonicalize(tuple)
	if err != nil {
		return nil, fmt.Errorf("audit: canonicalize receipt tuple: %w", err)
	}
	return canon, nil
}

// ReceiptMatchesRow reports whether rcpt is bound to the redacted row: it
// names the row's id and sequence number, its original_hash is the row's
// preserved pre-redaction hash (row.Hash), and its new_hash is content, the
// row's content hash recomputed from its current columns (ComputeHash). A
// receipt whose new_hash differs authorizes other content than the row
// holds, as after a second rewrite of a redacted row, so the row does not
// match. The chain verifier runs this check; the periodic receipt scan,
// which does not read the successor's prev_hash, checks the same new_hash
// binding without original_hash. Neither the tenant nor the signature is
// checked here.
//
// spec: §12.8 (Compliance Interfaces)
func ReceiptMatchesRow(rcpt RedactionReceipt, row Row, content string) bool {
	return rcpt.AuditEventID == row.ID && rcpt.RedactedSeq == row.Seq &&
		rcpt.OriginalHash == row.Hash && rcpt.NewHash == content
}

// ReceiptVerifier checks a receipt's signature. A chain verifier with no
// ReceiptVerifier trusts no receipt.
type ReceiptVerifier interface {
	VerifyReceipt(r RedactionReceipt) error
}

// ReceiptKey is one trusted receipt signing key: its KMS key identifier and
// its P-256 public key.
type ReceiptKey struct {
	KeyID     string
	PublicKey *ecdsa.PublicKey
}

// ReceiptKeySet is the set of receipt signing keys a verifier trusts,
// indexed by KMS key identifier. It holds public keys only and never calls
// the KMS. It is immutable after construction and safe for concurrent use.
type ReceiptKeySet struct {
	keys map[string]*ecdsa.PublicKey
}

// NewReceiptKeySet returns a key set over keys. A key with an empty
// identifier or a nil public key is refused.
func NewReceiptKeySet(keys ...ReceiptKey) (*ReceiptKeySet, error) {
	set := &ReceiptKeySet{keys: make(map[string]*ecdsa.PublicKey, len(keys))}
	for _, k := range keys {
		if k.KeyID == "" || k.PublicKey == nil {
			return nil, errors.New("audit: receipt key needs a key identifier and a public key")
		}
		set.keys[k.KeyID] = k.PublicKey
	}
	return set, nil
}

// Keys returns the trusted keys sorted by key identifier.
func (s *ReceiptKeySet) Keys() []ReceiptKey {
	if s == nil {
		return nil
	}
	out := make([]ReceiptKey, 0, len(s.keys))
	for id, pub := range s.keys {
		out = append(out, ReceiptKey{KeyID: id, PublicKey: pub})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].KeyID < out[j].KeyID })
	return out
}

// VerifyReceipt verifies r's ES256 signature under the trusted key its
// KeyID names. It returns ErrReceiptUnsigned for a receipt with no
// signature or key identifier, ErrReceiptKeyUnknown for a key identifier
// outside the set, and ErrReceiptSignature when the signature does not
// verify over the canonical tuple, so a changed field fails as a forged
// signature does. A nil set trusts nothing.
//
// spec: §12.8 (Compliance Interfaces), §11.7 (Audit Logging)
func (s *ReceiptKeySet) VerifyReceipt(r RedactionReceipt) error {
	if len(r.Signature) == 0 || r.KeyID == "" {
		return ErrReceiptUnsigned
	}
	if s == nil {
		return ErrReceiptKeyUnknown
	}
	pub, ok := s.keys[r.KeyID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrReceiptKeyUnknown, r.KeyID)
	}
	if len(r.Signature) != receiptSignatureSize {
		return fmt.Errorf("%w: signature is %d bytes, want %d", ErrReceiptSignature, len(r.Signature), receiptSignatureSize)
	}
	input, err := ReceiptSigningInput(r)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrReceiptSignature, err)
	}
	digest := sha256.Sum256(input)
	rInt := new(big.Int).SetBytes(r.Signature[:receiptSignatureSize/2])
	sInt := new(big.Int).SetBytes(r.Signature[receiptSignatureSize/2:])
	if !ecdsa.Verify(pub, digest[:], rInt, sInt) {
		return ErrReceiptSignature
	}
	return nil
}
