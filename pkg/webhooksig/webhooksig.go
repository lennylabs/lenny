// SPDX-License-Identifier: MIT

// Package webhooksig implements the §14 webhook signature scheme used
// by §7.1 session callbacks and §25.5 ops event subscriptions. A
// delivery carries an X-Lenny-Signature header of the form
// t=<unix_seconds>,v1=<hex_signature>, where the signature is an
// HMAC-SHA256 over "<unix_seconds>.<raw_body>". A receiver verifies
// the HMAC and rejects a timestamp outside a five-minute replay
// window. The package is pure crypto — no transport.
package webhooksig

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

// ReplayWindow is the §14 maximum delivery-timestamp skew: a signature
// whose t is more than this far from the receiver's clock is rejected.
const ReplayWindow = 5 * time.Minute

// Sentinel verification errors.
var (
	// ErrMalformedHeader reports an X-Lenny-Signature header that does
	// not parse as t=<unix>,v1=<hex>.
	ErrMalformedHeader = errors.New("webhooksig: X-Lenny-Signature header is malformed")
	// ErrReplayWindow reports a signature timestamp outside the
	// five-minute replay window.
	ErrReplayWindow = errors.New("webhooksig: signature timestamp is outside the replay window")
	// ErrSignatureMismatch reports a signature matching none of the
	// supplied secrets.
	ErrSignatureMismatch = errors.New("webhooksig: signature does not match")
)

// Sign produces the §14 X-Lenny-Signature header value for body,
// signed with secret at delivery time t.
func Sign(secret, body []byte, t time.Time) string {
	unix := strconv.FormatInt(t.Unix(), 10)
	return "t=" + unix + ",v1=" + computeHMAC(secret, unix, body)
}

// Verify validates a §14 X-Lenny-Signature header against body as of
// now. It rejects a timestamp outside the replay window and a
// signature matching none of the supplied secrets. More than one
// secret may be passed so a receiver honors the §25.5 secret-rotation
// overlap window, during which both the old and new secret validate.
// The HMAC comparison is constant-time.
func Verify(body []byte, header string, now time.Time, secrets ...[]byte) error {
	unix, v1, err := parseHeader(header)
	if err != nil {
		return err
	}
	ts, err := strconv.ParseInt(unix, 10, 64)
	if err != nil {
		return ErrMalformedHeader
	}
	skew := now.Unix() - ts
	if skew < 0 {
		skew = -skew
	}
	if skew > int64(ReplayWindow/time.Second) {
		return ErrReplayWindow
	}
	for _, secret := range secrets {
		expected := computeHMAC(secret, unix, body)
		if hmac.Equal([]byte(expected), []byte(v1)) {
			return nil
		}
	}
	return ErrSignatureMismatch
}

// computeHMAC returns the hex-encoded HMAC-SHA256 over the §14 signing
// input "<unix>.<body>".
func computeHMAC(secret []byte, unix string, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(unix))
	mac.Write([]byte("."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// parseHeader extracts the t and v1 components of an X-Lenny-Signature
// header.
func parseHeader(header string) (unix, v1 string, err error) {
	for _, part := range strings.Split(header, ",") {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			return "", "", ErrMalformedHeader
		}
		switch strings.TrimSpace(key) {
		case "t":
			unix = value
		case "v1":
			v1 = value
		}
	}
	if unix == "" || v1 == "" {
		return "", "", ErrMalformedHeader
	}
	return unix, v1, nil
}
