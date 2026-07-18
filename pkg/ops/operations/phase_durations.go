// SPDX-License-Identifier: MIT

package operations

import "time"

// platformUpgradePhaseDurations are the §25.8 line 3496 compiled-in
// per-phase expected durations for the platform_upgrade kind's
// Preflight through Verification phases (the pkg/upgrade.Phase name
// strings). They anchor the fixed_phase_durations ETA method the
// platform-upgrade status surfaces report before ops_operation_baselines
// has enough completed-upgrade samples for historical_p50 (§25.2 line
// 394). Each value is a deployer-independent estimate, well under the
// phase's own operator-tunable timeout budget (§25.8:
// platform.upgrade.opsRollTimeoutSeconds default 600,
// gatewayRollTimeoutSeconds default 1200, controllerRollTimeoutSeconds
// default 600) so an on-schedule upgrade's ETA counts down toward zero
// instead of idling at a worst-case value.
var platformUpgradePhaseDurations = map[string]time.Duration{
	"Preflight":       30 * time.Second,
	"OpsRoll":         90 * time.Second,
	"CRDUpdate":       20 * time.Second,
	"SchemaMigration": 3 * time.Minute,
	"GatewayRoll":     4 * time.Minute,
	"ControllerRoll":  90 * time.Second,
	"Verification":    30 * time.Second,
}

// FixedPhaseDuration returns the §25.2 compiled-in expected duration for
// kind k's current phase/step, or 0 when no fixed-duration table applies
// to the kind or step (the caller then falls through to a
// lower-confidence ETA method or "none"). Only platform_upgrade has
// spec-defined per-phase durations (§25.8 line 3496); other kinds return
// 0.
//
// spec: §25.2 line 393 (etaMethod fixed_phase_durations — "from
// compiled-in per-phase durations"), §25.8 line 3496 (platform_upgrade's
// etaSeconds uses etaMethod fixed_phase_durations).
func FixedPhaseDuration(k Kind, step string) time.Duration {
	if k != KindPlatformUpgrade {
		return 0
	}
	return platformUpgradePhaseDurations[step]
}
