// SPDX-License-Identifier: MIT

package jwt

import (
	"errors"
	"fmt"
)

// MultiVerifier verifies a token against an ordered set of member
// Verifiers and accepts the token when any member accepts it. It
// implements the Verifier interface, so it drops in anywhere the
// gateway uses a single-key verifier.
//
// The §10.2 production gateway runs with a single verifier (the Token
// Service signer). MultiVerifier exists for the §17.4 Embedded Mode
// gateway, which must accept two distinct token populations: the
// in-process Token Service tokens and the tokens minted by the
// embedded OIDC provider for `lenny token print`. The two are signed
// with different keys, so a single-key verifier cannot cover both.
//
// Verify tries each member in registration order and returns the
// claims from the first member that accepts the token. When no member
// accepts it, Verify returns the error from the first member: that
// member is the primary verifier, and its rejection reason (expired,
// signature_mismatch, malformed) is the most actionable one to report.
// One member rejecting a token signed by another member is expected
// and does not change the outcome.
//
// MultiVerifier is safe for concurrent use when its member Verifiers
// are. The member set is fixed at construction.
type MultiVerifier struct {
	members []Verifier
}

// NewMultiVerifier returns a MultiVerifier over members, tried in the
// given order. The first member is the primary verifier whose error is
// surfaced when every member rejects a token. NewMultiVerifier panics
// when members is empty, because a verifier with no keys would reject
// every request and that is a construction-time programmer error.
func NewMultiVerifier(members ...Verifier) *MultiVerifier {
	if len(members) == 0 {
		panic("jwt: NewMultiVerifier: no member verifiers")
	}
	for i, m := range members {
		if m == nil {
			panic(fmt.Sprintf("jwt: NewMultiVerifier: member %d is nil", i))
		}
	}
	return &MultiVerifier{members: append([]Verifier(nil), members...)}
}

// Verify returns the claims from the first member that accepts token.
// When no member accepts it, Verify returns the first member's error.
func (v *MultiVerifier) Verify(token string) (Claims, error) {
	var firstErr error
	for _, m := range v.members {
		claims, err := m.Verify(token)
		if err == nil {
			return claims, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return Claims{}, firstErr
}

// Len reports the number of member verifiers.
func (v *MultiVerifier) Len() int { return len(v.members) }

// IsUnknownKey reports whether err is a *VerifyError whose Reason
// indicates the token was signed by a key the verifier does not hold
// (missing_kid, unknown_kid, or key_retired). A MultiVerifier built
// over RotatingVerifier members returns one of these when a token
// matches no member's key set.
func IsUnknownKey(err error) bool {
	var ve *VerifyError
	if errors.As(err, &ve) {
		switch ve.Reason {
		case "missing_kid", "unknown_kid", "key_retired":
			return true
		}
	}
	return false
}

// Compile-time check: MultiVerifier satisfies the Verifier interface.
var _ Verifier = (*MultiVerifier)(nil)
