// SPDX-License-Identifier: MIT

package upgradeservice

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
)

// MetricsCollector is the §25.8 Prometheus collector for the live
// platform-upgrade gauges. It reads the orchestrator's singleton state
// on every scrape, so lenny_platform_upgrade_phase and
// lenny_platform_upgrade_duration_seconds reflect the current phase and
// elapsed time without a background goroutine. When no upgrade has been
// recorded the collector emits nothing (the gauges are absent rather
// than reporting a stale or zero upgrade).
//
// spec: §25.8 Metrics (line 3614) — lenny_platform_upgrade_phase
// (Gauge, target_version) and lenny_platform_upgrade_duration_seconds
// (Gauge, target_version).
type MetricsCollector struct {
	svc      *Service
	phase    *prometheus.Desc
	duration *prometheus.Desc
}

// NewMetricsCollector returns a collector over svc. Register it on the
// process registry (lenny-ops uses the default registry so the §16.9
// /metrics exposition scrapes it).
func NewMetricsCollector(svc *Service) *MetricsCollector {
	return &MetricsCollector{
		svc: svc,
		phase: prometheus.NewDesc(
			"lenny_platform_upgrade_phase",
			"§25.8 current platform-upgrade phase encoded as an integer (1=Preflight .. 7=Verification, 0 terminal).",
			[]string{"target_version"}, nil,
		),
		duration: prometheus.NewDesc(
			"lenny_platform_upgrade_duration_seconds",
			"§25.8 time in seconds since the active platform upgrade started.",
			[]string{"target_version"}, nil,
		),
	}
}

// Describe implements prometheus.Collector.
func (c *MetricsCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.phase
	ch <- c.duration
}

// Collect implements prometheus.Collector. A load error or an absent
// upgrade emits nothing; the metrics are deliberately omitted rather
// than reported as zero so a dashboard distinguishes "no upgrade" from
// "upgrade at phase 0".
func (c *MetricsCollector) Collect(ch chan<- prometheus.Metric) {
	if c == nil || c.svc == nil {
		return
	}
	snap, err := c.svc.MetricsSnapshot(context.Background())
	if err != nil || !snap.Present {
		return
	}
	ch <- prometheus.MustNewConstMetric(c.phase, prometheus.GaugeValue, float64(snap.PhaseCode), snap.TargetVersion)
	ch <- prometheus.MustNewConstMetric(c.duration, prometheus.GaugeValue, snap.DurationSeconds, snap.TargetVersion)
}
