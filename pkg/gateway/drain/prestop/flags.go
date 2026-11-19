// SPDX-License-Identifier: MIT

package prestop

import (
	"flag"

	"github.com/lennylabs/lenny/pkg/common/envdefault"
)

// Flags holds the flags only the preStop hook reads.
type Flags struct {
	// BarrierAckTimeoutSeconds is the value of
	// --checkpoint-barrier-ack-timeout-seconds.
	BarrierAckTimeoutSeconds *int
}

// RegisterFlags registers the flags of the preStop hook on fs and returns their
// values, which fs fills when it parses.
func RegisterFlags(fs *flag.FlagSet) *Flags {
	f := &Flags{}
	f.BarrierAckTimeoutSeconds = fs.Int("checkpoint-barrier-ack-timeout-seconds",
		envdefault.Int("LENNY_CHECKPOINT_BARRIER_ACK_TIMEOUT_SECONDS", DefaultBarrierAckTimeoutSeconds),
		"§10.1 / §11.3 checkpointBarrierAckTimeoutSeconds: the single wall-clock deadline the preStop CheckpointBarrier fan-out runs under across all coordinated pods. Default 90. Override via LENNY_CHECKPOINT_BARRIER_ACK_TIMEOUT_SECONDS. F-11.3.15.")
	return f
}
