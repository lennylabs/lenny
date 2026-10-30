// SPDX-License-Identifier: MIT

// Package preflight holds the decision logic for the lenny-preflight
// install-and-upgrade checks (§17.9). Each check is a pure function
// over the inputs the preflight Job collects from the cluster and the
// chart values, so the checks are unit-testable without a live
// cluster; the lenny-preflight binary is the thin shim that gathers
// the inputs and runs them.
package preflight

import "fmt"

// Decision is the outcome of one preflight check.
type Decision struct {
	// Passed is true when the check found no problem.
	Passed bool
	// Reason is the failure message when Passed is false, carrying the
	// §17.9 error code as its prefix; empty when Passed is true.
	Reason string
}

// PhaseStampEntry is one feature-flag record in the
// lenny-deployment-phase-stamp ConfigMap (§17.2). The ConfigMap stores
// each value as a JSON object; the preflight Job decodes it into this
// struct before calling CheckPhaseStamp.
type PhaseStampEntry struct {
	// Enabled is true when the flag has been turned on at least once.
	Enabled bool `json:"enabled"`
	// EnabledAt is the RFC3339 time the flag was first enabled.
	EnabledAt string `json:"enabledAt"`
}

// CheckPhaseStamp verifies the §17.2 feature-flag downgrade invariant:
// for every admission-plane feature flag the phase-stamp records as
// enabled, the incoming chart values must also enable it, unless the
// operator has set acceptFeatureFlagDowngrade for that flag. A
// recorded-enabled flag rendered as disabled without the override is
// an invalid admission-plane downgrade and fails the check fail-closed
// with PREFLIGHT_PHASE_STAMP_MISMATCH.
//
// incoming maps each feature flag to its value in the rendering chart
// values; stamp is the decoded phase-stamp ConfigMap; accepted maps
// each flag to its acceptFeatureFlagDowngrade override value.
func CheckPhaseStamp(incoming map[string]bool, stamp map[string]PhaseStampEntry, accepted map[string]bool) Decision {
	for flag, entry := range stamp {
		if !entry.Enabled {
			continue
		}
		if incoming[flag] {
			continue
		}
		if accepted[flag] {
			continue
		}
		return Decision{
			Passed: false,
			Reason: fmt.Sprintf("PREFLIGHT_PHASE_STAMP_MISMATCH: flag %q is recorded as enabled "+
				"in lenny-deployment-phase-stamp (enabledAt=%s) but the incoming values render it "+
				"as disabled without acceptFeatureFlagDowngrade.%s=true", flag, entry.EnabledAt, flag),
		}
	}
	return Decision{Passed: true}
}
