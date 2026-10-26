// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract tests for JWT forgery resistance on the §10.2 auth
// chain: algorithm-confusion (`alg: none`, `alg: RS256`) and
// key-substitution (forged `kid`) attacks against the §10.2 key set
// that verifies every Lenny-issued bearer.
package rest_auth_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	kmsstub "github.com/lennylabs/lenny/tests/testinfra/stubs/kms"
)

// newForgeryTestServer wires the auth middleware over the §10.2 key set
// loaded from a KMS stub, the verifier production uses for every
// Lenny-issued token, so a forged token's `alg` and `kid` headers route
// through the same algorithm and key-selection rules production traffic
// does. It returns the store, so a test can assert no session row was
// created for a rejected forgery attempt, and a signer over the key
// set's signing entry, whose kid every forged token stamps so the
// forgery reaches the checks behind key selection.
func newForgeryTestServer(t *testing.T) (*httptest.Server, *memstore.Store, *jwt.RemoteSigner) {
	t.Helper()
	stub := kmsstub.New(t)
	keys, err := jwt.LoadKeySet(context.Background(), stub.AsProvider(), nil)
	if err != nil {
		t.Fatalf("LoadKeySet: %v", err)
	}
	signer, err := jwt.NewRemoteSigner(stub.AsProvider(), keys.Signing())
	if err != nil {
		t.Fatalf("NewRemoteSigner: %v", err)
	}
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{DevTenantHeader: true})
	wrapped := authmw.Wrap(srv.Handler(), authmw.Options{
		MultiTenant: false,
		RequireAuth: true,
		Verifier:    keys,
	})
	ts := httptest.NewServer(wrapped)
	t.Cleanup(ts.Close)
	return ts, store, signer
}

// forgeryClaims is the far-future user bearer every forgery carries.
func forgeryClaims() jwt.Claims {
	return jwt.Claims{
		Subject:  "mallory@acme.com",
		TenantID: "acme",
		Typ:      auth.TokenUserBearer,
		Expiry:   farFutureExpiry,
		IssuedAt: farFutureIssued,
	}
}

// craftES256Token builds a compact JWS from an arbitrary JOSE header and
// Claims payload, signed with an ES256 r||s signature under key. The
// caller controls the header, so a test can stamp the trusted kid on a
// token signed by a key the server does not hold.
func craftES256Token(t *testing.T, header map[string]any, claims jwt.Claims, key *ecdsa.PrivateKey) string {
	t.Helper()
	signingInput := encodeSigningInput(t, header, claims)
	digest := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatalf("ecdsa.Sign: %v", err)
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// encodeSigningInput returns the base64url header.payload signing input.
func encodeSigningInput(t *testing.T, header map[string]any, claims jwt.Claims) string {
	t.Helper()
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(headerJSON) + "." +
		base64.RawURLEncoding.EncodeToString(payloadJSON)
}

// craftToken builds a compact JWS from an arbitrary JOSE header and
// Claims payload, HMAC-signing the result with secret. The caller fully
// controls the header, so a test can stamp `alg: RS256` or any other
// forged value.
func craftToken(t *testing.T, header map[string]any, claims jwt.Claims, secret []byte) string {
	t.Helper()
	signingInput := encodeSigningInput(t, header, claims)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// craftTokenWithRawSignature is craftToken with a literal (already
// base64url-encoded) signature segment instead of a computed one, so a
// test can present the empty signature a JWS `alg: none` header calls
// for.
func craftTokenWithRawSignature(t *testing.T, header map[string]any, claims jwt.Claims, sigB64 string) string {
	t.Helper()
	return encodeSigningInput(t, header, claims) + "." + sigB64
}

// assertForgeryRejected asserts the canonical §10.2 TOKEN_INVALID
// rejection (401) and that the forgery minted no session row, so a
// test can tell a bug that merely returns the right status code apart
// from one that also lets a session slip through before the rejection.
func assertForgeryRejected(t *testing.T, ts *httptest.Server, store *memstore.Store, token string) {
	t.Helper()
	resp, body := do(t, ts, map[string]string{"Authorization": "Bearer " + token},
		map[string]any{"runtimeRef": "claude-code"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d (body %v)", resp.StatusCode, body)
	}
	envelope, _ := body["error"].(map[string]any)
	if envelope["code"] != "TOKEN_INVALID" {
		t.Errorf("error code: want TOKEN_INVALID, got %v", envelope["code"])
	}
	count, err := store.CountActiveSessionsGlobal(context.Background())
	if err != nil {
		t.Fatalf("CountActiveSessionsGlobal: %v", err)
	}
	if count != 0 {
		t.Errorf("session count: want 0 (forgery must not create a session), got %d", count)
	}
}

// spec: §10.2 (Authentication) "Session capability context" — the
// verifier of Lenny-issued tokens rejects a token whose `alg` is
// anything other than ES256, including `none`. A JWS `alg: none` token
// carries the empty octet string as its signature (RFC 7518 §3.6) and
// asserts no cryptographic proof at all. The forged header stamps the
// key set's signing kid, so only the algorithm rule stands between the
// forgery and acceptance.
// spec: §13.3 (Credential Flow)
// diagnosis: a 201 (or any non-401/non-TOKEN_INVALID) response means
// the verifier branched on the JOSE `alg` header and skipped signature
// verification for "none", accepting a forged token with no
// cryptographic proof behind it.
func TestAlgNoneForgeryRejected(t *testing.T) {
	ts, store, signer := newForgeryTestServer(t)
	header := map[string]any{"alg": "none", "typ": "JWT", "kid": signer.KeyID()}
	// RFC 7518 §3.6: the JWS Signature for "none" is the empty octet
	// string, i.e. an empty (zero-length) base64url segment.
	tok := craftTokenWithRawSignature(t, header, forgeryClaims(), "")
	assertForgeryRejected(t, ts, store, tok)
}

// spec: §10.2 (Authentication) "Verifier key set and rotation" — the
// key set selects the public key the token's `kid` names and checks
// the ES256 signature under that key. A key-substitution forgery
// stamps the real signing `kid` on a token signed with an attacker's
// own P-256 key, betting that the verifier trusts the `kid` label
// rather than checking the signature under the key it holds for that
// id. The forgery carries `alg: ES256` and the trusted kid, so it
// passes the algorithm and key-selection rules and reaches the
// signature check. A token the real signer minted is then accepted by
// the same server, which shows the rejection came from the signature
// check rather than from a server that rejects every bearer.
// spec: §13.3 (Credential Flow)
// diagnosis: a 201 for the forgery means the key set accepted a
// signature it did not check under the public key bound to the
// claimed `kid`; a 401 for the genuine token means the fixture rejects
// every bearer and the forgery assertion proves nothing.
func TestKeySubstitutionForgedKIDRejected(t *testing.T) {
	ts, store, signer := newForgeryTestServer(t)
	// The attacker knows the real signing kid (kids are not secret:
	// every token carries its kid in the JOSE header) but does not hold
	// the KMS-held private key, so they sign with a key of their own.
	attackerKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	forgedHeader := map[string]any{"alg": "ES256", "typ": "JWT", "kid": signer.KeyID()}
	tok := craftES256Token(t, forgedHeader, forgeryClaims(), attackerKey)
	assertForgeryRejected(t, ts, store, tok)

	genuine, err := signer.Sign(context.Background(), jwt.Claims{
		Subject:  "alice@acme.com",
		TenantID: "acme",
		Typ:      auth.TokenUserBearer,
		Expiry:   farFutureExpiry,
		IssuedAt: farFutureIssued,
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	resp, body := do(t, ts, map[string]string{"Authorization": "Bearer " + genuine},
		map[string]any{"runtimeRef": "claude-code"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("genuine token: want 201, got %d (body %v)", resp.StatusCode, body)
	}
}

// spec: §10.2 (Authentication) "Session capability context" — the
// classic algorithm-confusion attack relabels a token's JOSE header
// with an algorithm other than the one bound to the key, so a verifier
// that dispatches on the attacker-controlled `alg` header is tricked
// into mis-verifying. The forged header claims RS256, carries the real
// signing `kid`, and is HMAC-signed with public material (the kid
// string itself). The key set accepts ES256 only, so the header's
// claimed algorithm never selects a verification path.
// spec: §13.3 (Credential Flow)
// diagnosis: a 201 (or any non-401/non-TOKEN_INVALID) response means
// the verifier picked its verification algorithm from the token's own
// header instead of fixing it to ES256, i.e. the classic
// algorithm-confusion vulnerability is present.
func TestAlgorithmConfusionRS256HeaderRejected(t *testing.T) {
	ts, store, signer := newForgeryTestServer(t)
	confusedHeader := map[string]any{"alg": "RS256", "typ": "JWT", "kid": signer.KeyID()}
	tok := craftToken(t, confusedHeader, forgeryClaims(), []byte(signer.KeyID()))
	assertForgeryRejected(t, ts, store, tok)
}
