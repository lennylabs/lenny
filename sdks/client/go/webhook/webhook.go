// SPDX-License-Identifier: MIT

// Package webhook verifies §14 Lenny webhook signatures. A webhook
// delivery carries an X-Lenny-Signature header of the form
// t=<unix_seconds>,v1=<hex_signature>, where the signature is an
// HMAC-SHA256 over "<unix_seconds>.<raw_body>". A receiver verifies
// the HMAC with the callbackSecret it supplied at session creation
// and rejects a delivery timestamp outside a five-minute replay
// window.
//
// The Verifier wraps the platform's webhooksig implementation so an
// SDK consumer verifies deliveries with the same scheme the gateway
// signs them.
package webhook

import (
	"errors"
	"net/http"
	"time"

	"github.com/lennylabs/lenny/pkg/webhooksig"
)

// SignatureHeader is the §14 HTTP header that carries the webhook
// signature.
const SignatureHeader = "X-Lenny-Signature"

// ReplayWindow is the §14 maximum delivery-timestamp skew. A
// delivery whose timestamp is more than this far from the receiver's
// clock is rejected.
const ReplayWindow = webhooksig.ReplayWindow

// Verification errors. Each wraps the corresponding webhooksig
// sentinel, so a caller may match either this package's error or the
// platform sentinel with errors.Is.
var (
	// ErrMalformedSignature reports an X-Lenny-Signature header that
	// does not parse as t=<unix>,v1=<hex>.
	ErrMalformedSignature = webhooksig.ErrMalformedHeader
	// ErrReplayWindow reports a delivery timestamp outside the
	// five-minute replay window.
	ErrReplayWindow = webhooksig.ErrReplayWindow
	// ErrSignatureMismatch reports a signature that matches none of
	// the configured secrets.
	ErrSignatureMismatch = webhooksig.ErrSignatureMismatch
	// ErrMissingSignature reports a delivery with no
	// X-Lenny-Signature header.
	ErrMissingSignature = errors.New("webhook: request is missing the X-Lenny-Signature header")
)

// Verifier checks webhook delivery signatures against one or more
// secrets. Holding more than one secret lets a receiver verify
// deliveries during a secret-rotation overlap window. The zero value
// is not usable; construct with New.
type Verifier struct {
	secrets [][]byte
	now     func() time.Time
}

// Option configures a Verifier.
type Option func(*Verifier)

// WithClock overrides the time source the Verifier uses for the
// replay-window check. Tests inject a fixed clock; production leaves
// this unset.
func WithClock(now func() time.Time) Option {
	return func(v *Verifier) {
		if now != nil {
			v.now = now
		}
	}
}

// New returns a Verifier that accepts a delivery signed with any of
// the supplied secrets. Pass the callbackSecret supplied at session
// creation; pass both the old and new secret during a rotation
// overlap. New returns an error when no secret is supplied.
func New(secrets [][]byte, opts ...Option) (*Verifier, error) {
	if len(secrets) == 0 {
		return nil, errors.New("webhook: at least one signing secret is required")
	}
	v := &Verifier{
		secrets: secrets,
		now:     func() time.Time { return time.Now() },
	}
	for _, opt := range opts {
		opt(v)
	}
	return v, nil
}

// NewWithSecret is the single-secret convenience constructor.
func NewWithSecret(secret []byte, opts ...Option) (*Verifier, error) {
	return New([][]byte{secret}, opts...)
}

// Verify checks that signatureHeader is a valid §14 signature over
// body as of the Verifier's clock. signatureHeader is the raw value
// of the X-Lenny-Signature header. Verify returns nil when the
// signature is valid, ErrMalformedSignature when the header does not
// parse, ErrReplayWindow when the timestamp is stale, and
// ErrSignatureMismatch when the HMAC matches no configured secret.
func (v *Verifier) Verify(body []byte, signatureHeader string) error {
	if signatureHeader == "" {
		return ErrMissingSignature
	}
	return webhooksig.Verify(body, signatureHeader, v.now(), v.secrets...)
}

// VerifyRequest reads the X-Lenny-Signature header from r and
// verifies it against body. The caller is responsible for reading
// r.Body into body before calling, because Verify must hash the
// exact raw bytes the gateway signed.
func (v *Verifier) VerifyRequest(r *http.Request, body []byte) error {
	return v.Verify(body, r.Header.Get(SignatureHeader))
}
