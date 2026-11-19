// SPDX-License-Identifier: MIT

package checkpointer

import (
	"flag"

	"github.com/lennylabs/lenny/pkg/common/envdefault"
)

// Flags holds the flags only the checkpointer reads.
type Flags struct {
	// JitterFraction is the value of --checkpoint-jitter-fraction.
	JitterFraction *float64

	// GrantWindow is the value of --checkpoint-grant-window.
	GrantWindow *int

	// CapabilityTTLSeconds is the value of --checkpoint-capability-ttl-seconds.
	CapabilityTTLSeconds *int
}

// RegisterFlags registers the flags of the checkpointer on fs and returns their
// values, which fs fills when it parses.
func RegisterFlags(fs *flag.FlagSet) *Flags {
	f := &Flags{}
	f.JitterFraction = fs.Float64("checkpoint-jitter-fraction", envdefault.Float64("LENNY_CHECKPOINT_JITTER_FRACTION", DefaultJitterFraction),
		"§4.4. Each session's first periodic checkpoint is scheduled at `checkpointInterval + random(0, checkpointInterval × jitterFraction)`, preventing thundering-herd checkpoint storms at Tier 3 scale. Range [0.0, 1.0]; default 0.2 spreads the first checkpoint uniformly across a 120-second window at the default 600-second interval. Override via LENNY_CHECKPOINT_JITTER_FRACTION.")
	f.GrantWindow = fs.Int("checkpoint-grant-window",
		envdefault.Int("LENNY_CHECKPOINT_GRANT_WINDOW", DefaultGrantWindow),
		"§10.1.7 / §17.8.1 checkpointGrantWindow: the number of chunk-upload capabilities the gateway keeps outstanding at once while draining a session's workspace checkpoint. It bounds both the pipelining depth and the worst-case unreconciled overage (checkpointGrantWindow × partialChunkSizeBytes) against a backend that ignores the signed Content-Length. Default 4; a pool config value overrides this deployment-wide default. Override via LENNY_CHECKPOINT_GRANT_WINDOW.")
	f.CapabilityTTLSeconds = fs.Int("checkpoint-capability-ttl-seconds",
		envdefault.Int("LENNY_CHECKPOINT_CAPABILITY_TTL_SECONDS", DefaultCapabilityTTLSeconds),
		"§13.2 / §17.8.1 checkpointCapabilityTTLSeconds: the expiry, in seconds, of each gateway-minted presigned checkpoint upload or restore capability. Default 30. Override via LENNY_CHECKPOINT_CAPABILITY_TTL_SECONDS.")
	return f
}
