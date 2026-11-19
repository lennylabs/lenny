// SPDX-License-Identifier: MIT

package dualstore

import (
	"flag"
	"time"

	"github.com/lennylabs/lenny/pkg/common/envdefault"
)

// Flags holds the flags only the dual-store unavailability monitor reads.
type Flags struct {
	// MaxUnavailableSeconds is the value of
	// --dual-store-unavailable-max-seconds.
	MaxUnavailableSeconds *int
}

// RegisterFlags registers the flags of the dual-store unavailability monitor on
// fs and returns their values, which fs fills when it parses.
func RegisterFlags(fs *flag.FlagSet) *Flags {
	f := &Flags{}
	f.MaxUnavailableSeconds = fs.Int("dual-store-unavailable-max-seconds",
		envdefault.Int("LENNY_DUAL_STORE_UNAVAILABLE_MAX_SECONDS", int(DefaultMaxUnavailable/time.Second)),
		"§10.1 dualStoreUnavailableMaxSeconds: the per-replica window after which sessions with no successful store interaction become eligible for graceful termination once a store recovers. Default 60. The §10.1 dual-store monitor is active only when both --postgres-dsn and --redis-url are set. Override via LENNY_DUAL_STORE_UNAVAILABLE_MAX_SECONDS. F-10.1.3.")
	return f
}
