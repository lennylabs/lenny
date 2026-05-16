// SPDX-License-Identifier: MIT

package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"

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

// ScorerStats is the §10.7 per-scorer aggregate for one variant.
type ScorerStats struct {
	Count int     `json:"count"`
	Mean  float64 `json:"mean"`
	P50   float64 `json:"p50"`
	P95   float64 `json:"p95"`
}

// VariantResults is the §10.7 per-variant results block.
type VariantResults struct {
	VariantID   string                 `json:"variantId"`
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
// per scorer, the eval-score count, mean, and the p50 / p95
// percentiles.
//
// The §10.7 per-dimension `scores` breakdown and the
// delegation_depth / inherited / breakdown_by query filters are not
// yet supported.
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
		out.Variants = append(out.Variants, aggregateVariant(vid, byVariant[vid]))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// aggregateVariant computes the §10.7 per-variant results from the
// variant's eval rows.
func aggregateVariant(variantID string, rows []evalstore.EvalResult) VariantResults {
	sessions := map[string]bool{}
	byScorer := map[string][]float64{}
	for _, row := range rows {
		sessions[row.SessionID] = true
		if row.Score != nil {
			byScorer[row.Scorer] = append(byScorer[row.Scorer], *row.Score)
		}
	}
	scorers := map[string]ScorerStats{}
	for scorer, scores := range byScorer {
		scorers[scorer] = scorerStats(scores)
	}
	return VariantResults{VariantID: variantID, SampleCount: len(sessions), Scorers: scorers}
}

// scorerStats computes the count, mean, and p50 / p95 percentiles of a
// scorer's eval scores.
func scorerStats(scores []float64) ScorerStats {
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
