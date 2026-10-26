// SPDX-License-Identifier: MIT

package jwt

import (
	"fmt"
	"time"
)

// The auth.capabilityTokenTtlSeconds default and bound. The value is the
// default lifetime of every Lenny-issued JWT whose holder obtains a new token
// before exp by requesting one: the playground bearers the gateway mints and
// the POST /v1/oauth/token JWTs the Token Service issues for a request that
// carries no expires_in.
//
// spec: §10.2 (Authentication) "JWT claims" expiry; §27.2.
const (
	DefaultCapabilityTokenTTL = 900 * time.Second
	MinCapabilityTokenTTL     = 120 * time.Second
	MaxCapabilityTokenTTL     = 3600 * time.Second
)

// ValidateCapabilityTokenTTL returns an error naming
// auth.capabilityTokenTtlSeconds when d lies outside the §10.2 bound
// 120s..3600s. Both binaries that read the value treat the error as fatal at
// startup.
//
// spec: §10.2 (Authentication) "JWT claims" expiry.
func ValidateCapabilityTokenTTL(d time.Duration) error {
	if d < MinCapabilityTokenTTL || d > MaxCapabilityTokenTTL {
		return fmt.Errorf("auth.capabilityTokenTtlSeconds %s is outside the bound %s..%s",
			d, fmtSeconds(MinCapabilityTokenTTL), fmtSeconds(MaxCapabilityTokenTTL))
	}
	return nil
}

// fmtSeconds renders d as a whole number of seconds with an "s" suffix, so the
// bound reads 120s..3600s rather than 2m0s..1h0m0s.
func fmtSeconds(d time.Duration) string {
	return fmt.Sprintf("%ds", int64(d/time.Second))
}
