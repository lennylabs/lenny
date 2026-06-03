// SPDX-License-Identifier: MIT

package upgradeservice

// The §25.8 canonical error-code table (spec line 3629). Every code the
// platform-lifecycle surface returns is declared here so the symbols
// exist in code even before the handler that returns each one is built
// (the preflight, image-resolution, manual-CRD-rollback, and config
// paths are surfaced as upgrade orchestration grows). External operator
// agents and the §25.2 canonical-error envelope reference these strings;
// declaring them in one place keeps the wire values aligned with the
// spec table verbatim.
const (
	// CodeUpgradeAlreadyInProgress (POLICY, 409): an upgrade is already
	// running. The platform_upgrade_state singleton holds one upgrade at
	// a time, so a second start is rejected.
	CodeUpgradeAlreadyInProgress = "UPGRADE_ALREADY_IN_PROGRESS"
	// CodeUpgradePreflightFailed (PERMANENT, 422): preflight checks
	// failed. details.failures lists each (image not pullable, health not
	// green, version too old).
	CodeUpgradePreflightFailed = "UPGRADE_PREFLIGHT_FAILED"
	// CodeUpgradeImageNotPullable (PERMANENT, 422): one or more target
	// images could not be resolved from the configured registry.
	// details.images lists the failing references.
	CodeUpgradeImageNotPullable = "UPGRADE_IMAGE_NOT_PULLABLE"
	// CodeUpgradeRollbackUnavailable (PERMANENT, 409): schema migration
	// completed; rollback requires a database restore.
	CodeUpgradeRollbackUnavailable = "UPGRADE_ROLLBACK_UNAVAILABLE"
	// CodeUpgradeRollbackManualCRD (PERMANENT, 409): CRD rollback requires
	// manual intervention.
	CodeUpgradeRollbackManualCRD = "UPGRADE_ROLLBACK_MANUAL_CRD"
	// CodeUpgradeNotInProgress (PERMANENT, 409): no upgrade to
	// proceed/pause/rollback.
	CodeUpgradeNotInProgress = "UPGRADE_NOT_IN_PROGRESS"
	// CodeUpgradeChannelUnreachable (TRANSIENT, 503): the release channel
	// is unreachable. Declared in check.go as CodeChannelUnreachable; the
	// alias keeps the canonical-table name available alongside the others.
	CodeUpgradeChannelUnreachable = CodeChannelUnreachable
	// CodeConfigValidationFailed (PERMANENT, 422): config schema
	// validation failed. details.errors lists each violation.
	CodeConfigValidationFailed = "CONFIG_VALIDATION_FAILED"
	// CodeConfigRestartRequired (PERMANENT, 422): the setting change
	// requires a gateway restart.
	CodeConfigRestartRequired = "CONFIG_RESTART_REQUIRED"
)

// Section258ErrorCodes returns the nine §25.8 canonical error codes in
// spec-table order (spec line 3629). The package test asserts the set
// matches the spec table exactly so a future rename cannot silently
// drift a wire value away from the documented contract.
func Section258ErrorCodes() []string {
	return []string{
		CodeUpgradeAlreadyInProgress,
		CodeUpgradePreflightFailed,
		CodeUpgradeImageNotPullable,
		CodeUpgradeRollbackUnavailable,
		CodeUpgradeRollbackManualCRD,
		CodeUpgradeNotInProgress,
		CodeUpgradeChannelUnreachable,
		CodeConfigValidationFailed,
		CodeConfigRestartRequired,
	}
}
