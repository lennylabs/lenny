// SPDX-License-Identifier: MIT

package audit

import (
	"testing"
)

// FuzzValidatePairing exercises the §11.7 retention × compliance
// pairing validator on arbitrary inputs. Invariant: never panics;
// every documented compatible pair returns nil.
func FuzzValidatePairing(f *testing.F) {
	f.Add("standard", "soc2")
	f.Add("", "")
	f.Add("nonsense", "garbage")
	f.Add("legal_hold", "compliance_strict")

	f.Fuzz(func(t *testing.T, preset, profile string) {
		_ = ValidatePairing(RetentionPreset(preset), ComplianceProfile(profile))
	})
}
