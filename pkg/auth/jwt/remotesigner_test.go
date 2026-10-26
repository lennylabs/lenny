// SPDX-License-Identifier: MIT

package jwt_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/kms/kmstest"
	kmsstub "github.com/lennylabs/lenny/tests/testinfra/stubs/kms"
)

// shortSigProvider returns a signature of the wrong length.
type shortSigProvider struct{ kms.Provider }

func (shortSigProvider) Sign(context.Context, string, []byte) ([]byte, error) {
	return make([]byte, 63), nil
}

// recordingProvider records the alias and digest each Sign receives.
type recordingProvider struct {
	kms.Provider
	alias  string
	digest []byte
}

func (p *recordingProvider) Sign(ctx context.Context, alias string, digest []byte) ([]byte, error) {
	p.alias, p.digest = alias, append([]byte(nil), digest...)
	return p.Provider.Sign(ctx, alias, digest)
}

// spec: 10.2 (Authentication)
func TestRemoteSignerSendsSHA256DigestUnderSigningAlias_spec_10_2(t *testing.T) {
	stub := kmsstub.New(t)
	ks := loadStubKeySet(t, stub, nil)
	rec := &recordingProvider{Provider: stub.AsProvider()}
	signer, err := jwt.NewRemoteSigner(rec, ks.Signing())
	if err != nil {
		t.Fatalf("NewRemoteSigner: %v", err)
	}
	if signer.KeyID() != ks.Signing().KeyID {
		t.Fatalf("KeyID = %q, want %q", signer.KeyID(), ks.Signing().KeyID)
	}
	pubCalls := stub.PublicKeyCalls()
	tok, err := signer.Sign(context.Background(), ksClaims("alice@acme.com"))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if stub.PublicKeyCalls() != pubCalls {
		t.Fatal("the signer fetched a public key; it must build from the key set's entry")
	}
	if rec.alias != kms.AliasJWTSigning {
		t.Fatalf("Sign alias = %q, want %q", rec.alias, kms.AliasJWTSigning)
	}
	dot := strings.LastIndex(tok, ".")
	input := tok[:dot]
	want := sha256.Sum256([]byte(input))
	if string(rec.digest) != string(want[:]) {
		t.Fatal("the digest sent to the KMS is not the SHA-256 of the signing input")
	}
	sig, err := base64.RawURLEncoding.DecodeString(tok[dot+1:])
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	if !kmstest.VerifyRS(ks.Signing().PublicKey, want[:], sig) {
		t.Fatal("the token signature does not verify under the signing key")
	}
}

// spec: 10.2 (Authentication) "KMS signing failure mode"
func TestRemoteSignerPropagatesProviderFailures_spec_10_2(t *testing.T) {
	stub := kmsstub.New(t)
	ks := loadStubKeySet(t, stub, nil)
	signer, err := jwt.NewRemoteSigner(stub.AsProvider(), ks.Signing())
	if err != nil {
		t.Fatalf("NewRemoteSigner: %v", err)
	}

	stub.SetSignThrottled(true)
	if _, err := signer.Sign(context.Background(), ksClaims("a")); !errors.Is(err, kms.ErrThrottled) {
		t.Fatalf("throttled: err = %v, want kms.ErrThrottled", err)
	}
	stub.SetSignThrottled(false)

	hard := errors.New("kms outage")
	stub.SetSignError(hard)
	if _, err := signer.Sign(context.Background(), ksClaims("a")); !errors.Is(err, hard) {
		t.Fatalf("hard failure: err = %v, want %v", err, hard)
	}
	stub.SetSignError(nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	before := stub.SignCalls()
	if _, err := signer.Sign(ctx, ksClaims("a")); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context: err = %v, want context.Canceled", err)
	}
	if stub.SignCalls() != before {
		t.Fatal("a cancelled context still reached the KMS")
	}

	short, err := jwt.NewRemoteSigner(shortSigProvider{stub.AsProvider()}, ks.Signing())
	if err != nil {
		t.Fatalf("NewRemoteSigner: %v", err)
	}
	if _, err := short.Sign(context.Background(), ksClaims("a")); err == nil {
		t.Fatal("a 63-byte signature: want an error")
	}
}

// spec: 10.2 (Authentication)
func TestNewRemoteSignerRequiresProviderAndSigningEntry_spec_10_2(t *testing.T) {
	stub := kmsstub.New(t)
	ks := loadStubKeySet(t, stub, nil)
	if _, err := jwt.NewRemoteSigner(nil, ks.Signing()); err == nil {
		t.Fatal("nil provider: want an error")
	}
	if _, err := jwt.NewRemoteSigner(stub.AsProvider(), jwt.KeySetEntry{}); err == nil {
		t.Fatal("empty entry: want an error")
	}
	if _, err := jwt.NewRemoteSigner(stub.AsProvider(), jwt.KeySetEntry{KeyID: "k"}); err == nil {
		t.Fatal("entry without a public key: want an error")
	}
	// A RemoteSigner is not a verifier.
	var s any = &jwt.RemoteSigner{}
	if _, ok := s.(jwt.Verifier); ok {
		t.Fatal("RemoteSigner implements jwt.Verifier")
	}
}

// spec: 10.2 (Authentication)
func TestHMACSignerIsNotASigner_spec_10_2(t *testing.T) {
	var s any = jwt.NewHMACSigner("k", []byte("secret"))
	if _, ok := s.(jwt.Signer); ok {
		t.Fatal("HMACSigner satisfies jwt.Signer; production wiring could pass it as the Lenny-issued signer")
	}
}

// spec: 10.2 (Authentication)
func TestECPublicJWKThumbprintIsRFC7638_spec_10_2(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	j := mustJWK(t, &key.PublicKey)
	point, err := key.PublicKey.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	x := base64.RawURLEncoding.EncodeToString(point[1:33])
	y := base64.RawURLEncoding.EncodeToString(point[33:])
	if j.Kty != "EC" || j.Crv != "P-256" || j.X != x || j.Y != y || j.Alg != "ES256" {
		t.Fatalf("JWK = %+v", j)
	}
	canonical := `{"crv":"P-256","kty":"EC","x":"` + x + `","y":"` + y + `"}`
	sum := sha256.Sum256([]byte(canonical))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	got, err := j.Thumbprint()
	if err != nil {
		t.Fatalf("Thumbprint: %v", err)
	}
	if got != want {
		t.Fatalf("Thumbprint = %q, want %q", got, want)
	}
	// Optional members do not change the thumbprint.
	j.Kid, j.Use, j.Alg = "x", "", ""
	if again, _ := j.Thumbprint(); again != want {
		t.Fatalf("Thumbprint with optional members changed = %q", again)
	}
}

// spec: 10.2 (Authentication)
func TestECPublicJWKAndThumbprintRejectNonP256_spec_10_2(t *testing.T) {
	p384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if _, err := jwt.ECPublicJWK(&p384.PublicKey); err == nil {
		t.Fatal("P-384 key: want an error")
	}
	if _, err := jwt.ECPublicJWK(nil); err == nil {
		t.Fatal("nil key: want an error")
	}
	if _, err := (jwt.JWK{Kty: "oct", Kid: "k"}).Thumbprint(); err == nil {
		t.Fatal("oct JWK: want an error")
	}
	if _, err := (jwt.JWK{Kty: "EC", Crv: "P-256"}).Thumbprint(); err == nil {
		t.Fatal("EC JWK without coordinates: want an error")
	}
}
