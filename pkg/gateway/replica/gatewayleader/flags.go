// SPDX-License-Identifier: MIT

package gatewayleader

import (
	"flag"

	"github.com/lennylabs/lenny/pkg/common/envdefault"
)

// Flags holds the flags only the gateway leader election reads.
type Flags struct {
	// Election turns on the lease-based election of the
	// gateway-singleton sweeps.
	Election *bool
}

// RegisterFlags registers the leader election's flags on fs and returns
// their values, which fs fills when it parses.
func RegisterFlags(fs *flag.FlagSet) *Flags {
	f := &Flags{}
	f.Election = fs.Bool("gateway-leader-election",
		envdefault.Bool("LENNY_GATEWAY_LEADER_ELECTION", true),
		"§12.5: when true (default), gate the gateway-singleton background sweeps (artifact GC, tombstone hard-prune, audit-retention pruner, EventBus retranscribe worker, legal-hold reconciler, T4 KMS probe) under the lenny-gateway-leader Kubernetes Lease so exactly one gateway replica runs them. Falls back to always-run when the gateway is not in-cluster. Override via LENNY_GATEWAY_LEADER_ELECTION.")
	return f
}
