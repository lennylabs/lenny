// SPDX-License-Identifier: MIT

package load

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// startupDurationMetric is the §6.3 claim-to-ready histogram. Per
// recordStartupDuration in pkg/gateway/sessionserver/start.go it is
// PodClaim + CredentialAssignment + AgentSessionStart and excludes
// workspace materialization and setup commands, so its P95 is the
// gate-canonical 2s/5s SLO envelope (spec §6.3 line 348). The benchmark
// reads it from the gateway /metrics endpoint rather than timing the
// client request, whose envelope is broader.
const startupDurationMetric = "lenny_session_startup_duration_seconds"

// StartupBuckets is a snapshot of the §6.3 claim-to-ready histogram for
// one runtime class, summed across the pool and isolation_profile label
// dimensions. Le maps a bucket upper bound (the Prometheus le label) to
// its cumulative observation count; Sum and Count are the histogram's
// _sum and _count series. The in-process histogram is cumulative and
// shared across every scenario that hits the gateway, so a single
// snapshot mixes the traffic of the whole suite. Take a snapshot before
// and after an arm and pass both to P95Delta to isolate that arm.
type StartupBuckets struct {
	Le    map[float64]float64
	Sum   float64
	Count float64
}

// ScrapeStartupBuckets reads the gateway /metrics endpoint and returns
// the §6.3 claim-to-ready histogram snapshot for the given runtime class
// (runc, gvisor, kata). It fails the test on a transport or parse error;
// a class with no series yet yields an empty snapshot (Count == 0), which
// P95Delta treats as a zero-sample arm.
func ScrapeStartupBuckets(t testing.TB, baseURL, runtimeClass string) StartupBuckets {
	t.Helper()
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(strings.TrimRight(baseURL, "/") + "/metrics")
	if err != nil {
		t.Fatalf("ScrapeStartupBuckets: GET /metrics: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ScrapeStartupBuckets: GET /metrics returned %d", resp.StatusCode)
	}
	b, err := parseStartupBuckets(resp.Body, runtimeClass)
	if err != nil {
		t.Fatalf("ScrapeStartupBuckets: parse /metrics: %v", err)
	}
	return b
}

// parseStartupBuckets parses the Prometheus text exposition in r and
// aggregates the startupDurationMetric series whose runtime_class label
// matches runtimeClass. Bucket counts are summed across the remaining
// label dimensions (pool, isolation_profile), which is valid because
// every series shares the same le boundaries.
func parseStartupBuckets(r io.Reader, runtimeClass string) (StartupBuckets, error) {
	out := StartupBuckets{Le: map[float64]float64{}}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, labels, value, ok := parseMetricLine(line)
		if !ok {
			continue
		}
		switch name {
		case startupDurationMetric + "_bucket":
			if labels["runtime_class"] != runtimeClass {
				continue
			}
			le, err := parseLe(labels["le"])
			if err != nil {
				return StartupBuckets{}, err
			}
			out.Le[le] += value
		case startupDurationMetric + "_sum":
			if labels["runtime_class"] != runtimeClass {
				continue
			}
			out.Sum += value
		case startupDurationMetric + "_count":
			if labels["runtime_class"] != runtimeClass {
				continue
			}
			out.Count += value
		}
	}
	if err := sc.Err(); err != nil {
		return StartupBuckets{}, err
	}
	return out, nil
}

// parseMetricLine splits a single Prometheus exposition line into its
// metric name, label set, and value. It reports false for a line it
// cannot parse so the caller skips it.
func parseMetricLine(line string) (name string, labels map[string]string, value float64, ok bool) {
	labels = map[string]string{}
	brace := strings.IndexByte(line, '{')
	var rest string
	if brace < 0 {
		// No labels: "<name> <value>".
		sp := strings.IndexByte(line, ' ')
		if sp < 0 {
			return "", nil, 0, false
		}
		name = line[:sp]
		rest = strings.TrimSpace(line[sp+1:])
	} else {
		name = line[:brace]
		end := strings.IndexByte(line, '}')
		if end < 0 || end < brace {
			return "", nil, 0, false
		}
		labels = parseLabels(line[brace+1 : end])
		rest = strings.TrimSpace(line[end+1:])
	}
	// rest is "<value>" possibly followed by a timestamp.
	if sp := strings.IndexByte(rest, ' '); sp >= 0 {
		rest = rest[:sp]
	}
	v, err := strconv.ParseFloat(rest, 64)
	if err != nil {
		return "", nil, 0, false
	}
	return name, labels, v, true
}

// parseLabels parses a Prometheus label block (the text between the
// braces) into a map. It handles the quoted, comma-separated
// `key="value"` form the gateway emits; it does not handle escaped
// quotes inside a value, which the startup labels never contain.
func parseLabels(block string) map[string]string {
	labels := map[string]string{}
	for _, pair := range splitLabelPairs(block) {
		eq := strings.IndexByte(pair, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(pair[:eq])
		val := strings.TrimSpace(pair[eq+1:])
		val = strings.TrimPrefix(val, `"`)
		val = strings.TrimSuffix(val, `"`)
		labels[key] = val
	}
	return labels
}

// splitLabelPairs splits a label block on commas that sit outside a
// quoted value.
func splitLabelPairs(block string) []string {
	var pairs []string
	var cur strings.Builder
	inQuote := false
	for _, r := range block {
		switch r {
		case '"':
			inQuote = !inQuote
			cur.WriteRune(r)
		case ',':
			if inQuote {
				cur.WriteRune(r)
			} else {
				pairs = append(pairs, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		pairs = append(pairs, cur.String())
	}
	return pairs
}

// parseLe parses a Prometheus le bucket boundary, mapping "+Inf" to
// +Inf.
func parseLe(s string) (float64, error) {
	if s == "+Inf" || s == "Inf" {
		return math.Inf(1), nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("parse le %q: %w", s, err)
	}
	return v, nil
}

// P95Delta returns the 95th-percentile claim-to-ready latency in seconds
// for the observations that landed between the before and after
// snapshots, isolating one arm's traffic from the cumulative shared
// histogram. It returns (0, false) when no observation fell in the
// window. The quantile uses the standard Prometheus histogram_quantile
// linear interpolation within the resolving bucket.
func P95Delta(before, after StartupBuckets) (float64, bool) {
	return quantileDelta(0.95, before, after)
}

func quantileDelta(q float64, before, after StartupBuckets) (float64, bool) {
	type bucket struct {
		le    float64
		count float64 // cumulative delta count up to le
	}
	les := make([]float64, 0, len(after.Le))
	for le := range after.Le {
		les = append(les, le)
	}
	sort.Float64s(les)

	buckets := make([]bucket, 0, len(les))
	for _, le := range les {
		delta := after.Le[le] - before.Le[le]
		if delta < 0 {
			delta = 0 // a counter reset in this bucket between snapshots
		}
		buckets = append(buckets, bucket{le: le, count: delta})
	}
	if len(buckets) == 0 {
		return 0, false
	}
	// The difference of two cumulative histograms is not guaranteed to be
	// monotonic across le: concurrent traffic from other tests landing in
	// lower buckets between the snapshots, or a partial counter reset, can
	// leave a higher bucket's delta below a lower one's. The interpolation
	// below (and sort.Search) require a non-decreasing cumulative array, so
	// enforce monotonicity with a forward max-pass, exactly as Prometheus
	// histogram_quantile does (ensureMonotonic). Without it sort.Search's
	// binary search is undefined and the reported P95 is silently wrong.
	for i := 1; i < len(buckets); i++ {
		if buckets[i].count < buckets[i-1].count {
			buckets[i].count = buckets[i-1].count
		}
	}
	// The highest le must be the +Inf bucket, which carries the full count.
	// A snapshot whose top bucket is finite is truncated or malformed; treat
	// it as no measurement rather than mistaking a finite bucket for the total.
	if !math.IsInf(buckets[len(buckets)-1].le, 1) {
		return 0, false
	}
	total := buckets[len(buckets)-1].count
	if total <= 0 {
		return 0, false
	}
	rank := q * total
	// Find the first bucket whose cumulative delta count reaches rank.
	idx := sort.Search(len(buckets), func(i int) bool { return buckets[i].count >= rank })
	if idx == len(buckets) {
		idx = len(buckets) - 1
	}
	upperLe := buckets[idx].le
	if math.IsInf(upperLe, 1) {
		// The quantile falls in the open-ended +Inf bucket; Prometheus
		// returns the highest finite boundary as the best estimate.
		for i := len(buckets) - 1; i >= 0; i-- {
			if !math.IsInf(buckets[i].le, 1) {
				return buckets[i].le, true
			}
		}
		return 0, false
	}
	lowerLe := 0.0
	cumBefore := 0.0
	if idx > 0 {
		lowerLe = buckets[idx-1].le
		cumBefore = buckets[idx-1].count
	}
	cumAt := buckets[idx].count
	if cumAt == cumBefore {
		return upperLe, true
	}
	return lowerLe + (upperLe-lowerLe)*(rank-cumBefore)/(cumAt-cumBefore), true
}
