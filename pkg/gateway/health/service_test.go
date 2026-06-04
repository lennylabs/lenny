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
	// changes between Reports — the health_status_changed signal. The
	// probe cache is disabled so a mutated checker is re-evaluated on
	// each Report rather than served from the §25.3 line 526 cache.
	agg := health.NewAggregatorWithCache(0, nil)
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
				Name:   name,
				Status: s,
				Detail: "subsystem impaired",
				SuggestedAction: &conventions.SuggestedAction{
					Action:    "RESTART_COMPONENT",
					Reasoning: "restart " + name,
				},
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

// spec: §10.4 line 386 — the readiness probe gates only on the named
// hard backend dependencies, never on a non-gating checker (e.g. SIEM).
// F-10.4.6.
func TestHardDependencyStatus_spec_10_4_386(t *testing.T) {
	agg := health.NewAggregator()
	agg.Register(healthy("postgres"))
	agg.Register(failing("redis", health.StatusUnhealthy))
	agg.Register(failing("siem", health.StatusUnhealthy))

	// Gating on postgres only: a SIEM/redis outage does not flip the
	// verdict because neither name is queried.
	if got := agg.HardDependencyStatus(context.Background(), "postgres"); got != health.StatusHealthy {
		t.Errorf("postgres-only verdict = %q, want healthy", got)
	}
	// Worst-of across the queried set.
	if got := agg.HardDependencyStatus(context.Background(), "postgres", "redis"); got != health.StatusUnhealthy {
		t.Errorf("postgres+redis verdict = %q, want unhealthy", got)
	}
	// An unregistered name is skipped, not treated as unhealthy.
	if got := agg.HardDependencyStatus(context.Background(), "postgres", "missing"); got != health.StatusHealthy {
		t.Errorf("verdict with a missing checker = %q, want healthy", got)
	}
	// No names registered at all: nothing to gate on.
	if got := agg.HardDependencyStatus(context.Background()); got != health.StatusHealthy {
		t.Errorf("empty verdict = %q, want healthy", got)
	}
}

func TestAggregatorComponentLookup(t *testing.T) {
	agg := health.NewAggregator()
	agg.Register(failing("redis", health.StatusDegraded))
	comp, ok := agg.Component(context.Background(), "redis")
	if !ok {
		t.Fatal("redis component not found")
	}
	if comp.Status != health.StatusDegraded || comp.SuggestedAction == nil {
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
	health.Handler(agg, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var report health.Report
	_ = json.Unmarshal(rr.Body.Bytes(), &report)
	if report.Status != health.StatusHealthy {
		t.Errorf("report status: %q", report.Status)
	}
}

// TestHandlerUnhealthyReturns200_spec_25_3_530 asserts the health
// endpoint never returns 5xx: an unhealthy verdict still returns 200
// with status: unhealthy in the body so an agent distinguishes "the
// platform is unhealthy" from "the request to the health endpoint
// failed". spec: §25.3 line 530.
func TestHandlerUnhealthyReturns200_spec_25_3_530(t *testing.T) {
	agg := health.NewAggregator()
	agg.Register(failing("redis", health.StatusUnhealthy))
	for _, path := range []string{"/v1/admin/health", "/v1/admin/health/summary", "/v1/admin/health/redis"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		health.Handler(agg, nil).ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("%s: got %d, want 200 (health endpoint never returns 5xx)", path, rr.Code)
		}
	}
	// The full-report body still carries the unhealthy verdict.
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/health", nil)
	rr := httptest.NewRecorder()
	health.Handler(agg, nil).ServeHTTP(rr, req)
	var report health.Report
	_ = json.Unmarshal(rr.Body.Bytes(), &report)
	if report.Status != health.StatusUnhealthy {
		t.Errorf("report status: %q, want unhealthy", report.Status)
	}
}

func TestHandlerSummary(t *testing.T) {
	agg := health.NewAggregator()
	agg.Register(healthy("a"))
	agg.Register(healthy("b"))
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/health/summary", nil)
	rr := httptest.NewRecorder()
	health.Handler(agg, nil).ServeHTTP(rr, req)
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
	health.Handler(agg, nil).ServeHTTP(rr, req)
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
	health.Handler(agg, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("unknown component: got %d, want 404", rr.Code)
	}
	// spec: §25.3 line 547 — the health surface's only error code.
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body.Error.Code != "UNKNOWN_HEALTH_COMPONENT" {
		t.Errorf("error code = %q, want UNKNOWN_HEALTH_COMPONENT", body.Error.Code)
	}
}

// TestProbeResultsCachedFor5Seconds_spec_25_3_526 asserts a component's
// Check result is reused within the 5-second per-replica cache window
// and re-run once the window elapses, so concurrent health requests do
// not stampede the backing dependency. spec: §25.3 line 526.
func TestProbeResultsCachedFor5Seconds_spec_25_3_526(t *testing.T) {
	clock := time.Unix(1_000_000, 0)
	agg := health.NewAggregatorWithCache(5*time.Second, func() time.Time { return clock })

	var calls atomic.Int64
	agg.Register(health.CheckerFunc{
		ComponentName: "postgres",
		Fn: func(context.Context) health.Component {
			calls.Add(1)
			return health.Component{Name: "postgres", Status: health.StatusHealthy}
		},
	})

	// First Report probes; the next several within the window are served
	// from cache.
	for i := 0; i < 4; i++ {
		agg.Report(context.Background())
		agg.Component(context.Background(), "postgres")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("within the cache window the checker ran %d times, want 1", got)
	}

	// Advance just under the TTL — still cached.
	clock = clock.Add(4 * time.Second)
	agg.Report(context.Background())
	if got := calls.Load(); got != 1 {
		t.Fatalf("at 4s the checker ran %d times, want 1 (still cached)", got)
	}

	// Advance past the TTL — the probe runs again.
	clock = clock.Add(2 * time.Second)
	agg.Report(context.Background())
	if got := calls.Load(); got != 2 {
		t.Fatalf("past the 5s TTL the checker ran %d times, want 2", got)
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
