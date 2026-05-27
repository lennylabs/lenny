// SPDX-License-Identifier: MIT

package main

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
)

// TestJWKSAdvertisesAsymmetric covers the F-10.2.14 helper used by
// main to decide whether to log the "metadata-only" notice when
// --jwks-publish is set. The published JWKS for the v1 HMAC signer
// carries `kty: oct` only; asymmetric backends (RSA, EC) light the
// publication path up as load-bearing.
// spec: §10.2 line 195. F-10.2.14.
func TestJWKSAdvertisesAsymmetric(t *testing.T) {
	cases := []struct {
		name string
		doc  jwt.JWKSet
		want bool
	}{
		{"empty", jwt.JWKSet{}, false},
		{"hmac-only", jwt.JWKSet{Keys: []jwt.JWK{
			{Kty: "oct", Kid: "k1", Use: "sig", Alg: "HS256"},
			{Kty: "oct", Kid: "k2", Use: "sig", Alg: "HS256"},
		}}, false},
		{"rsa-present", jwt.JWKSet{Keys: []jwt.JWK{
			{Kty: "oct", Kid: "k1"},
			{Kty: "RSA", Kid: "k2"},
		}}, true},
		{"ec-only", jwt.JWKSet{Keys: []jwt.JWK{
			{Kty: "EC", Kid: "k1", Crv: "P-256"},
		}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := jwksAdvertisesAsymmetric(c.doc); got != c.want {
				t.Errorf("jwksAdvertisesAsymmetric(%s) = %v, want %v", c.name, got, c.want)
			}
		})
	}
}
