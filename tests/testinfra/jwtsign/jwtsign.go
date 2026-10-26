// SPDX-License-Identifier: MIT

// Package jwtsign adapts the HMAC test signer to the context-taking
// jwt.Signer interface for tests that stand an HMAC key in for the
// §10.2 ES256 signer. It lives under tests/testinfra so no package under
// pkg/ or cmd/ can import a type that makes an HMACSigner a jwt.Signer:
// production code reaches jwt.Signer only through RemoteSigner and
// BreakerSigner.
package jwtsign

import (
	"context"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
)

// HMACAdapter is a jwt.Signer over an HMACSigner. It is deliberately not
// a jwt.Verifier, so a test that relied on a handler picking its signer
// up as the verifier passes the HMACSigner as the Verifier explicitly.
type HMACAdapter struct {
	signer *jwt.HMACSigner
}

// HMAC returns a jwt.Signer whose Sign calls s.Sign and ignores ctx.
func HMAC(s *jwt.HMACSigner) *HMACAdapter { return &HMACAdapter{signer: s} }

// Sign signs c with the wrapped HMAC key. The key is in memory, so ctx
// bounds nothing.
func (a *HMACAdapter) Sign(_ context.Context, c jwt.Claims) (string, error) {
	return a.signer.Sign(c)
}

// KeyID returns the wrapped signer's kid.
func (a *HMACAdapter) KeyID() string { return a.signer.KeyID() }

// Compile-time check: HMACAdapter is a jwt.Signer.
var _ jwt.Signer = (*HMACAdapter)(nil)
