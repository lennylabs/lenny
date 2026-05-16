// SPDX-License-Identifier: MIT

package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"

	"github.com/lennylabs/lenny/pkg/experiment"
	"github.com/lennylabs/lenny/pkg/gateway/evalstore"
	"github.com/lennylabs/lenny/pkg/gateway/experimentstore"
)

// WithEvalResults wires the §10.7 eval-result store onto the Router.
// It backs the GET /v1/admin/experiments/{name}/results aggregation.
func (r *Router) WithEvalResults(s evalstore.Store) *Router {
	r.evals = s
	return r
}

// ScorerStats is the §10.7 per-scorer aggregate for one variant. When
// the scorer's eval results carried a multi-dimensional `scores` map,
// Dimensions holds the per-dimension aggregate under the same shape.
type ScorerStats struct {
	Count      int                    `json:"count"`
	Mean       float64                `json:"mean"`
	P50        float64                `json:"p50"`
	P95        float64                `json:"p95"`
	Dimensions map[string]ScorerStats `json:"dimensions,omitempty"`
}

// VariantResults is the §10.7 per-variant results block. The default
// response carries the flat Scorers map; a breakdown_by request
// replaces it with the Breakdowns array of per-bucket sub-aggregates.
type VariantResults struct {
	VariantID   string                 `json:"variantId"`
	SampleCount int                    `json:"sampleCount"`
	Scorers     map[string]ScorerStats `json:"scorers,omitempty"`
	BreakdownBy string                 `json:"breakdownBy,omitempty"`
	Breakdowns  []BreakdownBucket      `json:"breakdowns,omitempty"`
}

// BreakdownBucket is one §10.7 breakdown_by sub-aggregate: the scorers
// computed over the eval rows that share a single value of the
// breakdown field.
type BreakdownBucket struct {
	BucketValue any                    `json:"bucketValue"`
	SampleCount int                    `json:"sampleCount"`
	Scorers     map[string]ScorerStats `json:"scorers"`
}

// ExperimentResults is the §15.1 GET /v1/admin/experiments/{name}/results
// response: eval scores aggregated by variant.
type ExperimentResults struct {
	ExperimentID string           `json:"experimentId"`
	Status       string           `json:"status"`
	Variants     []VariantResults `json:"variants"`
}

// handleExperimentResults implements GET
// /v1/admin/experiments/{name}/results — the §10.7 results
// aggregation. For each variant (the named variants plus the implicit
// "control" group) it reports the distinct-session sample count and,
// per scorer, the eval-score count, mean, the p50 / p95 percentiles,
// and — when the scorer's results carried a multi-dimensional `scores`
// map — the same aggregate per dimension. The §10.7 `delegation_depth`,
// `inherited`, and `exclude_post_conclusion` query filters narrow the
// result set; the `breakdown_by` query parameter replaces each
// variant's flat scorers block with per-bucket sub-aggregates.
func (r *Router) handleExperimentResults(w http.ResponseWriter, req *http.Request) {
	tenant, _, err := r.authorizedTenantForUser(req, req.URL.Query().Get("tenantId"))
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	name := req.PathValue("name")
	exp, err := r.experiments.Get(req.Context(), tenant, name)
	if err != nil {
		if errors.Is(err, experimentstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "experiment not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	rows, err := r.evals.ListByExperiment(req.Context(), tenant, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	rows, ferr := filterEvalRows(rows, req.URL.Query())
	if ferr != "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", ferr, nil)
		return
	}
	breakdownBy := req.URL.Query().Get("breakdown_by")
	if breakdownBy != "" && !validBreakdownField(breakdownBy) {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"breakdown_by must be delegation_depth, inherited, or submitted_after_conclusion", nil)
		return
	}

	byVariant := map[string][]evalstore.EvalResult{}
	for _, row := range rows {
		byVariant[row.VariantID] = append(byVariant[row.VariantID], row)
	}
	// Report every named variant plus the implicit control group, in a
	// stable order, so a variant with no eval data still appears.
	variantIDs := make([]string, 0, len(exp.Variants)+1)
	for _, v := range exp.Variants {
		variantIDs = append(variantIDs, v.ID)
	}
	variantIDs = append(variantIDs, experiment.ControlVariantID)

	out := ExperimentResults{ExperimentID: exp.ID, Status: string(exp.Status)}
	for _, vid := range variantIDs {
		if breakdownBy != "" {
			out.Variants = append(out.Variants, aggregateBreakdown(vid, byVariant[vid], breakdownBy))
		} else {
			out.Variants = append(out.Variants, aggregateVariant(vid, byVariant[vid]))
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// filterEvalRows applies the §10.7 results-API query filters. A
// malformed filter value yields a non-empty error message; otherwise
// it returns the rows that pass every supplied filter.
func filterEvalRows(rows []evalstore.EvalResult, q url.Values) ([]evalstore.EvalResult, string) {
	var depth *uint32
	if v := q.Get("delegation_depth"); v != "" {
		n, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			return nil, "delegation_depth must be a non-negative integer"
		}
		d := uint32(n)
		depth = &d
	}
	var inherited *bool
	if v := q.Get("inherited"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, "inherited must be true or false"
		}
		inherited = &b
	}
	excludePost := false
	if v := q.Get("exclude_post_conclusion"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, "exclude_post_conclusion must be true or false"
		}
		excludePost = b
	}
	if depth == nil && inherited == nil && !excludePost {
		return rows, ""
	}
	out := make([]evalstore.EvalResult, 0, len(rows))
	for _, row := range rows {
		if depth != nil && row.DelegationDepth != *depth {
			continue
		}
		if inherited != nil && row.Inherited != *inherited {
			continue
		}
		if excludePost && row.SubmittedAfterConclusion {
			continue
		}
		out = append(out, row)
	}
	return out, ""
}

// aggregateVariant computes the §10.7 per-variant results from the
// variant's eval rows.
func aggregateVariant(variantID string, rows []evalstore.EvalResult) VariantResults {
	sessions := map[string]bool{}
	byScorer := map[string][]float64{}
	byScorerDim := map[string]map[string][]float64{}
	for _, row := range rows {
		sessions[row.SessionID] = true
		if row.Score != nil {
			byScorer[row.Scorer] = append(byScorer[row.Scorer], *row.Score)
		}
		for dim, val := range row.Scores {
			if byScorerDim[row.Scorer] == nil {
				byScorerDim[row.Scorer] = map[string][]float64{}
			}
			byScorerDim[row.Scorer][dim] = append(byScorerDim[row.Scorer][dim], val)
		}
	}
	// A scorer is reported when it carried an aggregate score, a
	// per-dimension score, or both.
	scorerNames := map[string]bool{}
	for s := range byScorer {
		scorerNames[s] = true
	}
	for s := range byScorerDim {
		scorerNames[s] = true
	}
	scorers := map[string]ScorerStats{}
	for scorer := range scorerNames {
		st := scorerStats(byScorer[scorer])
		if dims := byScorerDim[scorer]; len(dims) > 0 {
			st.Dimensions = map[string]ScorerStats{}
			for dim, vals := range dims {
				st.Dimensions[dim] = scorerStats(vals)
			}
		}
		scorers[scorer] = st
	}
	return VariantResults{VariantID: variantID, SampleCount: len(sessions), Scorers: scorers}
}

// validBreakdownField reports whether field is a §10.7 breakdown_by
// dimension.
func validBreakdownField(field string) bool {
	return field == "delegation_depth" || field == "inherited" ||
		field == "submitted_after_conclusion"
}

// breakdownValue extracts the breakdown-field value from a row.
func breakdownValue(row evalstore.EvalResult, field string) any {
	switch field {
	case "delegation_depth":
		return row.DelegationDepth
	case "inherited":
		return row.Inherited
	default: // submitted_after_conclusion
		return row.SubmittedAfterConclusion
	}
}

// breakdownLess orders §10.7 bucket values: numeric ascending for
// delegation_depth, false before true for the boolean fields.
func breakdownLess(a, b any) bool {
	switch av := a.(type) {
	case uint32:
		bv, _ := b.(uint32)
		return av < bv
	case bool:
		bv, _ := b.(bool)
		return !av && bv
	}
	return false
}

// aggregateBreakdown computes the §10.7 breakdown_by per-variant
// results: the variant's rows are bucketed by the breakdown field and
// each bucket is aggregated independently. Buckets with no rows are
// absent; breakdowns is always present (possibly empty). The variant
// sample count is the sum across buckets.
func aggregateBreakdown(variantID string, rows []evalstore.EvalResult, field string) VariantResults {
	buckets := map[any][]evalstore.EvalResult{}
	for _, row := range rows {
		k := breakdownValue(row, field)
		buckets[k] = append(buckets[k], row)
	}
	keys := make([]any, 0, len(buckets))
	for k := range buckets {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return breakdownLess(keys[i], keys[j]) })

	vr := VariantResults{VariantID: variantID, BreakdownBy: field, Breakdowns: []BreakdownBucket{}}
	for _, k := range keys {
		sub := aggregateVariant(variantID, buckets[k])
		vr.Breakdowns = append(vr.Breakdowns, BreakdownBucket{
			BucketValue: k, SampleCount: sub.SampleCount, Scorers: sub.Scorers,
		})
		vr.SampleCount += sub.SampleCount
	}
	return vr
}

// scorerStats computes the count, mean, and p50 / p95 percentiles of a
// set of eval scores. An empty set yields the zero ScorerStats.
func scorerStats(scores []float64) ScorerStats {
	if len(scores) == 0 {
		return ScorerStats{}
	}
	sorted := append([]float64(nil), scores...)
	sort.Float64s(sorted)
	sum := 0.0
	for _, s := range sorted {
		sum += s
	}
	return ScorerStats{
		Count: len(sorted),
		Mean:  sum / float64(len(sorted)),
		P50:   percentile(sorted, 50),
		P95:   percentile(sorted, 95),
	}
}

// percentile returns the nearest-rank pK of an ascending-sorted slice.
func percentile(sorted []float64, k int) float64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := (k*len(sorted) + 99) / 100 // ceil(k/100 * n)
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}
