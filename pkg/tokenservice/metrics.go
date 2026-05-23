// SPDX-License-Identifier: MIT

package tokenservice

import "time"

// Metrics captures the §16.1 Token Service catalog emit surface. The
// concrete implementation lives in `cmd/lenny-token-service` and wires
// the four declared metrics:
//
//   - lenny_token_service_request_duration_seconds (histogram)
//   - lenny_token_service_errors_total (counter, labelled by err_type)
//   - lenny_oauth_token_rate_limited_total (counter, labelled by tier)
//   - lenny_oauth_token_rate_limited_sampled_total (counter, by tier)
//
// Splitting the surface as an interface keeps the package testable and
// stops the Token Service from depending on the Prometheus client.
// spec: §4.3 / §16.1 / §13.3 lines 607–611.
type Metrics interface {
	// RecordRequestDuration observes one token-endpoint request's
	// duration. operation is "exchange" for /v1/oauth/token; future
	// gRPC methods supply their own label.
	RecordRequestDuration(operation string, d time.Duration)

	// IncErrors records one error response by error class. The
	// vocabulary mirrors the RFC 8693 error codes the handler emits
	// (invalid_client, invalid_grant, invalid_request,
	// unsupported_grant_type, invalid_scope, rate_limited,
	// server_error).
	IncErrors(operation, errorClass string)

	// IncRateLimited records every rate-limited rejection by §13.3
	// limit_tier (caller_per_second | caller_per_minute |
	// tenant_per_second).
	IncRateLimited(limitTier string)

	// IncRateLimitedSampled records the per-(tenant, sub, tier) per
	// 10-second sampled rejection used to emit the §13.3 audit row.
	IncRateLimitedSampled(limitTier string)
}

// NoMetrics is a no-op Metrics. It is the default when Options.Metrics
// is unset, so tests do not need to construct a real emitter.
type NoMetrics struct{}

// RecordRequestDuration implements Metrics.
func (NoMetrics) RecordRequestDuration(string, time.Duration) {}

// IncErrors implements Metrics.
func (NoMetrics) IncErrors(string, string) {}

// IncRateLimited implements Metrics.
func (NoMetrics) IncRateLimited(string) {}

// IncRateLimitedSampled implements Metrics.
func (NoMetrics) IncRateLimitedSampled(string) {}

// Ensure NoMetrics satisfies Metrics at compile time.
var _ Metrics = NoMetrics{}
