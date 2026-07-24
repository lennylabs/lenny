// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract test for the §27.3.1 OIDC authorization-code-with-PKCE
// flow. It drives the real pkg/gateway/mcpfabric/playground.OIDCExchanger
// (the httpOIDCExchanger built by NewHTTPOIDCExchanger, the same
// implementation the gateway wires in authMode=oidc) against a
// standards-conformant OIDC provider stub over real HTTP: the stub issues
// authorization codes bound to a PKCE code_challenge and enforces the
// RFC 7636 verifier check on redemption, and its /token response is
// validated end to end (signature, iss, aud, exp) by an RS256/JWKS
// validator the test injects through the OIDCExchanger's IDTokenValidator
// hook.
//
// Every other OIDC-mode playground test (pkg/gateway/mcpfabric/playground
// and tests/tier3_contract/playground/playground_test.go) exercises the
// callback handler against a fakeOIDC double that never performs a real
// PKCE derivation, network round trip, or ID-token signature check; this
// suite is the first to stand the real Exchanger against conformant
// provider behavior, so a regression in S256 challenge derivation, state
// binding, or ID-token validation is caught here rather than shipping
// undetected.
package playground_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/playground"
	oidcstub "github.com/lennylabs/lenny/tests/testinfra/stubs/oidc"
)

// oidcConformanceRedirectURI is the playground's OIDC redirect_uri. The
// stub never actually serves this address; the test intercepts the
// /authorize redirect before the client follows it, exactly as the
// browser hop from the gateway's real /playground/auth/login to the
// provider and back to /playground/auth/callback would, without needing
// a live gateway listening on it.
const oidcConformanceRedirectURI = "https://gateway.acme.example/playground/auth/callback"

// noRedirectClient returns each hop's response instead of following
// Location, so the test can inspect the redirect the provider issues.
func noRedirectClient() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// newConformanceExchanger builds the real OIDCExchanger wired against
// stub, with an IDTokenValidator that performs genuine RS256/JWKS
// signature verification plus iss/aud/exp/nbf claim checks — the
// §27.3.1 "validates the returned ID token (signature, iss, aud, exp,
// nbf)" contract the gateway must satisfy, regardless of which JWKS
// library production wiring eventually uses.
func newConformanceExchanger(t *testing.T, stub *oidcstub.Stub, clientID string) playground.OIDCExchanger {
	t.Helper()
	cfg := playground.HTTPOIDCConfig{
		AuthorizationEndpoint: stub.Issuer() + "/authorize",
		TokenEndpoint:         stub.Issuer() + "/token",
		ClientID:              clientID,
		IDTokenValidator:      newRS256Validator(t, stub.JWKSURL(), stub.Issuer(), clientID),
	}
	return playground.NewHTTPOIDCExchanger(cfg)
}

// generatePKCEVerifier returns an RFC 7636 code_verifier: 256 bits of
// entropy in the base64url unreserved alphabet. This is an independent
// derivation from the production package's unexported
// generateCodeVerifier (pkg/gateway/mcpfabric/playground/crypto.go);
// the contract this suite pins is the wire behavior a conformant
// verifier/challenge pair produces, not a specific call path.
func generatePKCEVerifier(t *testing.T) string {
	t.Helper()
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("generate PKCE verifier: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

// authorize drives a real HTTP GET against authURL (with any extra query
// parameters appended, such as the stub's "bad" fault-injection mode)
// and returns the code and state the provider's redirect carries. It
// fails the test if the provider does not redirect to
// oidcConformanceRedirectURI.
func authorize(t *testing.T, authURL, extraQuery string) (code, state string) {
	t.Helper()
	full := authURL
	if extraQuery != "" {
		full += "&" + extraQuery
	}
	resp, err := noRedirectClient().Get(full)
	if err != nil {
		t.Fatalf("GET %s: %v", full, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("provider /authorize status = %d, want 302", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, oidcConformanceRedirectURI) {
		t.Fatalf("provider redirected to %q, want it to start with %q", loc, oidcConformanceRedirectURI)
	}
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse redirect Location %q: %v", loc, err)
	}
	return u.Query().Get("code"), u.Query().Get("state")
}

// spec: §27.3.1 ("`GET /playground/auth/login` — Initiates the OIDC
// authorization-code flow. The gateway generates a per-login `state` and
// PKCE `code_verifier` ... and redirects the browser to the configured
// OIDC provider's authorization endpoint.")
// diagnosis: A failure here means the real OIDCExchanger no longer
//
//	constructs a spec-conformant authorization request: either the
//	code_challenge_method is not stamped as S256 (pkg/gateway/
//	mcpfabric/playground/oidc.go AuthorizationURL), or the anti-CSRF
//	state value round-trips through a live provider redirect
//	incorrectly. Either regression would silently disable PKCE
//	protection or the state-binding check the callback handler relies
//	on to reject a forged callback.
func TestOIDCExchangerAuthorizationRequestCarriesPKCEAndEchoesState(t *testing.T) {
	stub := oidcstub.New(t)
	exchanger := newConformanceExchanger(t, stub, "playground-conformance-test")

	state := "conformance-state-" + generatePKCEVerifier(t)[:8]
	verifier := generatePKCEVerifier(t)
	challenge := oidcstub.ChallengeS256(verifier)

	authURL := exchanger.AuthorizationURL(state, challenge, oidcConformanceRedirectURI)
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse authorization URL %q: %v", authURL, err)
	}
	q := u.Query()
	if got := q.Get("code_challenge_method"); got != "S256" {
		t.Errorf("code_challenge_method = %q, want S256", got)
	}
	if got := q.Get("code_challenge"); got != challenge {
		t.Errorf("code_challenge = %q, want %q", got, challenge)
	}
	if got := q.Get("state"); got != state {
		t.Errorf("state = %q, want %q", got, state)
	}
	if got := q.Get("redirect_uri"); got != oidcConformanceRedirectURI {
		t.Errorf("redirect_uri = %q, want %q", got, oidcConformanceRedirectURI)
	}

	// The state must survive a real round trip through the provider:
	// the browser leg the login endpoint kicks off.
	_, echoedState := authorize(t, authURL, "")
	if echoedState != state {
		t.Errorf("provider echoed state = %q, want %q", echoedState, state)
	}
}

// spec: §27.3.1 ("`GET /playground/auth/callback?code=…&state=…` — OIDC
// provider redirects here. The gateway verifies `state` against the
// state cookie, performs the PKCE-protected token exchange with the
// provider, validates the returned ID token (signature, `iss`, `aud`,
// `exp`, `nbf`), extracts standard Lenny claims (`user_id`, `tenant_id`,
// `caller_type`, `scope` ...)")
// diagnosis: A failure here means the real OIDCExchanger.Exchange no
//
//	longer completes a standards-conformant PKCE-protected token
//	exchange end to end: the code_verifier sent to /token no longer
//	matches the code_challenge derivation the login leg used, the
//	token-endpoint POST is malformed, or the extracted subject claims
//	no longer map user_id/tenant_id/scope correctly.
func TestOIDCExchangerCompletesConformantPKCEExchange(t *testing.T) {
	stub := oidcstub.New(t)
	exchanger := newConformanceExchanger(t, stub, "playground-conformance-test")

	state := "conformance-state"
	verifier := generatePKCEVerifier(t)
	challenge := oidcstub.ChallengeS256(verifier)
	authURL := exchanger.AuthorizationURL(state, challenge, oidcConformanceRedirectURI)

	// sub/tenant_id are stub-only test controls read at /authorize and
	// bound to the code; they have no collision with a query parameter
	// AuthorizationURL itself sets. "scope" is deliberately not
	// overridden here because AuthorizationURL already sets its own
	// scope parameter from cfg.Scopes and net/url.Values.Get returns
	// the first value for a repeated key, so a second "scope" appended
	// here would be silently ignored by the stub rather than exercise
	// an override.
	code, echoedState := authorize(t, authURL, "sub=bob%40acme.com&tenant_id=acme")
	if echoedState != state {
		t.Fatalf("provider echoed state = %q, want %q", echoedState, state)
	}

	subject, err := exchanger.Exchange(context.Background(), code, verifier, oidcConformanceRedirectURI)
	if err != nil {
		t.Fatalf("Exchange with a matching PKCE verifier: %v", err)
	}
	if subject.UserID != "bob@acme.com" {
		t.Errorf("subject.UserID = %q, want bob@acme.com", subject.UserID)
	}
	if subject.TenantID != "acme" {
		t.Errorf("subject.TenantID = %q, want acme", subject.TenantID)
	}
	// The default HTTPOIDCConfig.Scopes ("openid profile email") is
	// what AuthorizationURL requested, and the stub's id_token echoes
	// it back on the wire, so the extracted subject carries the same
	// value the §27.3.1 claim-extraction step would read from a real
	// provider's ID token.
	if subject.Scope != "openid profile email" {
		t.Errorf("subject.Scope = %q, want %q", subject.Scope, "openid profile email")
	}
}

// spec: §27.3.1 ("performs the PKCE-protected token exchange with the
// provider"). RFC 7636 §4.6 requires the authorization server to reject
// the token exchange when the presented code_verifier does not hash to
// the code_challenge bound to the code; a provider (or a gateway) that
// skips this check accepts a stolen authorization code from an attacker
// who intercepted it but never had the verifier.
// diagnosis: A failure here means either the stub's PKCE enforcement
//
//	regressed (it would accept a mismatched verifier, defeating PKCE's
//	purpose in this test) or the real OIDCExchanger.Exchange no longer
//	propagates a token-endpoint rejection as an error, which would let
//	the callback handler mistakenly establish a session from a rejected
//	exchange.
func TestOIDCExchangerRejectsMismatchedPKCEVerifier(t *testing.T) {
	stub := oidcstub.New(t)
	exchanger := newConformanceExchanger(t, stub, "playground-conformance-test")

	state := "conformance-state"
	verifier := generatePKCEVerifier(t)
	challenge := oidcstub.ChallengeS256(verifier)
	authURL := exchanger.AuthorizationURL(state, challenge, oidcConformanceRedirectURI)

	code, _ := authorize(t, authURL, "")

	wrongVerifier := generatePKCEVerifier(t)
	if wrongVerifier == verifier {
		t.Fatal("generated the same verifier twice; test entropy is broken")
	}
	if _, err := exchanger.Exchange(context.Background(), code, wrongVerifier, oidcConformanceRedirectURI); err == nil {
		t.Fatal("Exchange succeeded with a code_verifier that does not match the code_challenge, want a PKCE rejection")
	}
}

// spec: §27.3.1 ("validates the returned ID token (signature, `iss`,
// `aud`, `exp`, `nbf`)").
// diagnosis: A failure in any of these subtests means the real
//
//	OIDCExchanger.Exchange accepted an ID token it should have
//	rejected: a bad signature means a forged or tampered token would
//	be trusted; a wrong audience means a token minted for a different
//	client (audience confusion) would be trusted; an expired token
//	means a stale credential would be trusted. Any of these would let
//	an attacker or a misbehaving provider establish a playground
//	session the §27.3.1 validation step exists to prevent.
func TestOIDCExchangerRejectsNonconformantIDToken(t *testing.T) {
	cases := []struct {
		name string
		mode string
	}{
		{"bad signature", oidcstub.BadSignature},
		{"wrong audience", oidcstub.BadAudience},
		{"expired", oidcstub.BadExpired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := oidcstub.New(t)
			exchanger := newConformanceExchanger(t, stub, "playground-conformance-test")

			state := "conformance-state"
			verifier := generatePKCEVerifier(t)
			challenge := oidcstub.ChallengeS256(verifier)
			authURL := exchanger.AuthorizationURL(state, challenge, oidcConformanceRedirectURI)

			code, _ := authorize(t, authURL, "bad="+tc.mode)

			_, err := exchanger.Exchange(context.Background(), code, verifier, oidcConformanceRedirectURI)
			if err == nil {
				t.Fatalf("Exchange accepted a %s ID token, want a validation rejection", tc.name)
			}
			var oe *playground.OIDCError
			if !errors.As(err, &oe) {
				t.Fatalf("Exchange error = %v (%T), want a *playground.OIDCError", err, err)
			}
			if oe.Code != "oidc_id_token_invalid" {
				t.Errorf("OIDCError.Code = %q, want oidc_id_token_invalid", oe.Code)
			}
		})
	}
}

// newRS256Validator returns an IDTokenValidator that performs a
// standards-conformant OIDC ID-token check: it fetches the provider's
// published JWKS, verifies the token's RS256 signature against the key
// named by the token's `kid`, and checks `iss`, `aud`, `exp`, and (when
// present) `nbf`. This is the check §27.3.1 requires the gateway to
// perform. It is an independent hand-rolled implementation from the
// production validator (pkg/gateway/mcpfabric/playground/
// oidcdiscovery.go's newJWKSIDTokenValidator, wired at
// cmd/lenny-gateway/httpsurface.go via
// playground.NewDiscoveredHTTPOIDCExchanger) so this suite pins the
// wire contract §27.3.1 requires rather than duplicating the
// production validator's own logic against itself.
func newRS256Validator(t *testing.T, jwksURL, expectedIssuer, expectedAudience string) func(context.Context, string) (map[string]any, error) {
	t.Helper()
	return func(ctx context.Context, idToken string) (map[string]any, error) {
		header, payload, signingInput, sig, err := parseCompactJWT(idToken)
		if err != nil {
			return nil, err
		}
		alg, _ := header["alg"].(string)
		if alg != "RS256" {
			return nil, fmt.Errorf("id token alg %q, want RS256", alg)
		}
		kid, _ := header["kid"].(string)
		pub, err := fetchJWKSRSAPublicKey(ctx, jwksURL, kid)
		if err != nil {
			return nil, fmt.Errorf("resolve JWKS key: %w", err)
		}
		sum := sha256.Sum256(signingInput)
		if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig); err != nil {
			return nil, fmt.Errorf("id token signature invalid: %w", err)
		}
		iss, _ := payload["iss"].(string)
		if iss != expectedIssuer {
			return nil, fmt.Errorf("id token iss %q, want %q", iss, expectedIssuer)
		}
		aud, _ := payload["aud"].(string)
		if aud != expectedAudience {
			return nil, fmt.Errorf("id token aud %q, want %q", aud, expectedAudience)
		}
		now := time.Now().Unix()
		expClaim, ok := payload["exp"].(float64)
		if !ok {
			return nil, errors.New("id token carries no exp claim")
		}
		if int64(expClaim) <= now {
			return nil, fmt.Errorf("id token expired at %d (now %d)", int64(expClaim), now)
		}
		if nbfClaim, ok := payload["nbf"].(float64); ok && int64(nbfClaim) > now {
			return nil, fmt.Errorf("id token not valid until %d (now %d)", int64(nbfClaim), now)
		}
		return payload, nil
	}
}

// parseCompactJWT decodes a compact-serialization JWT into its header
// and payload claim maps, plus the raw signing input and signature
// bytes a caller needs to verify it independently.
func parseCompactJWT(token string) (header, payload map[string]any, signingInput, sig []byte, err error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, nil, nil, nil, fmt.Errorf("token has %d segments, want 3", len(parts))
	}
	hb, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("decode header: %w", err)
	}
	pb, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("decode payload: %w", err)
	}
	sig, err = base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("decode signature: %w", err)
	}
	if err := json.Unmarshal(hb, &header); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("unmarshal header: %w", err)
	}
	if err := json.Unmarshal(pb, &payload); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("unmarshal payload: %w", err)
	}
	return header, payload, []byte(parts[0] + "." + parts[1]), sig, nil
}

// fetchJWKSRSAPublicKey fetches the JWKS document at jwksURL and
// returns the RSA public key for the entry matching kid (or the sole
// entry, when the document carries exactly one key and kid is empty).
func fetchJWKSRSAPublicKey(ctx context.Context, jwksURL, kid string) (*rsa.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("JWKS endpoint returned %d", resp.StatusCode)
	}
	var doc struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("decode JWKS: %w", err)
	}
	for _, k := range doc.Keys {
		if k.Kty != "RSA" {
			continue
		}
		if kid != "" && k.Kid != kid {
			continue
		}
		nb, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			return nil, fmt.Errorf("decode JWK n: %w", err)
		}
		eb, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			return nil, fmt.Errorf("decode JWK e: %w", err)
		}
		return &rsa.PublicKey{
			N: new(big.Int).SetBytes(nb),
			E: int(new(big.Int).SetBytes(eb).Int64()),
		}, nil
	}
	return nil, fmt.Errorf("no RSA key in JWKS matching kid %q", kid)
}
