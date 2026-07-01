// SPDX-License-Identifier: MIT

// Package metrics is the §25.4 Metrics Source / cross-replica
// aggregation seam. It implements:
//
//   - MetricSource — a Prometheus-HTTP-API client that answers instant
//     and range queries against any Prometheus-compatible backend.
//   - PrometheusWithFallback — the §25.4 Prometheus-with-fan-out-
//     fallback aggregator. When Prometheus is unreachable (definition
//     in §25.4 "Prometheus Unreachable") it scrapes each gateway
//     replica's /metrics endpoint via the headless Service and
//     aggregates the result. Both modes go through the same
//     interface, so callers branch on neither.
//   - Reader — a pkg/gateway/recommendations.MetricReader satisfied by
//     the source. The same recommendation rule set evaluates against
//     the in-process gateway WindowStore and the Prometheus-backed
//     reader here without modification.
package metrics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/operability/recommendations"
	"github.com/lennylabs/lenny/pkg/ops/gateway"
)

// DataPoint is one (timestamp, value) sample returned by a range
// query. The §25.4 MetricSource interface yields a slice of these for
// callers that need to compute their own windowed aggregations.
type DataPoint struct {
	At    time.Time
	Value float64
}

// MetricSource is the §25.4 interface lenny-ops calls to read
// aggregate metrics across all gateway replicas. The Prometheus path
// is primary; the per-replica fan-out is the fallback.
type MetricSource interface {
	// Query runs an instant query and returns the scalar (or first
	// value of a single-element vector) result.
	Query(ctx context.Context, q string) (float64, error)
	// QueryRange runs a range query between [start, end] with the
	// given step.
	QueryRange(ctx context.Context, q string, start, end time.Time, step time.Duration) ([]DataPoint, error)
}

// PrometheusClient is the §25.4 plain Prometheus-HTTP-API client.
// It speaks the documented `/api/v1/query` and `/api/v1/query_range`
// endpoints so any Prometheus-compatible time-series backend works
// (Prometheus, Cortex, Thanos, Mimir, VictoriaMetrics — §25.4 "BYO
// model").
type PrometheusClient struct {
	baseURL string
	http    *http.Client
	timeout time.Duration
	metrics QueryMetrics
	now     func() time.Time
}

// QueryMetrics records the §25.4 Prometheus query latency. The
// production adapter observes lenny_prometheus_query_duration_seconds
// {kind}; tests pass a recording stub or the Noop.
//
// spec: §25.4 lines 1914-1916.
type QueryMetrics interface {
	// ObserveQuery records the wall-clock duration of one query. kind
	// is one of "instant", "range", or "alerts".
	ObserveQuery(kind string, seconds float64)
}

// NoopQueryMetrics discards query latencies.
type NoopQueryMetrics struct{}

// ObserveQuery implements QueryMetrics.
func (NoopQueryMetrics) ObserveQuery(string, float64) {}

// Query kind labels for lenny_prometheus_query_duration_seconds.
const (
	QueryKindInstant = "instant"
	QueryKindRange   = "range"
	QueryKindAlerts  = "alerts"
)

// PrometheusConfig configures a PrometheusClient.
type PrometheusConfig struct {
	// BaseURL is the Prometheus HTTP API base
	// (e.g., http://prometheus-server.lenny-system.svc.cluster.local).
	BaseURL string
	// HTTPClient overrides the underlying transport.
	HTTPClient *http.Client
	// QueryTimeout bounds each query. §25.4
	// ops.prometheus.queryTimeoutSeconds default is 15s.
	QueryTimeout time.Duration
	// Metrics records query latency. Nil uses NoopQueryMetrics.
	Metrics QueryMetrics
	// Now overrides the clock used to time queries. Nil uses time.Now.
	Now func() time.Time
}

// NewPrometheusClient validates cfg and returns a client.
func NewPrometheusClient(cfg PrometheusConfig) (*PrometheusClient, error) {
	if cfg.BaseURL == "" {
		return nil, errors.New("prometheus client: BaseURL is required")
	}
	if _, err := url.Parse(cfg.BaseURL); err != nil {
		return nil, fmt.Errorf("prometheus client: invalid BaseURL: %w", err)
	}
	timeout := cfg.QueryTimeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: timeout + time.Second}
	}
	m := cfg.Metrics
	if m == nil {
		m = NoopQueryMetrics{}
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &PrometheusClient{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		http:    hc,
		timeout: timeout,
		metrics: m,
		now:     now,
	}, nil
}

// observe records the wall-clock latency of a query of the given kind
// against lenny_prometheus_query_duration_seconds{kind}. It is called
// with a deferred closure capturing the start time.
func (c *PrometheusClient) observe(kind string, start time.Time) {
	c.metrics.ObserveQuery(kind, c.now().Sub(start).Seconds())
}

// promResponse is the Prometheus HTTP API common envelope.
type promResponse struct {
	Status    string          `json:"status"`
	Data      json.RawMessage `json:"data"`
	ErrorType string          `json:"errorType"`
	Error     string          `json:"error"`
}

// Query implements MetricSource.Query.
func (c *PrometheusClient) Query(ctx context.Context, q string) (float64, error) {
	defer c.observe(QueryKindInstant, c.now())
	params := url.Values{}
	params.Set("query", q)
	params.Set("timeout", strconv.FormatFloat(c.timeout.Seconds(), 'f', -1, 64)+"s")
	data, err := c.do(ctx, "/api/v1/query", params)
	if err != nil {
		return 0, err
	}
	var inst struct {
		ResultType string            `json:"resultType"`
		Result     []json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(data, &inst); err != nil {
		return 0, fmt.Errorf("prometheus: decode query result: %w", err)
	}
	switch inst.ResultType {
	case "scalar":
		// scalar: [timestamp, "value"]
		var pair [2]json.RawMessage
		if err := json.Unmarshal(data, &struct {
			ResultType string              `json:"resultType"`
			Result     *[2]json.RawMessage `json:"result"`
		}{Result: &pair}); err != nil {
			return 0, fmt.Errorf("prometheus: decode scalar: %w", err)
		}
		return parseSample(pair[1])
	case "vector":
		if len(inst.Result) == 0 {
			return 0, nil
		}
		var entry struct {
			Metric map[string]string  `json:"metric"`
			Value  [2]json.RawMessage `json:"value"`
		}
		if err := json.Unmarshal(inst.Result[0], &entry); err != nil {
			return 0, fmt.Errorf("prometheus: decode vector element: %w", err)
		}
		return parseSample(entry.Value[1])
	default:
		return 0, fmt.Errorf("prometheus: unsupported resultType %q", inst.ResultType)
	}
}

// QueryRange implements MetricSource.QueryRange.
func (c *PrometheusClient) QueryRange(ctx context.Context, q string, start, end time.Time, step time.Duration) ([]DataPoint, error) {
	defer c.observe(QueryKindRange, c.now())
	params := url.Values{}
	params.Set("query", q)
	params.Set("start", strconv.FormatFloat(float64(start.UnixNano())/1e9, 'f', -1, 64))
	params.Set("end", strconv.FormatFloat(float64(end.UnixNano())/1e9, 'f', -1, 64))
	params.Set("step", strconv.FormatFloat(step.Seconds(), 'f', -1, 64)+"s")
	params.Set("timeout", strconv.FormatFloat(c.timeout.Seconds(), 'f', -1, 64)+"s")
	data, err := c.do(ctx, "/api/v1/query_range", params)
	if err != nil {
		return nil, err
	}
	var matrix struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string    `json:"metric"`
			Values [][2]json.RawMessage `json:"values"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &matrix); err != nil {
		return nil, fmt.Errorf("prometheus: decode range result: %w", err)
	}
	if len(matrix.Result) == 0 {
		return nil, nil
	}
	// The recommendation reader collapses the first series; multi-
	// series queries return the first series so callers can shape
	// their PromQL with sum() / avg() to land on a single line.
	pts := make([]DataPoint, 0, len(matrix.Result[0].Values))
	for _, pair := range matrix.Result[0].Values {
		ts, err := parseSample(pair[0])
		if err != nil {
			return nil, fmt.Errorf("prometheus: decode timestamp: %w", err)
		}
		val, err := parseSample(pair[1])
		if err != nil {
			return nil, fmt.Errorf("prometheus: decode value: %w", err)
		}
		sec := int64(ts)
		nsec := int64((ts - float64(sec)) * 1e9)
		pts = append(pts, DataPoint{At: time.Unix(sec, nsec).UTC(), Value: val})
	}
	return pts, nil
}

// do issues an HTTP GET and parses the Prometheus response envelope.
func (c *PrometheusClient) do(ctx context.Context, path string, params url.Values) (json.RawMessage, error) {
	u := c.baseURL + path + "?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 500 {
		return nil, &PrometheusError{Status: resp.StatusCode, Body: string(body)}
	}
	var envelope promResponse
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("prometheus: decode envelope: %w", err)
	}
	if envelope.Status != "success" {
		return nil, fmt.Errorf("prometheus: %s: %s", envelope.ErrorType, envelope.Error)
	}
	return envelope.Data, nil
}

// parseSample parses a Prometheus sample value, which is JSON-encoded
// as either a number or a string carrying a float.
func parseSample(raw json.RawMessage) (float64, error) {
	if len(raw) == 0 {
		return 0, errors.New("empty sample")
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return 0, err
		}
		return strconv.ParseFloat(s, 64)
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		return 0, err
	}
	return f, nil
}

// PrometheusError is the §25.4 "Prometheus Unreachable" surface:
// HTTP 5xx or a connection error from the Prometheus API. Callers
// branch on this to swap to fan-out.
type PrometheusError struct {
	Status int
	Body   string
}

// Error implements error.
func (e *PrometheusError) Error() string {
	return fmt.Sprintf("prometheus: HTTP %d: %s", e.Status, e.Body)
}

// PrometheusWithFallback is the §25.4 primary aggregator. It tries
// Prometheus first; on any error matching the §25.4 "unreachable"
// definition it falls back to fanning out the supplied gateway
// /metrics scrape via the headless Service.
type PrometheusWithFallback struct {
	primary  MetricSource
	fallback *FanOutScraper
}

// NewPrometheusWithFallback returns a fallback-capable MetricSource.
// A nil fallback turns the source into a Prometheus-only wrapper.
func NewPrometheusWithFallback(primary MetricSource, fallback *FanOutScraper) *PrometheusWithFallback {
	return &PrometheusWithFallback{primary: primary, fallback: fallback}
}

// Query tries the Prometheus path; on unreachable-class errors falls
// back to the fan-out scraper. Range queries cannot be served by the
// scraper (it is point-in-time only) so QueryRange returns the
// primary error.
func (p *PrometheusWithFallback) Query(ctx context.Context, q string) (float64, error) {
	v, err := p.primary.Query(ctx, q)
	if err == nil {
		return v, nil
	}
	if !isUnreachable(err) || p.fallback == nil {
		return 0, err
	}
	return p.fallback.SumGauge(ctx, ExtractMetricName(q))
}

// QueryRange returns the primary's range query result. §25.4 keeps
// the fan-out fallback point-in-time only; the response's degradation
// envelope notes "lower-confidence — point-in-time values only".
func (p *PrometheusWithFallback) QueryRange(ctx context.Context, q string, start, end time.Time, step time.Duration) ([]DataPoint, error) {
	return p.primary.QueryRange(ctx, q, start, end, step)
}

// isUnreachable reports whether err matches the §25.4 "Prometheus
// Unreachable" definition: HTTP 5xx, query timeout, or connection
// error.
func isUnreachable(err error) bool {
	var pe *PrometheusError
	if errors.As(err, &pe) {
		return pe.Status >= 500
	}
	// Net/context errors are treated as unreachable.
	return errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, context.Canceled) ||
		strings.Contains(err.Error(), "connection refused") ||
		strings.Contains(err.Error(), "no such host")
}

// FanOutScraper is the §25.4 fallback: it scrapes the gateway's
// /metrics endpoint on every replica via the headless Service and
// sums (or maxes, depending on the metric type) the per-replica
// samples.
type FanOutScraper struct {
	client *gateway.Client
	path   string
}

// NewFanOutScraper returns a scraper that fetches `path` (typically
// `/metrics`) on every replica.
func NewFanOutScraper(client *gateway.Client, path string) *FanOutScraper {
	if path == "" {
		path = "/metrics"
	}
	return &FanOutScraper{client: client, path: path}
}

// SumGauge fans out a scrape and returns the sum of the named gauge
// across every replica that responded. Replicas that fail are
// skipped silently (the §25.4 partial-aggregation rule). When no
// replica responds the function returns 0 with no error so callers
// see a "no data" zero rather than the underlying timeout.
func (s *FanOutScraper) SumGauge(ctx context.Context, name string) (float64, error) {
	if s == nil || s.client == nil {
		return 0, ErrFanOutScrapeUnavailable
	}
	results, err := s.client.FanOutGetRaw(ctx, s.path)
	if err != nil {
		return 0, err
	}
	var total float64
	for _, r := range results {
		if r.Err != nil {
			continue
		}
		total += parsePromtextGauge(string(r.Body), name)
	}
	return total, nil
}

// ErrFanOutScrapeUnavailable is returned when SumGauge is called
// before the gateway client is wired.
var ErrFanOutScrapeUnavailable = errors.New("fan-out scraper: gateway client unavailable")

// ExtractMetricName returns the bare metric name from a PromQL
// expression of the form `metric_name`, `metric_name{...}`, or
// `sum(rate(metric_name[...]))`. The §25.4 fan-out path collapses
// to the underlying metric, ignoring the aggregation wrapping.
func ExtractMetricName(query string) string {
	q := query
	for {
		open := strings.LastIndexByte(q, '(')
		if open < 0 {
			break
		}
		close := strings.IndexByte(q[open:], ')')
		if close < 0 {
			break
		}
		q = strings.TrimSpace(q[open+1 : open+close])
	}
	// Drop label selectors and range vector windows so we land on the
	// bare identifier.
	if i := strings.IndexAny(q, "{["); i >= 0 {
		q = q[:i]
	}
	return strings.TrimSpace(q)
}

// parsePromtextGauge sums every sample of `name` in a Prometheus
// text-format payload. A non-numeric value is skipped — the caller
// already swallowed a network failure and an unparseable value is
// equally non-fatal during fan-out.
func parsePromtextGauge(text, name string) float64 {
	if name == "" {
		return 0
	}
	var total float64
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Match `name` or `name{labels}`.
		idx := strings.IndexAny(line, " {")
		if idx <= 0 || line[:idx] != name && !strings.HasPrefix(line, name+"{") {
			continue
		}
		// Find the trailing whitespace-separated value.
		valStart := strings.LastIndexByte(line, ' ')
		if valStart < 0 {
			continue
		}
		v, err := strconv.ParseFloat(line[valStart+1:], 64)
		if err != nil {
			continue
		}
		total += v
	}
	return total
}

// Reader is the §25.4 pkg/gateway/recommendations.MetricReader
// satisfied by a MetricSource. The lenny-ops recommendation
// evaluator reads through this so the same rule set runs against
// Prometheus and the in-process WindowStore on the gateway.
type Reader struct {
	source     MetricSource
	cache      sync.Map // key string → *cacheEntry
	cacheTTL   time.Duration
	queryShape QueryShape
}

// QueryShape supplies the PromQL expressions the Reader plugs the
// (metric, labels, window) tuple into. The default shape uses
// Prometheus' standard `sum`, `histogram_quantile`, and `rate`
// aggregations. Operators can override for non-standard metric
// pipelines.
type QueryShape func(kind ReadKind, name string, labels map[string]string, window time.Duration, quantile float64) string

// ReadKind enumerates the recommendation MetricReader entry points so
// QueryShape can pick the right PromQL aggregation.
type ReadKind int

const (
	// KindGauge resolves the current value of a gauge.
	KindGauge ReadKind = iota
	// KindCounter resolves the current value of a counter (cumulative).
	KindCounter
	// KindHistogramQuantile resolves a quantile of a histogram.
	KindHistogramQuantile
	// KindWindowedRate resolves the per-second rate of a counter over
	// the trailing window.
	KindWindowedRate
)

// NewReader returns a Reader backed by source. A nil QueryShape uses
// DefaultQueryShape; a non-positive cacheTTL turns caching off.
func NewReader(source MetricSource, shape QueryShape, cacheTTL time.Duration) *Reader {
	if shape == nil {
		shape = DefaultQueryShape
	}
	return &Reader{source: source, queryShape: shape, cacheTTL: cacheTTL}
}

// Compile-time guard that Reader satisfies the §25.3 MetricReader
// interface used by the gateway-side recommendation rules.
var _ recommendations.MetricReader = (*Reader)(nil)

// GaugeValue queries the most recent value of the named gauge.
func (r *Reader) GaugeValue(name string, labels map[string]string) (float64, bool) {
	q := r.queryShape(KindGauge, name, labels, 0, 0)
	return r.query(q)
}

// CounterValue queries the cumulative counter total. Both gauges and
// counters resolve to a single scalar via PromQL.
func (r *Reader) CounterValue(name string, labels map[string]string) (float64, bool) {
	q := r.queryShape(KindCounter, name, labels, 0, 0)
	return r.query(q)
}

// HistogramQuantile queries the named histogram's quantile.
func (r *Reader) HistogramQuantile(name string, labels map[string]string, quantile float64) (float64, bool) {
	if quantile < 0 || quantile > 1 {
		return 0, false
	}
	q := r.queryShape(KindHistogramQuantile, name, labels, 0, quantile)
	return r.query(q)
}

// WindowedRate queries the per-second rate of the named counter over
// the trailing window. Mirrors the gateway-side WindowStore semantics.
func (r *Reader) WindowedRate(name string, labels map[string]string, window time.Duration) (float64, bool) {
	if window <= 0 {
		return 0, false
	}
	q := r.queryShape(KindWindowedRate, name, labels, window, 0)
	return r.query(q)
}

// query runs the supplied PromQL, threading the configured cache TTL
// to amortise repeated calls in a single recommendation pass.
func (r *Reader) query(q string) (float64, bool) {
	if r.cacheTTL > 0 {
		if hit, ok := r.cacheLookup(q); ok {
			return hit, true
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	v, err := r.source.Query(ctx, q)
	if err != nil {
		return 0, false
	}
	if r.cacheTTL > 0 {
		r.cacheStore(q, v)
	}
	return v, true
}

type cacheEntry struct {
	value float64
	at    time.Time
}

func (r *Reader) cacheLookup(q string) (float64, bool) {
	v, ok := r.cache.Load(q)
	if !ok {
		return 0, false
	}
	entry := v.(*cacheEntry)
	if time.Since(entry.at) > r.cacheTTL {
		r.cache.Delete(q)
		return 0, false
	}
	return entry.value, true
}

func (r *Reader) cacheStore(q string, v float64) {
	r.cache.Store(q, &cacheEntry{value: v, at: time.Now()})
}

// DefaultQueryShape is the §25.4 reference PromQL builder. The
// `sum()` wrapper aggregates across labels not in `labels` so the
// recommendation rules see one number per (metric, label-set) tuple
// regardless of how many gateway replicas Prometheus scraped.
func DefaultQueryShape(kind ReadKind, name string, labels map[string]string, window time.Duration, quantile float64) string {
	selector := renderSelector(name, labels)
	switch kind {
	case KindGauge, KindCounter:
		return "sum(" + selector + ")"
	case KindWindowedRate:
		return "sum(rate(" + selector + "[" + formatDuration(window) + "]))"
	case KindHistogramQuantile:
		// `_bucket` is the Prometheus convention; the caller passes
		// the metric without it and DefaultQueryShape appends.
		bucketSelector := renderSelector(name+"_bucket", labels)
		// histogram_quantile expects `by (le)` aggregation; we sum
		// across replicas (the standard pattern).
		return "histogram_quantile(" + strconv.FormatFloat(quantile, 'f', -1, 64) +
			", sum by (le) (rate(" + bucketSelector + "[5m])))"
	}
	return selector
}

// renderSelector formats `metric{l1="v1",l2="v2"}` with labels
// sorted alphabetically so two callers with the same logical query
// hit the same cache slot.
func renderSelector(name string, labels map[string]string) string {
	if len(labels) == 0 {
		return name
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sortStrings(keys)
	var b strings.Builder
	b.WriteString(name)
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(k)
		b.WriteString(`="`)
		b.WriteString(strings.ReplaceAll(labels[k], `"`, `\"`))
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}

// formatDuration renders a duration in Prometheus' compact form
// (`30s`, `5m`, `1h`).
func formatDuration(d time.Duration) string {
	switch {
	case d >= time.Hour && d%time.Hour == 0:
		return strconv.FormatInt(int64(d/time.Hour), 10) + "h"
	case d >= time.Minute && d%time.Minute == 0:
		return strconv.FormatInt(int64(d/time.Minute), 10) + "m"
	default:
		return strconv.FormatInt(int64(d/time.Second), 10) + "s"
	}
}

// sortStrings sorts s in place; insertion sort is fast enough for the
// small label sets the recommendation rules produce.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
