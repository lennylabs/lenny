// SPDX-License-Identifier: MIT

// Package slo carries the §16.5 service-level-objective startup
// contract. The §16.5 SLO targets are first-principles design estimates
// that must not back customer SLA commitments until the Phase 14.5
// benchmark gate validates them, so a deployment running the unvalidated
// defaults surfaces a startup warning.
package slo

// ProvisionalWarning is the §16.5 line 609 startup warning a binary logs
// when the SLO targets remain at their provisional defaults. The text is
// transcribed verbatim from the spec so log scrapers can match on it.
// spec: spec/16_observability.md line 609.
const ProvisionalWarning = "[WARN] SLO targets in Section 16.5 are provisional first-principles estimates and have not been validated by load testing. Do not use for customer SLA commitments until Phase 14.5 benchmark gate is complete."

// StartupWarning reports the §16.5 provisional-SLO warning to emit at
// startup and whether it should be emitted. The warning is suppressed
// only when the Phase 14.5 benchmark automation has set the
// slo.validated flag (validated == true); any deployment that has not
// completed the validation gate emits it. spec: §16.5 lines 609, 623.
func StartupWarning(validated bool) (string, bool) {
	if validated {
		return "", false
	}
	return ProvisionalWarning, true
}
