// SPDX-License-Identifier: MIT

package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// spec: §25.4 lines 1914-1916 / F-25.4.18 — NewPromQueryMetrics registers
// lenny_prometheus_query_duration_seconds{kind} and pre-stamps the three
// closed-enum kinds so the series are scrapeable before the first query.
func TestPromQueryMetrics_RegistersAndPreStamps_spec_25_4_18(t *testing.T) {
	reg := prometheus.NewRegistry()
	m, err := NewPromQueryMetrics(reg)
	if err != nil {
		t.Fatalf("NewPromQueryMetrics: %v", err)
	}

	kinds := map[string]bool{}
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	var found *dto.MetricFamily
	for _, mf := range mfs {
		if mf.GetName() == "lenny_prometheus_query_duration_seconds" {
			found = mf
		}
	}
	if found == nil {
		t.Fatal("lenny_prometheus_query_duration_seconds not registered")
	}
	for _, metric := range found.GetMetric() {
		for _, l := range metric.GetLabel() {
			if l.GetName() == "kind" {
				kinds[l.GetValue()] = true
			}
		}
	}
	for _, want := range []string{QueryKindInstant, QueryKindRange, QueryKindAlerts} {
		if !kinds[want] {
			t.Errorf("kind %q series not pre-stamped", want)
		}
	}

	// An observation increments the corresponding kind's count.
	m.ObserveQuery(QueryKindInstant, 0.25)
	mfs, _ = reg.Gather()
	for _, mf := range mfs {
		if mf.GetName() != "lenny_prometheus_query_duration_seconds" {
			continue
		}
		for _, metric := range mf.GetMetric() {
			for _, l := range metric.GetLabel() {
				if l.GetName() == "kind" && l.GetValue() == QueryKindInstant {
					if metric.GetHistogram().GetSampleCount() != 1 {
						t.Errorf("instant query count = %d, want 1", metric.GetHistogram().GetSampleCount())
					}
				}
			}
		}
	}
}

// A duplicate registration on the same registry fails rather than
// silently double-counting.
func TestPromQueryMetrics_DuplicateRegistration_spec_25_4_18(t *testing.T) {
	reg := prometheus.NewRegistry()
	if _, err := NewPromQueryMetrics(reg); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if _, err := NewPromQueryMetrics(reg); err == nil {
		t.Error("second register on the same registry should fail")
	}
}
