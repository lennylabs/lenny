// SPDX-License-Identifier: MIT

package slotreaper

import (
	"flag"
	"time"

	"github.com/lennylabs/lenny/pkg/common/envdefault"
)

// Flags holds the flags only the stale-slot reaper reads.
type Flags struct {
	// Interval is the value of --slot-reaper-interval.
	Interval *time.Duration

	// Rate is the value of --slot-reaper-rate.
	Rate *int

	// TerminalAge is the value of --slot-reaper-terminal-age.
	TerminalAge *time.Duration
}

// RegisterFlags registers the flags of the stale-slot reaper on fs and returns
// their values, which fs fills when it parses.
func RegisterFlags(fs *flag.FlagSet) *Flags {
	f := &Flags{}
	f.Interval = fs.Duration("slot-reaper-interval",
		envdefault.Duration("LENNY_SLOT_REAPER_INTERVAL", DefaultInterval),
		"§5.2 stale-slot reaper cadence: the wait between the end of one leader-run reaper pass and the start of the next. Each pass releases the concurrent-workspace slot set members whose session row has been terminal for longer than --slot-reaper-terminal-age. Default 60s. Override via LENNY_SLOT_REAPER_INTERVAL.")
	f.Rate = fs.Int("slot-reaper-rate",
		envdefault.Int("LENNY_SLOT_REAPER_RATE", DefaultRate),
		"§5.2 stale-slot reaper rate: the most slot set members the reaper releases per second, and the most pods whose release is in flight at once. Must be positive. Default 50. Override via LENNY_SLOT_REAPER_RATE.")
	f.TerminalAge = fs.Duration("slot-reaper-terminal-age",
		envdefault.Duration("LENNY_SLOT_REAPER_TERMINAL_AGE", DefaultTerminalAge),
		"§5.2 stale-slot reaper terminal age: the gateway's copy of claimOrphanTimeout. A slot set member is released once its session row has been terminal for longer than this. It must equal the controller's --claim-orphan-timeout, and it must be at least one second more than the largest of --max-finalizing-state-timeout-seconds, --max-resuming-seconds, and the §7.1 terminal pipeline budget, which is --workspace-seal-max-duration-seconds plus --stream-failure-report-timeout (the post-terminal slot activity invariant), which the gateway checks at startup. Default 15m. Override via LENNY_SLOT_REAPER_TERMINAL_AGE.")
	return f
}
