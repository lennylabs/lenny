// SPDX-License-Identifier: MIT

package leaderjobs

import (
	"flag"
	"time"

	"github.com/lennylabs/lenny/pkg/common/envdefault"
	"github.com/lennylabs/lenny/pkg/tenantkms"
)

// Flags holds the flags only the leader-run T4 KMS probe reads.
type Flags struct {
	// T4KMSProbeIntervalSeconds is the value of
	// --t4-kms-probe-interval-seconds.
	T4KMSProbeIntervalSeconds *int

	// T4KMSProbeRateLimit is the value of --t4-kms-probe-rate-limit.
	T4KMSProbeRateLimit *float64
}

// RegisterFlags registers the flags of the leader-run T4 KMS probe on fs and
// returns their values, which fs fills when it parses.
func RegisterFlags(fs *flag.FlagSet) *Flags {
	f := &Flags{}
	// The cadence floor (60s) and the token-bucket rate ceiling keep a large
	// T4 fleet from bursting the KMS backend; both are operator-tunable.
	f.T4KMSProbeIntervalSeconds = fs.Int("t4-kms-probe-interval-seconds",
		envdefault.Int("LENNY_T4_KMS_PROBE_INTERVAL_SECONDS", int(tenantkms.DefaultProbeInterval/time.Second)),
		"§12.5 storage.t4KmsProbeInterval: the leader-elected continuous T4 KMS availability probe cadence in seconds (default 300, minimum 60; a smaller value is clamped up to the floor). Override via LENNY_T4_KMS_PROBE_INTERVAL_SECONDS.")
	f.T4KMSProbeRateLimit = fs.Float64("t4-kms-probe-rate-limit",
		envdefault.Float64("LENNY_T4_KMS_PROBE_RATE_LIMIT", tenantkms.DefaultProbeRateLimit),
		"§12.5 storage.t4KmsProbeRateLimit: token-bucket ceiling on T4 KMS probe issuance in probes/sec (default 10). A non-positive value disables rate limiting. Override via LENNY_T4_KMS_PROBE_RATE_LIMIT.")
	return f
}
