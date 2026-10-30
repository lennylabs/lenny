// SPDX-License-Identifier: MIT

package metrics_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/gateway"
	"github.com/lennylabs/lenny/pkg/ops/metrics"
)

// spec: §25.4 Metrics Source — Prometheus instant-query path returns
// a scalar value when the resultType is "vector".
func TestPrometheusClient_Query_Vector(t *testing.T) {
	srv := promServer(t, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"__name__":"lenny_sessions"},"value":[1700000000,"42"]}]}}`)
	defer srv.Close()
	c := newClient(t, srv.URL)
	v, err := c.Query(context.Background(), "lenny_sessions")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if v != 42 {
		t.Fatalf("value: want 42, got %v", v)
	}
}

// spec: §25.4 — scalar resultType is also valid and decodes the same
// way.
func TestPrometheusClient_Query_Scalar(t *testing.T) {
	srv := promServer(t, `{"status":"success","data":{"resultType":"scalar","result":[1700000000,"7.5"]}}`)
	defer srv.Close()
	c := newClient(t, srv.URL)
	v, err := c.Query(context.Background(), "scalar(1)")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if v != 7.5 {
		t.Fatalf("value: want 7.5, got %v", v)
	}
}

// spec: §25.4 — empty vector result is "no data", returned as zero
// (not an error) so recommendation rules treat the metric as absent.
func TestPrometheusClient_Query_EmptyVector(t *testing.T) {
	srv := promServer(t, `{"status":"success","data":{"resultType":"vector","result":[]}}`)
	defer srv.Close()
	c := newClient(t, srv.URL)
	v, err := c.Query(context.Background(), "lenny_sessions")
	if err != nil || v != 0 {
		t.Fatalf("want (0, nil), got (%v, %v)", v, err)
	}
}

// spec: §25.4 — query_range returns the first series' samples (multi-
// series queries should sum() in PromQL).
func TestPrometheusClient_QueryRange(t *testing.T) {
	body := `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[1700000000,"1"],[1700000060,"2"]]}]}}`
	srv := promServer(t, body)
	defer srv.Close()
	c := newClient(t, srv.URL)
	pts, err := c.QueryRange(context.Background(), "lenny_sessions", time.Unix(1700000000, 0), time.Unix(1700000060, 0), time.Minute)
	if err != nil {
		t.Fatalf("query_range: %v", err)
	}
	if len(pts) != 2 || pts[0].Value != 1 || pts[1].Value != 2 {
		t.Fatalf("points: %+v", pts)
	}
}

// spec: §25.4 "Prometheus Unreachable" — HTTP 5xx is classified as
// unreachable so the fallback path activates.
func TestPrometheusClient_5xx_IsUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := newClient(t, srv.URL)
	_, err := c.Query(context.Background(), "lenny_sessions")
	var pe *metrics.PrometheusError
	if !errors.As(err, &pe) || pe.Status != 500 {
		t.Fatalf("want PrometheusError 500, got %v", err)
	}
}

// spec: §25.4 — Prometheus reports `status:"error"` for malformed
// queries. The client surfaces the errorType + error string.
func TestPrometheusClient_PrometheusError(t *testing.T) {
	srv := promServer(t, `{"status":"error","errorType":"bad_data","error":"unknown function"}`)
	defer srv.Close()
	c := newClient(t, srv.URL)
	_, err := c.Query(context.Background(), "bogus(metric)")
	if err == nil || !strings.Contains(err.Error(), "bad_data") {
		t.Fatalf("want bad_data, got %v", err)
	}
}

// spec: §25.4 — PrometheusWithFallback falls back to fan-out when the
// primary returns an unreachable-class error.
func TestPrometheusWithFallback_FallsBackOn5xx(t *testing.T) {
	primary := failingSource{err: &metrics.PrometheusError{Status: 502, Body: "bad gateway"}}
	gwSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintln(w, "# HELP lenny_active_sessions count")
		fmt.Fprintln(w, "lenny_active_sessions 12")
	}))
	defer gwSrv.Close()
	gc, err := gateway.NewClient(gateway.Config{
		BaseURL:           gwSrv.URL,
		PerRequestTimeout: time.Second,
		Discovery:         staticDiscovery{endpoints: []string{gwSrv.URL}},
		FanOutTimeout:     time.Second,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	scraper := metrics.NewFanOutScraper(gc, "/metrics")
	source := metrics.NewPrometheusWithFallback(primary, scraper)
	v, err := source.Query(context.Background(), "lenny_active_sessions")
	if err != nil {
		t.Fatalf("fallback query: %v", err)
	}
	if v != 12 {
		t.Fatalf("fallback value: want 12, got %v", v)
	}
}

// spec: §25.4 — when the primary succeeds the fallback is not
// consulted.
func TestPrometheusWithFallback_PrimarySuccess(t *testing.T) {
	primary := constantSource{value: 5}
	source := metrics.NewPrometheusWithFallback(primary, nil)
	v, err := source.Query(context.Background(), "anything")
	if err != nil || v != 5 {
		t.Fatalf("want (5, nil), got (%v, %v)", v, err)
	}
}

// spec: §25.4 — a non-unreachable error (e.g., 4xx PromQL syntax
// error) is *not* swallowed by the fallback; callers see the real
// error.
func TestPrometheusWithFallback_4xxNotFallback(t *testing.T) {
	primary := failingSource{err: errors.New("bad request")}
	source := metrics.NewPrometheusWithFallback(primary, metrics.NewFanOutScraper(nil, "/metrics"))
	_, err := source.Query(context.Background(), "anything")
	if err == nil || !strings.Contains(err.Error(), "bad request") {
		t.Fatalf("want bad request, got %v", err)
	}
}

// spec: §25.4 — Reader.WindowedRate composes the canonical rate()
// PromQL via DefaultQueryShape and returns the underlying source's
// value.
func TestReader_WindowedRate(t *testing.T) {
	got := ""
	source := recordingSource{onQuery: func(q string) (float64, error) {
		got = q
		return 3.14, nil
	}}
	r := metrics.NewReader(source, nil, 0)
	v, ok := r.WindowedRate("lenny_requests_total", map[string]string{"job": "gateway"}, 5*time.Minute)
	if !ok || v != 3.14 {
		t.Fatalf("WindowedRate: ok=%v v=%v", ok, v)
	}
	want := `sum(rate(lenny_requests_total{job="gateway"}[5m]))`
	if got != want {
		t.Fatalf("query: want %q, got %q", want, got)
	}
}

// spec: §25.4 — Reader.HistogramQuantile rejects quantiles outside
// [0, 1].
func TestReader_HistogramQuantile_RejectsOutOfRange(t *testing.T) {
	r := metrics.NewReader(constantSource{value: 1}, nil, 0)
	if _, ok := r.HistogramQuantile("metric", nil, 1.5); ok {
		t.Fatal("quantile > 1 should be rejected")
	}
	if _, ok := r.HistogramQuantile("metric", nil, -0.1); ok {
		t.Fatal("quantile < 0 should be rejected")
	}
}

// spec: §25.4 Fallback Caching — the Reader's TTL cache reuses the
// last value for the same query within the TTL window.
func TestReader_Cache_ReusesValueWithinTTL(t *testing.T) {
	calls := 0
	source := recordingSource{onQuery: func(q string) (float64, error) {
		calls++
		return float64(calls), nil
	}}
	r := metrics.NewReader(source, nil, time.Minute)
	v1, _ := r.GaugeValue("metric", nil)
	v2, _ := r.GaugeValue("metric", nil)
	if v1 != 1 || v2 != 1 || calls != 1 {
		t.Fatalf("cache: v1=%v v2=%v calls=%d", v1, v2, calls)
	}
}

// spec: §25.4 — ExtractMetricName recovers the bare metric name from
// any aggregation wrapping so the fan-out fallback can scrape a plain
// gauge.
func TestExtractMetricName(t *testing.T) {
	cases := map[string]string{
		"lenny_active_sessions":                              "lenny_active_sessions",
		`lenny_active_sessions{pool="warm"}`:                 "lenny_active_sessions",
		`sum(rate(lenny_requests_total{job="gateway"}[5m]))`: "lenny_requests_total",
		`sum(lenny_active_sessions)`:                         "lenny_active_sessions",
	}
	for in, want := range cases {
		if got := metrics.ExtractMetricName(in); got != want {
			t.Errorf("ExtractMetricName(%q): want %q, got %q", in, want, got)
		}
	}
}

// spec: §25.4 partial-aggregation — a fan-out scrape that reaches
// only some replicas still returns the sum across the responders.
func TestFanOutScraper_SumGauge_PartialReplicas(t *testing.T) {
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintln(w, "lenny_active_sessions 7")
	}))
	defer good.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "", http.StatusInternalServerError)
	}))
	defer bad.Close()
	gc, err := gateway.NewClient(gateway.Config{
		BaseURL:           good.URL,
		PerRequestTimeout: time.Second,
		Discovery:         staticDiscovery{endpoints: []string{good.URL, bad.URL}},
		FanOutTimeout:     time.Second,
	})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	scraper := metrics.NewFanOutScraper(gc, "/metrics")
	v, err := scraper.SumGauge(context.Background(), "lenny_active_sessions")
	if err != nil {
		t.Fatalf("sum: %v", err)
	}
	if v != 7 {
		t.Fatalf("partial sum: want 7, got %v", v)
	}
}

// promServer is a stub Prometheus HTTP API that returns body for every
// request.
func promServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
}

func newClient(t *testing.T, base string) *metrics.PrometheusClient {
	t.Helper()
	c, err := metrics.NewPrometheusClient(metrics.PrometheusConfig{BaseURL: base, QueryTimeout: time.Second})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return c
}

// constantSource is a MetricSource that returns a fixed value.
type constantSource struct{ value float64 }

func (c constantSource) Query(context.Context, string) (float64, error) { return c.value, nil }
func (c constantSource) QueryRange(context.Context, string, time.Time, time.Time, time.Duration) ([]metrics.DataPoint, error) {
	return nil, nil
}

// failingSource is a MetricSource that always errors.
type failingSource struct{ err error }

func (f failingSource) Query(context.Context, string) (float64, error) { return 0, f.err }
func (f failingSource) QueryRange(context.Context, string, time.Time, time.Time, time.Duration) ([]metrics.DataPoint, error) {
	return nil, f.err
}

// recordingSource captures every query string the Reader emits.
type recordingSource struct {
	onQuery func(string) (float64, error)
}

func (r recordingSource) Query(_ context.Context, q string) (float64, error) { return r.onQuery(q) }
func (r recordingSource) QueryRange(context.Context, string, time.Time, time.Time, time.Duration) ([]metrics.DataPoint, error) {
	return nil, nil
}

// staticDiscovery is a ReplicaDiscovery that returns a fixed endpoint
// list.
type staticDiscovery struct{ endpoints []string }

func (s staticDiscovery) Endpoints(context.Context) ([]string, error) { return s.endpoints, nil }
