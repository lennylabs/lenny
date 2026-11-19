// SPDX-License-Identifier: MIT

package slotcounter

import (
	"flag"
	"time"

	"github.com/lennylabs/lenny/pkg/common/envdefault"
)

// Flags holds the flags only the slot counter reads.
type Flags struct {
	// PostgresFallbackMaxSeconds is the value of
	// --slot-counter-postgres-fallback-max-seconds.
	PostgresFallbackMaxSeconds *int

	// RecoveryProbeInterval is the value of
	// --slot-counter-recovery-probe-interval.
	RecoveryProbeInterval *time.Duration
}

// RegisterFlags registers the flags of the slot counter on fs and returns their
// values, which fs fills when it parses.
func RegisterFlags(fs *flag.FlagSet) *Flags {
	f := &Flags{}
	f.PostgresFallbackMaxSeconds = fs.Int("slot-counter-postgres-fallback-max-seconds",
		envdefault.Int("LENNY_SLOT_COUNTER_POSTGRES_FALLBACK_MAX_SECONDS", int(DefaultPostgresFallbackMaxWindow/time.Second)),
		"§12.4 slotCounterPostgresFallbackMaxSeconds: the bounded window during a Redis outage in which the §5.2 slot counter gates intra-pod capacity on the Postgres fallback (GetActiveSlotsByPod under a per-pod advisory lock). After this window with Redis still unreachable, slot admission fails closed. Default 60. Override via LENNY_SLOT_COUNTER_POSTGRES_FALLBACK_MAX_SECONDS.")
	f.RecoveryProbeInterval = fs.Duration("slot-counter-recovery-probe-interval",
		envdefault.Duration("LENNY_SLOT_COUNTER_RECOVERY_PROBE_INTERVAL", DefaultRecoveryProbeInterval),
		"§12.4 slot-set recovery probe cadence: how often the gateway probes Redis so that, when an outage ends, a replica with no slot traffic still rebuilds the per-pod slot sets (adds its fallback admissions and leaked marks, and deletes the rehydrated flag of every pod a non-terminal concurrent-workspace session names) before fast-path enforcement resumes. A non-positive value keeps the default. Default 2s. Override via LENNY_SLOT_COUNTER_RECOVERY_PROBE_INTERVAL.")
	return f
}
