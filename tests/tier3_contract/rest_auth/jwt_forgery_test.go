// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract tests for JWT forgery resistance on the §10.2 auth
// chain: algorithm-confusion (`alg: none`) and key-substitution
// (forged `kid`) attacks against the bearer-token validation path
// documented at §13.3 (RFC 9068 claims, JWKS-backed signature
// verification).
package rest_auth_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// newForgeryTestServer wires the auth middleware over a
// RotatingVerifier (rather than the bare HMACSigner newTestServer
// uses) so a forged token's `kid` header routes through the same
// key-selection path production traffic does. Returns the store
// alongside the server so a test can assert no session row was
// created for a rejected forgery attempt.
func newForgeryTestServer(t *testing.T) (*httptest.Server, *memstore.Store, *jwt.HMACSigner) {
	t.Helper()
	signer := jwt.NewHMACSigner("gateway-key-1", []byte("gateway-secret-material"))
	verifier := jwt.NewRotatingVerifier(signer, 24*time.Hour)
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{})
	wrapped := authmw.Wrap(srv.Handler(), authmw.Options{
		MultiTenant: false,
		RequireAuth: true,
		Verifier:    verifier,
	})
	ts := httptest.NewServer(wrapped)
	t.Cleanup(ts.Close)
	return ts, store, signer
}

// craftToken builds a compact JWS from an arbitrary JOSE header and
// Claims payload, HMAC-signing the result with secret. Unlike
// HMACSigner.Sign, the caller fully controls the header (so a test can
// stamp `alg: RS256`, a spoofed `kid`, or any other forged value)
// while the signature is computed however the test scenario calls for.
func craftToken(t *testing.T, header map[string]any, claims jwt.Claims, secret []byte) string {
	t.Helper()
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(headerJSON) + "." +
		base64.RawURLEncoding.EncodeToString(payloadJSON)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// craftTokenWithRawSignature is craftToken with a literal (already
// base64url-encoded) signature segment instead of a computed one, so a
// test can present the empty signature a JWS `alg: none` header calls
// for rather than an HMAC digest.
func craftTokenWithRawSignature(t *testing.T, header map[string]any, claims jwt.Claims, sigB64 string) string {
	t.Helper()
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(headerJSON) + "." +
		base64.RawURLEncoding.EncodeToString(payloadJSON)
	return signingInput + "." + sigB64
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

// spec: §13.3 line 623 — "Lenny verifies the token's signature against
// the IdP's JWKS" (and, symmetrically, the gateway's own Verifier
// checks a presented bearer's signature on every request). A JWS
// `alg: none` token carries the empty octet string as its signature
// (RFC 7518 §3.6) and asserts no cryptographic proof at all; the
// server's Verifier must still require a signature that matches the
// key the token's `kid` names rather than trusting the header's claim
// that no signature is needed.
// diagnosis: a 201 (or any non-401/non-TOKEN_INVALID) response means
// the verifier branched on the JOSE `alg` header and skipped signature
// verification for "none", accepting a forged token with no
// cryptographic proof behind it.
func TestAlgNoneForgeryRejected(t *testing.T) {
	ts, store, signer := newForgeryTestServer(t)
	header := map[string]any{"alg": "none", "typ": "JWT", "kid": signer.KeyID()}
	claims := jwt.Claims{
		Subject:  "mallory@acme.com",
		TenantID: "acme",
		Typ:      auth.TokenUserBearer,
		Expiry:   farFutureExpiry,
		IssuedAt: farFutureIssued,
	}
	// RFC 7518 §3.6: the JWS Signature for "none" is the empty octet
	// string, i.e. an empty (zero-length) base64url segment.
	tok := craftTokenWithRawSignature(t, header, claims, "")
	assertForgeryRejected(t, ts, store, tok)
}

// spec: §13.3 line 613 combined with the §10.3 key-rotation `kid`
// routing in 10_gateway-internals.md §10.2 — the verifier "tries the
// current and previous key versions" identified by `kid`, so a token
// is only ever checked against the specific keyed secret its `kid`
// names. A key-substitution forgery stamps the real, currently-active
// `kid` on a token signed with an attacker-controlled secret, betting
// that the verifier trusts the `kid` label rather than re-deriving
// the signature under the key it actually holds for that id.
// diagnosis: a 201 (or any non-401/non-TOKEN_INVALID) response means
// the RotatingVerifier accepted a signature it did not itself
// recompute against the keyed secret bound to the claimed `kid`,
// i.e. a forged token with a spoofed key identifier was honored.
func TestKeySubstitutionForgedKIDRejected(t *testing.T) {
	ts, store, signer := newForgeryTestServer(t)
	// The attacker knows the real gateway's current kid (kids are not
	// secret — they are published on /.well-known/jwks.json) but does
	// not hold the real gateway-secret-material HMAC key, so they sign
	// with a key of their own choosing while spoofing the trusted kid.
	forgedHeader := map[string]any{"alg": "HS256", "typ": "JWT", "kid": signer.KeyID()}
	claims := jwt.Claims{
		Subject:  "mallory@acme.com",
		TenantID: "acme",
		Typ:      auth.TokenUserBearer,
		Expiry:   farFutureExpiry,
		IssuedAt: farFutureIssued,
	}
	tok := craftToken(t, forgedHeader, claims, []byte("attacker-controlled-secret"))
	assertForgeryRejected(t, ts, store, tok)
}

// spec: §13.3 line 623 (JWKS-backed signature verification) — the
// classic RS-to-HS "algorithm confusion" attack rewrites an
// asymmetric-key token's JOSE header to claim a symmetric algorithm
// (or, as attempted here, the reverse: an HS256-verified token is
// relabeled RS256) so a verifier that dispatches on the attacker-
// controlled `alg` header, rather than the algorithm bound to the
// key it actually holds for the token's `kid`, is tricked into
// mis-verifying. The forged header here also carries the real `kid`,
// as an attacker who has fetched the public JWKS document (which
// carries only `{kty, use, alg, kid}` for an "oct" key per the §10.3
// JWKS publication contract — no secret material) would attempt,
// hoping some published field can substitute for the actual secret.
// diagnosis: a 201 (or any non-401/non-TOKEN_INVALID) response means
// the verifier picked its verification algorithm from the token's own
// header instead of always re-deriving the expected signature from
// the secret keyed under `kid`, i.e. the classic algorithm-confusion
// vulnerability is present.
func TestAlgorithmConfusionRS256HeaderRejected(t *testing.T) {
	ts, store, signer := newForgeryTestServer(t)
	confusedHeader := map[string]any{"alg": "RS256", "typ": "JWT", "kid": signer.KeyID()}
	claims := jwt.Claims{
		Subject:  "mallory@acme.com",
		TenantID: "acme",
		Typ:      auth.TokenUserBearer,
		Expiry:   farFutureExpiry,
		IssuedAt: farFutureIssued,
	}
	// The attacker has no real key material for the "RS256" they
	// claim, so they sign with whatever public-looking bytes they can
	// get their hands on (here, the published kid string itself,
	// standing in for "public" material a real JWKS document would
	// expose for an asymmetric key).
	tok := craftToken(t, confusedHeader, claims, []byte(signer.KeyID()))
	assertForgeryRejected(t, ts, store, tok)
}
