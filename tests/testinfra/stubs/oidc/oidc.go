// SPDX-License-Identifier: MIT

// Package oidc implements a minimal OIDC stub that tests can spin
// up in-process. It exposes the OIDC discovery document, a JWKS
// endpoint, and the /authorize + /token endpoints. The stub does no
// real cryptography beyond an RSA key pair generated at startup —
// the same key signs every issued token and verifies every JWKS
// lookup.
//
// Use this when a tier-2 component or tier-4 integration test needs
// an authentication source without paying the cost of running a
// real IdP container. Production-grade behavior (token rotation,
// session management, real PKCE state) is intentionally absent;
// see tests/testinfra/oidc-real/ for the heavier alternative once
// that lands.
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
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"
)

// Stub is a running OIDC stub bound to a random loopback port.
type Stub struct {
	server *httptest.Server
	key    *rsa.PrivateKey
	kid    string

	mu     sync.Mutex
	tokens map[string]bool // jti → not-yet-revoked
	// codes holds each authorization code /authorize issued with the
	// PKCE challenge, client, and redirect URI it was issued for; /token
	// redeems a code once.
	codes map[string]pendingCode
	// login is the identity the ID token of an authorization-code
	// exchange carries; SetLoginIdentity sets it.
	login MintOptions
}

// pendingCode is an authorization code awaiting redemption at /token.
type pendingCode struct {
	challenge, clientID, redirectURI string
}

// New starts the stub and registers a t.Cleanup that closes it.
func New(t testing.TB) *Stub {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("oidc stub: generate key: %v", err)
	}
	s := &Stub{
		key:    key,
		kid:    "oidc-stub-1",
		tokens: make(map[string]bool),
		codes:  make(map[string]pendingCode),
		login:  MintOptions{Subject: "alice@acme.com", TenantID: "acme"},
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

// SetLoginIdentity sets the subject, tenant, and extra claims of the ID
// token an authorization-code exchange returns. Its audience is always
// the exchange's client_id. The default identity is alice@acme.com in
// acme.
func (s *Stub) SetLoginIdentity(opts MintOptions) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.login = opts
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
	// RS256 is RSASSA-PKCS1-v1_5 over SHA-256 (RFC 7518 section 3.3),
	// whose signature input carries the SHA-256 DigestInfo prefix.
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

// handleAuthorize issues an authorization code for the request's PKCE
// S256 challenge, client, and redirect URI, and redirects to the
// redirect URI with the code and the state.
func (s *Stub) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	state := q.Get("state")
	redirect := q.Get("redirect_uri")
	if redirect == "" {
		http.Error(w, "redirect_uri required", http.StatusBadRequest)
		return
	}
	if q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
		http.Error(w, "response_type=code with an S256 code_challenge required", http.StatusBadRequest)
		return
	}
	// Echo the canonical code value the test sets; production OIDC
	// would generate one.
	code := q.Get("code_hint")
	if code == "" {
		code = "stub-code-" + randomID()
	}
	s.mu.Lock()
	s.codes[code] = pendingCode{challenge: q.Get("code_challenge"), clientID: q.Get("client_id"), redirectURI: redirect}
	s.mu.Unlock()
	target, err := url.Parse(redirect)
	if err != nil {
		http.Error(w, "redirect_uri is not a URL", http.StatusBadRequest)
		return
	}
	rq := target.Query()
	rq.Set("code", code)
	rq.Set("state", state)
	target.RawQuery = rq.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
}

// handleToken redeems an authorization code for an ID token, after
// checking the PKCE code_verifier, the client, and the redirect URI
// against the code. A grant other than authorization_code is answered
// with an access token for the form's sub, tenant_id, and scope.
func (s *Stub) handleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if r.Form.Get("grant_type") == "authorization_code" {
		s.redeemCode(w, r)
		return
	}
	sub := r.Form.Get("sub")
	if sub == "" {
		sub = "alice@acme.com"
	}
	tenant := r.Form.Get("tenant_id")
	if tenant == "" {
		tenant = "acme"
	}
	scope := r.Form.Get("scope")
	tok := s.MintToken(MintOptions{
		Subject:  sub,
		TenantID: tenant,
		Scope:    scope,
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token": tok,
		"token_type":   "Bearer",
		"expires_in":   3600,
		"scope":        scope,
	})
}

// redeemCode serves the authorization_code grant.
func (s *Stub) redeemCode(w http.ResponseWriter, r *http.Request) {
	code := r.Form.Get("code")
	s.mu.Lock()
	pending, ok := s.codes[code]
	delete(s.codes, code)
	login := s.login
	s.mu.Unlock()
	sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
	switch {
	case !ok:
		writeTokenError(w, "invalid_grant", "unknown or redeemed code")
		return
	case base64.RawURLEncoding.EncodeToString(sum[:]) != pending.challenge:
		writeTokenError(w, "invalid_grant", "code_verifier does not match the code_challenge")
		return
	case r.Form.Get("client_id") != pending.clientID || r.Form.Get("redirect_uri") != pending.redirectURI:
		writeTokenError(w, "invalid_grant", "client_id or redirect_uri differs from the authorization request")
		return
	}
	login.Audience = pending.clientID
	idToken := s.MintToken(login)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token": idToken,
		"id_token":     idToken,
		"token_type":   "Bearer",
		"expires_in":   3600,
	})
}

// writeTokenError writes an RFC 6749 token-endpoint error.
func writeTokenError(w http.ResponseWriter, code, description string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "error_description": description})
}

func randomID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
