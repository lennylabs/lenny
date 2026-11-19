// SPDX-License-Identifier: MIT

// Package extractionthreshold is part of the seam model: it reads the
// per-seam extraction thresholds of §4.1 from the environment and emits
// them on the lenny_gateway_extraction_threshold gauge at startup.
//
// The operator sets each threshold through the Helm value
// gateway.extractionThresholds.<subsystem>.<metric>, so the Helm release
// history records the values behind an extraction decision. The chart
// renders each value as an environment variable
// (`LENNY_EXTRACTION_THRESHOLD_<SUBSYSTEM>_<METRIC>`). The package reads
// them, applies the provisional §4.1 default to each unset entry, and
// sets the gauge for each value.
//
// spec: GW
package extractionthreshold
