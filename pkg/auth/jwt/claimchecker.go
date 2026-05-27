// SPDX-License-Identifier: MIT

package jwt

import "fmt"

// ExpectedClaims declares the iss / aud values the gateway expects on
// a Bearer JWT. ClaimChecker rejects any token whose iss or aud claim
// does not match.
//
// spec: §10.2 line 237 — the standard auth chain validates
// "signature, iss, aud, exp, nbf" together. The signer wrappers
// (HMACSigner / KMSSigner / RotatingVerifier) validate signature / exp
// / nbf; ClaimChecker layers iss and aud on top so the entire chain
// matches the spec.
type ExpectedClaims struct {
	// Issuer is the canonical issuer string the gateway minted with.
	// When empty, the iss check is skipped — a deployment without an
	// issuer configured (a dev gateway) does not enforce the claim.
	Issuer string

	// Audiences is the set of audience strings the gateway considers
	// its own. A token's `aud` claim must intersect this set. An empty
	// Audiences skips the aud check, matching the empty-Issuer
	// semantics.
	Audiences []string
}

// audienceMatch reports whether got carries any of the expected
// audiences. The intersection semantics match RFC 7519 §4.1.3, where
// the `aud` claim is an array and a verifier may declare itself as
// any one of its members.
func audienceMatch(got, expected []string) bool {
	for _, a := range got {
		for _, e := range expected {
			if a == e {
				return true
			}
		}
	}
	return false
}

// ClaimChecker is a Verifier wrapper that asserts iss and aud claims
// after the inner verifier has validated signature / exp / nbf. It
// implements the Verifier interface so it drops in anywhere the
// gateway uses a single-key verifier (including inside a
// MultiVerifier).
//
// spec: §10.2 line 237.
type ClaimChecker struct {
	inner    Verifier
	expected ExpectedClaims
}

// NewClaimChecker returns a Verifier that delegates to inner and, on
// success, asserts the iss + aud claims against expected. An empty
// Issuer / empty Audiences skips the respective check, so a dev
// gateway without configured values runs unchanged.
func NewClaimChecker(inner Verifier, expected ExpectedClaims) *ClaimChecker {
	if inner == nil {
		panic("jwt: NewClaimChecker: inner verifier is nil")
	}
	return &ClaimChecker{inner: inner, expected: expected}
}

// Verify implements the Verifier interface.
func (c *ClaimChecker) Verify(token string) (Claims, error) {
	claims, err := c.inner.Verify(token)
	if err != nil {
		return Claims{}, err
	}
	if c.expected.Issuer != "" && claims.Issuer != c.expected.Issuer {
		return Claims{}, &VerifyError{
			Reason: "issuer_mismatch",
			Detail: fmt.Sprintf("token iss=%q, want %q", claims.Issuer, c.expected.Issuer),
		}
	}
	if len(c.expected.Audiences) > 0 && !audienceMatch(claims.Audience, c.expected.Audiences) {
		return Claims{}, &VerifyError{
			Reason: "audience_mismatch",
			Detail: fmt.Sprintf("token aud=%v, want one of %v", claims.Audience, c.expected.Audiences),
		}
	}
	return claims, nil
}

// Compile-time interface check.
var _ Verifier = (*ClaimChecker)(nil)
