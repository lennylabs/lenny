// SPDX-License-Identifier: MIT

package recycle

import (
	"flag"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/common/envdefault"
)

// Flags holds the flags only the reserved-hold and recycle-boundary
// coordinators read.
type Flags struct {
	// ClaimHoldTTLSeconds is the value of --claim-hold-ttl-seconds.
	ClaimHoldTTLSeconds *int
}

// RegisterFlags registers the flags of the reserved-hold and recycle-boundary
// coordinators on fs and returns their values, which fs fills when it parses.
func RegisterFlags(fs *flag.FlagSet) *Flags {
	f := &Flags{}
	f.ClaimHoldTTLSeconds = fs.Int("claim-hold-ttl-seconds",
		envdefault.Int("LENNY_CLAIM_HOLD_TTL_SECONDS", int(DefaultClaimHoldTTL/time.Second)),
		"§4.6.1 reserved-hold TTL: when a recycling pod's occupancy reaches zero the gateway patches the per-pod PodClaim to `reserved` and holds it this many seconds so a back-to-back same-tenant session rebinds (reserved → bound) with no acquisition round trip; on expiry the holder deletes the claim and the pod returns to idle. A reserved pod is counted as occupied for inventory and scaling (§4.6.2), so a high value depresses apparent idle inventory and delays retirement-limit evaluation; the gateway warns at startup when it is set high. Default 10. Override via LENNY_CLAIM_HOLD_TTL_SECONDS.")
	return f
}

// HoldTTLWarning returns the startup warning the gateway logs for a
// reserved-hold TTL above HighClaimHoldTTLWarnSeconds, and false when the TTL
// is at or below that ceiling. The warning does not change the TTL the
// coordinators apply.
// spec: §4.6.1, §4.6.2.
func (f *Flags) HoldTTLWarning() (string, bool) {
	if *f.ClaimHoldTTLSeconds <= HighClaimHoldTTLWarnSeconds {
		return "", false
	}
	return fmt.Sprintf("WARNING: claimHoldTTLSeconds=%ds exceeds the advisory ceiling of %ds (§4.6.1); a long reserved hold depresses apparent idle inventory and delays retirement-limit evaluation",
		*f.ClaimHoldTTLSeconds, HighClaimHoldTTLWarnSeconds), true
}
