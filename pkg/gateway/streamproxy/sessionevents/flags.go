// SPDX-License-Identifier: MIT

package sessionevents

import (
	"flag"
	"fmt"

	"github.com/lennylabs/lenny/pkg/common/envdefault"
)

// The accepted range and the default of the per-session replay buffer
// depth, in events, which operators tune through
// gateway.sessionEventReplayBufferDepth. The default matches the reconnect
// window of 60 seconds at 10 events per second. F-10.4.5.
//
// spec: §10.4 (Gateway Reliability)
const (
	MinReplayBufferDepth     = 64
	MaxReplayBufferDepth     = 4096
	DefaultReplayBufferDepth = 512
)

// Flags holds the flags only the session event bus reads.
type Flags struct {
	// ReplayBufferDepth is the per-session replay buffer depth that NewBus
	// takes as its maxHistory.
	ReplayBufferDepth *int
}

// RegisterFlags registers the session event bus's flags on fs and returns
// their values, which fs fills when it parses.
func RegisterFlags(fs *flag.FlagSet) *Flags {
	f := &Flags{}
	f.ReplayBufferDepth = fs.Int("session-event-replay-buffer-depth", envdefault.Int("LENNY_SESSION_EVENT_REPLAY_BUFFER_DEPTH", DefaultReplayBufferDepth),
		"§10.4 per-session SSE replay buffer depth (events). Default 512 matches the §10.4 60s reconnect window at 10 events/s; accepted range 64..4096. Override via LENNY_SESSION_EVENT_REPLAY_BUFFER_DEPTH.")
	return f
}

// Validate reports a replay buffer depth outside the accepted range, in
// the words the gateway's startup check used before the flag moved here.
func (f *Flags) Validate() error {
	if d := *f.ReplayBufferDepth; d < MinReplayBufferDepth || d > MaxReplayBufferDepth {
		return fmt.Errorf("--session-event-replay-buffer-depth must be in [%d, %d] (got %d) (§10.4)", MinReplayBufferDepth, MaxReplayBufferDepth, d)
	}
	return nil
}
