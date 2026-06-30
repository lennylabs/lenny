// SPDX-License-Identifier: MIT

package gatewaymetrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// erasureMetrics holds the §12.8 user-level erasure job throughput and SLA metrics.
type erasureMetrics struct {
	erasureJobFailed          *prometheus.CounterVec
	erasureJobsActive         prometheus.Gauge
	erasureJobDuration        prometheus.Observer
	erasureJobDeadlineSeconds prometheus.Gauge
	erasureJobAgeSeconds      *prometheus.GaugeVec
}

// newErasureMetrics constructs, registers, and materializes the erasure metric subsystem
// against reg. spec: §16 observability metrics.
func newErasureMetrics(reg *prometheus.Registry) (erasureMetrics, error) {
	var m erasureMetrics
	// spec: §12.8 CMP-026 / §16.1 line 262 — user-level erasure job
	// failures by failure phase. failure_phase distinguishes the §12.8
	// failure modes (store_delete, pseudonymization, verification, and the
	// MemoryStore erasure preflight memory_store_preflight); the §16.5
	// ErasureJobFailed alert fires on any increase.
	erasureJobFailed, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_erasure_job_failed_total",
		Help: "§12.8 user-level erasure job failures by tenant and failure_phase.",
	}, []string{"tenant_id", "failure_phase"})
	if err != nil {
		return m, err
	}
	// spec: §12.8 line 768 — erasure throughput / SLA signals.
	// lenny_erasure_jobs_active tracks in-progress jobs;
	// lenny_erasure_job_duration_seconds tracks completion time;
	// lenny_erasure_job_age_seconds and lenny_erasure_job_deadline_seconds
	// feed the §16.5 ErasureJobOverdue alert
	// (age_seconds > scalar(deadline_seconds)).
	erasureJobsActive, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_erasure_jobs_active",
		Help: "§12.8 user-level erasure jobs currently in progress on this replica.",
	}, nil)
	if err != nil {
		return m, err
	}
	erasureJobDuration, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name: "lenny_erasure_job_duration_seconds",
		Help: "§12.8 user-level erasure job wall-clock duration (initiation to terminal phase).",
		// Erasure spans seconds (in-memory) to the T3 72h SLA bound.
		Buckets: []float64{1, 5, 30, 60, 300, 1800, 3600, 21600, 86400, 259200},
	}, nil)
	if err != nil {
		return m, err
	}
	erasureJobDeadlineSeconds, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_erasure_job_deadline_seconds",
		Help: "§12.8 line 768 erasure SLA deadline (seconds) the §16.5 ErasureJobOverdue alert compares against.",
	}, nil)
	if err != nil {
		return m, err
	}
	erasureJobAgeSeconds, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_erasure_job_age_seconds",
		Help: "§12.8 age (seconds) of an in-progress user-level erasure job, by tenant and job.",
	}, []string{"tenant_id", "job_id"})
	if err != nil {
		return m, err
	}
	reg.MustRegister(erasureJobFailed,
		erasureJobsActive,
		erasureJobDuration,
		erasureJobDeadlineSeconds,
		erasureJobAgeSeconds)
	erasureActive := erasureJobsActive.WithLabelValues()
	erasureDuration := erasureJobDuration.WithLabelValues()
	erasureDeadline := erasureJobDeadlineSeconds.WithLabelValues()
	m.erasureJobFailed = erasureJobFailed
	m.erasureJobsActive = erasureActive
	m.erasureJobDuration = erasureDuration
	m.erasureJobDeadlineSeconds = erasureDeadline
	m.erasureJobAgeSeconds = erasureJobAgeSeconds
	return m, nil
}
