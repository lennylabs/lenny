// SPDX-License-Identifier: MIT

package jwt

import (
	"encoding/json"
	"strings"
	"testing"
)

// spec: 10.3
// diagnosis: an HMAC signer's PublicJWK emitted private key material
// or named the wrong algorithm. A symmetric key's JWK must carry only
// kid/alg/use/kty; the shared secret must never leave the process.
func TestHMACSignerPublicJWKHasNoSecret(t *testing.T) {
	t.Parallel()
	s := NewHMACSigner("kid-1", []byte("secret-shared-with-no-one"))
	jwk := s.PublicJWK()

	if jwk.Kty != "oct" {
		t.Errorf("kty = %q, want oct", jwk.Kty)
	}
	if jwk.Use != "sig" {
		t.Errorf("use = %q, want sig", jwk.Use)
	}
	if jwk.Alg != "HS256" {
		t.Errorf("alg = %q, want HS256", jwk.Alg)
	}
	if jwk.Kid != "kid-1" {
		t.Errorf("kid = %q, want kid-1", jwk.Kid)
	}

	body, err := json.Marshal(jwk)
	if err != nil {
		t.Fatalf("marshal JWK: %v", err)
	}
	if strings.Contains(string(body), "secret-shared-with-no-one") {
		t.Fatalf("JWK marshalled with secret material: %s", body)
	}
	// "k" is the JWK field name for a symmetric secret (RFC 7518
	// §6.4). It must not appear.
	if strings.Contains(string(body), "\"k\"") {
		t.Fatalf("JWK marshalled with a `k` field: %s", body)
	}
}
