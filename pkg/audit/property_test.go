// SPDX-License-Identifier: MIT

package audit

import (
	"testing"

	"pgregory.net/rapid"
)

// TestPropertyValidatePairingRoundtrip — for every pairing produced
// by CompatibleProfiles, ValidatePairing returns nil. The two
// functions must agree.
//
// spec: 11.7 (audit retention × compliance pairing matrix)
// diagnosis: A documented compatible pair was rejected, or an
//
//	incompatible pair was accepted. The two tables are out
//	of sync.
func TestPropertyValidatePairingRoundtrip(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		preset := rapid.SampledFrom(AllRetentionPresets()).Draw(rt, "preset")
		compatible := preset.CompatibleProfiles()
		for _, profile := range compatible {
			if err := ValidatePairing(preset, profile); err != nil {
				rt.Errorf("compatible pairing rejected: %v × %v: %v", preset, profile, err)
			}
		}
	})
}

// TestPropertyAlarmingSubset — IsAlarming for the documented
// alarming states is true; for verified/unchecked it is false.
//
// spec: 11.7 (audit alarm semantics)
// diagnosis: IsAlarming flipped on a known-good state, or
//
//	missed a known-bad one. The verifier would then misroute
//	alerts.
func TestPropertyAlarmingSubset(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		state := rapid.SampledFrom(AllChainIntegrities()).Draw(rt, "state")
		// Per pkg/audit's TestChainIntegrityIsAlarming, ChainBroken
		// is the only state that alarms by itself. GapSuspected
		// requires the verifier to escalate; it does not alarm
		// directly.
		if state == ChainBroken {
			if !state.IsAlarming() {
				rt.Errorf("ChainBroken must alarm")
			}
		} else if state.IsAlarming() {
			rt.Errorf("non-broken state reported as alarming: %v", state)
		}
	})
}

// TestPropertyPresetDaysNonNegative — every retention preset reports
// a non-negative day count.
//
// spec: 11.7 (retention preset semantics)
// diagnosis: A negative day count would let downstream callers
//
//	produce malformed time windows.
func TestPropertyPresetDaysNonNegative(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		preset := rapid.SampledFrom(AllRetentionPresets()).Draw(rt, "preset")
		if preset.PresetDays() < 0 {
			rt.Errorf("preset %v reports negative days: %d", preset, preset.PresetDays())
		}
	})
}
