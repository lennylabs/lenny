// SPDX-License-Identifier: MIT

package signingkeys_test

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/signingkeys"
)

// TestTheUploadTokenVerificationTimeFollowsTheConfiguredValues checks that
// a replaced upload-token key verifies for the stale bound (the overlap
// window) plus the longer of the overlap window and the token lifetime,
// computed from the values the policy is given: 10 minutes at the
// defaults, and longer when the token lifetime or the overlap window grows.
//
// spec: §7.1 (Normal Flow)
func TestTheUploadTokenVerificationTimeFollowsTheConfiguredValues(t *testing.T) {
	cases := []struct {
		name              string
		overlap, lifetime time.Duration
		want              time.Duration
	}{
		{"defaults", 5 * time.Minute, 5 * time.Minute, 10 * time.Minute},
		{"a token lifetime above the overlap window", 5 * time.Minute, 15 * time.Minute, 20 * time.Minute},
		{"a token lifetime below the overlap window", 10 * time.Minute, time.Minute, 20 * time.Minute},
		{"no token lifetime", 5 * time.Minute, 0, 10 * time.Minute},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := signingkeys.UploadTokenVerificationTime(c.overlap, c.lifetime); got != c.want {
				t.Errorf("UploadTokenVerificationTime(%s, %s) = %s, want %s", c.overlap, c.lifetime, got, c.want)
			}
			p := signingkeys.UploadTokenPolicy(24*time.Hour, c.overlap, c.lifetime)
			if p.VerifyFor != c.want || p.RotationInterval != 24*time.Hour || p.Purpose != signingkeys.PurposeUploadToken {
				t.Errorf("UploadTokenPolicy = %+v, want VerifyFor %s", p, c.want)
			}
		})
	}
}
