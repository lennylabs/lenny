// SPDX-License-Identifier: MIT

package failopen

import "fmt"

// ValidateUserFraction enforces the §12.4 line 222 config-time invariant
// on quotaUserFailOpenFraction: a value outside (0, 1.0] is rejected at
// startup with the spec's CONFIG_INVALID message. The gateway calls this
// before constructing the controller so a misconfigured fraction fails the
// process fast rather than silently neutralizing the per-user cap.
//
// spec: §12.4 line 222 ("0 < quotaUserFailOpenFraction <= 1.0").
func ValidateUserFraction(fraction float64) error {
	if fraction <= 0 || fraction > 1.0 {
		return fmt.Errorf("CONFIG_INVALID: quotaUserFailOpenFraction (%v) must satisfy 0 < quotaUserFailOpenFraction <= 1.0", fraction)
	}
	return nil
}

// UserFractionWeakened reports whether the configured fraction is at or
// above the §12.4 line 222 threshold where the per-user fail-open cap is
// substantially weakened (the QuotaFailOpenUserFractionInoperative warning
// applies). spec: §12.4 line 222.
func UserFractionWeakened(fraction float64) bool {
	return fraction >= UserFractionWeakThreshold
}
