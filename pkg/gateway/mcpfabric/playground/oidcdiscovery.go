// SPDX-License-Identifier: MIT

package playground

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	josejwt "github.com/go-jose/go-jose/v4"
)

// discoveryTimeout bounds the well-known discovery and JWKS-fetch
// requests DiscoverHTTPOIDCConfig performs.
const discoveryTimeout = 15 * time.Second

// oidcDiscoveryDocument is the subset of the OpenID Connect Discovery
// 1.0 document the gateway needs to drive the §27.3.1
// authorization-code-with-PKCE flow: where to send the browser, where
// to redeem the code, and where to fetch the signing keys that
// validate the returned ID token.
type oidcDiscoveryDocument struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
}

// DiscoverHTTPOIDCConfig fetches issuerURL's
// /.well-known/openid-configuration document and returns an
// HTTPOIDCConfig wired against it: AuthorizationEndpoint and
// TokenEndpoint from the discovery document, and an IDTokenValidator
// that verifies a returned ID token's signature against the
// provider's published JWKS (fetched from the discovered jwks_uri)
// and checks iss, aud, exp, and nbf — the §27.3.1 "validates the
// returned ID token (signature, iss, aud, exp, nbf)" contract.
// clientID is the playground's OIDC client registration (the
// gateway's --oidc-client-id / auth.oidc.clientId, §10.3).
// httpClient, when nil, defaults to a client bounded by
// discoveryTimeout.
//
// spec: §27.3.1 ("GET /playground/auth/login — Initiates the OIDC
// authorization-code flow ... and redirects the browser to the
// configured OIDC provider's authorization endpoint"; "validates the
// returned ID token (signature, iss, aud, exp, nbf)")
func DiscoverHTTPOIDCConfig(ctx context.Context, issuerURL, clientID string, httpClient *http.Client) (HTTPOIDCConfig, error) {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: discoveryTimeout}
	}
	trimmedIssuer := strings.TrimRight(issuerURL, "/")
	doc, err := fetchDiscoveryDocument(ctx, httpClient, trimmedIssuer)
	if err != nil {
		return HTTPOIDCConfig{}, fmt.Errorf("fetch OIDC discovery document: %w", err)
	}
	if doc.AuthorizationEndpoint == "" || doc.TokenEndpoint == "" || doc.JWKSURI == "" {
		return HTTPOIDCConfig{}, fmt.Errorf("OIDC discovery document at %s is missing authorization_endpoint, token_endpoint, or jwks_uri", trimmedIssuer)
	}
	expectedIssuer := doc.Issuer
	if expectedIssuer == "" {
		expectedIssuer = trimmedIssuer
	}
	return HTTPOIDCConfig{
		AuthorizationEndpoint: doc.AuthorizationEndpoint,
		TokenEndpoint:         doc.TokenEndpoint,
		ClientID:              clientID,
		IDTokenValidator:      newJWKSIDTokenValidator(httpClient, doc.JWKSURI, expectedIssuer, clientID),
	}, nil
}

// NewDiscoveredHTTPOIDCExchanger performs OIDC discovery against
// issuerURL (see DiscoverHTTPOIDCConfig) and returns the resulting
// OIDCExchanger. This is the constructor the gateway calls to build
// playground.Options.OIDC when playground.authMode=oidc
// (cmd/lenny-gateway/httpsurface.go, §27.2/§27.3).
func NewDiscoveredHTTPOIDCExchanger(ctx context.Context, issuerURL, clientID string, httpClient *http.Client) (OIDCExchanger, error) {
	cfg, err := DiscoverHTTPOIDCConfig(ctx, issuerURL, clientID, httpClient)
	if err != nil {
		return nil, err
	}
	return NewHTTPOIDCExchanger(cfg), nil
}

// fetchDiscoveryDocument performs the OpenID Connect Discovery 1.0
// GET against issuerURL + "/.well-known/openid-configuration".
func fetchDiscoveryDocument(ctx context.Context, httpClient *http.Client, issuerURL string) (oidcDiscoveryDocument, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, issuerURL+"/.well-known/openid-configuration", nil)
	if err != nil {
		return oidcDiscoveryDocument{}, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return oidcDiscoveryDocument{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return oidcDiscoveryDocument{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return oidcDiscoveryDocument{}, fmt.Errorf("discovery endpoint returned %d: %s", resp.StatusCode, string(body))
	}
	var doc oidcDiscoveryDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return oidcDiscoveryDocument{}, fmt.Errorf("discovery document is not JSON: %w", err)
	}
	return doc, nil
}

// asymmetricSignatureAlgorithms is the set of JWS algorithms
// newJWKSIDTokenValidator accepts. Symmetric (HMAC) algorithms are
// deliberately excluded: a validator that accepted HS256 against a
// JWKS document publishing only public RSA/EC material would be open
// to an algorithm-confusion attack (an attacker signs a forged token
// with HMAC using the provider's public key bytes as the "secret").
// Every entry a conformant OIDC provider signs ID tokens with is
// asymmetric.
var asymmetricSignatureAlgorithms = []josejwt.SignatureAlgorithm{
	josejwt.RS256, josejwt.RS384, josejwt.RS512,
	josejwt.ES256, josejwt.ES384, josejwt.ES512,
	josejwt.PS256, josejwt.PS384, josejwt.PS512,
}

// newJWKSIDTokenValidator returns an HTTPOIDCConfig.IDTokenValidator
// that performs the §27.3.1 "signature, iss, aud, exp, nbf" ID-token
// check: it fetches the provider's published JWKS at jwksURI on every
// call (the playground login path is low-frequency; caching is left
// for a future iteration), verifies the token's signature against the
// key matching its `kid` using go-jose, and checks the standard
// claims.
func newJWKSIDTokenValidator(httpClient *http.Client, jwksURI, expectedIssuer, expectedAudience string) func(ctx context.Context, idToken string) (map[string]any, error) {
	return func(ctx context.Context, idToken string) (map[string]any, error) {
		sig, err := josejwt.ParseSigned(idToken, asymmetricSignatureAlgorithms)
		if err != nil {
			return nil, fmt.Errorf("parse ID token: %w", err)
		}
		if len(sig.Signatures) != 1 {
			return nil, fmt.Errorf("ID token carries %d signatures, want 1", len(sig.Signatures))
		}
		kid := sig.Signatures[0].Header.KeyID
		jwks, err := fetchJWKS(ctx, httpClient, jwksURI)
		if err != nil {
			return nil, fmt.Errorf("fetch JWKS: %w", err)
		}
		keys := jwks.Key(kid)
		if len(keys) == 0 && kid == "" && len(jwks.Keys) == 1 {
			// RFC 7517 permits an entry with no "kid"; a provider that
			// publishes exactly one key and a token whose header omits
			// kid is unambiguous.
			keys = jwks.Keys
		}
		if len(keys) == 0 {
			return nil, fmt.Errorf("no JWKS key matches ID token kid %q", kid)
		}
		var payload []byte
		var verifyErr error
		for _, k := range keys {
			payload, verifyErr = sig.Verify(k.Key)
			if verifyErr == nil {
				break
			}
		}
		if verifyErr != nil {
			return nil, fmt.Errorf("ID token signature invalid: %w", verifyErr)
		}
		var claims map[string]any
		if err := json.Unmarshal(payload, &claims); err != nil {
			return nil, fmt.Errorf("ID token payload is not JSON: %w", err)
		}
		if iss, _ := claims["iss"].(string); iss != expectedIssuer {
			return nil, fmt.Errorf("ID token iss %q, want %q", iss, expectedIssuer)
		}
		if !audienceMatches(claims["aud"], expectedAudience) {
			return nil, fmt.Errorf("ID token aud does not include %q", expectedAudience)
		}
		now := time.Now().Unix()
		expClaim, ok := claims["exp"].(float64)
		if !ok {
			return nil, fmt.Errorf("ID token carries no exp claim")
		}
		if int64(expClaim) <= now {
			return nil, fmt.Errorf("ID token expired at %d (now %d)", int64(expClaim), now)
		}
		if nbfClaim, ok := claims["nbf"].(float64); ok && int64(nbfClaim) > now {
			return nil, fmt.Errorf("ID token not valid until %d (now %d)", int64(nbfClaim), now)
		}
		return claims, nil
	}
}

// audienceMatches reports whether aud (a JWT `aud` claim, either a
// single string or an array of strings per RFC 7519 §4.1.3) contains
// want.
func audienceMatches(aud any, want string) bool {
	switch v := aud.(type) {
	case string:
		return v == want
	case []any:
		for _, e := range v {
			if s, ok := e.(string); ok && s == want {
				return true
			}
		}
	}
	return false
}

// fetchJWKS fetches and decodes the JWK Set document at jwksURI.
func fetchJWKS(ctx context.Context, httpClient *http.Client, jwksURI string) (josejwt.JSONWebKeySet, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURI, nil)
	if err != nil {
		return josejwt.JSONWebKeySet{}, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return josejwt.JSONWebKeySet{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return josejwt.JSONWebKeySet{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return josejwt.JSONWebKeySet{}, fmt.Errorf("JWKS endpoint returned %d: %s", resp.StatusCode, string(body))
	}
	var jwks josejwt.JSONWebKeySet
	if err := json.Unmarshal(body, &jwks); err != nil {
		return josejwt.JSONWebKeySet{}, fmt.Errorf("JWKS document is not JSON: %w", err)
	}
	return jwks, nil
}
