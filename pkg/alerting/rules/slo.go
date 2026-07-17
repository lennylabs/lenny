// SPDX-License-Identifier: MIT

package rules

import (
	"fmt"
	"time"
)

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

// SLONotificationTargetPlaceholder is the token the chart-fragment render
// carries in place of a concrete OpenSLO notification-target type. It
// mirrors SLOTierPlaceholder: the gen-alerting-rules build step renders the
// chart fragment with the placeholder intact so the Helm template can
// substitute monitoring.openslo.notificationTarget.type at install time.
// The docs and CLI callers, which have no Helm layer, pass
// OpenSLODefaultNotificationTarget so their output is placeholder-free and
// schema-valid.
//
// spec: §16.10 (deployer-configurable notification-target type).
const SLONotificationTargetPlaceholder = "__OPENSLO_NOTIFICATION_TARGET__"

// OpenSLODefaultNotificationTarget is the schema-valid default OpenSLO
// AlertNotificationTarget type the docs and CLI callers render into
// spec.target. It is the operator-tunable default the Helm chart exposes as
// monitoring.openslo.notificationTarget.type.
//
// spec: §16.10 (default notification-target type: webhook).
const OpenSLODefaultNotificationTarget = "webhook"

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

// burnRateFastMultiplierThreshold / burnRateSlowMultiplierThreshold are
// the PromQL the burn-rate alerts compare against. §16.5 line 640 makes
// the window multipliers operator-configurable via Helm
// (slo.burnRate.fastMultiplier / slowMultiplier). The gateway mirrors
// those values onto the lenny_slo_burn_rate_{fast,slow}_multiplier gauges
// (gatewaymetrics.SetSLOBurnRateMultipliers), and each alert reads the
// gauge via scalar(...). The `or vector(default)` fallback keeps the
// alert firing at the §16.5 base multiplier when the gauge is absent (a
// gateway-down window), so an operator cannot silence the burn-rate
// alerts by losing the threshold-mirror gauge. F-16.5.3.
var (
	burnRateFastMultiplierThreshold = fmt.Sprintf("scalar(lenny_slo_burn_rate_fast_multiplier or vector(%d))", burnRateFastMultiplier)
	burnRateSlowMultiplierThreshold = fmt.Sprintf("scalar(lenny_slo_burn_rate_slow_multiplier or vector(%d))", burnRateSlowMultiplier)
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
			Name:        "session-creation-success-rate",
			AlertName:   "SessionCreationSuccessRateBurnRate",
			Objective:   "Session creation success rate >= 99.5%",
			Target:      0.995,
			RunbookSlug: "session-creation-success-rate-burn-rate",
			// Session creation is the two-step POST /v1/sessions handler
			// (handleCreate): a 5xx response is a failed creation attempt,
			// a 4xx is a client error excluded from the availability
			// budget. The error rate over total attempts is the §16.5
			// "Successful session starts / total attempts" SLI. The
			// gateway HTTP middleware (gatewaymetrics.Middleware) emits
			// lenny_gateway_requests_total{method,route,status_class} with
			// route="/v1/sessions" for the create handler. spec: §16.5
			// lines 613, 631, 640.
			BurnRateExpr: `(sum(rate(lenny_gateway_requests_total{method="POST",route="/v1/sessions",status_class="5xx"}[1h])) / sum(rate(lenny_gateway_requests_total{method="POST",route="/v1/sessions"}[1h]))) / (1 - 0.995)`,
			SLI: SLIRatio{
				Counter: true,
				Good:    `sum(rate(lenny_gateway_requests_total{` + t + `,method="POST",route="/v1/sessions",status_class!="5xx"}[1h]))`,
				Total:   `sum(rate(lenny_gateway_requests_total{` + t + `,method="POST",route="/v1/sessions"}[1h]))`,
			},
		},
		{
			Name:        "session-creation-latency",
			AlertName:   "SessionCreationLatencyBurnRate",
			Objective:   "Session creation latency P99 < 500ms",
			Target:      0.99,
			RunbookSlug: "session-creation-latency-burn-rate",
			// The slow fraction is the share of POST /v1/sessions creation
			// requests slower than the 500ms SLO, against the 1% error
			// budget (P99). The le="0.5" boundary is a prometheus.DefBuckets
			// bucket on lenny_gateway_request_duration_seconds, the HTTP
			// middleware histogram labelled by method/route; route is
			// "/v1/sessions" for the two-step create handler, which does no
			// pod-claim work (startup latency is the separate
			// StartupLatencyBurnRate SLO). spec: §16.5 lines 614, 632, 640.
			BurnRateExpr: `(1 - (sum(rate(lenny_gateway_request_duration_seconds_bucket{method="POST",route="/v1/sessions",le="0.5"}[1h])) / sum(rate(lenny_gateway_request_duration_seconds_count{method="POST",route="/v1/sessions"}[1h])))) / 0.01`,
			SLI: SLIRatio{
				Counter: true,
				Good:    `sum(rate(lenny_gateway_request_duration_seconds_bucket{` + t + `,method="POST",route="/v1/sessions",le="0.5"}[1h]))`,
				Total:   `sum(rate(lenny_gateway_request_duration_seconds_count{` + t + `,method="POST",route="/v1/sessions"}[1h]))`,
			},
		},
		{
			Name:        "session-availability",
			AlertName:   "SessionAvailabilityBurnRate",
			Objective:   "Session availability >= 99.9%",
			Target:      0.999,
			RunbookSlug: "session-availability-burn-rate",
			// lenny_session_unavailability_ratio is the fraction of active
			// sessions currently in a retry/recovery state (resume_pending,
			// resuming, awaiting_client_action) — the inverse of "uptime of
			// sessions not in retry/recovery state". The gateway export
			// loop refreshes it (SetSessionUnavailabilityRatio) as
			// recovery_sessions / active_sessions. spec: §16.5 lines 616,
			// 633, 640.
			BurnRateExpr: `lenny_session_unavailability_ratio / (1 - 0.999)`,
			SLI: SLIRatio{
				Good:  `(1 - lenny_session_unavailability_ratio{` + t + `})`,
				Total: `vector(1)`,
			},
		},
		{
			Name:        "gateway-availability",
			AlertName:   "GatewayAvailabilityBurnRate",
			Objective:   "Gateway availability >= 99.95%",
			Target:      0.9995,
			RunbookSlug: "gateway-availability-burn-rate",
			// Gateway availability is the share of HTTP requests served
			// without a 5xx across every route — a request that returns
			// 5xx was not served by a healthy replica. The HTTP middleware
			// emits lenny_gateway_requests_total{status_class}; the 5xx
			// fraction over total is the error rate, normalised by the
			// 0.05% availability budget. spec: §16.5 lines 617, 634, 640.
			BurnRateExpr: `(sum(rate(lenny_gateway_requests_total{status_class="5xx"}[1h])) / sum(rate(lenny_gateway_requests_total[1h]))) / (1 - 0.9995)`,
			SLI: SLIRatio{
				Counter: true,
				Good:    `sum(rate(lenny_gateway_requests_total{` + t + `,status_class!="5xx"}[1h]))`,
				Total:   `sum(rate(lenny_gateway_requests_total{` + t + `}[1h]))`,
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
			Name:        "checkpoint-duration",
			AlertName:   "CheckpointDurationBurnRate",
			Objective:   "Checkpoint duration P95 < 2s (<= 100MB)",
			Target:      0.95,
			RunbookSlug: "checkpoint-duration-burn-rate",
			// The slow fraction is the share of checkpoints slower than the
			// 2s SLO, against the 5% error budget. le="2" is an explicit
			// bucket boundary on lenny_checkpoint_duration_seconds, the
			// end-to-end checkpoint wall-time histogram the gateway emits.
			// spec: §16.5 line 638, §16.1 line 103.
			BurnRateExpr: `(1 - (sum(rate(lenny_checkpoint_duration_seconds_bucket{le="2"}[1h])) / sum(rate(lenny_checkpoint_duration_seconds_count[1h])))) / 0.05`,
			SLI: SLIRatio{
				Counter: true,
				Good:    `sum(rate(lenny_checkpoint_duration_seconds_bucket{` + t + `,le="2"}[1h]))`,
				Total:   `sum(rate(lenny_checkpoint_duration_seconds_count{` + t + `}[1h]))`,
			},
		},
	}
}
