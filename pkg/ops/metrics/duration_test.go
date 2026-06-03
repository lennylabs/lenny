// SPDX-License-Identifier: MIT

package metrics_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/metrics"
)

// recordingQueryMetrics captures the observed query latencies by kind.
type recordingQueryMetrics struct {
	mu      sync.Mutex
	samples map[string][]float64
}

func (r *recordingQueryMetrics) ObserveQuery(kind string, seconds float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.samples == nil {
		r.samples = map[string][]float64{}
	}
	r.samples[kind] = append(r.samples[kind], seconds)
}

func (r *recordingQueryMetrics) for_(kind string) []float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.samples[kind]
}

// TestPrometheusClient_ObservesQueryDuration covers §25.4 lines 1914-1916:
// every instant and range query records its wall-clock latency against
// lenny_prometheus_query_duration_seconds{kind}.
func TestPrometheusClient_ObservesQueryDuration_spec_25_4(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/query" {
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"scalar","result":[0,"42"]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
	}))
	defer srv.Close()

	rec := &recordingQueryMetrics{}
	// A clock that advances 250ms per read so the observed durations are
	// deterministic and non-zero (start read, then end read).
	var ticks int
	base := time.Unix(5_000, 0)
	now := func() time.Time {
		d := time.Duration(ticks) * 250 * time.Millisecond
		ticks++
		return base.Add(d)
	}
	c, err := metrics.NewPrometheusClient(metrics.PrometheusConfig{
		BaseURL: srv.URL,
		Metrics: rec,
		Now:     now,
	})
	if err != nil {
		t.Fatalf("NewPrometheusClient: %v", err)
	}
	if _, err := c.Query(context.Background(), "up"); err != nil {
		t.Fatalf("Query: %v", err)
	}
	if _, err := c.QueryRange(context.Background(), "up", base, base.Add(time.Hour), time.Minute); err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	if got := rec.for_(metrics.QueryKindInstant); len(got) != 1 || got[0] <= 0 {
		t.Fatalf("instant samples = %v, want one positive sample", got)
	}
	if got := rec.for_(metrics.QueryKindRange); len(got) != 1 || got[0] <= 0 {
		t.Fatalf("range samples = %v, want one positive sample", got)
	}
}

// TestNoopQueryMetrics covers the default no-op seam: an unconfigured
// client observes silently.
func TestNoopQueryMetrics(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"scalar","result":[0,"1"]}}`))
	}))
	defer srv.Close()
	c, err := metrics.NewPrometheusClient(metrics.PrometheusConfig{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewPrometheusClient: %v", err)
	}
	if _, err := c.Query(context.Background(), "up"); err != nil {
		t.Fatalf("Query: %v", err)
	}
}
