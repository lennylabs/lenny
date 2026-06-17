// SPDX-License-Identifier: MIT

package preflight

import (
	"context"
	"fmt"
	"strings"
)

// PrometheusConfig carries the §25.4 "Preflight validation" inputs the
// prometheus-reachability check evaluates. spec: §25.4 lines 1466-1470,
// §17.6.
type PrometheusConfig struct {
	// URL is the ops.prometheus.url chart value: the operator-supplied
	// Prometheus-HTTP-API-compatible endpoint. Empty means no Prometheus
	// is configured.
	URL string
	// Tier is the global.deploymentTier chart value (tier1 | tier2 |
	// tier3). Tier 1 emits an INFO advisory; Tier 2/3 emit a WARN unless
	// acknowledged.
	Tier string
	// AcknowledgeNoPrometheus is the monitoring.acknowledgeNoPrometheus
	// chart value: a Tier 2/3 operator's explicit acknowledgement that
	// the deployment intentionally runs without a reachable Prometheus,
	// suppressing the WARN.
	AcknowledgeNoPrometheus bool
}

// PrometheusProber performs a live reachability probe against the
// configured Prometheus endpoint. The lenny-preflight Job constructs a
// real prober (an HTTP GET against the Prometheus query API); tests pass
// a fake. A nil prober skips the live dial — a configured URL is then
// treated as reachable (the airgap / skip-network-probes path).
type PrometheusProber interface {
	Probe(ctx context.Context, url string) error
}

// PrometheusProbeFunc adapts a function to PrometheusProber.
type PrometheusProbeFunc func(ctx context.Context, url string) error

// Probe calls f.
func (f PrometheusProbeFunc) Probe(ctx context.Context, url string) error { return f(ctx, url) }

// PrometheusReachabilityCheck is the §17.6 / §25.4 prometheus-reachability
// preflight check.
type PrometheusReachabilityCheck struct {
	Config PrometheusConfig
	// Prober runs the live reachability probe. Nil treats a configured
	// URL as reachable (no dial), used on the skip-network-probes path.
	Prober PrometheusProber
}

// tierIsProduction reports whether the deployment tier is Tier 2 or
// Tier 3 (the tiers at which Prometheus is a required dependency).
func tierIsProduction(tier string) bool {
	t := strings.ToLower(strings.TrimSpace(tier))
	return strings.HasPrefix(t, "tier2") || strings.HasPrefix(t, "tier3")
}

// Decide evaluates the §25.4 "Preflight validation" Prometheus
// reachability posture. The check is non-blocking by design (§25.4 line
// 1467 — "operators may have legitimate reasons to deploy without
// Prometheus temporarily and a hard install gate would be obstructive"),
// so it always returns Passed: true. It emits a tier-specific advisory in
// the Reason field: an INFO at Tier 1 and a WARN at Tier 2/3, the latter
// suppressed when monitoring.acknowledgeNoPrometheus is set.
//
// A configured URL that the prober reaches passes silently. A configured
// URL that the prober cannot reach is treated the same as no URL for
// advisory purposes (the operator intended Prometheus but it is not
// answering). When no prober is wired (skip-network-probes / airgap) a
// configured URL is assumed reachable and passes silently.
//
// spec: §25.4 lines 1462-1470 (preflight validation, tier-specific
// INFO/WARN, acknowledgeNoPrometheus suppression). F-25.4.25.
func (c PrometheusReachabilityCheck) Decide(ctx context.Context) Decision {
	configured := strings.TrimSpace(c.Config.URL) != ""
	reachable := false
	if configured {
		if c.Prober == nil {
			reachable = true
		} else if err := c.Prober.Probe(ctx, c.Config.URL); err == nil {
			reachable = true
		}
	}
	if reachable {
		return Decision{Passed: true}
	}

	if tierIsProduction(c.Config.Tier) {
		if c.Config.AcknowledgeNoPrometheus {
			return Decision{Passed: true}
		}
		where := "(no ops.prometheus.url configured)"
		if configured {
			where = fmt.Sprintf("at %q (configured but unreachable)", c.Config.URL)
		}
		return Decision{Passed: true, Reason: fmt.Sprintf(
			"WARNING: Prometheus not configured %s. Several lenny-ops features (capacity "+
				"recommendations, historical diagnostics, alerting) require persistent time-series "+
				"storage. Configuring a Prometheus-compatible endpoint is strongly recommended for "+
				"production deployments. Set monitoring.acknowledgeNoPrometheus=true to acknowledge "+
				"running without it (§25.4).", where,
		)}
	}
	return Decision{Passed: true, Reason: "INFO: Prometheus not configured. lenny-ops will operate " +
		"in degraded mode. This is acceptable for development (§25.4)."}
}
