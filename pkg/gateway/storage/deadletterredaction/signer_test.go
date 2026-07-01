// SPDX-License-Identifier: MIT

package deadletterredaction

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/kms"
)

// The HMAC signer produces a deterministic, non-empty signature and
// reports its key id. spec: §12.8 line 810 (the receipt signature is the
// provenance token that authenticates a redaction).
func TestHMACReceiptSigner_deterministic(t *testing.T) {
	t.Parallel()
	signer := NewHMACReceiptSigner("boot", []byte("0123456789abcdef0123456789abcdef"))
	sig1, keyID, err := signer.Sign(context.Background(), []byte("receipt-tuple"))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if keyID != "boot" {
		t.Errorf("keyID = %q, want boot", keyID)
	}
	if len(sig1) == 0 {
		t.Fatal("signature is empty; the missing-receipt detector would page on it")
	}
	sig2, _, _ := signer.Sign(context.Background(), []byte("receipt-tuple"))
	if string(sig1) != string(sig2) {
		t.Error("signature is not deterministic over identical input")
	}
	other, _, _ := signer.Sign(context.Background(), []byte("different"))
	if string(sig1) == string(other) {
		t.Error("distinct messages produced identical signatures")
	}
}

// An empty-key signer refuses to sign rather than emitting an empty
// signature a verifier would treat as a missing receipt.
func TestHMACReceiptSigner_noKey(t *testing.T) {
	t.Parallel()
	if _, _, err := NewHMACReceiptSigner("boot", nil).Sign(context.Background(), []byte("x")); err == nil {
		t.Fatal("expected error signing with no key")
	}
}

// The KMS-backed constructor unwraps a signing key through the §4 KMS
// provider and signs with it. spec: §12.8 line 810.
func TestNewKMSReceiptSigner_localProvider(t *testing.T) {
	t.Parallel()
	provider, err := kms.NewLocalRandom()
	if err != nil {
		t.Fatalf("local kms: %v", err)
	}
	signer, err := NewKMSReceiptSigner(context.Background(), provider, "platform:audit-redaction-signing", "boot")
	if err != nil {
		t.Fatalf("NewKMSReceiptSigner: %v", err)
	}
	sig, keyID, err := signer.Sign(context.Background(), []byte("tuple"))
	if err != nil || len(sig) == 0 || keyID != "boot" {
		t.Fatalf("Sign = (%d bytes, %q, %v), want non-empty/boot/nil", len(sig), keyID, err)
	}
}

// A nil provider is rejected.
func TestNewKMSReceiptSigner_nilProvider(t *testing.T) {
	t.Parallel()
	if _, err := NewKMSReceiptSigner(context.Background(), nil, "alias", "boot"); err == nil {
		t.Fatal("expected error for nil provider")
	}
}
