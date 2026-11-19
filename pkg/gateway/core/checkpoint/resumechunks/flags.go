// SPDX-License-Identifier: MIT

package resumechunks

import (
	"flag"

	"github.com/lennylabs/lenny/pkg/common/envdefault"
)

// Flags holds the flags only the resume chunk resolver reads.
type Flags struct {
	// PartialRecoveryThresholdFraction is the value of
	// --partial-recovery-threshold-fraction.
	PartialRecoveryThresholdFraction *float64
}

// RegisterFlags registers the flags of the resume chunk resolver on fs and
// returns their values, which fs fills when it parses.
func RegisterFlags(fs *flag.FlagSet) *Flags {
	f := &Flags{}
	f.PartialRecoveryThresholdFraction = fs.Float64("partial-recovery-threshold-fraction",
		envdefault.Float64("LENNY_PARTIAL_RECOVERY_THRESHOLD_FRACTION", 0.5),
		"§10.1.7 / §17.8.1 partialRecoveryThresholdFraction: the minimum fraction of the baseline full-checkpoint bytes a partial checkpoint must have confirmed before the resume path reassembles it rather than falling back to the last full checkpoint. A fraction in [0.0, 1.0]. Default 0.5. Override via LENNY_PARTIAL_RECOVERY_THRESHOLD_FRACTION.")
	return f
}
