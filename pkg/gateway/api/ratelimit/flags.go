// SPDX-License-Identifier: MIT

package ratelimit

import (
	"flag"
	"time"

	"github.com/lennylabs/lenny/pkg/common/envdefault"
)

// Flags holds the flags only the rate limit stage of the request pipeline
// reads.
type Flags struct {
	// GlobalPerMin is the --rate-limit-global-per-min flag.
	GlobalPerMin *int
	// PerUserPerMin is the --rate-limit-per-user-per-min flag.
	PerUserPerMin *int
	// PerTenantPerMin is the --rate-limit-per-tenant-per-min flag.
	PerTenantPerMin *int
	// FailOpenMaxSeconds is the --rate-limit-failopen-max-seconds flag.
	FailOpenMaxSeconds *int
}

// RegisterFlags registers these flags on fs and returns their values, which
// fs fills when it parses. Each flag's default is read from its environment
// variable, as the composition root read it before the flags moved here.
func RegisterFlags(fs *flag.FlagSet) *Flags {
	f := &Flags{}
	f.GlobalPerMin = fs.Int("rate-limit-global-per-min", 0,
		"§11.1 global requests-per-minute admission limit. Zero disables the global rate limit.")
	f.PerUserPerMin = fs.Int("rate-limit-per-user-per-min", 0,
		"§11.1 per-user requests-per-minute admission limit. Zero disables the per-user rate limit.")
	f.PerTenantPerMin = fs.Int("rate-limit-per-tenant-per-min", 0,
		"§13.3 / §11.1 per-tenant requests-per-minute admission limit (fair-share brake across a tenant's users). Zero disables the per-tenant rate limit. F-11.1.8.")
	// §11.3 — rateLimitFailOpenMaxSeconds, operator-tunable.
	// Once a fail-open episode (counter-error window) has run past this
	// cap, the middleware switches to fail-closed and rejects requests
	// with 429 RATE_LIMITED until the counter recovers. F-11.3.22.
	f.FailOpenMaxSeconds = fs.Int("rate-limit-failopen-max-seconds",
		envdefault.Int("LENNY_RATE_LIMIT_FAILOPEN_MAX_SECONDS", int(DefaultFailOpenMaxSeconds/time.Second)),
		"§11.3 rateLimitFailOpenMaxSeconds: cap on a single fail-open episode in the §11.1 admission middleware. Negative disables the cap. Default 60s. Override via LENNY_RATE_LIMIT_FAILOPEN_MAX_SECONDS.")
	return f
}
