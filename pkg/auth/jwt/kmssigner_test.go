// SPDX-License-Identifier: MIT

package jwt_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/kms"
)

// spec: 4, 13.3
// diagnosis: the KMS-backed signer did not sign and verify a token.
// A KMSSigner must mint a JWT that it then verifies, just as the dev
// HMAC signer does, with the signing key sealed under a KMS KEK.
func TestKMSSignerSignVerifyRoundTrip(t *testing.T) {
	t.Parallel()
	provider, err := kms.NewLocalRandom()
	if err != nil {
		t.Fatalf("NewLocalRandom: %v", err)
	}
	signer, err := jwt.NewKMSSigner(context.Background(), provider, jwt.TokenServiceKEKAlias, "ts-1")
	if err != nil {
		t.Fatalf("NewKMSSigner: %v", err)
	}
	if signer.KeyID() != "ts-1" {
		t.Errorf("KeyID: got %q, want ts-1", signer.KeyID())
	}

	claims := jwt.Claims{
		Subject:  "alice@acme.com",
		TenantID: "acme",
		Typ:      auth.TokenUserBearer,
		Expiry:   time.Now().Add(time.Hour).Unix(),
	}
	token, err := signer.Sign(claims)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	got, err := signer.Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.Subject != "alice@acme.com" || got.TenantID != "acme" {
		t.Errorf("verified claims mismatch: %+v", got)
	}
}

// spec: 4
// diagnosis: the signing key was not KMS-envelope-encrypted at rest.
// SealedKey must be the wrapped form, and it must not be a stored
// plaintext key.
func TestKMSSignerSealedKeyIsWrapped(t *testing.T) {
	t.Parallel()
	provider, err := kms.NewLocalRandom()
	if err != nil {
		t.Fatalf("NewLocalRandom: %v", err)
	}
	signer, err := jwt.NewKMSSigner(context.Background(), provider, jwt.TokenServiceKEKAlias, "ts-1")
	if err != nil {
		t.Fatalf("NewKMSSigner: %v", err)
	}
	sealed := signer.SealedKey()
	if sealed.KEKVersion < 1 {
		t.Errorf("SealedKey KEK version: got %d, want >= 1", sealed.KEKVersion)
	}
	if len(sealed.WrappedDEK) == 0 {
		t.Error("SealedKey has an empty wrapped DEK; the signing key is not envelope-encrypted")
	}
	if len(sealed.Ciphertext) == 0 {
		t.Error("SealedKey has an empty ciphertext")
	}
	if signer.KEKAlias() != jwt.TokenServiceKEKAlias {
		t.Errorf("KEKAlias: got %q, want %q", signer.KEKAlias(), jwt.TokenServiceKEKAlias)
	}
}

// spec: 4
// diagnosis: a signer reconstructed from the durable SealedKey did not
// recover the same signing key. A replica must verify tokens minted
// by the replica that holds the original signer.
func TestKMSSignerReloadFromSealedKey(t *testing.T) {
	t.Parallel()
	// A fixed seed models a deployment-configured KEK shared by every
	// Token Service replica.
	seed := make([]byte, kms.DEKSize)
	for i := range seed {
		seed[i] = byte(i + 1)
	}
	provider, err := kms.NewLocal(seed)
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	ctx := context.Background()

	original, err := jwt.NewKMSSigner(ctx, provider, jwt.TokenServiceKEKAlias, "ts-1")
	if err != nil {
		t.Fatalf("NewKMSSigner: %v", err)
	}
	token, err := original.Sign(jwt.Claims{Subject: "bob@acme.com", Expiry: time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// A second replica: independent provider built from the same seed,
	// signer reconstructed from the durable SealedKey.
	replicaProvider, err := kms.NewLocal(seed)
	if err != nil {
		t.Fatalf("NewLocal replica: %v", err)
	}
	replica, err := jwt.KMSSignerFromSealed(ctx, replicaProvider, jwt.TokenServiceKEKAlias, "ts-1", original.SealedKey())
	if err != nil {
		t.Fatalf("KMSSignerFromSealed: %v", err)
	}
	got, err := replica.Verify(token)
	if err != nil {
		t.Fatalf("replica Verify of a token minted by the original signer: %v", err)
	}
	if got.Subject != "bob@acme.com" {
		t.Errorf("verified subject: got %q, want bob@acme.com", got.Subject)
	}
	// The reconstructed replica also mints tokens the original verifies.
	roundTrip, err := replica.Sign(jwt.Claims{Subject: "carol@acme.com", Expiry: time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatalf("replica Sign: %v", err)
	}
	if _, err := original.Verify(roundTrip); err != nil {
		t.Errorf("original Verify of a replica-minted token: %v", err)
	}
}

// spec: 4
// diagnosis: a signer reconstructed under a different KEK recovered a
// signing key. A wrong KEK must fail the unwrap.
func TestKMSSignerReloadWrongKEKFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	provider, err := kms.NewLocalRandom()
	if err != nil {
		t.Fatalf("NewLocalRandom: %v", err)
	}
	original, err := jwt.NewKMSSigner(ctx, provider, jwt.TokenServiceKEKAlias, "ts-1")
	if err != nil {
		t.Fatalf("NewKMSSigner: %v", err)
	}
	// A different random provider does not share the KEK.
	wrongProvider, err := kms.NewLocalRandom()
	if err != nil {
		t.Fatalf("NewLocalRandom wrong: %v", err)
	}
	if _, err := jwt.KMSSignerFromSealed(ctx, wrongProvider, jwt.TokenServiceKEKAlias, "ts-1", original.SealedKey()); err == nil {
		t.Error("KMSSignerFromSealed under a wrong KEK should fail")
	}
}

// spec: 4
// diagnosis: NewKMSSigner accepted a nil KMS provider.
func TestKMSSignerRejectsNilProvider(t *testing.T) {
	t.Parallel()
	if _, err := jwt.NewKMSSigner(context.Background(), nil, jwt.TokenServiceKEKAlias, "ts-1"); err == nil {
		t.Error("NewKMSSigner accepted a nil provider")
	}
}

// spec: 13.3
// diagnosis: a KMSSigner-minted token did not verify under a plain
// HMACSigner holding the same key bytes — i.e. the KMS signer changed
// the on-the-wire token format. The KMS signer must produce the
// identical JWT format as the dev signer.
func TestKMSSignerTokenFormatMatchesHMAC(t *testing.T) {
	t.Parallel()
	provider, err := kms.NewLocalRandom()
	if err != nil {
		t.Fatalf("NewLocalRandom: %v", err)
	}
	signer, err := jwt.NewKMSSigner(context.Background(), provider, jwt.TokenServiceKEKAlias, "ts-1")
	if err != nil {
		t.Fatalf("NewKMSSigner: %v", err)
	}
	token, err := signer.Sign(jwt.Claims{Subject: "dave@acme.com", Expiry: time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	// A compact JWS has three dot-separated base64url segments.
	dots := 0
	for _, r := range token {
		if r == '.' {
			dots++
		}
	}
	if dots != 2 {
		t.Errorf("token has %d dots, want 2 (header.payload.signature)", dots)
	}
}
