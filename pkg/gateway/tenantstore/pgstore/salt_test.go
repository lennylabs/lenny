// SPDX-License-Identifier: MIT

package pgstore

import (
	"bytes"
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/kms"
)

// sealSalt/openSalt run entirely against the KMS provider and never
// touch the pool, so a nil-pool Store exercises the §12.8 envelope path
// without Postgres.

// spec: §12.8 lines 845-846 — the erasure_salt is envelope-encrypted at
// rest and round-trips back to the original 256-bit value. F-12.8.5.
func TestSaltSealOpenRoundTrip_spec_12_8_845(t *testing.T) {
	prov, err := kms.NewLocalRandom()
	if err != nil {
		t.Fatalf("kms: %v", err)
	}
	s := New(nil, WithKMS(prov))
	ctx := context.Background()
	salt := bytes.Repeat([]byte{0xA5}, 32)

	blob, err := s.sealSalt(ctx, "acme", salt)
	if err != nil {
		t.Fatalf("sealSalt: %v", err)
	}
	if len(blob) == 0 {
		t.Fatal("sealed blob is empty")
	}
	if bytes.Contains(blob, salt) {
		t.Fatal("§12.8 line 845: sealed blob must not contain the plaintext salt")
	}

	got, err := s.openSalt(ctx, "acme", blob)
	if err != nil {
		t.Fatalf("openSalt: %v", err)
	}
	if !bytes.Equal(got, salt) {
		t.Fatalf("round-trip mismatch: got %x want %x", got, salt)
	}
}

// spec: §12.8 line 850 — a destroyed (empty) salt seals to a NULL column
// and opens back to nil. F-12.8.5.
func TestSaltEmptySealsToNull_spec_12_8_850(t *testing.T) {
	prov, _ := kms.NewLocalRandom()
	s := New(nil, WithKMS(prov))
	ctx := context.Background()

	blob, err := s.sealSalt(ctx, "acme", nil)
	if err != nil {
		t.Fatalf("sealSalt(nil): %v", err)
	}
	if blob != nil {
		t.Fatalf("empty salt must seal to a nil/NULL blob, got %x", blob)
	}
	got, err := s.openSalt(ctx, "acme", nil)
	if err != nil {
		t.Fatalf("openSalt(nil): %v", err)
	}
	if got != nil {
		t.Fatalf("NULL blob must open to nil salt, got %x", got)
	}
}

// spec: §12.8 line 845 — a non-empty salt write with no KMS provider is
// rejected rather than persisted in plaintext. F-12.8.5.
func TestSaltSealFailsClosedWithoutKMS_spec_12_8_845(t *testing.T) {
	s := New(nil) // no WithKMS
	ctx := context.Background()

	if _, err := s.sealSalt(ctx, "acme", bytes.Repeat([]byte{1}, 32)); err == nil {
		t.Fatal("sealSalt must fail closed when no KMS provider is wired")
	}
	// An empty salt is still allowed (it writes NULL), even without KMS.
	if _, err := s.sealSalt(ctx, "acme", nil); err != nil {
		t.Fatalf("sealSalt(nil) without KMS should be a no-op: %v", err)
	}
}

// spec: §12.8 lines 845-846 — the KEK alias binds the ciphertext to the
// tenant, so a blob sealed for one tenant does not open under another's
// alias. F-12.8.5.
func TestSaltCrossTenantOpenRejected_spec_12_8_846(t *testing.T) {
	prov, _ := kms.NewLocalRandom()
	s := New(nil, WithKMS(prov))
	ctx := context.Background()
	salt := bytes.Repeat([]byte{0x3C}, 32)

	blob, err := s.sealSalt(ctx, "acme", salt)
	if err != nil {
		t.Fatalf("sealSalt: %v", err)
	}
	if _, err := s.openSalt(ctx, "globex", blob); err == nil {
		t.Fatal("§12.8 line 846: salt sealed for acme must not open under globex's KEK alias")
	}
}
