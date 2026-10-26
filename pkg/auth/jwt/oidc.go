// SPDX-License-Identifier: MIT

package jwt

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
)

// Defaults for OIDCConfig. The specification fixes none of them, so the
// gateway exposes each as an operator-tunable flag.
const (
	// DefaultOIDCKeyRefreshInterval is how long a fetched JWK Set serves
	// before the verifier fetches it again on the next verification.
	DefaultOIDCKeyRefreshInterval = time.Hour

	// DefaultOIDCMinRefetchInterval is the minimum spacing between two
	// fetches of the IdP's metadata. A token naming a kid the cached set
	// lacks triggers a fetch only once per interval, so a stream of
	// tokens with invented kids costs the IdP at most one request per
	// interval.
	DefaultOIDCMinRefetchInterval = 30 * time.Second

	// DefaultOIDCFetchTimeout bounds one discovery or JWK Set request.
	DefaultOIDCFetchTimeout = 10 * time.Second
)

// oidcMaxDocumentBytes caps the discovery document and the JWK Set the
// verifier reads, so a misbehaving IdP endpoint cannot exhaust memory.
const oidcMaxDocumentBytes = 1 << 20

// oidcMinRSABits is the smallest RSA modulus the verifier accepts from a
// JWK Set. RFC 7518 section 3.3 requires at least 2048 bits for RS256.
const oidcMinRSABits = 2048

// OIDCConfig configures an OIDCVerifier.
type OIDCConfig struct {
	// IssuerURL is the IdP issuer (`auth.oidc.issuerUrl`). The verifier
	// reads the discovery document at IssuerURL +
	// "/.well-known/openid-configuration", requires the document's
	// `issuer` to equal IssuerURL, and requires every token's `iss` to
	// equal it.
	IssuerURL string

	// Audience is the OIDC client registration (`auth.oidc.clientId`).
	// Every token's `aud` must contain it.
	Audience string

	// HTTPClient performs the discovery and JWK Set requests. Nil uses a
	// client with no timeout of its own; FetchTimeout bounds each request.
	HTTPClient *http.Client

	// KeyRefreshInterval, MinRefetchInterval, and FetchTimeout default to
	// the DefaultOIDC* constants when zero.
	KeyRefreshInterval time.Duration
	MinRefetchInterval time.Duration
	FetchTimeout       time.Duration

	// Now is the clock. Nil uses time.Now.
	Now func() time.Time
}

// OIDCVerifier verifies a bearer issued by the deployer's upstream OIDC
// identity provider. It discovers the IdP's `jwks_uri` from the issuer's
// discovery document, caches the published signing keys indexed by kid,
// and refreshes them after KeyRefreshInterval and when a token names a
// kid the cache lacks (at most once per MinRefetchInterval). It checks
// the signature, `iss`, `aud`, `exp`, and `nbf`, then normalizes the
// token to a `user_bearer`.
//
// The first fetch happens on the first verification rather than at
// construction, so the gateway starts while the IdP is unreachable and
// refuses IdP bearers with reason `jwks_unavailable` until a fetch
// succeeds. A failed refresh keeps the previously fetched keys.
//
// OIDCVerifier does not resolve the tenant. The gateway wraps it in a
// TenantClaimVerifier, because the tenant of an IdP-verified token comes
// from `auth.tenantIdClaim`.
//
// OIDCVerifier is safe for concurrent use. mu guards keys, jwksURI,
// fetchedAt, and lastAttempt; a fetch runs under mu, so concurrent
// verifications that need a fetch wait for one fetch rather than each
// issuing their own.
//
// spec: §10.2 (Authentication) the Client → Gateway boundary "OIDC/OAuth
// 2.1 (MCP-standard protected resource server)", the `user_bearer` type
// (an upstream IdP-issued OIDC ID token "is treated as a `user_bearer`
// for validation purposes"), and the standard auth chain "signature,
// `iss`, `aud`, `exp`, `nbf`"; §10.3 (Gateway startup configuration
// validation) `auth.oidc.issuerUrl` and `auth.oidc.clientId`.
type OIDCVerifier struct {
	cfg OIDCConfig

	mu          sync.Mutex
	keys        map[string]crypto.PublicKey
	jwksURI     string
	fetchedAt   time.Time
	lastAttempt time.Time
}

// NewOIDCVerifier validates cfg and returns a verifier. It performs no
// network request.
func NewOIDCVerifier(cfg OIDCConfig) (*OIDCVerifier, error) {
	cfg.IssuerURL = strings.TrimSpace(cfg.IssuerURL)
	cfg.Audience = strings.TrimSpace(cfg.Audience)
	if cfg.IssuerURL == "" {
		return nil, errors.New("jwt: oidc verifier: empty issuer URL")
	}
	if !strings.HasPrefix(cfg.IssuerURL, "https://") && !strings.HasPrefix(cfg.IssuerURL, "http://") {
		return nil, fmt.Errorf("jwt: oidc verifier: issuer URL %q is not an http(s) URL", cfg.IssuerURL)
	}
	if cfg.Audience == "" {
		return nil, errors.New("jwt: oidc verifier: empty audience (client id)")
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{}
	}
	if cfg.KeyRefreshInterval <= 0 {
		cfg.KeyRefreshInterval = DefaultOIDCKeyRefreshInterval
	}
	if cfg.MinRefetchInterval <= 0 {
		cfg.MinRefetchInterval = DefaultOIDCMinRefetchInterval
	}
	if cfg.FetchTimeout <= 0 {
		cfg.FetchTimeout = DefaultOIDCFetchTimeout
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &OIDCVerifier{cfg: cfg}, nil
}

// oidcHeader is the subset of the JOSE header OIDCVerifier reads.
type oidcHeader struct {
	Alg  string          `json:"alg"`
	Kid  string          `json:"kid"`
	Crit json.RawMessage `json:"crit"`
}

// Verify implements Verifier. Every failure is a *VerifyError.
func (v *OIDCVerifier) Verify(token string) (Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Claims{}, &VerifyError{Reason: "malformed", Detail: fmt.Sprintf("token has %d segments, want 3", len(parts))}
	}
	h, err := parseOIDCHeader(parts[0])
	if err != nil {
		return Claims{}, err
	}
	key, err := v.key(h)
	if err != nil {
		return Claims{}, err
	}
	if err := verifyOIDCSignature(h.Alg, key, parts[0]+"."+parts[1], parts[2]); err != nil {
		return Claims{}, err
	}
	claims, err := v.decodeOIDCClaims(parts[1])
	if err != nil {
		return Claims{}, err
	}
	return normalizeUserBearer(claims), nil
}

// parseOIDCHeader decodes the JOSE header and applies the algorithm
// rule: only the asymmetric algorithms an IdP publishes keys for are
// accepted, so `none` and every HMAC algorithm are refused before any
// key lookup. A header that names critical extensions is refused,
// because RFC 7515 section 4.1.11 requires a verifier to understand
// every extension listed there.
func parseOIDCHeader(headerB64 string) (oidcHeader, error) {
	raw, err := decode(headerB64)
	if err != nil {
		return oidcHeader{}, &VerifyError{Reason: "malformed", Detail: "header is not base64url"}
	}
	var h oidcHeader
	if err := json.Unmarshal(raw, &h); err != nil {
		return oidcHeader{}, &VerifyError{Reason: "malformed", Detail: fmt.Sprintf("header not JSON: %v", err)}
	}
	if _, ok := oidcAlgorithms[h.Alg]; !ok {
		return oidcHeader{}, &VerifyError{Reason: "unsupported_alg", Detail: fmt.Sprintf("alg %q is not an accepted IdP signing algorithm", h.Alg)}
	}
	if len(h.Crit) > 0 {
		return oidcHeader{}, &VerifyError{Reason: "malformed", Detail: "header names critical extensions"}
	}
	return h, nil
}

// decodeOIDCClaims decodes the payload, applies the shared exp and nbf
// checks, and checks `exp` presence, `iss`, and `aud`. An IdP may encode
// `aud` as a single string (RFC 7519 section 4.1.3), so the payload is
// normalized to the array form Claims carries before decoding.
func (v *OIDCVerifier) decodeOIDCClaims(payloadB64 string) (Claims, error) {
	payload, err := decode(payloadB64)
	if err != nil {
		return Claims{}, &VerifyError{Reason: "malformed", Detail: "payload is not base64url"}
	}
	payload, err = normalizeAudience(payload)
	if err != nil {
		return Claims{}, err
	}
	claims, err := decodeClaimsJSON(payload, v.cfg.Now())
	if err != nil {
		return Claims{}, err
	}
	if claims.Expiry == 0 {
		return Claims{}, &VerifyError{Reason: "missing_exp", Detail: "IdP token carries no exp"}
	}
	if claims.Issuer != v.cfg.IssuerURL {
		return Claims{}, &VerifyError{Reason: "issuer_mismatch", Detail: fmt.Sprintf("token iss=%q, want %q", claims.Issuer, v.cfg.IssuerURL)}
	}
	if !audienceMatch(claims.Audience, []string{v.cfg.Audience}) {
		return Claims{}, &VerifyError{Reason: "audience_mismatch", Detail: fmt.Sprintf("token aud=%v, want %q", claims.Audience, v.cfg.Audience)}
	}
	return claims, nil
}

// normalizeAudience rewrites a string `aud` as a one-element array.
func normalizeAudience(payload []byte) ([]byte, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return nil, &VerifyError{Reason: "malformed", Detail: fmt.Sprintf("payload not JSON: %v", err)}
	}
	raw, ok := fields["aud"]
	if !ok {
		return payload, nil
	}
	var single string
	if err := json.Unmarshal(raw, &single); err != nil {
		return payload, nil
	}
	arr, err := json.Marshal([]string{single})
	if err != nil {
		return nil, fmt.Errorf("jwt: encode audience: %w", err)
	}
	fields["aud"] = arr
	out, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("jwt: encode payload: %w", err)
	}
	return out, nil
}

// normalizeUserBearer is the §10.2 boundary-normalization step: an
// IdP-verified token is a `user_bearer` whatever `typ` the IdP stamped
// (Keycloak, for example, stamps `ID` or `Bearer`). A `user_bearer`
// carries no `session_id` and no delegation depth, and the `origin`
// claim is stamped by the playground ingress route rather than by
// credential material, so the step clears all three. The key that
// verified the token is the discriminator, because an IdP-signed token
// can carry any payload.
func normalizeUserBearer(c Claims) Claims {
	c.Typ = auth.TokenUserBearer
	c.SessionID = ""
	c.DelegationDepth = 0
	c.Origin = ""
	return c
}

// key returns the public key the header names, fetching the IdP's
// metadata when the cache is empty, older than KeyRefreshInterval, or
// lacks the kid, subject to MinRefetchInterval.
func (v *OIDCVerifier) key(h oidcHeader) (crypto.PublicKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	now := v.cfg.Now()
	stale := v.keys == nil || now.Sub(v.fetchedAt) >= v.cfg.KeyRefreshInterval
	if stale && v.mayFetch(now) {
		v.refreshLocked(now)
	}
	if key, err := v.selectLocked(h); err == nil || v.keys == nil || !v.mayFetch(now) {
		return key, err
	}
	// The kid is unknown to a cache that may predate an IdP key
	// rotation: fetch once and select again.
	v.refreshLocked(now)
	return v.selectLocked(h)
}

// mayFetch reports whether MinRefetchInterval has elapsed since the last
// fetch attempt. Callers hold mu.
func (v *OIDCVerifier) mayFetch(now time.Time) bool {
	return v.lastAttempt.IsZero() || now.Sub(v.lastAttempt) >= v.cfg.MinRefetchInterval
}

// refreshLocked fetches the discovery document (until one succeeds) and
// the JWK Set, replacing the cached keys on success. A failure keeps the
// previous keys. Callers hold mu.
func (v *OIDCVerifier) refreshLocked(now time.Time) {
	v.lastAttempt = now
	ctx, cancel := context.WithTimeout(context.Background(), v.cfg.FetchTimeout)
	defer cancel()
	if v.jwksURI == "" {
		uri, err := v.discover(ctx)
		if err != nil {
			return
		}
		v.jwksURI = uri
	}
	keys, err := v.fetchKeys(ctx, v.jwksURI)
	if err != nil {
		return
	}
	v.keys = keys
	v.fetchedAt = now
}

// selectLocked picks the cached key for h. A header without a kid
// selects the only cached key compatible with its alg, as OIDC Core
// section 10.1 permits when the IdP publishes a single signing key.
// Callers hold mu.
func (v *OIDCVerifier) selectLocked(h oidcHeader) (crypto.PublicKey, error) {
	if v.keys == nil {
		return nil, &VerifyError{Reason: "jwks_unavailable", Detail: "no signing keys fetched from the IdP"}
	}
	if h.Kid != "" {
		key, ok := v.keys[h.Kid]
		if !ok {
			return nil, &VerifyError{Reason: "unknown_kid", Detail: fmt.Sprintf("kid %q is not in the IdP key set", h.Kid)}
		}
		if !keyMatchesAlg(key, h.Alg) {
			return nil, &VerifyError{Reason: "unsupported_alg", Detail: fmt.Sprintf("alg %q does not match the type of key %q", h.Alg, h.Kid)}
		}
		return key, nil
	}
	var match crypto.PublicKey
	n := 0
	for _, key := range v.keys {
		if keyMatchesAlg(key, h.Alg) {
			match = key
			n++
		}
	}
	if n != 1 {
		return nil, &VerifyError{Reason: "missing_kid", Detail: fmt.Sprintf("JOSE header carries no kid and %d IdP keys match alg %q", n, h.Alg)}
	}
	return match, nil
}

// oidcDiscovery is the subset of the OIDC Discovery 1.0 provider
// metadata the verifier reads.
type oidcDiscovery struct {
	Issuer  string `json:"issuer"`
	JWKSURI string `json:"jwks_uri"`
}

// discover reads the issuer's discovery document and returns its
// `jwks_uri`. OIDC Discovery 1.0 section 4.3 requires the document's
// `issuer` to equal the issuer URL the document was fetched from.
func (v *OIDCVerifier) discover(ctx context.Context) (string, error) {
	var doc oidcDiscovery
	url := strings.TrimSuffix(v.cfg.IssuerURL, "/") + "/.well-known/openid-configuration"
	if err := v.getJSON(ctx, url, &doc); err != nil {
		return "", err
	}
	if doc.Issuer != v.cfg.IssuerURL {
		return "", fmt.Errorf("jwt: oidc discovery: issuer %q, want %q", doc.Issuer, v.cfg.IssuerURL)
	}
	if doc.JWKSURI == "" {
		return "", errors.New("jwt: oidc discovery: no jwks_uri")
	}
	return doc.JWKSURI, nil
}

// getJSON GETs url and decodes a 200 response body of at most
// oidcMaxDocumentBytes into out.
func (v *OIDCVerifier) getJSON(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("jwt: oidc: build request for %s: %w", url, err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := v.cfg.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("jwt: oidc: GET %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("jwt: oidc: GET %s: status %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, oidcMaxDocumentBytes+1))
	if err != nil {
		return fmt.Errorf("jwt: oidc: read %s: %w", url, err)
	}
	if len(body) > oidcMaxDocumentBytes {
		return fmt.Errorf("jwt: oidc: %s exceeds %d bytes", url, oidcMaxDocumentBytes)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("jwt: oidc: decode %s: %w", url, err)
	}
	return nil
}

// oidcJWKSet is an RFC 7517 section 5 JWK Set.
type oidcJWKSet struct {
	Keys []JWK `json:"keys"`
}

// fetchKeys reads the JWK Set and returns its usable signing keys by
// kid. A key with `use` other than `sig`, an unsupported key type, a
// malformed key, or an RSA modulus under 2048 bits is skipped. A set
// with no usable key is an error, so a broken endpoint never replaces a
// working cache with an empty one.
func (v *OIDCVerifier) fetchKeys(ctx context.Context, uri string) (map[string]crypto.PublicKey, error) {
	var set oidcJWKSet
	if err := v.getJSON(ctx, uri, &set); err != nil {
		return nil, err
	}
	keys := map[string]crypto.PublicKey{}
	for _, jwk := range set.Keys {
		if jwk.Use != "" && jwk.Use != "sig" {
			continue
		}
		key, err := jwk.publicKey()
		if err != nil {
			continue
		}
		if jwk.Alg != "" && !keyMatchesAlg(key, jwk.Alg) {
			continue
		}
		keys[jwk.Kid] = key
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("jwt: oidc: JWK Set at %s holds no usable signing key", uri)
	}
	return keys, nil
}

// publicKey decodes an RSA or EC JWK into a public key.
func (j JWK) publicKey() (crypto.PublicKey, error) {
	switch j.Kty {
	case "RSA":
		n, err := decode(j.N)
		if err != nil || len(n) == 0 {
			return nil, errors.New("jwt: RSA JWK has no valid n")
		}
		e, err := decode(j.E)
		if err != nil || len(e) == 0 || len(e) > 4 {
			return nil, errors.New("jwt: RSA JWK has no valid e")
		}
		pub := &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
		if pub.N.BitLen() < oidcMinRSABits {
			return nil, fmt.Errorf("jwt: RSA JWK modulus is %d bits, want at least %d", pub.N.BitLen(), oidcMinRSABits)
		}
		return pub, nil
	case "EC":
		curve, ok := map[string]elliptic.Curve{"P-256": elliptic.P256(), "P-384": elliptic.P384(), "P-521": elliptic.P521()}[j.Crv]
		if !ok {
			return nil, fmt.Errorf("jwt: EC JWK curve %q is unsupported", j.Crv)
		}
		x, errX := decode(j.X)
		y, errY := decode(j.Y)
		size := (curve.Params().BitSize + 7) / 8
		if errX != nil || errY != nil || len(x) != size || len(y) != size {
			return nil, fmt.Errorf("jwt: EC JWK coordinates are not %d-byte base64url values", size)
		}
		// ParseUncompressedPublicKey rejects a point that is not on the
		// curve, so an invalid-curve key never reaches ecdsa.Verify.
		pub, err := ecdsa.ParseUncompressedPublicKey(curve, append(append([]byte{4}, x...), y...))
		if err != nil {
			return nil, fmt.Errorf("jwt: EC JWK point: %w", err)
		}
		return pub, nil
	default:
		return nil, fmt.Errorf("jwt: JWK kty %q is unsupported", j.Kty)
	}
}
