// SPDX-License-Identifier: MIT

package deadletterredaction

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/kms"
)

// sharedLocal returns a local provider over the master key at keyFile with
// the receipt alias mapping refs, as one gateway replica resolves it.
func sharedLocal(t *testing.T, keyFile string, refs map[string]string) kms.Provider {
	t.Helper()
	local, err := kms.NewLocalFromKeyFile(keyFile)
	if err != nil {
		t.Fatalf("local kms: %v", err)
	}
	for alias, ref := range refs {
		local.SetSigningReference(alias, ref)
	}
	return local
}

// newMasterKeyFile writes a fresh local master key and returns its path.
func newMasterKeyFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "master.key")
	if _, err := kms.NewLocalFromKeyFile(path); err != nil {
		t.Fatalf("create master key: %v", err)
	}
	return path
}

// signedFixtureReceipt signs a receipt through svc's signer exactly as the
// redaction does, and returns it with the signature and key identifier.
func signedFixtureReceipt(t *testing.T, svc *Service) audit.RedactionReceipt {
	t.Helper()
	at := audit.ReceiptTimestamp(time.Date(2026, 11, 26, 10, 0, 0, 987654321, time.UTC))
	r := audit.RedactionReceipt{
		ReceiptID: "00000000-0000-4000-8000-0000000000a1", AuditEventID: "00000000-0000-4000-8000-000000000001",
		TenantID: "acme", RedactedSeq: 7, OriginalHash: "0a0b", NewHash: "0c0d",
		ErasureJobID: "00000000-0000-4000-8000-0000000000b1", LegalBasis: svc.legalBasis,
		RedactorIdentity: svc.redactor, Timestamp: at,
	}
	sig, keyID, err := svc.signReceipt(context.Background(), r.ReceiptID, r.AuditEventID, r.TenantID,
		r.RedactedSeq, r.OriginalHash, r.NewHash, r.ErasureJobID, at)
	if err != nil {
		t.Fatalf("signReceipt: %v", err)
	}
	r.Signature, r.KeyID = sig, keyID
	return r
}

// TestReceiptSignedOnOneReplicaVerifiesOnAnother pins that a receipt one
// gateway process signs verifies in a second process that shares only the
// KMS: each loads its own signer and key set, and neither holds a secret
// of the other.
//
// spec: §12.8 (Compliance Interfaces)
func TestReceiptSignedOnOneReplicaVerifiesOnAnother(t *testing.T) {
	keyFile := newMasterKeyFile(t)
	refs := map[string]string{kms.AliasAuditSigning: "audit-key-v1"}
	_, signerA, err := LoadReceiptSigning(context.Background(), sharedLocal(t, keyFile, refs), refs, nil)
	if err != nil {
		t.Fatalf("replica A: %v", err)
	}
	keysB, _, err := LoadReceiptSigning(context.Background(), sharedLocal(t, keyFile, refs), refs, nil)
	if err != nil {
		t.Fatalf("replica B: %v", err)
	}
	r := signedFixtureReceipt(t, newSvc(&fakeStore{}, &fakeEmitter{}, signerA))
	if r.KeyID != "audit-key-v1" {
		t.Errorf("signature_kms_key_id = %q, want the mapped reference audit-key-v1", r.KeyID)
	}
	if len(r.Signature) != 64 {
		t.Errorf("signature is %d bytes, want the 64-byte r||s encoding", len(r.Signature))
	}
	if err := keysB.VerifyReceipt(r); err != nil {
		t.Fatalf("replica B rejected replica A's receipt: %v", err)
	}
	// A process over another master key holds other keys and trusts none
	// of replica A's receipts.
	other := newMasterKeyFile(t)
	keysC, _, err := LoadReceiptSigning(context.Background(), sharedLocal(t, other, refs), refs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := keysC.VerifyReceipt(r); !errors.Is(err, audit.ErrReceiptSignature) {
		t.Errorf("a key set over another KMS verified the receipt: %v", err)
	}
}

// TestReceiptSignedBeforeARotationVerifiesAfterIt pins the rotation: once
// platform:audit-signing maps to a new key version and platform:audit-verify
// to the previous one, receipts signed before the rotation still verify,
// new receipts carry the new key identifier, and a key set that lost the
// previous mapping no longer trusts the old receipts.
//
// spec: §12.8 (Compliance Interfaces)
func TestReceiptSignedBeforeARotationVerifiesAfterIt(t *testing.T) {
	keyFile := newMasterKeyFile(t)
	before := map[string]string{kms.AliasAuditSigning: "audit-key-v1"}
	_, signerV1, err := LoadReceiptSigning(context.Background(), sharedLocal(t, keyFile, before), before, nil)
	if err != nil {
		t.Fatal(err)
	}
	old := signedFixtureReceipt(t, newSvc(&fakeStore{}, &fakeEmitter{}, signerV1))

	after := map[string]string{kms.AliasAuditSigning: "audit-key-v2", kms.AliasAuditVerify: "audit-key-v1"}
	keys, signerV2, err := LoadReceiptSigning(context.Background(), sharedLocal(t, keyFile, after), after, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := keys.VerifyReceipt(old); err != nil {
		t.Fatalf("receipt signed before the rotation: %v", err)
	}
	fresh := signedFixtureReceipt(t, newSvc(&fakeStore{}, &fakeEmitter{}, signerV2))
	if fresh.KeyID != "audit-key-v2" {
		t.Errorf("new receipt key id = %q, want audit-key-v2", fresh.KeyID)
	}
	if err := keys.VerifyReceipt(fresh); err != nil {
		t.Fatalf("receipt signed after the rotation: %v", err)
	}
	if got := len(keys.Keys()); got != 2 {
		t.Errorf("trusted keys = %d, want 2", got)
	}
	dropped := map[string]string{kms.AliasAuditSigning: "audit-key-v2"}
	keysDropped, _, err := LoadReceiptSigning(context.Background(), sharedLocal(t, keyFile, dropped), dropped, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := keysDropped.VerifyReceipt(old); !errors.Is(err, audit.ErrReceiptKeyUnknown) {
		t.Errorf("old receipt with no verify alias: %v, want ErrReceiptKeyUnknown", err)
	}
}

// TestReceiptSignedByARetiredListedKeyVerifiesAfterTwoRotations pins the
// retired-key list: after two rotations neither alias names the key that
// signed a receipt, and the receipt verifies while the key's identifier is
// listed, with the key read from the KMS by its identifier. A receipt under
// a key that is neither named nor listed does not verify, and the key route
// lists the retired key with the alias keys.
//
// spec: §12.8 (Compliance Interfaces)
func TestReceiptSignedByARetiredListedKeyVerifiesAfterTwoRotations(t *testing.T) {
	keyFile := newMasterKeyFile(t)
	v1 := map[string]string{kms.AliasAuditSigning: "audit-key-v1"}
	_, signerV1, err := LoadReceiptSigning(context.Background(), sharedLocal(t, keyFile, v1), v1, nil)
	if err != nil {
		t.Fatal(err)
	}
	old := signedFixtureReceipt(t, newSvc(&fakeStore{}, &fakeEmitter{}, signerV1))
	v4 := map[string]string{kms.AliasAuditSigning: "audit-key-v4"}
	_, signerV4, err := LoadReceiptSigning(context.Background(), sharedLocal(t, keyFile, v4), v4, nil)
	if err != nil {
		t.Fatal(err)
	}
	unlisted := signedFixtureReceipt(t, newSvc(&fakeStore{}, &fakeEmitter{}, signerV4))

	// Two rotations later: v3 signs, v2 verifies, and v1 is retired.
	v3 := map[string]string{kms.AliasAuditSigning: "audit-key-v3", kms.AliasAuditVerify: "audit-key-v2"}
	unlistedKeys, _, err := LoadReceiptSigning(context.Background(), sharedLocal(t, keyFile, v3), v3, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := unlistedKeys.VerifyReceipt(old); !errors.Is(err, audit.ErrReceiptKeyUnknown) {
		t.Errorf("receipt of an unlisted retired key: %v, want ErrReceiptKeyUnknown", err)
	}
	keys, _, err := LoadReceiptSigning(context.Background(), sharedLocal(t, keyFile, v3), v3, []string{"audit-key-v1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := keys.VerifyReceipt(old); err != nil {
		t.Fatalf("receipt of a listed retired key after two rotations: %v", err)
	}
	if err := keys.VerifyReceipt(unlisted); !errors.Is(err, audit.ErrReceiptKeyUnknown) {
		t.Errorf("receipt of a key outside the trusted set: %v, want ErrReceiptKeyUnknown", err)
	}
	var ids []string
	for _, k := range keys.Keys() {
		ids = append(ids, k.KeyID)
	}
	if want := []string{"audit-key-v1", "audit-key-v2", "audit-key-v3"}; strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Errorf("trusted keys = %v, want %v", ids, want)
	}
}

// TestARetiredReceiptKeyTheKMSCannotReadStopsTheLoad pins that a listed
// retired key whose public key the KMS cannot return stops the loader, so
// the gateway does not start with a trusted set short of a listed key.
//
// spec: §12.8 (Compliance Interfaces)
func TestARetiredReceiptKeyTheKMSCannotReadStopsTheLoad(t *testing.T) {
	provider, err := kms.NewLocalRandom()
	if err != nil {
		t.Fatal(err)
	}
	gone := retiredKeyGone{Provider: provider}
	if _, _, err := LoadReceiptSigning(context.Background(), gone, nil, []string{"audit-key-v0"}); !errors.Is(err, kms.ErrUnknownKEK) {
		t.Errorf("LoadReceiptSigning with an unreadable retired key = %v, want ErrUnknownKEK", err)
	}
	if _, _, err := LoadReceiptSigning(context.Background(), provider, nil, []string{""}); !errors.Is(err, kms.ErrUnknownKEK) {
		t.Errorf("LoadReceiptSigning with an empty retired key identifier = %v, want ErrUnknownKEK", err)
	}
}

// retiredKeyGone is a KMS that no longer holds any key by identifier.
type retiredKeyGone struct{ kms.Provider }

func (retiredKeyGone) PublicKeyByID(_ context.Context, keyID string) (*ecdsa.PublicKey, error) {
	return nil, fmt.Errorf("%w: %s", kms.ErrUnknownKEK, keyID)
}

// TestUnmappedReceiptAliasUsesItsNameUnderTheLocalProvider pins the
// development posture: with no --kms-alias entry the local provider derives
// the key from the alias name, which is the key identifier receipts carry.
//
// spec: §12.8 (Compliance Interfaces)
func TestUnmappedReceiptAliasUsesItsNameUnderTheLocalProvider(t *testing.T) {
	provider, err := kms.NewLocalRandom()
	if err != nil {
		t.Fatal(err)
	}
	keys, signer, err := LoadReceiptSigning(context.Background(), provider, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if signer.KeyID() != kms.AliasAuditSigning {
		t.Errorf("key id = %q, want %q", signer.KeyID(), kms.AliasAuditSigning)
	}
	r := signedFixtureReceipt(t, newSvc(&fakeStore{}, &fakeEmitter{}, signer))
	if err := keys.VerifyReceipt(r); err != nil {
		t.Fatal(err)
	}
}

// failingProvider is a KMS whose Sign and PublicKey fail.
type failingProvider struct {
	kms.Provider
	err error
}

func (f failingProvider) Sign(context.Context, string, []byte) ([]byte, error) { return nil, f.err }

func (f failingProvider) PublicKey(context.Context, string) (*ecdsa.PublicKey, error) {
	return nil, f.err
}

// TestReceiptSigningFailsClosedWhenTheKMSFails pins that a KMS failure
// stops the loader at startup and fails the redaction step, so no row is
// rewritten under an unsigned receipt.
//
// spec: §12.8 (Compliance Interfaces)
func TestReceiptSigningFailsClosedWhenTheKMSFails(t *testing.T) {
	down := failingProvider{err: kms.ErrThrottled}
	if _, _, err := LoadReceiptSigning(context.Background(), down, nil, nil); !errors.Is(err, kms.ErrThrottled) {
		t.Errorf("LoadReceiptSigning = %v, want the KMS error", err)
	}
	if _, _, err := LoadReceiptSigning(context.Background(), nil, nil, nil); err == nil {
		t.Error("nil provider accepted")
	}
	signer, err := NewKMSReceiptSigner(down, kms.AliasAuditSigning, "audit-key-v1")
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{rows: []audit.Row{{
		ID: "00000000-0000-4000-8000-000000000001", Seq: 1, TenantID: "acme",
		EventType: "session.created", Payload: json.RawMessage(`{"user_id":"alice@acme.com"}`),
	}}}
	n, err := newSvc(store, &fakeEmitter{}, signer).RedactForUser(context.Background(), "acme", "alice@acme.com")
	if !errors.Is(err, kms.ErrThrottled) || n != 0 {
		t.Errorf("RedactForUser = (%d, %v), want (0, the KMS error)", n, err)
	}
	if len(store.redacted) != 0 {
		t.Errorf("a row was rewritten without a signed receipt: %v", store.redacted)
	}
}

// TestNewKMSReceiptSignerRefusesAnIncompleteConfiguration covers the
// constructor guards. It verifies no requirement.
func TestNewKMSReceiptSignerRefusesAnIncompleteConfiguration(t *testing.T) {
	provider, err := kms.NewLocalRandom()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewKMSReceiptSigner(nil, kms.AliasAuditSigning, "k"); err == nil {
		t.Error("nil provider accepted")
	}
	if _, err := NewKMSReceiptSigner(provider, "", "k"); err == nil {
		t.Error("empty alias accepted")
	}
	if _, err := NewKMSReceiptSigner(provider, kms.AliasAuditSigning, ""); err == nil {
		t.Error("empty key id accepted")
	}
}
