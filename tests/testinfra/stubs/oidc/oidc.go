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
	sig, _ := rsa.SignPKCS1v15(nil, s.key, 0, sum[:])
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
	http.Redirect(w, r, fmt.Sprintf("%s?code=%s&state=%s", redirect, code, state), http.StatusFound)
}

func (s *Stub) handleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
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

func randomID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
