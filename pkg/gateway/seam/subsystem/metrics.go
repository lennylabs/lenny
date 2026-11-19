// SPDX-License-Identifier: MIT

package subsystem

import (
	"context"
	"errors"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// Metrics emits the §4.1 / §16.1 per-subsystem metric family with a
// `subsystem` label so the §16.5 GatewaySubsystemCircuitOpen,
// GatewayQueueDepthHigh, and GatewayLatencyHigh alerts can express
// "any subsystem" conditions in PromQL.
//
// The four published vectors are:
//
//   - lenny_gateway_subsystem_circuit_state{subsystem}     (gauge: 0/1/2)
//   - lenny_gateway_subsystem_queue_depth{subsystem}        (gauge)
//   - lenny_gateway_subsystem_request_duration_seconds{subsystem} (histogram)
//   - lenny_gateway_subsystem_errors_total{subsystem,error_type}   (counter)
//
// Subsystem-specific gauge names (`lenny_upload_handler_active_uploads`,
// `lenny_stream_proxy_queue_depth`, etc.) are emitted by callers via
// the Observe* hooks below; this package owns the unified
// label-keyed family alone so the metric set stays consistent with
// the §16.1 catalog.
//
// spec: §4.1 (per-subsystem metrics) / §16.1
type Metrics struct {
	circuitState *prometheus.GaugeVec
	queueDepth   *prometheus.GaugeVec
	duration     *prometheus.HistogramVec
	errors       *prometheus.CounterVec
}

// NewMetrics registers the §4.1 per-subsystem metric family against
// the supplied registerer. Re-registering the same metric collector
// (e.g., when several Subsystem values share one registry) is
// idempotent: the constructor catches the Prometheus AlreadyRegistered
// error and reuses the existing collector so a Helm-tested registry
// with several subsystems wired against it does not panic.
func NewMetrics(reg prometheus.Registerer) (*Metrics, error) {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}
	circuitState, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_gateway_subsystem_circuit_state",
		Help: "§4.1 per-subsystem circuit-breaker state: 0=closed, 1=half-open, 2=open.",
	}, []string{"subsystem"})
	if err != nil {
		return nil, err
	}
	queueDepth, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_gateway_subsystem_queue_depth",
		Help: "§4.1 per-subsystem in-flight plus queued request count.",
	}, []string{"subsystem"})
	if err != nil {
		return nil, err
	}
	duration, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name: "lenny_gateway_subsystem_request_duration_seconds",
		Help: "§4.1 per-subsystem request duration in seconds, recorded on every Subsystem.Do.",
		// Buckets cover stream-proxy attach (sub-second), upload-
		// handler (multi-second), and the §16.5 GatewayLatencyHigh
		// alert's tier thresholds (0.5s, 1s, 2s).
		Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 30},
	}, []string{"subsystem"})
	if err != nil {
		return nil, err
	}
	errCounter, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_gateway_subsystem_errors_total",
		Help: "§4.1 per-subsystem error count by error type.",
	}, []string{"subsystem", "error_type"})
	if err != nil {
		return nil, err
	}

	for _, c := range []prometheus.Collector{circuitState, queueDepth, duration, errCounter} {
		if err := reg.Register(c); err != nil {
			var are prometheus.AlreadyRegisteredError
			if errors.As(err, &are) {
				// Reuse the previously registered collector so a
				// fresh Metrics value can be returned without a
				// duplicate-registration panic.
				switch existing := are.ExistingCollector.(type) {
				case *prometheus.GaugeVec:
					if c == circuitState {
						circuitState = existing
					} else if c == queueDepth {
						queueDepth = existing
					}
				case *prometheus.HistogramVec:
					duration = existing
				case *prometheus.CounterVec:
					errCounter = existing
				}
				continue
			}
			return nil, err
		}
	}

	return &Metrics{
		circuitState: circuitState,
		queueDepth:   queueDepth,
		duration:     duration,
		errors:       errCounter,
	}, nil
}

// SetCircuitState updates the §4.1 lenny_gateway_subsystem_circuit_state
// gauge for the named subsystem. The value mapping (0 closed, 1 half-
// open, 2 open) matches State.MetricValue.
func (m *Metrics) SetCircuitState(name string, state State) {
	if m == nil {
		return
	}
	m.circuitState.WithLabelValues(name).Set(float64(state.MetricValue()))
}

// SetQueueDepth updates the §4.1 lenny_gateway_subsystem_queue_depth
// gauge for the named subsystem. The §16.5 GatewayQueueDepthHigh alert
// reads this gauge against a tier-scaled threshold.
func (m *Metrics) SetQueueDepth(name string, depth int) {
	if m == nil {
		return
	}
	m.queueDepth.WithLabelValues(name).Set(float64(depth))
}

// ObserveDuration records one §4.1
// lenny_gateway_subsystem_request_duration_seconds sample for the
// named subsystem. The histogram is the source of the §16.5
// GatewayLatencyHigh alert's p95 expression.
func (m *Metrics) ObserveDuration(name string, d time.Duration) {
	if m == nil {
		return
	}
	m.duration.WithLabelValues(name).Observe(d.Seconds())
}

// IncError increments the §4.1 lenny_gateway_subsystem_errors_total
// counter for the named subsystem and error type. The errorType label
// follows the §16.1.1 `error_type` enum (e.g., `circuit_open`,
// `limiter_cancelled`, `upstream_failure`).
func (m *Metrics) IncError(name, errorType string) {
	if m == nil {
		return
	}
	if errorType == "" {
		errorType = "unspecified"
	}
	m.errors.WithLabelValues(name, errorType).Inc()
}

// Observer is the per-instance hook a Subsystem invokes after every
// Do call. The default implementation in this file emits the §4.1
// metric family; a subsystem with its own custom gauges (Upload
// Handler's `lenny_upload_handler_active_uploads`, etc.) provides an
// extension Observer that wraps the default with the additional
// callbacks per §4.1's "Add an Observe hook so individual subsystem
// instances can emit their own subsystem-specific names too".
type Observer interface {
	// OnStateChange is called when the breaker transitions between
	// closed, half-open, and open. The subsystem label value is the
	// owning Subsystem.Name.
	OnStateChange(subsystem string, from, to State)

	// OnDo is called once per Subsystem.Do invocation with the
	// observed duration, the post-call state, the post-call queue
	// depth, and the error (nil on success).
	OnDo(subsystem string, duration time.Duration, state State, queueDepth int, err error)
}

// metricsObserver routes Subsystem callbacks through a *Metrics value
// so a single Observer wires the §4.1 emission for every subsystem.
type metricsObserver struct {
	m *Metrics
}

// NewMetricsObserver returns an Observer that emits the §4.1
// per-subsystem metric family on every Subsystem.Do. A nil m is
// permitted and produces a no-op observer; this keeps tests that
// instantiate a Subsystem without metrics simple.
func NewMetricsObserver(m *Metrics) Observer {
	return &metricsObserver{m: m}
}

// OnStateChange forwards the post-transition state to the circuit
// gauge so the §16.5 GatewaySubsystemCircuitOpen alert observes the
// new value on the next scrape.
func (o *metricsObserver) OnStateChange(name string, from, to State) {
	if o == nil || o.m == nil {
		return
	}
	o.m.SetCircuitState(name, to)
}

// OnDo emits the per-call samples: the request duration histogram, the
// queue depth gauge, the post-call circuit state, and (on err != nil)
// the error counter. classifyError maps the Subsystem.Do errors to the
// canonical `error_type` enum.
func (o *metricsObserver) OnDo(name string, d time.Duration, state State, queueDepth int, err error) {
	if o == nil || o.m == nil {
		return
	}
	o.m.ObserveDuration(name, d)
	o.m.SetQueueDepth(name, queueDepth)
	o.m.SetCircuitState(name, state)
	if err != nil {
		o.m.IncError(name, classifyError(err))
	}
}

// classifyError maps a Subsystem.Do error to the §16.1.1 `error_type`
// label value. The classifier groups the two short-circuit paths
// (open breaker, cancelled limiter context) so the §16.5 alerts can
// distinguish self-protection rejections from upstream failures.
func classifyError(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrCircuitOpen):
		return "circuit_open"
	case errors.Is(err, ErrLimiterStopped):
		return "limiter_cancelled"
	default:
		return "upstream_failure"
	}
}

// noopObserver is the zero-value Observer the package returns when
// no metrics value is wired; it preserves the "subsystem types are
// zero-value-usable" invariant from the package doc.
type noopObserver struct{}

func (noopObserver) OnStateChange(string, State, State)            {}
func (noopObserver) OnDo(string, time.Duration, State, int, error) {}

// nopObserver is shared by every Subsystem that has no Observer wired.
var nopObserver Observer = noopObserver{}

// nowFunc selects a clock for the duration histogram. The variable is
// overridden by the unit tests to feed deterministic timestamps; the
// production path uses time.Now.
var nowFunc = time.Now

// DoObserved is the §4.1-instrumented variant of Subsystem.Do. It
// records the request duration histogram, refreshes the queue-depth
// gauge, updates the circuit-state gauge on every call, fires the
// state-transition callback when the breaker moves, and increments
// the error counter on a non-nil error.
//
// Callers that need a Subsystem.Do without metrics emission keep
// using Subsystem.Do directly; DoObserved is a strict superset that
// invokes Do internally.
//
// spec: §4.1 (per-subsystem metrics)
func (s *Subsystem) DoObserved(ctx context.Context, obs Observer, fn func(context.Context) error) error {
	if obs == nil {
		obs = nopObserver
	}
	before := s.State()
	start := nowFunc()
	err := s.Do(ctx, fn)
	d := nowFunc().Sub(start)
	after := s.State()
	depth := s.QueueDepth() + s.InFlight()
	obs.OnDo(s.Name, d, after, depth, err)
	if before != after {
		obs.OnStateChange(s.Name, before, after)
	}
	return err
}

// PublishGauges pushes the Subsystem's current state and load
// (queue depth + in-flight count) to the §4.1 metric family. It is
// the periodic-poller hook a caller invokes from a watchdog tick
// when its handler path uses the lower-level Breaker.Allow /
// Limiter.TryAcquire primitives directly and therefore does not pass
// through DoObserved. The poller's cadence matches the gateway
// /metrics scrape interval.
//
// spec: §4.1 (per-subsystem metrics)
func (s *Subsystem) PublishGauges(m *Metrics) {
	if s == nil || m == nil {
		return
	}
	m.SetCircuitState(s.Name, s.State())
	m.SetQueueDepth(s.Name, s.QueueDepth()+s.InFlight())
}
