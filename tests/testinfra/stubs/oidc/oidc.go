// SPDX-License-Identifier: MIT

// Package oidc implements a minimal OIDC stub that tests can spin
// up in-process. It exposes the OIDC discovery document, a JWKS
// endpoint, and the /authorize + /token endpoints. The stub does no
// real cryptography beyond an RSA key pair generated at startup —
// the same key signs every issued token and verifies every JWKS
// lookup.
//
// The /authorize + /token pair enforces RFC 7636 PKCE: /authorize
// records the code_challenge presented against the issued code, and
// /token rejects the authorization_code grant when the presented
// code_verifier does not hash (S256) to that challenge, or when the
// code is unknown or already redeemed. The /token response carries
// both access_token and id_token (the same RS256 JWT), with the
// id_token's aud set from the request's client_id, so a caller can
// drive a standards-conformant OIDC authorization-code exchange
// end to end.
//
// Use this when a tier-2 component or tier-4 integration test needs
// an authentication source without paying the cost of running a
// real IdP container. Production-grade behavior (token rotation,
// session management) is intentionally absent; see
// tests/testinfra/oidc-real/ for the heavier alternative once that
// lands.
//
// Usage:
//
//	idp := oidc.New(t)
//	t.Logf("issuer: %s", idp.Issuer())
//	tok := idp.MintToken(oidc.MintOptions{
//	    Subject:  "alice@acme.com",
//	    TenantID: "acme",
//	    Scope:    "sessions:read",
//	})
package oidc

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// Stub is a running OIDC stub bound to a random loopback port.
type Stub struct {
	server *httptest.Server
	key    *rsa.PrivateKey
	kid    string

	mu      sync.Mutex
	tokens  map[string]bool        // jti → not-yet-revoked
	pending map[string]pendingAuth // authorization code → PKCE/subject binding, deleted on redemption
}

// pendingAuth is the state /authorize binds to an issued authorization
// code so /token can enforce PKCE and reproduce the requested subject.
type pendingAuth struct {
	challenge string
	method    string
	clientID  string
	subject   string
	tenantID  string
	scope     string
	bad       string
}

// Fault-injection modes for the "bad" /authorize query parameter. A
// caller that wants to exercise a consumer's ID-token validation
// failure path sets bad=<mode> on the authorization request; /token
// then deliberately mis-mints the id_token it returns on redemption
// of that code. Each mode corresponds to one of the §27.3.1
// "signature, iss, aud, exp, nbf" checks a conformant consumer must
// perform.
const (
	BadSignature = "bad_signature" // id_token signature does not verify
	BadAudience  = "bad_audience"  // id_token aud does not match the requesting client_id
	BadExpired   = "bad_expired"   // id_token exp is already in the past
)

// New starts the stub and registers a t.Cleanup that closes it.
func New(t testing.TB) *Stub {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("oidc stub: generate key: %v", err)
	}
	s := &Stub{
		key:     key,
		kid:     "oidc-stub-1",
		tokens:  make(map[string]bool),
		pending: make(map[string]pendingAuth),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", s.handleDiscovery)
	mux.HandleFunc("/jwks.json", s.handleJWKS)
	mux.HandleFunc("/authorize", s.handleAuthorize)
	mux.HandleFunc("/token", s.handleToken)
	s.server = httptest.NewServer(mux)
	t.Cleanup(s.server.Close)
	return s
}

// Issuer returns the stub's issuer URL, suitable for use as the
// `iss` claim or the OIDC discovery URL.
func (s *Stub) Issuer() string { return s.server.URL }

// JWKSURL returns the JWKS endpoint URL.
func (s *Stub) JWKSURL() string { return s.server.URL + "/jwks.json" }

// MintOptions configures MintToken.
type MintOptions struct {
	Subject  string
	TenantID string
	Scope    string
	Audience string
	Lifetime time.Duration
	Extra    map[string]any
}

// MintToken issues an RS256-signed JWT.
func (s *Stub) MintToken(opts MintOptions) string {
	if opts.Lifetime == 0 {
		opts.Lifetime = time.Hour
	}
	if opts.Audience == "" {
		opts.Audience = "lenny-gateway"
	}
	now := time.Now().UTC()
	payload := map[string]any{
		"iss":       s.Issuer(),
		"sub":       opts.Subject,
		"aud":       opts.Audience,
		"exp":       now.Add(opts.Lifetime).Unix(),
		"iat":       now.Unix(),
		"jti":       randomID(),
		"tenant_id": opts.TenantID,
		"scope":     opts.Scope,
	}
	for k, v := range opts.Extra {
		payload[k] = v
	}
	header := map[string]any{"alg": "RS256", "typ": "JWT", "kid": s.kid}
	hb, _ := json.Marshal(header)
	pb, _ := json.Marshal(payload)
	hp := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(pb)
	sum := sha256.Sum256([]byte(hp))
	// crypto.SHA256 (not the zero Hash) so the signature carries the
	// standard PKCS#1 v1.5 DigestInfo prefix RFC 7518 RS256 and every
	// conformant JWKS-based verifier expects; a caller that verifies
	// with crypto.rsa.VerifyPKCS1v15(pub, crypto.SHA256, ...) — the
	// standard construction, not a stub-specific one — must succeed.
	sig, _ := rsa.SignPKCS1v15(nil, s.key, crypto.SHA256, sum[:])
	return hp + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// Revoke marks the given jti as revoked. Subsequent IsRevoked calls
// return true.
func (s *Stub) Revoke(jti string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[jti] = true
}

// IsRevoked reports whether jti has been revoked.
func (s *Stub) IsRevoked(jti string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tokens[jti]
}

// PublicPEM returns the stub's public key in PEM form for clients
// that want to verify tokens directly rather than fetching JWKS.
func (s *Stub) PublicPEM() string {
	pubBytes, _ := x509.MarshalPKIXPublicKey(&s.key.PublicKey)
	block := &pem.Block{Type: "RSA PUBLIC KEY", Bytes: pubBytes}
	return string(pem.EncodeToMemory(block))
}

func (s *Stub) handleDiscovery(w http.ResponseWriter, r *http.Request) {
	doc := map[string]any{
		"issuer":                                s.Issuer(),
		"authorization_endpoint":                s.Issuer() + "/authorize",
		"token_endpoint":                        s.Issuer() + "/token",
		"jwks_uri":                              s.JWKSURL(),
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"grant_types_supported":                 []string{"authorization_code", "client_credentials"},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(doc)
}

func (s *Stub) handleJWKS(w http.ResponseWriter, r *http.Request) {
	n := base64.RawURLEncoding.EncodeToString(s.key.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(s.key.E)).Bytes())
	jwks := map[string]any{
		"keys": []map[string]any{
			{
				"kty": "RSA",
				"use": "sig",
				"alg": "RS256",
				"kid": s.kid,
				"n":   n,
				"e":   e,
			},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(jwks)
}

func (s *Stub) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	state := q.Get("state")
	redirect := q.Get("redirect_uri")
	if redirect == "" {
		http.Error(w, "redirect_uri required", http.StatusBadRequest)
		return
	}
	// Echo the canonical code value the test sets; production OIDC
	// would generate one.
	code := q.Get("code_hint")
	if code == "" {
		code = "stub-code-" + randomID()
	}
	// Bind the PKCE challenge (and any test-supplied subject override)
	// to the issued code so /token can enforce RFC 7636 on redemption.
	s.mu.Lock()
	s.pending[code] = pendingAuth{
		challenge: q.Get("code_challenge"),
		method:    q.Get("code_challenge_method"),
		clientID:  q.Get("client_id"),
		subject:   q.Get("sub"),
		tenantID:  q.Get("tenant_id"),
		scope:     q.Get("scope"),
		bad:       q.Get("bad"),
	}
	s.mu.Unlock()
	http.Redirect(w, r, fmt.Sprintf("%s?code=%s&state=%s", redirect, code, state), http.StatusFound)
}

func (s *Stub) handleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	code := r.Form.Get("code")
	s.mu.Lock()
	pa, known := s.pending[code]
	if known {
		// RFC 6749 §4.1.2: an authorization code is single-use.
		delete(s.pending, code)
	}
	s.mu.Unlock()

	if r.Form.Get("grant_type") == "authorization_code" {
		if !known {
			writeTokenError(w, "invalid_grant", "unknown or already-redeemed authorization code")
			return
		}
		if pa.challenge != "" {
			if pa.method != "" && pa.method != "S256" {
				writeTokenError(w, "invalid_request", "unsupported code_challenge_method")
				return
			}
			verifier := r.Form.Get("code_verifier")
			if verifier == "" || ChallengeS256(verifier) != pa.challenge {
				writeTokenError(w, "invalid_grant", "code_verifier does not hash to the code_challenge presented at /authorize")
				return
			}
		}
	}

	sub := firstNonEmpty(r.Form.Get("sub"), pa.subject, "alice@acme.com")
	tenant := firstNonEmpty(r.Form.Get("tenant_id"), pa.tenantID, "acme")
	scope := firstNonEmpty(r.Form.Get("scope"), pa.scope, "")
	aud := firstNonEmpty(r.Form.Get("client_id"), pa.clientID)
	mintOpts := MintOptions{Subject: sub, TenantID: tenant, Scope: scope, Audience: aud}
	if pa.bad == BadAudience {
		// A provider that returns an id_token minted for a different
		// client than the one that requested it (audience confusion).
		mintOpts.Audience = "malicious-client.example"
	}
	if pa.bad == BadExpired {
		mintOpts.Lifetime = -time.Hour
	}
	tok := s.MintToken(mintOpts)
	idToken := tok
	if pa.bad == BadSignature {
		idToken = corruptSignature(idToken)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token": tok,
		// The stub does not distinguish an access token from an ID
		// token: both are the same RS256 JWT carrying the standard
		// Lenny claims, which is sufficient for a caller validating
		// the id_token per RFC 7636 / OIDC Core §3.1.3.3, except when
		// a "bad" fault-injection mode deliberately mis-mints one of
		// them.
		"id_token":   idToken,
		"token_type": "Bearer",
		"expires_in": 3600,
		"scope":      scope,
	})
}

// corruptSignature flips one byte of a compact JWT's signature segment
// so the token fails signature verification while remaining a
// well-formed 3-segment JWT (the header and payload, and therefore
// the claims a careless validator might read without verifying the
// signature first, are untouched).
func corruptSignature(token string) string {
	dot := strings.LastIndexByte(token, '.')
	if dot < 0 || dot == len(token)-1 {
		return token
	}
	sigPart := token[dot+1:]
	sig, err := base64.RawURLEncoding.DecodeString(sigPart)
	if err != nil || len(sig) == 0 {
		return token
	}
	sig[0] ^= 0xFF
	return token[:dot+1] + base64.RawURLEncoding.EncodeToString(sig)
}

// writeTokenError writes an RFC 6749 §5.2 token-error-response body.
func writeTokenError(w http.ResponseWriter, code, description string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":             code,
		"error_description": description,
	})
}

// firstNonEmpty returns the first non-empty string among vals, or "".
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// ChallengeS256 derives the RFC 7636 S256 code_challenge from a
// code_verifier: base64url(SHA-256(ASCII(verifier))) without padding.
// Exported so a caller driving the stub through a real
// authorization-code-with-PKCE flow can compute the challenge to send
// to /authorize without duplicating the derivation.
func ChallengeS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func randomID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
