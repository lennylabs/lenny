// SPDX-License-Identifier: MIT

package recommendations

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// DefaultSampleInterval is the cadence at which the Sampler reads the
// in-process registry into the recommendation ring buffers. The §25.3
// rules evaluate over sliding windows of hours, so a 30-second sample
// keeps the windows fresh without measurable overhead. The interval also
// fixes the WindowedRate granularity: a counter-rate rule needs at least
// two samples in its window, so the first recommendation for a
// rate-based rule appears one interval after the gateway starts.
const DefaultSampleInterval = 30 * time.Second

// aggregation selects how the Sampler collapses a metric family's
// per-label-set series into the single aggregate value the §25.3
// recommendation evaluators read (they query the store with nil labels,
// the platform-wide aggregate). spec: §25.3 lines 588-597.
type aggregation int

const (
	// aggSum totals the series across every label set. Used for counters
	// (total exhaustion events, total OOM kills, total claims) where the
	// platform-wide rate is the sum of the per-pool rates.
	aggSum aggregation = iota
	// aggMax takes the worst series across every label set. Used for the
	// ratio gauges (utilization, CPU, storage, rejection) where the
	// recommendation should trigger when any single pool/replica crosses
	// the threshold, matching the `max by ()` / "any pool" framing of the
	// §16.5 alert expressions.
	aggMax
)

// seriesSpec names one in-process metric the §25.3 recommendation rules
// read and how to aggregate it across labels.
type seriesSpec struct {
	name string
	agg  aggregation
}

// recommendationSeries is the set of in-process metric families the
// §25.3 capacity-recommendation evaluators consult. The Sampler records
// every series the gateway's registry currently exposes; series that
// originate in another process (the warm-pool controller, the kubelet
// OOM signal) are absent from the gateway registry and are served by
// `lenny-ops` through its Prometheus-backed reader instead — the §25.3
// per-replica-scope note. Listing every evaluator metric here means a
// metric added to the gateway registry later is sampled with no further
// wiring. spec: §25.3 lines 580-597.
func recommendationSeries() []seriesSpec {
	return []seriesSpec{
		{"lenny_warmpool_claims_total", aggSum},
		{"lenny_warmpool_exhausted_total", aggSum},
		{"lenny_pod_oom_killed_total", aggSum},
		{"lenny_credential_pool_utilization", aggMax},
		{"lenny_credential_pool_assignable_count", aggSum},
		{"lenny_gateway_cpu_utilization_ratio", aggMax},
		{"lenny_storage_utilization_ratio", aggMax},
		{"lenny_quota_rejection_ratio", aggMax},
	}
}

// Sampler periodically reads the §25.3 recommendation source metrics out
// of the gateway's in-process Prometheus registry and records them into
// the WindowStore the rules engine evaluates against. It is the
// production caller that populates the otherwise-empty ring buffers, so
// `/v1/admin/recommendations` serves real per-replica data rather than a
// permanently-empty result. spec: §25.3 lines 588-597 — "The gateway's
// metric registry feeds the ring buffers, and the rules engine reads
// from those buffers via MetricReader."
type Sampler struct {
	gatherer prometheus.Gatherer
	store    *WindowStore
	specs    []seriesSpec
	interval time.Duration
}

// NewSampler returns a Sampler that reads gatherer into store on the
// default interval. A nil gatherer or store yields a no-op Sampler so a
// gateway started without recommendations does not panic.
func NewSampler(gatherer prometheus.Gatherer, store *WindowStore) *Sampler {
	return &Sampler{
		gatherer: gatherer,
		store:    store,
		specs:    recommendationSeries(),
		interval: DefaultSampleInterval,
	}
}

// WithInterval overrides the sample cadence (tests use a short window).
// A non-positive interval keeps the default. Returns the receiver.
func (s *Sampler) WithInterval(d time.Duration) *Sampler {
	if d > 0 {
		s.interval = d
	}
	return s
}

// Sample performs one read of the registry, recording every configured
// series the registry currently exposes into the store. A series that is
// not registered (originates in another process, or has never been
// emitted) is skipped, leaving its window empty — the §25.3
// insufficient-data path. It returns the gather error, if any.
func (s *Sampler) Sample() error {
	if s.gatherer == nil || s.store == nil {
		return nil
	}
	mfs, err := s.gatherer.Gather()
	if err != nil {
		return err
	}
	byName := make(map[string]*dto.MetricFamily, len(mfs))
	for _, mf := range mfs {
		byName[mf.GetName()] = mf
	}
	for _, spec := range s.specs {
		mf, ok := byName[spec.name]
		if !ok {
			continue
		}
		if v, ok := aggregateFamily(mf, spec.agg); ok {
			s.store.Record(spec.name, nil, v)
		}
	}
	return nil
}

// Run samples the registry on the configured interval until ctx is done.
// It records an initial sample immediately so a recommendation rule that
// reads a gauge has data before the first tick.
func (s *Sampler) Run(ctx context.Context) {
	_ = s.Sample()
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = s.Sample()
		}
	}
}

// aggregateFamily collapses a metric family's series into one value via
// agg. It reads the gauge or counter value of each series (whichever the
// family carries) and returns ok=false when the family has no readable
// series.
func aggregateFamily(mf *dto.MetricFamily, agg aggregation) (float64, bool) {
	var acc float64
	seen := false
	for _, m := range mf.GetMetric() {
		v, ok := metricValue(m)
		if !ok {
			continue
		}
		if !seen {
			acc = v
			seen = true
			continue
		}
		switch agg {
		case aggSum:
			acc += v
		case aggMax:
			if v > acc {
				acc = v
			}
		}
	}
	return acc, seen
}

// metricValue reads the scalar value of a single series. Counters and
// gauges carry a single value; other metric kinds (histograms, summaries)
// are not sampled here because the WindowStore models raw scalar samples.
func metricValue(m *dto.Metric) (float64, bool) {
	switch {
	case m.GetCounter() != nil:
		return m.GetCounter().GetValue(), true
	case m.GetGauge() != nil:
		return m.GetGauge().GetValue(), true
	default:
		return 0, false
	}
}
