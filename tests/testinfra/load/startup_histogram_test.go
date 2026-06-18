// SPDX-License-Identifier: MIT

package load

import (
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// exposition is a §16 Prometheus text fixture carrying the §6.3
// claim-to-ready histogram for two runtime classes plus an unrelated
// metric, exercising the runtime_class filter and the cross-label
// (pool, isolation_profile) bucket aggregation.
const exposition = `
# HELP lenny_session_startup_duration_seconds claim-to-ready
# TYPE lenny_session_startup_duration_seconds histogram
lenny_session_startup_duration_seconds_bucket{pool="echo-pool-sidecar",runtime_class="runc",isolation_profile="standard",le="0.5"} 0
lenny_session_startup_duration_seconds_bucket{pool="echo-pool-sidecar",runtime_class="runc",isolation_profile="standard",le="1"} 3
lenny_session_startup_duration_seconds_bucket{pool="echo-pool-sidecar",runtime_class="runc",isolation_profile="standard",le="2"} 12
lenny_session_startup_duration_seconds_bucket{pool="echo-pool-sidecar",runtime_class="runc",isolation_profile="standard",le="5"} 14
lenny_session_startup_duration_seconds_bucket{pool="echo-pool-sidecar",runtime_class="runc",isolation_profile="standard",le="+Inf"} 14
lenny_session_startup_duration_seconds_sum{pool="echo-pool-sidecar",runtime_class="runc",isolation_profile="standard"} 18.0
lenny_session_startup_duration_seconds_count{pool="echo-pool-sidecar",runtime_class="runc",isolation_profile="standard"} 14
lenny_session_startup_duration_seconds_bucket{pool="preconnect-echo-pool",runtime_class="runc",isolation_profile="standard",le="0.5"} 0
lenny_session_startup_duration_seconds_bucket{pool="preconnect-echo-pool",runtime_class="runc",isolation_profile="standard",le="1"} 2
lenny_session_startup_duration_seconds_bucket{pool="preconnect-echo-pool",runtime_class="runc",isolation_profile="standard",le="2"} 6
lenny_session_startup_duration_seconds_bucket{pool="preconnect-echo-pool",runtime_class="runc",isolation_profile="standard",le="5"} 6
lenny_session_startup_duration_seconds_bucket{pool="preconnect-echo-pool",runtime_class="runc",isolation_profile="standard",le="+Inf"} 6
lenny_session_startup_duration_seconds_sum{pool="preconnect-echo-pool",runtime_class="runc",isolation_profile="standard"} 5.4
lenny_session_startup_duration_seconds_count{pool="preconnect-echo-pool",runtime_class="runc",isolation_profile="standard"} 6
lenny_session_startup_duration_seconds_bucket{pool="g-pool",runtime_class="gvisor",isolation_profile="sandboxed",le="0.5"} 0
lenny_session_startup_duration_seconds_bucket{pool="g-pool",runtime_class="gvisor",isolation_profile="sandboxed",le="5"} 99
lenny_session_startup_duration_seconds_bucket{pool="g-pool",runtime_class="gvisor",isolation_profile="sandboxed",le="+Inf"} 99
unrelated_metric_total{runtime_class="runc"} 1234
`

// spec: 6.3 (claim-to-ready histogram parse + P95 over a snapshot window)
func TestParseStartupBucketsAggregatesByRuntimeClass(t *testing.T) {
	b, err := parseStartupBuckets(strings.NewReader(exposition), "runc")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// Two runc pools aggregate: le="2" is 12 + 6 = 18, +Inf is 14 + 6 = 20.
	if got := b.Le[2]; got != 18 {
		t.Errorf("le=2 aggregate: got %v, want 18", got)
	}
	if got := b.Le[math.Inf(1)]; got != 20 {
		t.Errorf("le=+Inf aggregate: got %v, want 20", got)
	}
	if b.Count != 20 {
		t.Errorf("count aggregate: got %v, want 20", b.Count)
	}
	// The gvisor series must not leak into the runc snapshot.
	if got := b.Le[5]; got != 20 { // 14 + 6, not 14 + 6 + 99
		t.Errorf("le=5 must exclude gvisor: got %v, want 20", got)
	}
}

// spec: 6.3 (gate-canonical P95 isolates one arm via a before/after delta)
func TestP95DeltaIsolatesWindow(t *testing.T) {
	// Before: the runc histogram already holds the full 20 observations
	// from the exposition. After: 10 new observations all land in the
	// (2, 5] bucket, so le="5" and le="+Inf" each rise by 10.
	before, err := parseStartupBuckets(strings.NewReader(exposition), "runc")
	if err != nil {
		t.Fatalf("parse before: %v", err)
	}
	after := StartupBuckets{Le: map[float64]float64{}}
	for le, c := range before.Le {
		after.Le[le] = c
	}
	after.Le[5] += 10
	after.Le[math.Inf(1)] += 10
	after.Count = before.Count + 10

	p95, ok := P95Delta(before, after)
	if !ok {
		t.Fatal("P95Delta: no observations in window")
	}
	// total delta = 10, rank = 9.5, all in (2, 5]: 2 + (5-2)*9.5/10 = 4.85.
	if math.Abs(p95-4.85) > 1e-9 {
		t.Errorf("P95Delta: got %v, want 4.85", p95)
	}
}

// spec: 6.3 (empty window yields no measurement rather than a zero)
func TestP95DeltaEmptyWindow(t *testing.T) {
	snap, err := parseStartupBuckets(strings.NewReader(exposition), "runc")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, ok := P95Delta(snap, snap); ok {
		t.Error("P95Delta over an unchanged snapshot should report no observations")
	}
}

// spec: 6.3 (a full single-snapshot quantile, no prior traffic)
func TestP95DeltaFromZeroBaseline(t *testing.T) {
	after, err := parseStartupBuckets(strings.NewReader(exposition), "runc")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	zero := StartupBuckets{Le: map[float64]float64{}}
	p95, ok := P95Delta(zero, after)
	if !ok {
		t.Fatal("P95Delta: no observations")
	}
	// total = 20, rank = 19; le="2" cum 18 < 19 <= le="5" cum 20:
	// 2 + (5-2)*(19-18)/(20-18) = 3.5.
	if math.Abs(p95-3.5) > 1e-9 {
		t.Errorf("P95Delta from zero: got %v, want 3.5", p95)
	}
}

// spec: 6.3 (a counter reset / concurrent low-bucket traffic between
// snapshots must not yield a wrong quantile via a non-monotonic delta)
func TestP95DeltaNonMonotonicDelta(t *testing.T) {
	// after's le="5" delta drops below le="2" delta (a high bucket reset
	// or other traffic shrank it), which without the monotonicity pass
	// would make sort.Search return the wrong bucket. The forward max-pass
	// lifts le="5" back to le="2"'s cumulative count.
	before := StartupBuckets{Le: map[float64]float64{0.5: 0, 1: 0, 2: 0, 5: 10, math.Inf(1): 10}}
	after := StartupBuckets{Le: map[float64]float64{0.5: 0, 1: 0, 2: 20, 5: 10, math.Inf(1): 20}}
	// Raw deltas: le2=20, le5=0, +Inf=10 -> non-monotonic (le5 < le2).
	// After ensureMonotonic: le2=20, le5=20, +Inf=20; total=20, rank=19,
	// resolves in (1,2]: 1 + (2-1)*(19-0)/(20-0) = 1.95.
	p95, ok := P95Delta(before, after)
	if !ok {
		t.Fatal("P95Delta: no observations")
	}
	if math.Abs(p95-1.95) > 1e-9 {
		t.Errorf("P95Delta non-monotonic: got %v, want 1.95 (monotonicity pass applied)", p95)
	}
}

// spec: 6.3 (a full counter reset clamps every delta to zero -> no data)
func TestP95DeltaFullCounterReset(t *testing.T) {
	before := StartupBuckets{Le: map[float64]float64{1: 5, 2: 8, math.Inf(1): 8}}
	after := StartupBuckets{Le: map[float64]float64{1: 0, 2: 0, math.Inf(1): 0}}
	if _, ok := P95Delta(before, after); ok {
		t.Error("P95Delta over a full counter reset should report no observations")
	}
}

// spec: 6.3 (quantile in the open-ended +Inf bucket returns the highest
// finite boundary as the estimate)
func TestP95DeltaInInfBucket(t *testing.T) {
	zero := StartupBuckets{Le: map[float64]float64{}}
	// All 10 observations land above le="5", in (5, +Inf].
	after := StartupBuckets{Le: map[float64]float64{0.5: 0, 1: 0, 2: 0, 5: 0, math.Inf(1): 10}}
	p95, ok := P95Delta(zero, after)
	if !ok {
		t.Fatal("P95Delta: no observations")
	}
	if p95 != 5 {
		t.Errorf("P95Delta in +Inf bucket: got %v, want 5 (highest finite boundary)", p95)
	}
}

// spec: 6.3 (a snapshot whose only bucket is +Inf has no finite estimate)
func TestP95DeltaOnlyInfBucket(t *testing.T) {
	zero := StartupBuckets{Le: map[float64]float64{}}
	after := StartupBuckets{Le: map[float64]float64{math.Inf(1): 5}}
	if _, ok := P95Delta(zero, after); ok {
		t.Error("P95Delta with only a +Inf bucket should report no finite estimate")
	}
}

// spec: 6.3 (a snapshot whose top bucket is finite is treated as no data)
func TestP95DeltaNoInfBucket(t *testing.T) {
	zero := StartupBuckets{Le: map[float64]float64{}}
	after := StartupBuckets{Le: map[float64]float64{1: 5, 5: 10}} // no +Inf
	if _, ok := P95Delta(zero, after); ok {
		t.Error("P95Delta without a +Inf bucket should report no measurement")
	}
}

// spec: 6.3 (runtime_class with no series yields no measurement)
func TestP95DeltaEmptyBuckets(t *testing.T) {
	if _, ok := P95Delta(StartupBuckets{Le: map[float64]float64{}}, StartupBuckets{Le: map[float64]float64{}}); ok {
		t.Error("P95Delta over empty snapshots should report no measurement")
	}
}

// spec: 6.3 (a malformed le boundary is a parse error)
func TestParseStartupBucketsBadLe(t *testing.T) {
	bad := `lenny_session_startup_duration_seconds_bucket{runtime_class="runc",le="notanumber"} 5`
	if _, err := parseStartupBuckets(strings.NewReader(bad), "runc"); err == nil {
		t.Error("parseStartupBuckets should error on a non-numeric le")
	}
}

// spec: 6.3 (scrape parses the gateway /metrics histogram over HTTP)
func TestScrapeStartupBuckets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/metrics" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, exposition)
	}))
	defer srv.Close()
	b := ScrapeStartupBuckets(t, srv.URL, "runc")
	if b.Count != 20 {
		t.Errorf("ScrapeStartupBuckets count: got %v, want 20", b.Count)
	}
	if got := b.Le[math.Inf(1)]; got != 20 {
		t.Errorf("ScrapeStartupBuckets +Inf: got %v, want 20", got)
	}
}
