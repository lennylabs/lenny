// SPDX-License-Identifier: MIT

package poolscaling

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// scriptedQuerier answers each PromQL query from a map keyed by a
// substring of the query, so the test asserts the §5.2 line 573 query
// shapes without an exact string match.
type scriptedQuerier struct {
	answers map[string]float64
	err     map[string]error
	seen    []string
}

func (q *scriptedQuerier) Query(_ context.Context, query string) (float64, error) {
	q.seen = append(q.seen, query)
	for sub, e := range q.err {
		if strings.Contains(query, sub) {
			return 0, e
		}
	}
	for sub, v := range q.answers {
		if strings.Contains(query, sub) {
			return v, nil
		}
	}
	return 0, nil
}

// spec: §5.2 line 573 — base_demand_p95 from
// rate(lenny_stateless_requests_total[5m]) and burst_p99_claims from
// max_over_time(lenny_stateless_concurrent_active[5m]).
func TestStatelessDemand_ObservedPoolDerivesBothSignals(t *testing.T) {
	q := &scriptedQuerier{answers: map[string]float64{
		"count(lenny_stateless_requests_total":                2, // present
		"sum(rate(lenny_stateless_requests_total":             4.5,
		"sum(max_over_time(lenny_stateless_concurrent_active": 7,
	}}
	s := &PrometheusStatelessDemandSource{Querier: q}
	d, err := s.PoolDemand(context.Background(), "acme-stateless")
	if err != nil {
		t.Fatalf("PoolDemand: %v", err)
	}
	if !d.Observed {
		t.Fatal("Observed = false, want true for a pool with stateless series")
	}
	if d.BaseDemandP95 != 4.5 {
		t.Errorf("BaseDemandP95 = %v, want 4.5", d.BaseDemandP95)
	}
	if d.BurstP99Claims != 7 {
		t.Errorf("BurstP99Claims = %v, want 7", d.BurstP99Claims)
	}
	// The pool label must be present in every query.
	for _, query := range q.seen {
		if !strings.Contains(query, `pool="acme-stateless"`) {
			t.Errorf("query missing pool matcher: %s", query)
		}
	}
}

// A non-stateless pool (no request series) reports Observed=false so the
// controller holds it at bootstrap minWarm, and does NOT issue the
// demand queries.
func TestStatelessDemand_AbsentSeriesStaysBootstrap(t *testing.T) {
	q := &scriptedQuerier{answers: map[string]float64{
		"count(lenny_stateless_requests_total": 0, // no series
	}}
	s := &PrometheusStatelessDemandSource{Querier: q}
	d, err := s.PoolDemand(context.Background(), "session-pool")
	if err != nil {
		t.Fatalf("PoolDemand: %v", err)
	}
	if d.Observed {
		t.Fatal("Observed = true, want false for a pool with no stateless series")
	}
	for _, query := range q.seen {
		if strings.Contains(query, "rate(") || strings.Contains(query, "max_over_time(") {
			t.Errorf("demand query issued for an absent-series pool: %s", query)
		}
	}
}

// A Prometheus query error fails the reconcile (fail-closed) rather than
// silently reporting Observed=false.
func TestStatelessDemand_QueryErrorPropagates(t *testing.T) {
	q := &scriptedQuerier{
		answers: map[string]float64{"count(lenny_stateless_requests_total": 1},
		err:     map[string]error{"sum(rate(": errors.New("prometheus down")},
	}
	s := &PrometheusStatelessDemandSource{Querier: q}
	if _, err := s.PoolDemand(context.Background(), "acme-stateless"); err == nil {
		t.Fatal("PoolDemand error = nil, want propagated query error")
	}
}
