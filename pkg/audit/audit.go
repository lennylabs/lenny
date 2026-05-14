// SPDX-License-Identifier: MIT

// Package audit encodes the §11.7 audit-log primitives that are pure
// enums and lookup tables: the ChainIntegrity verifier states, the
// ComplianceProfile platform setting, the RetentionPreset → days
// mapping from §16.4, and the OCSFTranslationState enum from the
// translator state machine.
//
// The audit-log row writer, the per-tenant Postgres advisory lock,
// the SIEM forwarder, the OCSF translator, and the
// `audit_log_deferred_writes` reconciliation flow all live in later
// phases that import this package.
package audit

import (
	"fmt"
	"time"
)

// ChainIntegrity is the §11.7 enum the verifier stamps on each
// audit_log row to indicate whether the hash chain is valid through
// this row.
type ChainIntegrity string

const (
	// ChainVerified: the hash chain has been validated end-to-end
	// through this row.
	ChainVerified ChainIntegrity = "verified"

	// ChainBroken: mismatch detected that is not attributable to a
	// known outage or a receipted GDPR redaction. Drives the §16.5
	// AuditChainGap critical alert.
	ChainBroken ChainIntegrity = "broken"

	// ChainUnchecked: verification has not yet run, or has been
	// disabled at this scope.
	ChainUnchecked ChainIntegrity = "unchecked"

	// ChainRechainedPostOutage: chain was rewritten after the §11.7
	// item 6 reconciliation from audit_log_deferred_writes. Expected
	// value when lenny-ops deferred writes are reconciled after a
	// Postgres outage.
	ChainRechainedPostOutage ChainIntegrity = "rechained_post_outage"

	// ChainGapSuspected: sequence-number gap detected; the verifier
	// cross-references ops_postgres_outage_log to attribute the
	// cause.
	ChainGapSuspected ChainIntegrity = "gap_suspected"

	// ChainRedactedGDPR: row was rewritten in place by the §12.8
	// DeleteByUser GDPR Article 17 redaction step. The discontinuity
	// is authorized iff a signed RedactionReceipt is present and
	// signature-verified; otherwise the verifier surfaces this as a
	// missing-receipt instance via the §16.5
	// AuditRedactionReceiptMissing alert.
	ChainRedactedGDPR ChainIntegrity = "redacted_gdpr"
)

// AllChainIntegrities returns the closed §11.7 enum in spec order.
func AllChainIntegrities() []ChainIntegrity {
	return []ChainIntegrity{
		ChainVerified, ChainBroken, ChainUnchecked,
		ChainRechainedPostOutage, ChainGapSuspected, ChainRedactedGDPR,
	}
}

// IsValid reports whether c is one of the six §11.7 verifier states.
func (c ChainIntegrity) IsValid() bool {
	for _, v := range AllChainIntegrities() {
		if c == v {
			return true
		}
	}
	return false
}

// IsAlarming reports whether c should fire a §16.5 alert. ChainBroken
// always fires AuditChainGap; ChainRedactedGDPR alarms only when the
// signed receipt is missing (caller has additional context to decide).
// The other states are operational signals, not alerts.
func (c ChainIntegrity) IsAlarming() bool {
	return c == ChainBroken
}

// ComplianceProfile is the §11.7 platform-wide control setting that
// governs which runtime controls are enforced (SIEM requirement,
// grant-check interval, pgaudit, etc.). The retention window is a
// separate axis governed by RetentionPreset below.
type ComplianceProfile string

const (
	ComplianceNone    ComplianceProfile = "none"
	ComplianceSOC2    ComplianceProfile = "soc2"
	ComplianceFedRAMP ComplianceProfile = "fedramp"
	ComplianceHIPAA   ComplianceProfile = "hipaa"
)

// AllComplianceProfiles returns the closed enum.
func AllComplianceProfiles() []ComplianceProfile {
	return []ComplianceProfile{ComplianceNone, ComplianceSOC2, ComplianceFedRAMP, ComplianceHIPAA}
}

// IsValid reports whether p is one of the §11.7 compliance profiles.
func (p ComplianceProfile) IsValid() bool {
	for _, v := range AllComplianceProfiles() {
		if p == v {
			return true
		}
	}
	return false
}

// RequiresSIEM reports whether the profile mandates a configured
// `audit.siem.endpoint`. Without one, the gateway refuses to start
// per §16.4 / §11.7 — except under ComplianceNone, where the
// AuditSIEMNotConfigured warning fires but the gateway starts.
func (p ComplianceProfile) RequiresSIEM() bool {
	return p == ComplianceSOC2 || p == ComplianceFedRAMP || p == ComplianceHIPAA
}

// RetentionPreset names a Postgres-retention bundle from §16.4. Each
// preset maps to a fixed retention window in days. Operators can also
// supply a custom value with the `custom` preset.
type RetentionPreset string

const (
	// PresetSOC2: 365 days. Default.
	PresetSOC2 RetentionPreset = "soc2"

	// PresetFedRAMPHigh: 1095 days (3 years).
	PresetFedRAMPHigh RetentionPreset = "fedramp-high"

	// PresetHIPAA: 2190 days (6 years).
	PresetHIPAA RetentionPreset = "hipaa"

	// PresetNIS2DORA: 1825 days (5 years).
	PresetNIS2DORA RetentionPreset = "nis2-dora"

	// PresetCustom: deployer-specified; PresetDays returns 0 and
	// callers must use the explicit `audit.retentionDays` value.
	PresetCustom RetentionPreset = "custom"
)

// AllRetentionPresets returns the closed §16.4 enum in spec order.
func AllRetentionPresets() []RetentionPreset {
	return []RetentionPreset{PresetSOC2, PresetFedRAMPHigh, PresetHIPAA, PresetNIS2DORA, PresetCustom}
}

// IsValid reports whether p is one of the §16.4 retention presets.
func (p RetentionPreset) IsValid() bool {
	for _, v := range AllRetentionPresets() {
		if p == v {
			return true
		}
	}
	return false
}

// PresetDays returns the §16.4 retention window for the preset in
// days. PresetCustom returns 0 — callers must read the explicit
// `audit.retentionDays` value.
func (p RetentionPreset) PresetDays() int {
	switch p {
	case PresetSOC2:
		return 365
	case PresetFedRAMPHigh:
		return 1095
	case PresetHIPAA:
		return 2190
	case PresetNIS2DORA:
		return 1825
	}
	return 0
}

// PresetWindow returns the retention as a time.Duration. Zero
// duration for PresetCustom.
func (p RetentionPreset) PresetWindow() time.Duration {
	return time.Duration(p.PresetDays()) * 24 * time.Hour
}

// CompatibleProfiles returns the compliance profiles a preset is a
// valid pairing for. §16.4 says:
//
//	soc2          — soc2 or fedramp (default)
//	fedramp-high  — fedramp + this preset (FedRAMP High mandate)
//	hipaa         — hipaa
//	nis2-dora     — none or soc2 + this preset
//	custom        — any
func (p RetentionPreset) CompatibleProfiles() []ComplianceProfile {
	switch p {
	case PresetSOC2:
		return []ComplianceProfile{ComplianceSOC2, ComplianceFedRAMP}
	case PresetFedRAMPHigh:
		return []ComplianceProfile{ComplianceFedRAMP}
	case PresetHIPAA:
		return []ComplianceProfile{ComplianceHIPAA}
	case PresetNIS2DORA:
		return []ComplianceProfile{ComplianceNone, ComplianceSOC2}
	case PresetCustom:
		return AllComplianceProfiles()
	}
	return nil
}

// ValidatePairing reports whether the preset is compatible with the
// supplied compliance profile. Returns *PairingError on mismatch.
func ValidatePairing(p RetentionPreset, c ComplianceProfile) error {
	if !p.IsValid() {
		return fmt.Errorf("audit: retention preset %q is not in the §16.4 enum", p)
	}
	if !c.IsValid() {
		return fmt.Errorf("audit: compliance profile %q is not in the §11.7 enum", c)
	}
	for _, ok := range p.CompatibleProfiles() {
		if ok == c {
			return nil
		}
	}
	return &PairingError{Preset: p, Profile: c, Compatible: p.CompatibleProfiles()}
}

// PairingError captures a preset/profile mismatch.
type PairingError struct {
	Preset     RetentionPreset
	Profile    ComplianceProfile
	Compatible []ComplianceProfile
}

func (e *PairingError) Error() string {
	return fmt.Sprintf("audit: retention preset %q is not compatible with compliance profile %q (compatible: %v)", e.Preset, e.Profile, e.Compatible)
}

// OCSFTranslationState is the §11.7 audit_log.ocsf_translation_state
// enum. Tracks the per-row progress of the OCSF translator's
// retry/dead-letter pipeline.
type OCSFTranslationState string

const (
	OCSFPending      OCSFTranslationState = "pending"
	OCSFRetryPending OCSFTranslationState = "retry_pending"
	OCSFSucceeded    OCSFTranslationState = "succeeded"
	OCSFDeadLettered OCSFTranslationState = "dead_lettered"
)

// AllOCSFStates returns the closed §11.7 enum in spec order
// (matching the state-machine progression).
func AllOCSFStates() []OCSFTranslationState {
	return []OCSFTranslationState{OCSFPending, OCSFRetryPending, OCSFSucceeded, OCSFDeadLettered}
}

// IsValid reports whether s is one of the four §11.7 translator
// states.
func (s OCSFTranslationState) IsValid() bool {
	for _, v := range AllOCSFStates() {
		if s == v {
			return true
		}
	}
	return false
}

// IsTerminal reports whether s is a sink state (the translator will
// not advance further from here). Succeeded and dead_lettered are
// terminal; pending and retry_pending are not.
func (s OCSFTranslationState) IsTerminal() bool {
	return s == OCSFSucceeded || s == OCSFDeadLettered
}
