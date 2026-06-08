// SPDX-License-Identifier: MIT

package evalstore

import "context"

// ScorerAggregate is the §10.7 per-scorer aggregate read from the
// lenny_eval_aggregates materialized view: the same count, mean, p50,
// and p95 the on-read aggregation computes, with optional per-dimension
// sub-aggregates under the same shape. Count/Mean/P50/P95 are zero when
// the scorer carried only per-dimension scores (no top-level score),
// matching the base-table aggregation.
type ScorerAggregate struct {
	Count      int
	Mean       float64
	P50        float64
	P95        float64
	Dimensions map[string]ScorerAggregate
}

// VariantAggregate is the §10.7 per-variant aggregate read from the
// materialized view. SampleCount is the distinct-session count over
// every eval row for the variant (including rows with no score), the
// same value the base-table aggregation reports.
type VariantAggregate struct {
	VariantID   string
	SampleCount int
	Scorers     map[string]ScorerAggregate
}

// AggregateReader is the optional materialized-view read path a Store
// may implement. When the gateway enables the §10.7 matview
// (evalAggregationRefreshSeconds > 0), the results handler serves the
// unfiltered, no-breakdown aggregation through AggregatesByExperiment
// instead of recomputing from the base table, and the refresh loop
// drives the periodic REFRESH through RefreshAggregates. A Store that
// does not back the matview (e.g. Memory) omits this interface and the
// handler always aggregates on read.
//
// spec: §10.7 line 954 (matview used only for unfiltered, no-breakdown
// requests), §10.7 line 1088 (refresh scheduling and routing).
type AggregateReader interface {
	// AggregatesByExperiment returns the per-variant aggregates for the
	// experiment, keyed by variant id. A variant with no eval rows is
	// absent from the map; the caller fills the gap with an empty
	// VariantResults so every named variant still appears.
	AggregatesByExperiment(ctx context.Context, tenantID, experimentID string) (map[string]VariantAggregate, error)
	// RefreshAggregates runs one REFRESH MATERIALIZED VIEW CONCURRENTLY
	// of lenny_eval_aggregates across every tenant.
	RefreshAggregates(ctx context.Context) error
}
