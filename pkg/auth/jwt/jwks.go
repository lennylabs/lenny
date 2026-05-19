// SPDX-License-Identifier: MIT

package jwt

import (
	"encoding/json"
	"net/http"
	"sync"
)

// JWK is the §10.3 JSON Web Key (RFC 7517) representation of one
// signing key the gateway has held. Each entry carries the JOSE
// metadata a client needs to route a token to its verifying key. Only
// the closed set of fields a Lenny JWKS publishes is modelled here;
// the JWKS document itself is a JSON object with a "keys" array, so
// JWK models a single entry within that array rather than the
// wrapper.
//
// For an asymmetric key (RSA, EC), N/E (RSA modulus + exponent) or
// X/Y/Crv (EC point) describe the public half. For a symmetric key
// (HMAC, the §10.2 dev signer and the §4 KMS-wrapped HS256 signer)
// the JWK MUST NOT carry the secret; only Kty/Use/Alg/Kid identify
// the key, which is enough for a client to confirm an incoming token's
// `kid` references a key the gateway recognises. A consumer that needs
// to verify HMAC signatures must obtain the secret out of band (e.g.,
// through a KMS unwrap call against the Token Service KEK); the JWKS
// endpoint deliberately never publishes private material.
type JWK struct {
	// Kty is the JOSE key type: "oct" for symmetric HMAC keys, "RSA"
	// for RSA, "EC" for elliptic curve. RFC 7517 §4.1.
	Kty string `json:"kty"`

	// Use is the key purpose. Lenny only signs JWTs, so this is
	// always "sig". RFC 7517 §4.2.
	Use string `json:"use,omitempty"`

	// Alg is the JOSE algorithm the key signs with: HS256 for the
	// HMAC signers, RS256/ES256 when a future asymmetric backend
	// lands. RFC 7517 §4.4.
	Alg string `json:"alg,omitempty"`

	// Kid is the JOSE key id stamped on every token signed by this
	// key; the rotating verifier routes a token to its key by Kid.
	// RFC 7517 §4.5.
	Kid string `json:"kid"`

	// N is the base64url-encoded RSA modulus. Present only for RSA.
	N string `json:"n,omitempty"`

	// E is the base64url-encoded RSA exponent. Present only for RSA.
	E string `json:"e,omitempty"`

	// Crv is the EC curve name (e.g., "P-256"). Present only for EC.
	Crv string `json:"crv,omitempty"`

	// X is the base64url-encoded EC point x-coordinate. EC only.
	X string `json:"x,omitempty"`

	// Y is the base64url-encoded EC point y-coordinate. EC only.
	Y string `json:"y,omitempty"`
}

// JWKSet is the §10.3 JSON Web Key Set (RFC 7517 §5) document
// the gateway publishes at /.well-known/jwks.json. During the §10.3
// 24h overlap window the set carries the current key plus every
// retained previous key, so a client that caches the document still
// verifies a token signed under a key the gateway has just rotated
// away from. Past the overlap window the rotated key drops out of
// Keys once RetireExpired prunes it.
type JWKSet struct {
	Keys []JWK `json:"keys"`
}

// PublicJWK is the interface a keyed signer implements to publish its
// JOSE metadata into a JWKS document without exposing private key
// material. The implementation returns a JWK whose key-material
// fields (N/E for RSA, X/Y/Crv for EC, k for "oct") are populated
// only for asymmetric public halves; a symmetric key returns a JWK
// with Kty="oct" and Kid/Alg/Use set but no secret. The rotating
// verifier walks its key set, asks each entry for its PublicJWK, and
// renders the union into the served document.
type PublicJWK interface {
	// PublicJWK returns the published JWK entry for this key. The
	// returned JWK MUST NOT carry private key material; HMAC signers
	// publish Kty="oct" with no `k` field, asymmetric signers
	// publish the public half only.
	PublicJWK() JWK
}

// PublicJWK on an HMACSigner returns the metadata-only JWK for the
// dev / §4 KMS-wrapped HS256 signing key. The shared secret is
// deliberately omitted; the entry exists so a client can confirm a
// token's `kid` references a key the gateway recognises and so the
// publication surface is uniform across the HMAC and (future)
// asymmetric backends.
func (s *HMACSigner) PublicJWK() JWK {
	return JWK{
		Kty: "oct",
		Use: "sig",
		Alg: "HS256",
		Kid: s.keyID,
	}
}

// PublicJWK on a KMSSigner delegates to the inner HMACSigner. The
// §4 KMS-envelope-backed signer's on-the-wire algorithm is HS256, so
// the JWKS entry is identical to the dev HMAC signer's.
func (s *KMSSigner) PublicJWK() JWK { return s.inner.PublicJWK() }

// JWKSource is the minimum surface the JWKS handler needs from a
// rotating-key holder: the current key and every retained previous
// key, each as a PublicJWK. A RotatingVerifier (whose key versions
// are HMACSigners or KMSSigners, both of which satisfy PublicJWK)
// satisfies JWKSource through the methods below.
type JWKSource interface {
	// CurrentJWK returns the JWK for the key new tokens are signed
	// with. The handler always emits this entry.
	CurrentJWK() JWK

	// PreviousJWKs returns the JWKs for every retained previous key,
	// in unspecified order. During the §10.3 overlap window these
	// entries are still authoritative for token verification; once
	// RetireExpired prunes a previous key, it disappears from this
	// slice.
	PreviousJWKs() []JWK
}

// CurrentJWK on a RotatingVerifier returns the JWK of the key new
// tokens are signed with. The underlying keyedVerifier MUST also
// satisfy PublicJWK; both HMACSigner and KMSSigner do.
func (v *RotatingVerifier) CurrentJWK() JWK {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return publicJWKOf(v.current.verifier)
}

// PreviousJWKs on a RotatingVerifier returns the JWK of every
// retained previous key. The entries stay until RetireExpired prunes
// a key whose §10.3 overlap window has closed.
func (v *RotatingVerifier) PreviousJWKs() []JWK {
	v.mu.RLock()
	defer v.mu.RUnlock()
	out := make([]JWK, 0, len(v.previous))
	for _, kv := range v.previous {
		out = append(out, publicJWKOf(kv.verifier))
	}
	return out
}

// publicJWKOf returns the PublicJWK of v, or a metadata-only JWK that
// names only the kid when v does not satisfy PublicJWK. A keyed
// verifier without PublicJWK should not exist in practice — every
// concrete signer in this package implements it — so the fallback is
// defensive rather than load-bearing.
func publicJWKOf(v keyedVerifier) JWK {
	if pj, ok := v.(PublicJWK); ok {
		return pj.PublicJWK()
	}
	return JWK{Kty: "oct", Use: "sig", Kid: v.KeyID()}
}

// JWKSHandler serves the §10.3 JWK Set document at the JWKS endpoint
// (RFC 7517 §5; the well-known path is /.well-known/jwks.json per
// RFC 8615). It reads the live key set from a JWKSource on every
// request so a key rotation lands in the served document without a
// gateway restart and a RetireExpired sweep drops the retired key
// from the response the moment the overlap window closes.
//
// JWKSHandler is safe for concurrent use; the only shared state is
// the JWKSource reference, which is set at construction and is itself
// safe for concurrent reads. The handler is unauthenticated by
// design: a JWKS document carries only public material, so anyone
// who can call /v1/oauth/token can also see which keys validate the
// resulting tokens.
type JWKSHandler struct {
	source JWKSource
	mu     sync.Mutex // serializes Marshal output ordering, see below
}

// NewJWKSHandler returns a JWKSHandler that publishes source's keys.
// Panics on a nil source — the construction-time invariant catches a
// gateway that wires the JWKS endpoint without a key set.
func NewJWKSHandler(source JWKSource) *JWKSHandler {
	if source == nil {
		panic("jwt: NewJWKSHandler: nil JWKSource")
	}
	return &JWKSHandler{source: source}
}

// ServeHTTP serves GET /.well-known/jwks.json. Method other than GET
// returns 405; the body is `application/json` per RFC 7517. The
// document is emitted with deterministic key ordering (current first,
// then previous keys sorted by kid) so a client comparing two
// snapshots can diff cleanly.
func (h *JWKSHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	doc := h.Document()

	h.mu.Lock()
	body, err := json.Marshal(doc)
	h.mu.Unlock()
	if err != nil {
		http.Error(w, "marshal jwks", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/jwk-set+json")
	// JWKS rotations propagate through the overlap window, so a short
	// cache is safe; a longer cache would make a freshly rotated key
	// take time to surface to clients caching the document.
	w.Header().Set("Cache-Control", "public, max-age=300")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write(body)
}

// Document builds the published JWKSet. Exposed so callers (tests,
// preflight checks) can inspect the document without going through
// HTTP. The returned set always carries the current key first,
// followed by every retained previous key sorted by kid; the
// ordering is deterministic so a snapshot diff across rotations is
// meaningful.
func (h *JWKSHandler) Document() JWKSet {
	cur := h.source.CurrentJWK()
	prev := h.source.PreviousJWKs()
	// Stable order within the previous set: sort by kid. The current
	// key always leads; clients walk Keys in order and the first
	// matching kid wins (a JWKS does not require uniqueness, but in
	// practice every kid is unique because Rotate forbids reuse).
	sortJWKsByKid(prev)
	keys := make([]JWK, 0, 1+len(prev))
	keys = append(keys, cur)
	keys = append(keys, prev...)
	return JWKSet{Keys: keys}
}

// sortJWKsByKid sorts the slice in place by ascending Kid. The
// simplest insertion sort suffices: the previous-keys set rarely
// exceeds a handful of entries during normal operation.
func sortJWKsByKid(s []JWK) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1].Kid > s[j].Kid; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
