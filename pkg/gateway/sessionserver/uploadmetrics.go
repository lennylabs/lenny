// SPDX-License-Identifier: MIT

package sessionserver

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// UploadHandlerMetrics receives the §16.1 gateway upload-handler
// observations: the cumulative bytes committed through the handler and
// the current in-flight depth of the §4.1 Upload Handler subsystem. The
// subsystem's concurrency limiter and circuit breaker (the §7.4 line 448
// isolation guarantee) are wired separately on the Subsystem value; this
// interface supplies the two catalogued metric emitters those primitives
// did not previously feed. Implementations must be non-blocking — the
// upload path records best-effort. spec: §7.4 line 448; §16.1 — F-13.4.12.
type UploadHandlerMetrics interface {
	// AddUploadBytes increments lenny_upload_bytes_total by n, the
	// uncompressed bytes a successful upload committed to the blob store.
	AddUploadBytes(n int64)
	// SetUploadQueueDepth sets lenny_upload_queue_depth to the Upload
	// Handler subsystem's current in-flight-plus-queued request count.
	SetUploadQueueDepth(depth int)
	// AddExtractionAbort increments
	// lenny_upload_extraction_aborted_total{error_type} for one §7.4 line
	// 462 archive-extraction abort. errorType is the §13.4 sub-code
	// (max_decompressed_size, non_regular_entry, symlink, etc.). F-7.4.11.
	AddExtractionAbort(errorType string)
}

// PromUploadMetrics is the prometheus-backed UploadHandlerMetrics. It
// owns the §16.1 upload-handler-specific metric names
// (lenny_upload_bytes_total, lenny_upload_queue_depth,
// lenny_upload_extraction_aborted_total) that the unified per-subsystem
// family (lenny_gateway_subsystem_queue_depth{subsystem}) does not carry
// under their catalogued names. spec: §16.1 — F-13.4.12, F-7.4.11.
type PromUploadMetrics struct {
	bytesTotal    prometheus.Counter
	queueDepth    prometheus.Gauge
	extractAborts *prometheus.CounterVec
}

// NewUploadMetrics registers the §16.1 upload-handler metric set against
// reg and returns the emitter. spec: §16.1 — F-13.4.12, F-7.4.11.
func NewUploadMetrics(reg prometheus.Registerer) (*PromUploadMetrics, error) {
	bytesVec, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_upload_bytes_total",
		Help: "Cumulative bytes written through the gateway upload handler (§16.1).",
	}, nil)
	if err != nil {
		return nil, err
	}
	depthVec, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_upload_queue_depth",
		Help: "In-flight upload requests in the §4.1 gateway Upload Handler subsystem (§16.1).",
	}, nil)
	if err != nil {
		return nil, err
	}
	abortVec, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_upload_extraction_aborted_total",
		Help: "Archive-extraction aborts by §13.4 error type (§16.1).",
	}, []string{"error_type"})
	if err != nil {
		return nil, err
	}
	metrics.MustRegister(reg, bytesVec)
	metrics.MustRegister(reg, depthVec)
	metrics.MustRegister(reg, abortVec)
	// Materialize the unlabelled children at construction so /metrics
	// emits both series before the first upload, mirroring the unlabelled
	// gauges in gatewaymetrics.New.
	return &PromUploadMetrics{
		bytesTotal:    bytesVec.WithLabelValues(),
		queueDepth:    depthVec.WithLabelValues(),
		extractAborts: abortVec,
	}, nil
}

// AddUploadBytes implements UploadHandlerMetrics.
func (m *PromUploadMetrics) AddUploadBytes(n int64) {
	if m == nil || n <= 0 {
		return
	}
	m.bytesTotal.Add(float64(n))
}

// SetUploadQueueDepth implements UploadHandlerMetrics.
func (m *PromUploadMetrics) SetUploadQueueDepth(depth int) {
	if m == nil {
		return
	}
	if depth < 0 {
		depth = 0
	}
	m.queueDepth.Set(float64(depth))
}

// AddExtractionAbort implements UploadHandlerMetrics.
func (m *PromUploadMetrics) AddExtractionAbort(errorType string) {
	if m == nil || errorType == "" {
		return
	}
	m.extractAborts.WithLabelValues(errorType).Inc()
}
