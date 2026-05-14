// SPDX-License-Identifier: MIT

package jwt_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
)

// FuzzHMACVerifyDoesNotPanic ensures the §10.2 verifier never panics
// regardless of how malformed the input string is. A panic-inducing
// input would let a malicious token take down the gateway via the
// auth middleware.
//
// We do not assert verification succeeds on arbitrary inputs — the
// invariant is that the verifier either returns a Claims value or a
// typed *VerifyError, never a panic.
func FuzzHMACVerifyDoesNotPanic(f *testing.F) {
	signer := jwt.NewHMACSigner("k1", []byte("dev-secret"))

	// Seed corpus: a valid token, a tampered token, a totally
	// random string, and the empty string.
	if tok, err := signer.Sign(jwt.Claims{Subject: "alice", TenantID: "acme", Expiry: 1<<31 - 1}); err == nil {
		f.Add(tok)
		f.Add(tok[:len(tok)-1] + "X") // tampered signature
	}
	f.Add("")
	f.Add(".")
	f.Add("..")
	f.Add("a.b.c")
	f.Add("aaaaaaaaaaaaaaaaaaaaaaa.bbbbbbbbbbbbbbbbbbbb.cccccccccccccccccccc")

	f.Fuzz(func(t *testing.T, token string) {
		_, _ = signer.Verify(token)
		// The only invariant we care about here is "no panic".
		// Any return value (success or VerifyError) is acceptable.
	})
}
