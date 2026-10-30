// SPDX-License-Identifier: MIT

package subsystem_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"

	"github.com/lennylabs/lenny/pkg/gateway/core/subsystem"
)

// spec: §4.1 — NewMetrics registers the four §16.1 per-subsystem
// vectors against the supplied registry; setting any series with the
// `subsystem` label surfaces the metric on the next Gather.
func TestMetricsRegistration(t *testing.T) {
	reg := prometheus.NewRegistry()
	m, err := subsystem.NewMetrics(reg)
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}
	if m == nil {
		t.Fatal("NewMetrics returned nil Metrics")
	}
	// Force one series per vector so they appear in Gather output.
	m.SetCircuitState("stream_proxy", subsystem.StateClosed)
	m.SetQueueDepth("stream_proxy", 0)
	m.ObserveDuration("stream_proxy", time.Millisecond)
	m.IncError("stream_proxy", "upstream_failure")

	mf, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	wantNames := map[string]bool{
		"lenny_gateway_subsystem_circuit_state":            false,
		"lenny_gateway_subsystem_queue_depth":              false,
		"lenny_gateway_subsystem_request_duration_seconds": false,
		"lenny_gateway_subsystem_errors_total":             false,
	}
	for _, f := range mf {
		if _, ok := wantNames[f.GetName()]; ok {
			wantNames[f.GetName()] = true
		}
	}
	for name, found := range wantNames {
		if !found {
			t.Errorf("missing registered metric %q", name)
		}
	}
}

// spec: §4.1 — NewMetrics is safe to call twice against the same
// registerer (a second wiring re-uses the previously registered
// collectors instead of panicking).
func TestMetricsRegistrationIdempotent(t *testing.T) {
	reg := prometheus.NewRegistry()
	if _, err := subsystem.NewMetrics(reg); err != nil {
		t.Fatalf("first NewMetrics: %v", err)
	}
	if _, err := subsystem.NewMetrics(reg); err != nil {
		t.Fatalf("second NewMetrics: %v", err)
	}
}

// spec: §4.1 — DoObserved emits the request-duration histogram,
// queue-depth gauge, and circuit-state gauge with the subsystem
// label set to Subsystem.Name on every Do call.
func TestDoObservedEmitsLabelsOnHappyPath(t *testing.T) {
	reg := prometheus.NewRegistry()
	m, err := subsystem.NewMetrics(reg)
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}
	s := &subsystem.Subsystem{
		Name:    "upload_handler",
		Breaker: &subsystem.Breaker{FailureThreshold: 3},
		Limiter: &subsystem.Limiter{MaxConcurrent: 2},
	}
	obs := subsystem.NewMetricsObserver(m)

	if err := s.DoObserved(context.Background(), obs, func(ctx context.Context) error {
		return nil
	}); err != nil {
		t.Fatalf("DoObserved: %v", err)
	}

	// Histogram must record at least one observation.
	if got := histogramSampleCount(t, reg, "lenny_gateway_subsystem_request_duration_seconds", map[string]string{"subsystem": "upload_handler"}); got == 0 {
		t.Errorf("expected at least one duration sample for upload_handler, got 0")
	}
	// Queue depth and circuit-state gauges should be observable for
	// upload_handler.
	dump := mustDump(t, reg)
	if !strings.Contains(dump, `lenny_gateway_subsystem_queue_depth{subsystem="upload_handler"}`) {
		t.Errorf("queue_depth gauge missing upload_handler label; dump:\n%s", dump)
	}
	if !strings.Contains(dump, `lenny_gateway_subsystem_circuit_state{subsystem="upload_handler"}`) {
		t.Errorf("circuit_state gauge missing upload_handler label; dump:\n%s", dump)
	}
}

// spec: §4.1 — the circuit-state gauge advances 0→2 when the
// breaker trips open via a sequence of failures.
func TestDoObservedCircuitStateChange(t *testing.T) {
	reg := prometheus.NewRegistry()
	m, err := subsystem.NewMetrics(reg)
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}
	clk := &fakeClock{t: time.Unix(0, 0)}
	s := &subsystem.Subsystem{
		Name:    "stream_proxy",
		Breaker: &subsystem.Breaker{FailureThreshold: 1, Cooldown: time.Hour, Now: clk.now},
	}
	obs := subsystem.NewMetricsObserver(m)

	failErr := errors.New("upstream down")
	if err := s.DoObserved(context.Background(), obs, func(ctx context.Context) error {
		return failErr
	}); !errors.Is(err, failErr) {
		t.Fatalf("DoObserved = %v, want %v", err, failErr)
	}

	state := testutil.ToFloat64(prometheusGauge(reg, "lenny_gateway_subsystem_circuit_state", map[string]string{"subsystem": "stream_proxy"}))
	if state != 2 {
		t.Fatalf("circuit_state{stream_proxy}=%v after failure, want 2 (open)", state)
	}
}

// spec: §4.1 — the errors_total counter increments on every Do that
// returns a non-nil error, labelled by error_type.
func TestDoObservedErrorCounter(t *testing.T) {
	reg := prometheus.NewRegistry()
	m, err := subsystem.NewMetrics(reg)
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}
	s := &subsystem.Subsystem{
		Name:    "mcp_fabric",
		Breaker: &subsystem.Breaker{FailureThreshold: 100},
	}
	obs := subsystem.NewMetricsObserver(m)

	wantErr := errors.New("upstream failure")
	if err := s.DoObserved(context.Background(), obs, func(ctx context.Context) error {
		return wantErr
	}); !errors.Is(err, wantErr) {
		t.Fatalf("DoObserved = %v, want %v", err, wantErr)
	}

	counter := prometheusCounter(reg, "lenny_gateway_subsystem_errors_total", map[string]string{
		"subsystem":  "mcp_fabric",
		"error_type": "upstream_failure",
	})
	if got := testutil.ToFloat64(counter); got != 1 {
		t.Fatalf("errors_total{mcp_fabric, upstream_failure} = %v, want 1", got)
	}
}

// spec: §4.1 — an open-breaker rejection counts as a `circuit_open`
// error type so the §16.5 alerts can distinguish self-protection
// rejections from upstream failures.
func TestDoObservedCircuitOpenErrorClassification(t *testing.T) {
	reg := prometheus.NewRegistry()
	m, err := subsystem.NewMetrics(reg)
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}
	clk := &fakeClock{t: time.Unix(0, 0)}
	s := &subsystem.Subsystem{
		Name:    "llm_proxy",
		Breaker: &subsystem.Breaker{FailureThreshold: 1, Cooldown: time.Hour, Now: clk.now},
	}
	obs := subsystem.NewMetricsObserver(m)

	// Trip the breaker first.
	failErr := errors.New("upstream down")
	_ = s.DoObserved(context.Background(), obs, func(ctx context.Context) error { return failErr })

	// Second call sees the open breaker.
	err = s.DoObserved(context.Background(), obs, func(ctx context.Context) error { return nil })
	if !errors.Is(err, subsystem.ErrCircuitOpen) {
		t.Fatalf("DoObserved on open breaker = %v, want ErrCircuitOpen", err)
	}

	circuitOpen := prometheusCounter(reg, "lenny_gateway_subsystem_errors_total", map[string]string{
		"subsystem":  "llm_proxy",
		"error_type": "circuit_open",
	})
	if got := testutil.ToFloat64(circuitOpen); got != 1 {
		t.Fatalf("errors_total{llm_proxy, circuit_open} = %v, want 1", got)
	}
}

// spec: §4.1 — DoObserved tolerates a nil Observer; it still
// executes fn and returns its error.
func TestDoObservedNilObserver(t *testing.T) {
	s := &subsystem.Subsystem{Name: "stream_proxy"}
	called := 0
	if err := s.DoObserved(context.Background(), nil, func(ctx context.Context) error {
		called++
		return nil
	}); err != nil {
		t.Fatalf("DoObserved: %v", err)
	}
	if called != 1 {
		t.Fatalf("fn called %d times, want 1", called)
	}
}

// spec: §4.1 — queue depth is reported as the sum of in-flight and
// queued callers so the §16.5 GatewayQueueDepthHigh alert observes
// load even when no caller is currently blocked.
func TestDoObservedQueueDepthIncludesInFlight(t *testing.T) {
	reg := prometheus.NewRegistry()
	m, err := subsystem.NewMetrics(reg)
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}
	s := &subsystem.Subsystem{
		Name:    "stream_proxy",
		Limiter: &subsystem.Limiter{MaxConcurrent: 4},
	}
	obs := subsystem.NewMetricsObserver(m)

	// Hold one slot so InFlight reads 1 during the observed call.
	r, err := s.Limiter.Acquire(context.Background())
	if err != nil {
		t.Fatalf("preload Acquire: %v", err)
	}
	defer r()

	if err := s.DoObserved(context.Background(), obs, func(ctx context.Context) error {
		// During fn, two in-flight (held + this one). Sleep so the
		// observation captures the load.
		return nil
	}); err != nil {
		t.Fatalf("DoObserved: %v", err)
	}

	// After Do, only the held slot remains; the queue_depth gauge
	// reflects the most recent observation, which is 1.
	depth := testutil.ToFloat64(prometheusGauge(reg, "lenny_gateway_subsystem_queue_depth", map[string]string{"subsystem": "stream_proxy"}))
	if depth < 1 {
		t.Fatalf("queue_depth{stream_proxy} = %v, want >= 1 (held slot)", depth)
	}
}

// Helpers --------------------------------------------------------------

// histogramSampleCount returns the total observation count for a
// histogram series matching every label in `labels`.
func histogramSampleCount(t *testing.T, reg *prometheus.Registry, name string, labels map[string]string) uint64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		for _, m := range f.GetMetric() {
			if !labelsMatch(m.GetLabel(), labels) {
				continue
			}
			if h := m.GetHistogram(); h != nil {
				return h.GetSampleCount()
			}
		}
	}
	return 0
}

// prometheusGauge returns a synthetic gauge whose current value
// equals the value of the matching series in the registry, suitable
// for testutil.ToFloat64.
func prometheusGauge(reg *prometheus.Registry, name string, labels map[string]string) prometheus.Gauge {
	g := prometheus.NewGauge(prometheus.GaugeOpts{Name: name})
	g.Set(extractValue(reg, name, labels))
	return g
}

func prometheusCounter(reg *prometheus.Registry, name string, labels map[string]string) prometheus.Counter {
	c := prometheus.NewCounter(prometheus.CounterOpts{Name: name})
	c.Add(extractValue(reg, name, labels))
	return c
}

func extractValue(reg *prometheus.Registry, name string, labels map[string]string) float64 {
	families, err := reg.Gather()
	if err != nil {
		return 0
	}
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		for _, m := range f.GetMetric() {
			if !labelsMatch(m.GetLabel(), labels) {
				continue
			}
			if g := m.GetGauge(); g != nil {
				return g.GetValue()
			}
			if c := m.GetCounter(); c != nil {
				return c.GetValue()
			}
		}
	}
	return 0
}

func labelsMatch(pairs []*dto.LabelPair, want map[string]string) bool {
	for k, v := range want {
		found := false
		for _, lp := range pairs {
			if lp.GetName() == k && lp.GetValue() == v {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// mustDump renders the registry to the Prometheus text format the
// tests inspect for label assertions.
func mustDump(t *testing.T, reg *prometheus.Registry) string {
	t.Helper()
	mf, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	var b strings.Builder
	for _, f := range mf {
		for _, m := range f.GetMetric() {
			b.WriteString(f.GetName())
			b.WriteString("{")
			for i, lp := range m.GetLabel() {
				if i > 0 {
					b.WriteString(",")
				}
				b.WriteString(lp.GetName())
				b.WriteString("=\"")
				b.WriteString(lp.GetValue())
				b.WriteString("\"")
			}
			b.WriteString("}\n")
		}
	}
	return b.String()
}
