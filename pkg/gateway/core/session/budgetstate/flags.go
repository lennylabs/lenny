// SPDX-License-Identifier: MIT

package budgetstate

import (
	"flag"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/common/envdefault"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/slotreaper"
)

// Flags holds the flags only the per-replica budget state sweep reads.
type Flags struct {
	// SweepInterval is the value of --budget-state-sweep-interval.
	SweepInterval *time.Duration
}

// RegisterFlags registers the flags of the per-replica budget state sweep on fs
// and returns their values, which fs fills when it parses.
func RegisterFlags(fs *flag.FlagSet) *Flags {
	f := &Flags{}
	f.SweepInterval = fs.Duration("budget-state-sweep-interval",
		envdefault.Duration("LENNY_BUDGET_STATE_SWEEP_INTERVAL", slotreaper.DefaultInterval),
		"§11.2 per-replica budget state sweep cadence. Every replica resolves the session ids its budget enforcer, usage recorder, and token-usage anomaly detector track, and forgets the per-session state of each session whose row is terminal or absent. Must be positive. Default 60s. Override via LENNY_BUDGET_STATE_SWEEP_INTERVAL.")
	return f
}

// Validate refuses a non-positive --budget-state-sweep-interval, which would
// stop every replica from reclaiming the per-session state of sessions that
// ended elsewhere. The words are the ones the gateway's startup check used
// before the flag moved here.
// spec: §11.2.
func (f *Flags) Validate() error {
	if d := *f.SweepInterval; d <= 0 {
		return fmt.Errorf("--budget-state-sweep-interval must be positive, got %s (§11.2)", d)
	}
	return nil
}
