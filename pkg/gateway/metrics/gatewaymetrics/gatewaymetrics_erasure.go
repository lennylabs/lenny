// SPDX-License-Identifier: MIT

package gatewaymetrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// erasureMetrics holds the §12.8 user-level erasure job throughput and SLA metrics.
type erasureMetrics struct {
	erasureJobFailed         *prometheus.CounterVec
	erasureJobsActive        prometheus.Gauge
	erasureJobDuration       prometheus.Observer
	erasureJobOverdueSeconds *prometheus.GaugeVec
	erasureJobAgeSeconds     *prometheus.GaugeVec
}

// newErasureMetrics constructs, registers, and materializes the erasure metric subsystem
// against reg. spec: §16 observability metrics.
func newErasureMetrics(reg *prometheus.Registry) (erasureMetrics, error) {
	var m erasureMetrics
	// spec: §12.8 CMP-026 / §16.1 — user-level erasure job
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
	// spec: §12.8 — erasure throughput / SLA signals.
	// lenny_erasure_jobs_active tracks in-progress jobs;
	// lenny_erasure_job_duration_seconds tracks completion time;
	// lenny_erasure_job_age_seconds reports each in-progress job's age, and
	// lenny_erasure_job_overdue_seconds, the age minus the job's own
	// deadline, feeds the §16.5 ErasureJobOverdue alert
	// (max by (tenant_id, job_id) (overdue_seconds) > 0). The leader's
	// sampler publishes all three from the job store, so the active count
	// is cluster-wide. spec: §12.8 (Erasure SLA enforcement).
	erasureJobsActive, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_erasure_jobs_active",
		Help: "§12.8 user-level erasure jobs currently in progress.",
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
	erasureJobOverdueSeconds, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_erasure_job_overdue_seconds",
		Help: "§12.8 seconds an in-progress user-level erasure job is past its deadline (negative before it), by tenant and job; ErasureJobOverdue fires above 0.",
	}, []string{"tenant_id", "job_id"})
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
		erasureJobOverdueSeconds,
		erasureJobAgeSeconds)
	erasureActive := erasureJobsActive.WithLabelValues()
	erasureDuration := erasureJobDuration.WithLabelValues()
	m.erasureJobFailed = erasureJobFailed
	m.erasureJobsActive = erasureActive
	m.erasureJobDuration = erasureDuration
	m.erasureJobOverdueSeconds = erasureJobOverdueSeconds
	m.erasureJobAgeSeconds = erasureJobAgeSeconds
	return m, nil
}
