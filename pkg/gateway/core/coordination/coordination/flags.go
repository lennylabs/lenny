// SPDX-License-Identifier: MIT

package coordination

import (
	"flag"
	"time"
)

// Flags holds the flags only the session coordination sweeper reads.
type Flags struct {
	// Interval is the value of --coordination-interval.
	Interval *time.Duration
}

// RegisterFlags registers the flags of the session coordination sweeper on fs
// and returns their values, which fs fills when it parses.
func RegisterFlags(fs *flag.FlagSet) *Flags {
	f := &Flags{}
	f.Interval = fs.Duration("coordination-interval", 15*time.Second,
		"§10.1 session-coordination lease sweep interval. Each sweep renews this replica's lease on every non-terminal session. Only active when --redis-url is set.")
	return f
}
