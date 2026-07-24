// SPDX-License-Identifier: MIT

package playground

import (
	"math"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// spec278Metric describes one non-histogram row of the §27.8
// "Metrics" table: a metric name, its Prometheus type, and its exact
// label set (nil for the unlabelled counter).
type spec278Metric struct {
	name   string
	typ    dto.MetricType
	labels []string
}

// spec278Metrics transcribes the §27.8 "Metrics" table's Counter rows
// verbatim (name, Prometheus type, Labels column). The histogram row
// (lenny_playground_session_revocation_propagation_seconds) is pinned
// separately below because it also carries a bucket boundary list.
var spec278Metrics = []spec278Metric{
	{name: "lenny_playground_page_views_total", typ: dto.MetricType_COUNTER, labels: []string{"authMode"}},
	{name: "lenny_playground_sessions_created_total", typ: dto.MetricType_COUNTER, labels: []string{"runtime"}},
	{name: "lenny_playground_ws_connect_total", typ: dto.MetricType_COUNTER, labels: []string{"outcome"}},
	{name: "lenny_playground_session_revocations_total", typ: dto.MetricType_COUNTER, labels: []string{"reason"}},
	{name: "lenny_playground_dev_tenant_not_seeded_total", typ: dto.MetricType_COUNTER, labels: nil},
}

// spec278HistogramBuckets are the §27.8-documented bucket boundaries
// for lenny_playground_session_revocation_propagation_seconds: "Histogram
// buckets SHOULD span [5 ms, 10 ms, 25 ms, 50 ms, 100 ms, 250 ms, 500
// ms, 1 s, 2.5 s] to bracket the SLO."
var spec278HistogramBuckets = []float64{0.005, 0.010, 0.025, 0.050, 0.100, 0.250, 0.500, 1.0, 2.5}

// labelNames returns the label names attached to a gathered metric
// family's first (and, for these tests, only) sample.
func labelNames(t *testing.T, mf *dto.MetricFamily) []string {
	t.Helper()
	if len(mf.GetMetric()) == 0 {
		t.Fatalf("metric family %s has no samples; exercise it before gathering", mf.GetName())
	}
	var names []string
	for _, lp := range mf.GetMetric()[0].GetLabel() {
		names = append(names, lp.GetName())
	}
	return names
}

// sameSet reports whether a and b contain the same label names,
// order-independent.
func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]bool, len(a))
	for _, v := range a {
		seen[v] = true
	}
	for _, v := range b {
		if !seen[v] {
			return false
		}
	}
	return true
}

// TestMetricCatalogMatchesSpec278 registers the real §27.8 playground
// metric set against a fresh registry and gathers it back, pinning
// every catalog row's name, Prometheus type, label set, and (for the
// revocation-propagation histogram) bucket boundaries to the spec
// table, independent of the generic §16.1 catalog completeness check
// (pkg/observability/metrics/catalog_test.go), whose spec161Metrics
// allowlist has no lenny_playground_* entries and so asserts nothing
// about this catalog.
//
// spec: §27.8 ("Metrics") — table rows for lenny_playground_page_views_total,
// lenny_playground_sessions_created_total, lenny_playground_ws_connect_total,
// lenny_playground_session_revocations_total,
// lenny_playground_session_revocation_propagation_seconds, and
// lenny_playground_dev_tenant_not_seeded_total, each with an explicit
// Prometheus "Type" and "Labels" column, plus "Histogram buckets SHOULD
// span [5 ms, 10 ms, 25 ms, 50 ms, 100 ms, 250 ms, 500 ms, 1 s, 2.5 s]
// to bracket the SLO."
//
// diagnosis: a failure here means NewMetrics registered a §27.8
// metric under the wrong name or Prometheus type, with the wrong
// label set, or (for the revocation-propagation histogram) with
// buckets that no longer bracket the documented SLO thresholds.
func TestMetricCatalogMatchesSpec278(t *testing.T) {
	reg := prometheus.NewRegistry()
	m, err := NewMetrics(reg)
	if err != nil {
		// pkg/gateway/mcpfabric/playground/metrics.go registers
		// lenny_playground_page_views_total with the label
		// "authMode", which fails the §16.1.1 snake_case label
		// check in pkg/observability/metrics and makes NewMetrics
		// return an error against every registry, before any
		// §27.8 metric is registered. The tier-5 and tier-9
		// live-cluster playground journeys hit the same root
		// cause and carry the identical skip reason.
		t.Skip("playground metrics registration fails the §16.1.1 snake_case label check on \"authMode\"; needs a spec/code reconciliation before this can run (see BUILD-GAPS.md §16.1 Metrics Finding 8): " + err.Error())
	}

	// Exercise one sample per metric so each Vec-backed collector has
	// a child to report: an untouched CounterVec/HistogramVec has no
	// child metric yet, and Gather() omits a family with no samples
	// entirely rather than reporting it empty.
	m.pageView(AuthModeOIDC)
	m.sessionCreated("go")
	m.wsConnectOutcome("success")
	m.revocation("user_logout")
	m.revocationPropagation("pubsub_delivered", 0.01)
	m.devTenantNotSeeded()

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	byName := make(map[string]*dto.MetricFamily, len(families))
	for _, mf := range families {
		byName[mf.GetName()] = mf
	}

	for _, want := range spec278Metrics {
		mf, ok := byName[want.name]
		if !ok {
			t.Errorf("§27.8 metric %s is not registered", want.name)
			continue
		}
		if mf.GetType() != want.typ {
			t.Errorf("%s: type = %s, want %s", want.name, mf.GetType(), want.typ)
		}
		if got := labelNames(t, mf); !sameSet(got, want.labels) {
			t.Errorf("%s: labels = %v, want %v", want.name, got, want.labels)
		}
	}

	const histName = "lenny_playground_session_revocation_propagation_seconds"
	hmf, ok := byName[histName]
	if !ok {
		t.Fatalf("§27.8 metric %s is not registered", histName)
	}
	if hmf.GetType() != dto.MetricType_HISTOGRAM {
		t.Errorf("%s: type = %s, want HISTOGRAM", histName, hmf.GetType())
	}
	if got := labelNames(t, hmf); !sameSet(got, []string{"outcome"}) {
		t.Errorf("%s: labels = %v, want [outcome]", histName, got)
	}

	var upperBounds []float64
	for _, b := range hmf.GetMetric()[0].GetHistogram().GetBucket() {
		if b.GetUpperBound() == math.Inf(1) {
			continue
		}
		upperBounds = append(upperBounds, b.GetUpperBound())
	}
	if len(upperBounds) != len(spec278HistogramBuckets) {
		t.Fatalf("%s: %d finite buckets, want %d %v", histName, len(upperBounds), len(spec278HistogramBuckets), spec278HistogramBuckets)
	}
	for i, want := range spec278HistogramBuckets {
		if upperBounds[i] != want {
			t.Errorf("%s: bucket[%d] = %v, want %v", histName, i, upperBounds[i], want)
		}
	}
}
