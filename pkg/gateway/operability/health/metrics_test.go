// SPDX-License-Identifier: MIT

package health

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestHealthMetricsRecordedOnReport_spec_25_3_540 asserts a Report sets
// lenny_health_status to the mapped code for each component and observes
// lenny_health_check_duration_seconds.
func TestHealthMetricsRecordedOnReport_spec_25_3_540(t *testing.T) {
	m, err := NewMetrics(prometheus.NewRegistry())
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}
	agg := NewAggregatorWithCache(0, nil) // disable cache so every Report probes
	agg.SetMetrics(m)
	agg.Register(CheckerFunc{ComponentName: "postgres", Fn: func(context.Context) Component {
		return Component{Status: StatusHealthy}
	}})
	agg.Register(CheckerFunc{ComponentName: "redis", Fn: func(context.Context) Component {
		return Component{Status: StatusUnhealthy}
	}})

	agg.Report(context.Background())

	if got := testutil.ToFloat64(m.status.WithLabelValues("postgres")); got != 0 {
		t.Errorf("status{postgres} = %v, want 0 (healthy)", got)
	}
	if got := testutil.ToFloat64(m.status.WithLabelValues("redis")); got != 2 {
		t.Errorf("status{redis} = %v, want 2 (unhealthy)", got)
	}
	if got := testutil.CollectAndCount(m.duration); got < 2 {
		t.Errorf("duration histogram series = %d, want >= 2 (one per probed component)", got)
	}
}

// TestStatusValueMapping_spec_25_3_542 asserts the §25.3 status-gauge
// encoding: 0=healthy, 1=degraded, 2=unhealthy.
func TestStatusValueMapping_spec_25_3_542(t *testing.T) {
	cases := map[Status]float64{
		StatusHealthy:   0,
		StatusDegraded:  1,
		StatusUnhealthy: 2,
		Status("weird"): 0,
	}
	for s, want := range cases {
		if got := statusValue(s); got != want {
			t.Errorf("statusValue(%q) = %v, want %v", s, got, want)
		}
	}
}

// TestNilHealthMetricsNoOp asserts an Aggregator without metrics reports
// without panicking.
func TestNilHealthMetricsNoOp(t *testing.T) {
	agg := NewAggregator()
	agg.Register(CheckerFunc{ComponentName: "x", Fn: func(context.Context) Component {
		return Component{Status: StatusHealthy}
	}})
	agg.Report(context.Background())
}
