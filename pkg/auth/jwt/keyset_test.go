// SPDX-License-Identifier: MIT

package jwt_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/kms"
	kmsstub "github.com/lennylabs/lenny/tests/testinfra/stubs/kms"
)

// ksSeed is a fixed 32-byte local-provider master key, so two providers
// built from it model two processes sharing one master key Secret.
var ksSeed = bytes.Repeat([]byte{0x5a}, kms.DEKSize)

// ksClaims returns a far-future user bearer for subject.
func ksClaims(subject string) jwt.Claims {
	return jwt.Claims{
		Issuer:   "https://lenny.example/gateway",
		Subject:  subject,
		TenantID: "acme",
		Expiry:   time.Now().Add(time.Hour).Unix(),
		IssuedAt: time.Now().Unix(),
		Typ:      auth.TokenUserBearer,
	}
}

// loadStubKeySet loads a key set from the testinfra KMS stub.
func loadStubKeySet(t *testing.T, stub *kmsstub.Stub, aliasToKey map[string]string) *jwt.KeySet {
	t.Helper()
	ks, err := jwt.LoadKeySet(context.Background(), stub.AsProvider(), aliasToKey)
	if err != nil {
		t.Fatalf("LoadKeySet: %v", err)
	}
	return ks
}

// remoteSign signs c through a RemoteSigner built from ks's signing entry.
func remoteSign(t *testing.T, provider kms.Provider, ks *jwt.KeySet, c jwt.Claims) string {
	t.Helper()
	signer, err := jwt.NewRemoteSigner(provider, ks.Signing())
	if err != nil {
		t.Fatalf("NewRemoteSigner: %v", err)
	}
	tok, err := signer.Sign(context.Background(), c)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return tok
}

// b64 is the base64url encoding JWS segments use.
func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// handToken assembles a compact token from a header map, claims, and a
// signing function over the signing input.
func handToken(t *testing.T, header map[string]any, c jwt.Claims, sign func(input string) []byte) string {
	t.Helper()
	h, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	p, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	input := b64(h) + "." + b64(p)
	return input + "." + b64(sign(input))
}

// hs256 returns an HMAC-SHA256 signing function under key.
func hs256(key []byte) func(string) []byte {
	return func(input string) []byte {
		mac := hmac.New(sha256.New, key)
		mac.Write([]byte(input))
		return mac.Sum(nil)
	}
}

// wantReason asserts err is a *jwt.VerifyError with the given reason.
func wantReason(t *testing.T, err error, reason string) {
	t.Helper()
	var ve *jwt.VerifyError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v, want *VerifyError with reason %q", err, reason)
	}
	if ve.Reason != reason {
		t.Fatalf("reason = %q (%v), want %q", ve.Reason, err, reason)
	}
}

// decodeHeader returns the JOSE header of a compact token.
func decodeHeader(t *testing.T, tok string) map[string]string {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(strings.SplitN(tok, ".", 2)[0])
	if err != nil {
		t.Fatalf("decode header: %v", err)
	}
	var h map[string]string
	if err := json.Unmarshal(raw, &h); err != nil {
		t.Fatalf("header JSON: %v", err)
	}
	return h
}

// spec: 10.2 (Authentication)
func TestKeySetVerifiesRemoteES256Token_spec_10_2(t *testing.T) {
	stub := kmsstub.New(t)
	ks := loadStubKeySet(t, stub, nil)
	in := ksClaims("alice@acme.com")
	in.Roles = []auth.Role{auth.RoleUser}
	tok := remoteSign(t, stub.AsProvider(), ks, in)

	h := decodeHeader(t, tok)
	pub, err := stub.AsProvider().PublicKey(context.Background(), kms.AliasJWTSigning)
	if err != nil {
		t.Fatalf("PublicKey: %v", err)
	}
	jwk, err := jwt.ECPublicJWK(pub)
	if err != nil {
		t.Fatalf("ECPublicJWK: %v", err)
	}
	wantKid, err := jwk.Thumbprint()
	if err != nil {
		t.Fatalf("Thumbprint: %v", err)
	}
	if h["alg"] != "ES256" || h["typ"] != "JWT" || h["kid"] != wantKid || len(h) != 3 {
		t.Fatalf("header = %v, want alg ES256, typ JWT, kid %s", h, wantKid)
	}
	if ks.Signing().KeyID != wantKid {
		t.Fatalf("signing kid = %q, want %q", ks.Signing().KeyID, wantKid)
	}
	if stub.SignCalls() != 1 {
		t.Fatalf("SignCalls = %d, want 1", stub.SignCalls())
	}

	got, err := ks.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.Subject != in.Subject || got.TenantID != in.TenantID || got.Typ != in.Typ || !got.HasRole(auth.RoleUser) {
		t.Fatalf("claims = %+v, want %+v", got, in)
	}
}

// spec: 10.2 (Authentication)
func TestKeySetRejectsNonES256Alg_spec_10_2(t *testing.T) {
	stub := kmsstub.New(t)
	ks := loadStubKeySet(t, stub, nil)
	kid := ks.Signing().KeyID
	pub := ks.Signing().PublicKey
	point, err := pub.Bytes()
	if err != nil {
		t.Fatalf("public key bytes: %v", err)
	}
	pkix, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("marshal PKIX: %v", err)
	}
	jwkJSON, err := json.Marshal(mustJWK(t, pub))
	if err != nil {
		t.Fatalf("marshal JWK: %v", err)
	}
	c := ksClaims("mallory@acme.com")
	cases := []struct {
		name string
		tok  string
	}{
		// The algorithm-confusion attack: an HS256 token keyed with the
		// public key a verifier holds, stamped with the real kid.
		{"HS256 keyed with the uncompressed public point", handToken(t, map[string]any{"alg": "HS256", "typ": "JWT", "kid": kid}, c, hs256(point))},
		{"HS256 keyed with the PKIX public key", handToken(t, map[string]any{"alg": "HS256", "typ": "JWT", "kid": kid}, c, hs256(pkix))},
		{"HS256 keyed with the public JWK", handToken(t, map[string]any{"alg": "HS256", "typ": "JWT", "kid": kid}, c, hs256(jwkJSON))},
		{"HS256 with an arbitrary secret", handToken(t, map[string]any{"alg": "HS256", "kid": kid}, c, hs256([]byte("secret")))},
		{"alg none with an empty signature", handToken(t, map[string]any{"alg": "none", "kid": kid}, c, func(string) []byte { return nil })},
		{"alg None", handToken(t, map[string]any{"alg": "None", "kid": kid}, c, func(string) []byte { return nil })},
		{"lower-case es256", handToken(t, map[string]any{"alg": "es256", "kid": kid}, c, func(string) []byte { return make([]byte, 64) })},
		{"ES384", handToken(t, map[string]any{"alg": "ES384", "kid": kid}, c, func(string) []byte { return make([]byte, 96) })},
		{"RS256", handToken(t, map[string]any{"alg": "RS256", "kid": kid}, c, func(string) []byte { return make([]byte, 256) })},
		{"no alg", handToken(t, map[string]any{"kid": kid}, c, func(string) []byte { return make([]byte, 64) })},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ks.Verify(tc.tok)
			wantReason(t, err, "unsupported_alg")
		})
	}
}

func mustJWK(t *testing.T, pub *ecdsa.PublicKey) jwt.JWK {
	t.Helper()
	j, err := jwt.ECPublicJWK(pub)
	if err != nil {
		t.Fatalf("ECPublicJWK: %v", err)
	}
	return j
}

// spec: 10.2 (Authentication) "Verifier key set and rotation"
func TestKeySetKidRejectionsMakeNoProviderCall_spec_10_2(t *testing.T) {
	stub := kmsstub.New(t)
	ks := loadStubKeySet(t, stub, map[string]string{kms.AliasJWTVerify: "ref-old"})
	pubCalls, signCalls := stub.PublicKeyCalls(), stub.SignCalls()
	if pubCalls != 2 {
		t.Fatalf("PublicKeyCalls after load = %d, want 2", pubCalls)
	}
	c := ksClaims("mallory@acme.com")
	sig := func(string) []byte { return make([]byte, 64) }
	cases := []struct {
		name, reason, tok string
	}{
		{"no kid", "missing_kid", handToken(t, map[string]any{"alg": "ES256", "typ": "JWT"}, c, sig)},
		{"empty kid", "missing_kid", handToken(t, map[string]any{"alg": "ES256", "kid": ""}, c, sig)},
		{"kid outside the set", "unknown_kid", handToken(t, map[string]any{"alg": "ES256", "kid": "attacker-chosen"}, c, sig)},
		{"KMS reference as kid", "unknown_kid", handToken(t, map[string]any{"alg": "ES256", "kid": kms.AliasJWTSigning}, c, sig)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ks.Verify(tc.tok)
			wantReason(t, err, tc.reason)
			if !jwt.IsUnknownKey(err) {
				t.Fatalf("IsUnknownKey(%v) = false", err)
			}
		})
	}
	if got := stub.PublicKeyCalls(); got != pubCalls {
		t.Fatalf("PublicKeyCalls = %d after kid rejections, want %d (no fetch on an unknown kid)", got, pubCalls)
	}
	if got := stub.SignCalls(); got != signCalls {
		t.Fatalf("SignCalls = %d after kid rejections, want %d", got, signCalls)
	}
}

// spec: 10.2 (Authentication) "Verifier key set and rotation"
func TestKeySetAcceptsVerifyKeyAcrossRotation_spec_10_2(t *testing.T) {
	ctx := context.Background()
	// Before the rotation: platform:jwt-signing maps to ref-1.
	before, err := kms.NewLocal(ksSeed)
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	before.SetSigningReference(kms.AliasJWTSigning, "ref-1")
	oldSet, err := jwt.LoadKeySet(ctx, before, map[string]string{kms.AliasJWTSigning: "ref-1"})
	if err != nil {
		t.Fatalf("LoadKeySet before rotation: %v", err)
	}
	oldTok := remoteSign(t, before, oldSet, ksClaims("alice@acme.com"))

	// After the rollout: ref-2 signs and ref-1 only verifies, on a process
	// that shares the master key.
	after, err := kms.NewLocal(ksSeed)
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	aliases := map[string]string{kms.AliasJWTSigning: "ref-2", kms.AliasJWTVerify: "ref-1"}
	after.SetSigningReference(kms.AliasJWTSigning, "ref-2")
	after.SetSigningReference(kms.AliasJWTVerify, "ref-1")
	newSet, err := jwt.LoadKeySet(ctx, after, aliases)
	if err != nil {
		t.Fatalf("LoadKeySet after rotation: %v", err)
	}
	if newSet.Len() != 2 {
		t.Fatalf("Len = %d, want 2", newSet.Len())
	}
	if newSet.Signing().KeyID == oldSet.Signing().KeyID {
		t.Fatal("the signing kid did not change with the signing reference")
	}
	if _, err := newSet.Verify(oldTok); err != nil {
		t.Fatalf("a token signed by the verify-only key: Verify: %v", err)
	}
	newTok := remoteSign(t, after, newSet, ksClaims("bob@acme.com"))
	if _, err := newSet.Verify(newTok); err != nil {
		t.Fatalf("a token signed by the new signing key: Verify: %v", err)
	}
	if h := decodeHeader(t, newTok); h["kid"] != newSet.Signing().KeyID {
		t.Fatalf("new token kid = %q, want the signing kid %q", h["kid"], newSet.Signing().KeyID)
	}
	// The pre-rotation set does not know the new key.
	_, err = oldSet.Verify(newTok)
	wantReason(t, err, "unknown_kid")
}

// spec: 10.2 (Authentication)
func TestKeySetRejectsForeignKey_spec_10_2(t *testing.T) {
	stub := kmsstub.New(t)
	ks := loadStubKeySet(t, stub, nil)
	foreign := kmsstub.New(t)
	foreignSet := loadStubKeySet(t, foreign, nil)
	foreignTok := remoteSign(t, foreign.AsProvider(), foreignSet, ksClaims("mallory@acme.com"))

	_, err := ks.Verify(foreignTok)
	wantReason(t, err, "unknown_kid")

	// A foreign signature stamped with this set's kid reaches the ECDSA
	// check and fails it.
	forger, err := jwt.NewRemoteSigner(foreign.AsProvider(), jwt.KeySetEntry{KeyID: ks.Signing().KeyID, PublicKey: ks.Signing().PublicKey})
	if err != nil {
		t.Fatalf("NewRemoteSigner: %v", err)
	}
	forged, err := forger.Sign(context.Background(), ksClaims("mallory@acme.com"))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	_, err = ks.Verify(forged)
	wantReason(t, err, "signature_mismatch")
}

// spec: 10.2 (Authentication)
func TestKeySetSignatureAndClaimChecks_spec_10_2(t *testing.T) {
	stub := kmsstub.New(t)
	ks := loadStubKeySet(t, stub, nil)
	good := remoteSign(t, stub.AsProvider(), ks, ksClaims("alice@acme.com"))
	parts := strings.Split(good, ".")

	expired := ksClaims("alice@acme.com")
	expired.Expiry = time.Now().Add(-time.Hour).Unix()
	notYet := ksClaims("alice@acme.com")
	notYet.NotBefore = time.Now().Add(time.Hour).Unix()
	tampered := ksClaims("root@acme.com")
	tamperedPayload, err := json.Marshal(tampered)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	flipped := append([]byte(nil), sigBytes...)
	flipped[10] ^= 0x01

	cases := []struct {
		name, reason, tok string
	}{
		{"expired", "expired", remoteSign(t, stub.AsProvider(), ks, expired)},
		{"not yet valid", "not_yet_valid", remoteSign(t, stub.AsProvider(), ks, notYet)},
		{"payload swapped under a valid signature", "signature_mismatch", parts[0] + "." + b64(tamperedPayload) + "." + parts[2]},
		{"one signature bit flipped", "signature_mismatch", parts[0] + "." + parts[1] + "." + b64(flipped)},
		{"DER-length signature", "signature_mismatch", parts[0] + "." + parts[1] + "." + b64(make([]byte, 70))},
		{"empty signature", "signature_mismatch", parts[0] + "." + parts[1] + "."},
		{"signature not base64url", "malformed", parts[0] + "." + parts[1] + ".!!"},
		{"two segments", "malformed", parts[0] + "." + parts[1]},
		{"header not base64url", "malformed", "!!." + parts[1] + "." + parts[2]},
		{"header not JSON", "malformed", b64([]byte("nope")) + "." + parts[1] + "." + parts[2]},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ks.Verify(tc.tok)
			wantReason(t, err, tc.reason)
		})
	}
}

// spec: 10.2 (Authentication) "Verifier key set and rotation"
func TestLoadKeySetLoadsNoVerifyKeyWhenUnmapped_spec_10_2(t *testing.T) {
	stub := kmsstub.New(t)
	for _, aliases := range []map[string]string{nil, {}, {kms.AliasJWTSigning: "ref-1", "tenant:acme": "alias/acme"}} {
		before := stub.PublicKeyCalls()
		ks := loadStubKeySet(t, stub, aliases)
		if ks.Len() != 1 {
			t.Fatalf("aliases %v: Len = %d, want 1", aliases, ks.Len())
		}
		if got := stub.PublicKeyCalls() - before; got != 1 {
			t.Fatalf("aliases %v: PublicKey calls = %d, want 1 (signing only)", aliases, got)
		}
	}
	// A verify key the provider holds but the alias map does not name
	// does not verify.
	verifySet := loadStubKeySet(t, stub, map[string]string{kms.AliasJWTVerify: "ref-old"})
	ks := loadStubKeySet(t, stub, nil)
	verifyPub, err := stub.AsProvider().PublicKey(context.Background(), kms.AliasJWTVerify)
	if err != nil {
		t.Fatalf("PublicKey: %v", err)
	}
	verifyKid, err := mustJWK(t, verifyPub).Thumbprint()
	if err != nil {
		t.Fatalf("Thumbprint: %v", err)
	}
	signer, err := jwt.NewRemoteSigner(&aliasSwapProvider{Provider: stub.AsProvider(), signAs: kms.AliasJWTVerify},
		jwt.KeySetEntry{KeyID: verifyKid, PublicKey: verifyPub})
	if err != nil {
		t.Fatalf("NewRemoteSigner: %v", err)
	}
	tok, err := signer.Sign(context.Background(), ksClaims("alice@acme.com"))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := verifySet.Verify(tok); err != nil {
		t.Fatalf("the mapped verify key: Verify: %v", err)
	}
	_, err = ks.Verify(tok)
	wantReason(t, err, "unknown_kid")
}

// aliasSwapProvider signs under signAs whenever asked to sign, so a
// RemoteSigner can mint with the verify key.
type aliasSwapProvider struct {
	kms.Provider
	signAs string
}

func (p *aliasSwapProvider) Sign(ctx context.Context, _ string, digest []byte) ([]byte, error) {
	return p.Provider.Sign(ctx, p.signAs, digest)
}

// failingPublicKeyProvider returns err from PublicKey for one alias.
type failingPublicKeyProvider struct {
	kms.Provider
	alias string
	err   error
}

func (p *failingPublicKeyProvider) PublicKey(ctx context.Context, alias string) (*ecdsa.PublicKey, error) {
	if alias == p.alias {
		return nil, p.err
	}
	return p.Provider.PublicKey(ctx, alias)
}

// spec: 10.2 (Authentication) "Verifier key set and rotation"
func TestLoadKeySetFailsWhenAMappedAliasFails_spec_10_2(t *testing.T) {
	unknown := fmt.Errorf("aws kms: GetPublicKey: NotFoundException: %w", kms.ErrUnknownKEK)
	cases := []struct {
		name    string
		alias   string
		aliases map[string]string
	}{
		{"signing alias", kms.AliasJWTSigning, nil},
		{"mapped verify alias", kms.AliasJWTVerify, map[string]string{kms.AliasJWTVerify: "ref-old"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &failingPublicKeyProvider{Provider: kmsstub.New(t).AsProvider(), alias: tc.alias, err: unknown}
			ks, err := jwt.LoadKeySet(context.Background(), p, tc.aliases)
			if !errors.Is(err, kms.ErrUnknownKEK) {
				t.Fatalf("err = %v, want one wrapping kms.ErrUnknownKEK", err)
			}
			if ks != nil {
				t.Fatal("a failed load returned a key set")
			}
			if !strings.Contains(err.Error(), tc.alias) {
				t.Fatalf("err = %v, want it to name %s", err, tc.alias)
			}
		})
	}
	// An unmapped verify alias is never fetched, so its failure is moot.
	p := &failingPublicKeyProvider{Provider: kmsstub.New(t).AsProvider(), alias: kms.AliasJWTVerify, err: unknown}
	if _, err := jwt.LoadKeySet(context.Background(), p, nil); err != nil {
		t.Fatalf("unmapped verify alias: LoadKeySet: %v", err)
	}
	if _, err := jwt.LoadKeySet(context.Background(), nil, nil); err == nil {
		t.Fatal("nil provider: want an error")
	}
	stub := kmsstub.New(t)
	stub.SetUnavailable(true)
	if _, err := jwt.LoadKeySet(context.Background(), stub.AsProvider(), nil); !errors.Is(err, kmsstub.ErrUnavailable) {
		t.Fatalf("unavailable KMS: err = %v, want ErrUnavailable", err)
	}
}
