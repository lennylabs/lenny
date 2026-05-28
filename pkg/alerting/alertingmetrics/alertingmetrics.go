// SPDX-License-Identifier: MIT

// Package alertingmetrics registers the §16.1 / §25.13 bundled-alerting
// observability surface: the two gauges that signal which formats the
// chart rendered the §16.5 catalog into and how many rules an operator
// overrode, plus the histogram the gateway's in-process tracker fills
// with per-rule evaluation latency.
//
// spec: §16.1 line 713, §25.13 lines 4833–4835; F-25.13.3.
package alertingmetrics

import (
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// FormatPrometheusRule is the §25.13 `format` label value emitted when
// the chart rendered the §16.5 catalog as a PrometheusRule CRD.
const FormatPrometheusRule = "prometheusrule"

// FormatConfigMap is the §25.13 `format` label value emitted when the
// chart rendered the §16.5 catalog as a Prometheus rule-file ConfigMap.
const FormatConfigMap = "configmap"

// Metrics is the §16.1 / §25.13 alerting observability surface. Hold
// it for the gateway lifetime; the gauges remain stamped and the
// histogram records the in-process evaluator's per-rule duration.
type Metrics struct {
	bundled      *prometheus.GaugeVec
	overrides    prometheus.Gauge
	evalDuration *prometheus.HistogramVec
}

// New registers the §25.13 metrics on reg and returns the handle. A
// nil reg uses prometheus.DefaultRegisterer.
func New(reg prometheus.Registerer) (*Metrics, error) {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}
	bundled := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "lenny_alerting_rules_bundled",
		Help: "1 if rules are rendered in the given chart format (§25.13 lines 4833).",
	}, []string{"format"})
	overrides := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_alerting_rule_overrides",
		Help: "Count of operator-overridden rules from monitoring.alertOverrides (§25.13 line 4834).",
	})
	// spec: §25.13 line 4828 — the per-scrape evaluation budget runs
	// ~10ms p95 per rule; the §16.5 catalog is now >150 rules so
	// expected total tick cost is in the 1s–2s range. Buckets cover
	// fast in-memory queries through the worst-case multi-second tail.
	evalDuration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "lenny_alerting_rule_eval_duration_seconds",
		Help:    "In-process tracker evaluation latency per §16.5 rule (§25.13 line 4835).",
		Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
	}, []string{"rule"})
	for _, c := range []prometheus.Collector{bundled, overrides, evalDuration} {
		if err := reg.Register(c); err != nil {
			return nil, fmt.Errorf("alertingmetrics: register: %w", err)
		}
	}
	m := &Metrics{
		bundled:      bundled,
		overrides:    overrides,
		evalDuration: evalDuration,
	}
	// Pre-stamp the closed-enum bundled gauge series so /metrics
	// always exposes the closed-enum format set, even before the
	// gateway has called SetBundledFormats. Operators reading the
	// series before chart wiring lands see 0 (unrendered) rather than
	// an absent label.
	for _, f := range []string{FormatPrometheusRule, FormatConfigMap} {
		m.bundled.WithLabelValues(f).Set(0)
	}
	return m, nil
}

// SetBundledFormats stamps `lenny_alerting_rules_bundled{format}` to 1
// for each rendered format. Every label value the chart could render
// for is pre-stamped so a missing series cannot be mistaken for "1
// without further qualification": the chart-format selector is
// closed-enum (§25.13 line 4705), so an unrendered format reads as 0.
func (m *Metrics) SetBundledFormats(formats ...string) {
	rendered := map[string]bool{}
	for _, f := range formats {
		switch f {
		case FormatPrometheusRule, FormatConfigMap:
			rendered[f] = true
		}
	}
	for _, f := range []string{FormatPrometheusRule, FormatConfigMap} {
		v := 0.0
		if rendered[f] {
			v = 1.0
		}
		m.bundled.WithLabelValues(f).Set(v)
	}
}

// SetOverrideCount stamps `lenny_alerting_rule_overrides` with the
// count of operator-customized rules. The gateway reads it from the
// chart-supplied env at startup.
func (m *Metrics) SetOverrideCount(n int) {
	m.overrides.Set(float64(n))
}

// ObserveRuleEvalDuration records one rule's in-process evaluation
// wall-clock against `lenny_alerting_rule_eval_duration_seconds{rule}`.
// Wire it to evaluator.Options.OnRuleEvalDuration.
func (m *Metrics) ObserveRuleEvalDuration(rule string, d time.Duration) {
	m.evalDuration.WithLabelValues(rule).Observe(d.Seconds())
}
