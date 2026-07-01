// SPDX-License-Identifier: MIT

package gatewaymetrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// coreMetrics holds the §4.1 / §16.1 gateway HTTP request and horizontal-scaling signals.
type coreMetrics struct {
	requestsTotal         *prometheus.CounterVec
	requestDuration       *prometheus.HistogramVec
	activeSessions        prometheus.Gauge
	activeStreams         prometheus.Gauge
	requestQueueDepth     prometheus.Gauge
	rejectionRate         prometheus.Gauge
	maxSessionsPerReplica *prometheus.GaugeVec
	minReplicas           prometheus.Gauge
	streamCeiling         prometheus.Gauge
	replicaCount          prometheus.Gauge
	extractionThreshold   *prometheus.GaugeVec
	gcPauseP99Ms          prometheus.Gauge
	// replayBufferUtilization is the §16.1
	// lenny_event_bus_replay_buffer_utilization gauge — ratio of the
	// in-memory replay buffer in use relative to capacity (0..1; 1.0
	// means the buffer is full and oldest events are being evicted).
	// The gateway samples the worst per-session ratio across the
	// session SSE bus periodically. spec: §10.4 line 389, §16 catalog.
	// F-10.4.11.
	replayBufferUtilization prometheus.Gauge
	// pdbBlockedEvictions is the §16.1
	// lenny_pdb_blocked_evictions_total counter labelled by `pdb` and
	// `controller`; increments each time the §10.4 PDB-status poller
	// observes the PDB blocking (DisruptionsAllowed=0). spec: §16
	// catalog. F-10.4.4.
	pdbBlockedEvictions *prometheus.CounterVec
}

// newCoreMetrics constructs, registers, and materializes the core metric subsystem
// against reg. spec: §16 observability metrics.
func newCoreMetrics(reg *prometheus.Registry) (coreMetrics, error) {
	var m coreMetrics
	requestsTotal, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_gateway_requests_total",
		Help: "Total gateway HTTP requests, labelled by method, route, and status class.",
	}, []string{"method", "route", "status_class"})
	if err != nil {
		return m, err
	}
	requestDuration, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_gateway_request_duration_seconds",
		Help:    "Gateway HTTP request duration in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "route"})
	if err != nil {
		return m, err
	}
	activeSessions, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_gateway_active_sessions",
		Help: "Current count of non-terminal sessions tracked by the gateway.",
	}, nil)
	if err != nil {
		return m, err
	}
	// §10.1 / §4.1 horizontal-scaling signals. activeStreams is the
	// secondary HPA metric (SCL-026); requestQueueDepth is the primary
	// HPA scale-out trigger (SCL-026) and rejectionRate is the second
	// leading indicator that detects saturation before CPU rises. All
	// three surface on /metrics so the §10.1 custom-metrics pipeline
	// (Prometheus Adapter or KEDA) can scrape them.
	activeStreams, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_gateway_active_streams",
		Help: "Open streaming connections on this gateway replica.",
	}, nil)
	if err != nil {
		return m, err
	}
	requestQueueDepth, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_gateway_request_queue_depth",
		Help: "Requests queued on this gateway replica awaiting a handler goroutine.",
	}, nil)
	if err != nil {
		return m, err
	}
	rejectionRate, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_gateway_rejection_rate",
		Help: "Gateway requests rejected with 429/503 per second on this replica.",
	}, nil)
	if err != nil {
		return m, err
	}
	// §4.1 / §16.1: maxSessionsPerReplica is a startup-set gauge. It is
	// the denominator of the §16.5 GatewaySessionBudgetNearExhaustion
	// alert and the §17.8.2 burst-absorption minReplicas formula. The
	// delivery_mode label distinguishes proxy from direct deliveryMode
	// (per spec, two gauge values are always reported per replica).
	maxSessionsPerReplica, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_gateway_max_sessions_per_replica",
		Help: "Maximum concurrent sessions this replica can serve under the given delivery mode (§4.1).",
	}, []string{"delivery_mode"})
	if err != nil {
		return m, err
	}
	// §4.1 / §16.5: emit the configured HPA-minimum replica count and
	// the per-replica stream ceiling as startup-set scalar gauges so the
	// `GatewayNoHealthyReplicas` and `GatewayActiveStreamsHigh` alert
	// expressions in pkg/alerting/rules/rules.go can read them via
	// scalar(...). The replicaCount gauge is emitted per-replica as the
	// constant 1 so the Prometheus `sum()` recording rule yields the
	// fleet-wide ready-replica count for the `lenny_gateway_replica_count`
	// alert numerator.
	minReplicas, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_gateway_min_replicas",
		Help: "Configured HPA minReplicas floor for the gateway Deployment (§4.1 / §16.5).",
	}, nil)
	if err != nil {
		return m, err
	}
	streamCeiling, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_gateway_stream_ceiling",
		Help: "Configured per-replica streaming-connection ceiling used by the GatewayActiveStreamsHigh alert (§4.1 / §16.5).",
	}, nil)
	if err != nil {
		return m, err
	}
	replicaCount, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_gateway_replica_count",
		Help: "Per-replica ready indicator; the recording rule sum() yields the fleet-wide ready replica count (§4.1 / §16.1).",
	}, nil)
	if err != nil {
		return m, err
	}
	// §4.1: surface the configured per-subsystem extraction
	// thresholds as a startup-set gauge so the values used for an
	// extraction decision are auditable against /metrics and the
	// Helm release history. The subsystem and metric labels match
	// the gateway.extractionThresholds.<subsystem>.<metric> Helm
	// keys.
	extractionThreshold, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_gateway_extraction_threshold",
		Help: "Configured §4.1 per-subsystem extraction threshold by metric.",
	}, []string{"subsystem", "metric"})
	if err != nil {
		return m, err
	}
	// §4.1 shared-process GC pressure signal. Periodic collector
	// reads runtime/debug.ReadGCStats and pushes the p99 over a
	// sliding window into this gauge so the Tier3GCPressureHigh
	// alert (and the Tier 3 promotion criterion) can evaluate.
	gcPauseP99Ms, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_gateway_gc_pause_p99_ms",
		Help: "Process-level GC pause p99 (ms) over the last sliding window (§4.1).",
	}, nil)
	if err != nil {
		return m, err
	}
	// spec: §10.4 line 389 / §16 catalog — operator-facing visibility
	// signal for the per-session SSE replay buffer. The gateway samples
	// MaxReplayBufferUtilization on the periodic poller cadence. F-10.4.11.
	replayBufferUtilization, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_event_bus_replay_buffer_utilization",
		Help: "Ratio of in-memory replay buffer in use relative to capacity (0..1); 1.0 means full and oldest events are being evicted (§10.4 line 389 / §16).",
	}, nil)
	if err != nil {
		return m, err
	}
	// spec: §16 catalog / §10.4 — PDB-blocking observation. Incremented
	// per polling sample where the gateway PDB has DisruptionsAllowed=0;
	// the §16.5 PDBBlockedEvictions alert evaluates a 10-minute rate.
	// F-10.4.4.
	pdbBlockedEvictions, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_pdb_blocked_evictions_total",
		Help: "PodDisruptionBudget-blocked evictions; labelled by `pdb` (resource name) and `controller` (eviction source).",
	}, []string{"pdb", "controller"})
	if err != nil {
		return m, err
	}
	reg.MustRegister(requestsTotal,
		requestDuration,
		maxSessionsPerReplica,
		extractionThreshold,
		activeSessions,
		activeStreams,
		requestQueueDepth,
		rejectionRate,
		gcPauseP99Ms,
		minReplicas,
		streamCeiling,
		replicaCount,
		replayBufferUtilization,
		pdbBlockedEvictions)
	gauge := activeSessions.WithLabelValues()
	streams := activeStreams.WithLabelValues()
	queueDepth := requestQueueDepth.WithLabelValues()
	rejections := rejectionRate.WithLabelValues()
	gcPause := gcPauseP99Ms.WithLabelValues()
	minReplicasChild := minReplicas.WithLabelValues()
	streamCeilingChild := streamCeiling.WithLabelValues()
	replicaCountChild := replicaCount.WithLabelValues()
	replayBufferUtilizationChild := replayBufferUtilization.WithLabelValues()
	m.requestsTotal = requestsTotal
	m.requestDuration = requestDuration
	m.activeSessions = gauge
	m.activeStreams = streams
	m.requestQueueDepth = queueDepth
	m.rejectionRate = rejections
	m.maxSessionsPerReplica = maxSessionsPerReplica
	m.minReplicas = minReplicasChild
	m.streamCeiling = streamCeilingChild
	m.replicaCount = replicaCountChild
	m.extractionThreshold = extractionThreshold
	m.gcPauseP99Ms = gcPause
	m.replayBufferUtilization = replayBufferUtilizationChild
	m.pdbBlockedEvictions = pdbBlockedEvictions
	return m, nil
}
