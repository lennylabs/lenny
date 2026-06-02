// SPDX-License-Identifier: MIT

package rules

import "time"

// SLOTierPlaceholder is the token the canonical SLO query templates carry
// in place of a concrete deployment tier. RenderOpenSLO substitutes it
// with the requested tier; the gen-alerting-rules build step renders the
// chart fragment with the placeholder left intact so the Helm chart can
// substitute global.deploymentTier at install time. The §16.9
// ServiceMonitor and PodMonitor metricRelabelings stamp the
// deployment_tier label onto every scraped series (§16.1.1), so an
// OpenSLO query scoped to deployment_tier="<tier>" resolves the same
// series the bundled alerts evaluate.
//
// spec: §16.10 line 734 (tier labels), §16.1.1 (deployment_tier label).
const SLOTierPlaceholder = "__DEPLOYMENT_TIER__"

// Burn-rate window parameters shared by the §16.5 burn-rate alerts
// (burnRateAlerts) and the §16.10 OpenSLO AlertPolicy export
// (RenderOpenSLO). A single source keeps the rendered OpenSLO alert
// conditions identical to the multi-window alerts the gateway evaluates.
//
// §16.5 line 627: the fast window is 1h at 14x burn, the slow window is
// 6h at 3x burn; both must fire simultaneously for a page.
const (
	burnRateFastMultiplier = 14
	burnRateSlowMultiplier = 3
)

var (
	burnRateFastWindow = 1 * time.Hour
	burnRateSlowWindow = 6 * time.Hour
)

// SLIRatio is the ratio indicator an OpenSLO SLI carries: a good (or bad)
// event query over a total query, both Prometheus expressions that
// reference the canonical §16.5 metric names and the deployment_tier
// label. Exactly one of Good or Bad is set. Counter records whether the
// good/total queries are over counters (rate of histogram buckets) or
// over an already-computed ratio gauge.
type SLIRatio struct {
	// Counter is true when Good/Total are counter rates (histogram
	// buckets), false when they reference a precomputed ratio gauge.
	Counter bool
	// Good is the PromQL for good events. Empty when Bad is set.
	Good string
	// Bad is the PromQL for bad events. Empty when Good is set.
	Bad string
	// Total is the PromQL for total events.
	Total string
}

// SLODefinition is one §16.5 service-level objective. It is the single
// source the §16.5 burn-rate alerts (burnRateAlerts) and the §16.10
// OpenSLO export (RenderOpenSLO) both derive from, so the OpenSLO
// documents cannot drift from the multi-window alerts the gateway
// evaluates and the chart bundles: both are a view of this catalog.
//
// spec: §16.5 lines 607-640 (SLO target table + burn-rate table), §16.10
// lines 732-736 (OpenSLO export is a view of the §16.5 catalog).
type SLODefinition struct {
	// Name is the OpenSLO object name (kebab-case), e.g.
	// "session-creation-success-rate".
	Name string
	// AlertName is the §16.5 burn-rate alert base name (the fast-window
	// critical rule; the slow-window warning rule appends "Slow").
	AlertName string
	// Objective is the human-readable SLO statement used verbatim as the
	// burn-rate alert `slo` annotation and the OpenSLO objective
	// displayName, e.g. "Session creation success rate >= 99.5%".
	Objective string
	// Target is the SLO objective as a ratio in [0,1], e.g. 0.995.
	Target float64
	// RunbookSlug is the §25.7 runbook slug for the fast-window alert.
	RunbookSlug string
	// BurnRateExpr is the budget-normalised base PromQL ratio the
	// burn-rate alerts compare against their window multipliers. A value
	// of N means the SLO error budget is burning at Nx the sustainable
	// rate (§16.5 "Burn-rate calculation").
	BurnRateExpr string
	// SLI is how the OpenSLO export expresses the indicator.
	SLI SLIRatio
}

// SLODefinitions returns the canonical §16.5 SLO catalog. The order is
// the §16.5 burn-rate table order; burnRateAlerts and RenderOpenSLO both
// preserve it. The query templates carry SLOTierPlaceholder where the
// deployment tier belongs.
//
// spec: §16.5 lines 611-640, §16.10 lines 732-736.
func SLODefinitions() []SLODefinition {
	t := `deployment_tier="` + SLOTierPlaceholder + `"`
	return []SLODefinition{
		{
			Name:         "session-creation-success-rate",
			AlertName:    "SessionCreationSuccessRateBurnRate",
			Objective:    "Session creation success rate >= 99.5%",
			Target:       0.995,
			RunbookSlug:  "session-creation-success-rate-burn-rate",
			BurnRateExpr: `lenny_session_creation_error_ratio / (1 - 0.995)`,
			SLI: SLIRatio{
				Good:  `(1 - lenny_session_creation_error_ratio{` + t + `})`,
				Total: `vector(1)`,
			},
		},
		{
			Name:         "session-creation-latency",
			AlertName:    "SessionCreationLatencyBurnRate",
			Objective:    "Session creation latency P99 < 500ms",
			Target:       0.99,
			RunbookSlug:  "session-creation-latency-burn-rate",
			BurnRateExpr: `lenny_session_creation_latency_slow_ratio / 0.01`,
			SLI: SLIRatio{
				Counter: true,
				Good:    `sum(rate(lenny_session_creation_duration_seconds_bucket{` + t + `,le="0.5"}[1h]))`,
				Total:   `sum(rate(lenny_session_creation_duration_seconds_count{` + t + `}[1h]))`,
			},
		},
		{
			Name:         "session-availability",
			AlertName:    "SessionAvailabilityBurnRate",
			Objective:    "Session availability >= 99.9%",
			Target:       0.999,
			RunbookSlug:  "session-availability-burn-rate",
			BurnRateExpr: `lenny_session_unavailability_ratio / (1 - 0.999)`,
			SLI: SLIRatio{
				Good:  `(1 - lenny_session_unavailability_ratio{` + t + `})`,
				Total: `vector(1)`,
			},
		},
		{
			Name:         "gateway-availability",
			AlertName:    "GatewayAvailabilityBurnRate",
			Objective:    "Gateway availability >= 99.95%",
			Target:       0.9995,
			RunbookSlug:  "gateway-availability-burn-rate",
			BurnRateExpr: `lenny_gateway_unavailability_ratio / (1 - 0.9995)`,
			SLI: SLIRatio{
				Good:  `(1 - lenny_gateway_unavailability_ratio{` + t + `})`,
				Total: `vector(1)`,
			},
		},
		{
			// The slow ratio is computed inline from the
			// lenny_session_startup_duration_seconds histogram (§6.3 line
			// 348, emitted by the gateway start path): the fraction of
			// runc starts slower than the 2s SLO threshold, against the 5%
			// error budget. The le="2" bucket boundary is one of the
			// histogram's explicit buckets. spec: §16.5 line 635, §6.3
			// line 348.
			Name:         "startup-latency-runc",
			AlertName:    "StartupLatencyBurnRate",
			Objective:    "Startup latency P95 < 2s (runc)",
			Target:       0.95,
			RunbookSlug:  "startup-latency-burn-rate",
			BurnRateExpr: `(1 - (sum(rate(lenny_session_startup_duration_seconds_bucket{isolation_profile="runc",le="2"}[1h])) / sum(rate(lenny_session_startup_duration_seconds_count{isolation_profile="runc"}[1h])))) / 0.05`,
			SLI: SLIRatio{
				Counter: true,
				Good:    `sum(rate(lenny_session_startup_duration_seconds_bucket{` + t + `,isolation_profile="runc",le="2"}[1h]))`,
				Total:   `sum(rate(lenny_session_startup_duration_seconds_count{` + t + `,isolation_profile="runc"}[1h]))`,
			},
		},
		{
			// As above for the gVisor 5s SLO threshold (le="5" bucket).
			// spec: §16.5 line 636, §6.3 line 348.
			Name:         "startup-latency-gvisor",
			AlertName:    "StartupLatencyGVisorBurnRate",
			Objective:    "Startup latency P95 < 5s (gVisor)",
			Target:       0.95,
			RunbookSlug:  "startup-latency-gvisor-burn-rate",
			BurnRateExpr: `(1 - (sum(rate(lenny_session_startup_duration_seconds_bucket{isolation_profile="gvisor",le="5"}[1h])) / sum(rate(lenny_session_startup_duration_seconds_count{isolation_profile="gvisor"}[1h])))) / 0.05`,
			SLI: SLIRatio{
				Counter: true,
				Good:    `sum(rate(lenny_session_startup_duration_seconds_bucket{` + t + `,isolation_profile="gvisor",le="5"}[1h]))`,
				Total:   `sum(rate(lenny_session_startup_duration_seconds_count{` + t + `,isolation_profile="gvisor"}[1h]))`,
			},
		},
		{
			// The slow ratio is computed inline from the
			// lenny_session_time_to_first_token_seconds histogram (§6.3
			// line 356, emitted by sessionserver on first agent-streamed
			// response event): the fraction of starts slower than the 10s
			// SLO threshold, against the 5% error budget. The le="10"
			// bucket boundary is one of the histogram's explicit buckets.
			// spec: §16.5 line 637, §6.3 line 356.
			Name:         "ttft",
			AlertName:    "TTFTBurnRate",
			Objective:    "Time to first token P95 < 10s",
			Target:       0.95,
			RunbookSlug:  "ttft-burn-rate",
			BurnRateExpr: `(1 - (sum(rate(lenny_session_time_to_first_token_seconds_bucket{le="10"}[1h])) / sum(rate(lenny_session_time_to_first_token_seconds_count[1h])))) / 0.05`,
			SLI: SLIRatio{
				Counter: true,
				Good:    `sum(rate(lenny_session_time_to_first_token_seconds_bucket{` + t + `,le="10"}[1h]))`,
				Total:   `sum(rate(lenny_session_time_to_first_token_seconds_count{` + t + `}[1h]))`,
			},
		},
		{
			Name:         "checkpoint-duration",
			AlertName:    "CheckpointDurationBurnRate",
			Objective:    "Checkpoint duration P95 < 2s (<= 100MB)",
			Target:       0.95,
			RunbookSlug:  "checkpoint-duration-burn-rate",
			BurnRateExpr: `lenny_checkpoint_duration_slow_ratio / 0.05`,
			SLI: SLIRatio{
				Good:  `(1 - lenny_checkpoint_duration_slow_ratio{` + t + `})`,
				Total: `vector(1)`,
			},
		},
	}
}
