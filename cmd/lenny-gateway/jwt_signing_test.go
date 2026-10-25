// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/kms"
	kmsstub "github.com/lennylabs/lenny/tests/testinfra/stubs/kms"
)

// localSigningProvider returns a local KMS provider seeded with seed and
// with platform:jwt-signing mapped to ref, as providerflags resolves it
// for every process that mounts one master key.
func localSigningProvider(t *testing.T, seed byte, ref string) *kms.Local {
	t.Helper()
	p, err := kms.NewLocal(bytes.Repeat([]byte{seed}, kms.DEKSize))
	if err != nil {
		t.Fatalf("kms.NewLocal: %v", err)
	}
	p.SetSigningReference(kms.AliasJWTSigning, ref)
	return p
}

// gatewaySigning builds a gateway's key set and signer over provider
// through the production construction.
func gatewaySigning(t *testing.T, provider kms.Provider) (*jwt.KeySet, *jwt.BreakerSigner) {
	t.Helper()
	ks, signer, err := jwt.LoadSigning(context.Background(), provider, map[string]string{kms.AliasJWTSigning: "local-key-1"}, &kmsBreakerObserver{})
	if err != nil {
		t.Fatalf("LoadSigning: %v", err)
	}
	return ks, signer
}

// mintUserBearer signs a user bearer for alice in tenant through signer.
func mintUserBearer(t *testing.T, signer jwt.Signer, tenant string) string {
	t.Helper()
	now := time.Now()
	tok, err := signer.Sign(context.Background(), jwt.Claims{
		Subject:  "alice@" + tenant + ".com",
		TenantID: tenant,
		IssuedAt: now.Unix(),
		Expiry:   now.Add(time.Hour).Unix(),
		JWTID:    "jti-" + tenant,
		Typ:      auth.TokenUserBearer,
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return tok
}

// TestGatewayKeySetVerifiesPeerReplicaTokens_spec_10_2 pins the corrected
// replica behaviour: two gateway processes that share the master key and
// the platform:jwt-signing reference each build their own key set and
// signer, and a bearer one mints verifies on the other. A process with a
// different master key refuses it as unknown_kid. Under the per-process
// random signing key each process trusted only its own tokens, so the
// cross-replica case failed.
//
// spec: 10.2 (Authentication) "Signing topology", "Verifier key set and rotation"
func TestGatewayKeySetVerifiesPeerReplicaTokens_spec_10_2(t *testing.T) {
	_, signerA := gatewaySigning(t, localSigningProvider(t, 0x11, "local-key-1"))
	keySetB, _ := gatewaySigning(t, localSigningProvider(t, 0x11, "local-key-1"))
	keySetOther, _ := gatewaySigning(t, localSigningProvider(t, 0x22, "local-key-1"))

	tok := mintUserBearer(t, signerA, "acme")
	claims, err := keySetB.Verify(tok)
	if err != nil {
		t.Fatalf("peer replica refused a token minted on replica A: %v", err)
	}
	if claims.TenantID != "acme" || claims.Typ != auth.TokenUserBearer {
		t.Errorf("claims = %+v, want tenant acme and typ user_bearer", claims)
	}
	_, err = keySetOther.Verify(tok)
	var ve *jwt.VerifyError
	if !errors.As(err, &ve) || ve.Reason != "unknown_kid" {
		t.Fatalf("foreign key set verify error = %v, want unknown_kid", err)
	}
}

// TestGatewaySignerBuiltFromOneKeySetFetch_spec_10_2 pins the one-fetch
// construction: building the gateway's signing surfaces calls PublicKey
// exactly once for platform:jwt-signing, the signer stamps the key set's
// signing kid on every token, and the breaker reports to the gateway's
// observer.
//
// spec: 10.2 (Authentication) "Verifier key set and rotation"
func TestGatewaySignerBuiltFromOneKeySetFetch_spec_10_2(t *testing.T) {
	stub := kmsstub.New(t)
	obs := &kmsBreakerObserver{}
	ks, signer, err := jwt.LoadSigning(context.Background(), stub.AsProvider(), map[string]string{kms.AliasJWTSigning: "key-1"}, obs)
	if err != nil {
		t.Fatalf("LoadSigning: %v", err)
	}
	if got := stub.PublicKeyCalls(); got != 1 {
		t.Errorf("PublicKey calls = %d, want 1", got)
	}
	if signer.Observer != obs {
		t.Error("breaker observer is not the gateway's kmsBreakerObserver")
	}
	tok := mintUserBearer(t, signer, "acme")
	header := decodeHeader(t, tok)
	if header["alg"] != jwt.AlgES256 || header["kid"] != ks.Signing().KeyID {
		t.Errorf("header = %v, want alg ES256 and kid %s", header, ks.Signing().KeyID)
	}
	if _, err := ks.Verify(tok); err != nil {
		t.Errorf("key set refused the signer's own token: %v", err)
	}
	if got := stub.PublicKeyCalls(); got != 1 {
		t.Errorf("PublicKey calls after sign and verify = %d, want 1", got)
	}
}

// TestGatewaySigningFailsWhenKeySetCannotLoad_spec_10_2 pins the startup
// failure path: a PublicKey error leaves the gateway with no signer and
// no verifier, and the error names the alias, so the caller's fatal log
// names the key-set load failure.
//
// spec: 10.2 (Authentication) "Verifier key set and rotation"
func TestGatewaySigningFailsWhenKeySetCannotLoad_spec_10_2(t *testing.T) {
	stub := kmsstub.New(t)
	boom := errors.New("kms endpoint refused the connection")
	stub.SetPublicKeyError(boom)
	ks, signer, err := jwt.LoadSigning(context.Background(), stub.AsProvider(), map[string]string{kms.AliasJWTSigning: "key-1"}, &kmsBreakerObserver{})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap the provider error", err)
	}
	if !strings.Contains(err.Error(), kms.AliasJWTSigning) {
		t.Errorf("err = %q, want it to name %s", err, kms.AliasJWTSigning)
	}
	if ks != nil || signer != nil {
		t.Errorf("key set %v and signer %v returned on failure, want nil", ks, signer)
	}
	if _, _, err := jwt.LoadSigning(context.Background(), nil, nil, &kmsBreakerObserver{}); err == nil {
		t.Error("nil provider accepted, want an error")
	}
}

// TestGatewayBearerVerifierRefusesNonKeySetTokens_spec_10_2 drives the
// Embedded Mode bearer verifier the gateway composes over its key set. A
// key-set token is admitted through the primary member with its own
// tenant_id. An HS256 token that names the key set's kid and is keyed
// with the public key bytes (the algorithm-confusion forgery), an HS256
// token keyed with the old per-process Token Service secret, and an
// unsigned alg none token are all refused.
//
// spec: 10.2 (Authentication) "Session capability context", 17.4
func TestGatewayBearerVerifierRefusesNonKeySetTokens_spec_10_2(t *testing.T) {
	ks, signer := gatewaySigning(t, localSigningProvider(t, 0x33, "local-key-1"))
	embedded := jwt.NewHMACSigner("embedded-kid", []byte("embedded-signing-secret-0123456789"))
	bearer := embeddedBearerVerifier(ks, embedded, "tenant_id")

	claims, err := bearer.Verify(mintUserBearer(t, signer, "acme"))
	if err != nil || claims.TenantID != "acme" {
		t.Fatalf("key-set token: claims %+v err %v, want admitted as acme", claims, err)
	}

	pub, err := json.Marshal(mustJWK(t, ks))
	if err != nil {
		t.Fatalf("marshal jwk: %v", err)
	}
	payload := map[string]any{
		"sub": "mallory@acme.com", "tenant_id": "acme", "typ": string(auth.TokenUserBearer),
		"exp": time.Now().Add(time.Hour).Unix(),
	}
	forgeries := map[string]string{
		"HS256 keyed with the public key": hs256(t, pub, ks.Signing().KeyID, payload),
		"HS256 per-process secret":        hs256(t, []byte("token-service-secret-abcdefghijklmnop"), "boot", payload),
		"alg none":                        unsignedToken(t, ks.Signing().KeyID, payload),
	}
	for name, tok := range forgeries {
		if _, err := bearer.Verify(tok); err == nil {
			t.Errorf("%s: admitted, want refused", name)
		}
	}
}

// mustJWK returns the EC JWK of the key set's signing key.
func mustJWK(t *testing.T, ks *jwt.KeySet) jwt.JWK {
	t.Helper()
	jwk, err := jwt.ECPublicJWK(ks.Signing().PublicKey)
	if err != nil {
		t.Fatalf("ECPublicJWK: %v", err)
	}
	return jwk
}

// unsignedToken encodes an alg none JWT with an empty signature.
func unsignedToken(t *testing.T, kid string, payload map[string]any) string {
	t.Helper()
	hb, err := json.Marshal(map[string]any{"alg": "none", "typ": "JWT", "kid": kid})
	if err != nil {
		t.Fatal(err)
	}
	pb, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(pb) + "."
}

// decodeHeader returns the JOSE header of tok.
func decodeHeader(t *testing.T, tok string) map[string]any {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(strings.SplitN(tok, ".", 2)[0])
	if err != nil {
		t.Fatalf("decode header: %v", err)
	}
	var h map[string]any
	if err := json.Unmarshal(raw, &h); err != nil {
		t.Fatalf("unmarshal header: %v", err)
	}
	return h
}
