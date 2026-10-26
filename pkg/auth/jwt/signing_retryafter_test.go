// SPDX-License-Identifier: MIT

package jwt

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

// spec: 10.2 (Authentication) "KMS signing failure mode"
// diagnosis: a mint path read the wrong Retry-After for a Sign error. A
// *SigningError anywhere in the chain carries the breaker's value, rounded
// up to whole seconds with a floor of 1; any other error, including nil
// for an unconfigured signer, takes the 1-second value the spec gives a
// signing error while the breaker is not open.
func TestSigningRetryAfterSeconds_spec_10_2(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, 1},
		{"plain error", errors.New("kms down"), 1},
		{"throttled 400ms", &SigningError{RetryAfter: 400 * time.Millisecond}, 1},
		{"open 2.3s", &SigningError{RetryAfter: 2300 * time.Millisecond, Err: ErrSigningUnavailable}, 3},
		{"wrapped open 30s", fmt.Errorf("mint: %w", &SigningError{RetryAfter: 30 * time.Second}), 30},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SigningRetryAfterSeconds(tc.err); got != tc.want {
				t.Errorf("SigningRetryAfterSeconds = %d, want %d", got, tc.want)
			}
		})
	}
}
