// SPDX-License-Identifier: MIT

package podregistry

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// spec: §12.6 line 484 — lenny_pod_registry_watch_lag_seconds is labeled
// by pool and implementation, so an operator can compare watch
// propagation latency across the CRD and Postgres backends.
func TestObserveWatchLagLabelsByImplementation_spec_12_6_484(t *testing.T) {
	reg := prometheus.NewRegistry()
	m, err := NewMetrics(reg)
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}
	m.observeWatchLag("echo-pool", implCRD, 0.25)
	m.observeWatchLag("claude-pool", implPostgres, 1.5)

	if got := testutil.ToFloat64(m.watchLag.WithLabelValues("echo-pool", "crd")); got != 0.25 {
		t.Errorf("crd watch lag = %v, want 0.25", got)
	}
	if got := testutil.ToFloat64(m.watchLag.WithLabelValues("claude-pool", "postgres")); got != 1.5 {
		t.Errorf("postgres watch lag = %v, want 1.5", got)
	}
}

// A negative lag sample (clock skew between the database now() and the
// reader) is clamped to 0 rather than reported as a nonsensical negative
// gauge value.
func TestObserveWatchLagClampsNegative_spec_12_6_484(t *testing.T) {
	reg := prometheus.NewRegistry()
	m, err := NewMetrics(reg)
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}
	m.observeWatchLag("echo-pool", implPostgres, -3.0)
	if got := testutil.ToFloat64(m.watchLag.WithLabelValues("echo-pool", "postgres")); got != 0 {
		t.Errorf("clamped watch lag = %v, want 0", got)
	}
}
