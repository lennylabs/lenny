// SPDX-License-Identifier: MIT

package health_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/health"
	"github.com/lennylabs/lenny/pkg/ops/conventions"
)

func TestOnTransitionFiresOnAggregateChange(t *testing.T) {
	// §25.3: the OnTransition hook fires when the aggregate status
	// changes between Reports — the health_status_changed signal.
	agg := health.NewAggregator()
	status := health.StatusHealthy
	agg.Register(health.CheckerFunc{
		ComponentName: "db",
		Fn:            func(context.Context) health.Component { return health.Component{Status: status} },
	})
	var transitions []string
	agg.OnTransition(func(prev, curr health.Status) {
		transitions = append(transitions, string(prev)+"->"+string(curr))
	})

	// The first Report establishes the baseline and fires nothing.
	agg.Report(context.Background())
	if len(transitions) != 0 {
		t.Fatalf("first report must not fire a transition: %v", transitions)
	}
	// The status degrades — one transition fires.
	status = health.StatusUnhealthy
	agg.Report(context.Background())
	// A further Report at the same status fires nothing.
	agg.Report(context.Background())
	if len(transitions) != 1 || transitions[0] != "healthy->unhealthy" {
		t.Errorf("transitions = %v, want one healthy->unhealthy", transitions)
	}
}

func healthy(name string) health.Checker {
	return health.CheckerFunc{
		ComponentName: name,
		Fn: func(context.Context) health.Component {
			return health.Component{Name: name, Status: health.StatusHealthy}
		},
	}
}

func failing(name string, s health.Status) health.Checker {
	return health.CheckerFunc{
		ComponentName: name,
		Fn: func(context.Context) health.Component {
			return health.Component{
				Name:            name,
				Status:          s,
				Detail:          "subsystem impaired",
				SuggestedAction: "restart " + name,
			}
		},
	}
}

func TestAggregatorAllHealthy(t *testing.T) {
	agg := health.NewAggregator()
	agg.Register(healthy("sessionstore"))
	agg.Register(healthy("blobstore"))
	report := agg.Report(context.Background())
	if report.Status != health.StatusHealthy {
		t.Errorf("status: %q, want healthy", report.Status)
	}
	if len(report.Components) != 2 {
		t.Errorf("components: %d", len(report.Components))
	}
	// Components sorted by name.
	if report.Components[0].Name != "blobstore" {
		t.Errorf("not sorted: %+v", report.Components)
	}
}

// TestReportStampsThresholdSource_spec_25_13_4848 asserts every
// Report carries the §25.4 envelope with
// thresholdSource=compiled-in-defaults — the gateway's in-process
// tracker always evaluates the compiled-in thresholds, never the
// operator-customized Prometheus rules. F-25.13.5.
func TestReportStampsThresholdSource_spec_25_13_4848(t *testing.T) {
	agg := health.NewAggregator()
	agg.Register(healthy("sessionstore"))
	report := agg.Report(context.Background())
	if report.Degradation == nil {
		t.Fatal("expected degradation envelope on the Report")
	}
	if report.Degradation.ThresholdSource != conventions.ThresholdSourceCompiledInDefaults {
		t.Errorf("thresholdSource = %q, want %q",
			report.Degradation.ThresholdSource,
			conventions.ThresholdSourceCompiledInDefaults)
	}
	if report.Degradation.Level != conventions.DegradationHealthy {
		t.Errorf("envelope level = %q, want healthy", report.Degradation.Level)
	}
	// A degraded component must surface as level=degraded on the
	// envelope; the threshold source still reflects the compiled-in
	// defaults since the gateway's evaluator did the work.
	agg.Register(failing("blobstore", health.StatusDegraded))
	report = agg.Report(context.Background())
	if report.Degradation.Level != conventions.DegradationDegraded {
		t.Errorf("envelope level after degraded component = %q, want degraded", report.Degradation.Level)
	}
	if report.Degradation.ThresholdSource != conventions.ThresholdSourceCompiledInDefaults {
		t.Errorf("thresholdSource (degraded) = %q, want compiled-in-defaults", report.Degradation.ThresholdSource)
	}
	// Unhealthy maps to the envelope's "failed" level per §25.4.
	agg.Register(failing("redis", health.StatusUnhealthy))
	report = agg.Report(context.Background())
	if report.Degradation.Level != conventions.DegradationFailed {
		t.Errorf("envelope level after unhealthy component = %q, want failed", report.Degradation.Level)
	}
}

func TestAggregatorTakesWorstStatus(t *testing.T) {
	agg := health.NewAggregator()
	agg.Register(healthy("a"))
	agg.Register(failing("b", health.StatusDegraded))
	if report := agg.Report(context.Background()); report.Status != health.StatusDegraded {
		t.Errorf("degraded should propagate: %q", report.Status)
	}

	agg.Register(failing("c", health.StatusUnhealthy))
	if report := agg.Report(context.Background()); report.Status != health.StatusUnhealthy {
		t.Errorf("unhealthy should propagate: %q", report.Status)
	}
}

func TestAggregatorComponentLookup(t *testing.T) {
	agg := health.NewAggregator()
	agg.Register(failing("redis", health.StatusDegraded))
	comp, ok := agg.Component(context.Background(), "redis")
	if !ok {
		t.Fatal("redis component not found")
	}
	if comp.Status != health.StatusDegraded || comp.SuggestedAction == "" {
		t.Errorf("component: %+v", comp)
	}
	if _, ok := agg.Component(context.Background(), "missing"); ok {
		t.Error("missing component should return ok=false")
	}
}

func TestHandlerHealthyReturns200(t *testing.T) {
	agg := health.NewAggregator()
	agg.Register(healthy("a"))
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/health", nil)
	rr := httptest.NewRecorder()
	health.Handler(agg).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var report health.Report
	_ = json.Unmarshal(rr.Body.Bytes(), &report)
	if report.Status != health.StatusHealthy {
		t.Errorf("report status: %q", report.Status)
	}
}

func TestHandlerUnhealthyReturns503(t *testing.T) {
	agg := health.NewAggregator()
	agg.Register(failing("redis", health.StatusUnhealthy))
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/health", nil)
	rr := httptest.NewRecorder()
	health.Handler(agg).ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("unhealthy: got %d, want 503", rr.Code)
	}
}

func TestHandlerSummary(t *testing.T) {
	agg := health.NewAggregator()
	agg.Register(healthy("a"))
	agg.Register(healthy("b"))
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/health/summary", nil)
	rr := httptest.NewRecorder()
	health.Handler(agg).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var resp struct {
		Status         string `json:"status"`
		ComponentCount int    `json:"componentCount"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.ComponentCount != 2 {
		t.Errorf("componentCount: %d", resp.ComponentCount)
	}
}

func TestHandlerComponentEndpoint(t *testing.T) {
	agg := health.NewAggregator()
	agg.Register(failing("blobstore", health.StatusDegraded))
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/health/blobstore", nil)
	rr := httptest.NewRecorder()
	health.Handler(agg).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK { // degraded → 200
		t.Fatalf("status: %d", rr.Code)
	}
	var comp health.Component
	_ = json.Unmarshal(rr.Body.Bytes(), &comp)
	if comp.Name != "blobstore" || comp.Status != health.StatusDegraded {
		t.Errorf("component: %+v", comp)
	}
}

func TestHandlerUnknownComponent404(t *testing.T) {
	agg := health.NewAggregator()
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/health/missing", nil)
	rr := httptest.NewRecorder()
	health.Handler(agg).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("unknown component: got %d, want 404", rr.Code)
	}
}

// TestProbesRunInParallel_spec_25_3_441 asserts that registered
// Checkers fan out concurrently rather than serially. The aggregator
// runtime is bounded by the slowest probe rather than the sum of all
// probe latencies, so a slow dependency does not stall the §25.3
// /v1/admin/health response beyond a single probe's ceiling.
// spec: §25.3 line 441.
func TestProbesRunInParallel_spec_25_3_441(t *testing.T) {
	const (
		n     = 8
		delay = 80 * time.Millisecond
	)
	agg := health.NewAggregator()
	var inflight atomic.Int32
	var peak atomic.Int32
	probe := func(name string) health.Checker {
		return health.CheckerFunc{
			ComponentName: name,
			Fn: func(context.Context) health.Component {
				cur := inflight.Add(1)
				for {
					p := peak.Load()
					if cur <= p || peak.CompareAndSwap(p, cur) {
						break
					}
				}
				time.Sleep(delay)
				inflight.Add(-1)
				return health.Component{Name: name, Status: health.StatusHealthy}
			},
		}
	}
	for i := 0; i < n; i++ {
		agg.Register(probe(string(rune('a' + i))))
	}
	start := time.Now()
	report := agg.Report(context.Background())
	elapsed := time.Since(start)
	if report.Status != health.StatusHealthy {
		t.Errorf("status: %q, want healthy", report.Status)
	}
	if len(report.Components) != n {
		t.Errorf("components: %d, want %d", len(report.Components), n)
	}
	// Sequential execution would take at least n*delay; parallel
	// execution completes in close to delay. Allow a generous 4×
	// margin so a slow CI host does not flake the test, while still
	// rejecting the n-times-serial baseline.
	if elapsed >= time.Duration(n)*delay/2 {
		t.Errorf("aggregate runtime: %v, want close to %v (parallel)", elapsed, delay)
	}
	if peak.Load() < 2 {
		t.Errorf("peak concurrent probes: %d, want ≥ 2", peak.Load())
	}
}
