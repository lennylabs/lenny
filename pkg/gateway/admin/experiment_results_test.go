// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/experiment"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/evalstore"
	"github.com/lennylabs/lenny/pkg/gateway/experimentstore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// spec: §10.7 / §15.1 experiment results aggregation.

func newResultsAdmin(t *testing.T) (*admin.Router, experimentstore.Store, evalstore.Store) {
	t.Helper()
	exps := experimentstore.NewMemory()
	evals := evalstore.NewMemory(0, nil)
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC) },
	}).WithExperiments(exps).WithEvalResults(evals)
	return router, exps, evals
}

func scorePtr(v float64) *float64 { return &v }

func seedExperiment(t *testing.T, exps experimentstore.Store, id string) {
	t.Helper()
	if err := exps.Create(context.Background(), experimentstore.Experiment{
		ID: id, TenantID: "acme", Status: experiment.StatusActive, BaseRuntime: "claude-worker",
		Variants:      []experimentstore.Variant{{ID: "treatment", Weight: 0.1}},
		TargetingMode: experiment.TargetingPercentage,
		Sticky:        experiment.StickyUser,
		Propagation:   experiment.PropagationInherit,
	}); err != nil {
		t.Fatalf("seed experiment %q: %v", id, err)
	}
}

func seedEval(t *testing.T, evals evalstore.Store, sessionID, experimentID, variantID, scorer string, score float64) {
	t.Helper()
	if _, err := evals.Put(context.Background(), evalstore.EvalResult{
		TenantID: "acme", SessionID: sessionID, ExperimentID: experimentID,
		VariantID: variantID, Scorer: scorer, Score: scorePtr(score),
	}); err != nil {
		t.Fatalf("seed eval: %v", err)
	}
}

func TestExperimentResultsAggregatesByVariant(t *testing.T) {
	router, exps, evals := newResultsAdmin(t)
	seedExperiment(t, exps, "exp_1")
	// treatment: two sessions scored 0.8 and 0.9; control: one at 0.5.
	seedEval(t, evals, "s1", "exp_1", "treatment", "llm-judge", 0.8)
	seedEval(t, evals, "s2", "exp_1", "treatment", "llm-judge", 0.9)
	seedEval(t, evals, "s3", "exp_1", "control", "llm-judge", 0.5)

	rr := doAdminReq(t, router.Handler(), http.MethodGet,
		"/v1/admin/experiments/exp_1/results?tenantId=acme", nil, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("results: status %d, body %s", rr.Code, rr.Body.String())
	}
	var res admin.ExperimentResults
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode results: %v", err)
	}
	byVariant := map[string]admin.VariantResults{}
	for _, v := range res.Variants {
		byVariant[v.VariantID] = v
	}
	treatment, ok := byVariant["treatment"]
	if !ok {
		t.Fatalf("results missing the treatment variant: %+v", res.Variants)
	}
	if treatment.SampleCount != 2 {
		t.Errorf("treatment sampleCount = %d, want 2", treatment.SampleCount)
	}
	st := treatment.Scorers["llm-judge"]
	if st.Count != 2 || st.Mean < 0.849 || st.Mean > 0.851 {
		t.Errorf("treatment llm-judge stats = %+v, want count 2 mean ~0.85", st)
	}
	control, ok := byVariant["control"]
	if !ok || control.Scorers["llm-judge"].Count != 1 {
		t.Errorf("control variant aggregate missing or wrong: %+v", control)
	}
}

func TestExperimentResultsPercentiles(t *testing.T) {
	router, exps, evals := newResultsAdmin(t)
	seedExperiment(t, exps, "exp_p")
	// Ten scores 0.1 .. 1.0 against the treatment variant.
	for i := 1; i <= 10; i++ {
		seedEval(t, evals, "s", "exp_p", "treatment", "judge", float64(i)/10.0)
	}
	rr := doAdminReq(t, router.Handler(), http.MethodGet,
		"/v1/admin/experiments/exp_p/results?tenantId=acme", nil, withAdminPrincipal)
	var res admin.ExperimentResults
	_ = json.Unmarshal(rr.Body.Bytes(), &res)
	var st admin.ScorerStats
	for _, v := range res.Variants {
		if v.VariantID == "treatment" {
			st = v.Scorers["judge"]
		}
	}
	// Nearest-rank: p50 = sorted[ceil(0.5*10)-1] = sorted[4] = 0.5;
	// p95 = sorted[ceil(0.95*10)-1] = sorted[9] = 1.0.
	if st.P50 < 0.49 || st.P50 > 0.51 {
		t.Errorf("p50 = %g, want ~0.5", st.P50)
	}
	if st.P95 < 0.99 || st.P95 > 1.01 {
		t.Errorf("p95 = %g, want ~1.0", st.P95)
	}
}

func TestExperimentResultsEmptyExperiment(t *testing.T) {
	router, exps, _ := newResultsAdmin(t)
	seedExperiment(t, exps, "exp_empty")
	rr := doAdminReq(t, router.Handler(), http.MethodGet,
		"/v1/admin/experiments/exp_empty/results?tenantId=acme", nil, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("results: status %d", rr.Code)
	}
	var res admin.ExperimentResults
	_ = json.Unmarshal(rr.Body.Bytes(), &res)
	// The named variant and the control group are reported even with no data.
	if len(res.Variants) != 2 {
		t.Errorf("variants = %d, want 2 (treatment + control)", len(res.Variants))
	}
	for _, v := range res.Variants {
		if v.SampleCount != 0 {
			t.Errorf("variant %s sampleCount = %d, want 0", v.VariantID, v.SampleCount)
		}
	}
}

func TestExperimentResultsDimensionBreakdown(t *testing.T) {
	router, exps, evals := newResultsAdmin(t)
	seedExperiment(t, exps, "exp_d")
	for _, sc := range []map[string]float64{
		{"coherence": 0.8, "safety": 1.0},
		{"coherence": 0.9, "safety": 1.0},
	} {
		if _, err := evals.Put(context.Background(), evalstore.EvalResult{
			TenantID: "acme", SessionID: "s", ExperimentID: "exp_d",
			VariantID: "treatment", Scorer: "judge", Score: scorePtr(0.85), Scores: sc,
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	rr := doAdminReq(t, router.Handler(), http.MethodGet,
		"/v1/admin/experiments/exp_d/results?tenantId=acme", nil, withAdminPrincipal)
	var res admin.ExperimentResults
	_ = json.Unmarshal(rr.Body.Bytes(), &res)
	var judge admin.ScorerStats
	for _, v := range res.Variants {
		if v.VariantID == "treatment" {
			judge = v.Scorers["judge"]
		}
	}
	if judge.Dimensions == nil {
		t.Fatalf("scorer aggregate missing the per-dimension breakdown: %+v", judge)
	}
	if coh := judge.Dimensions["coherence"]; coh.Count != 2 || coh.Mean < 0.84 || coh.Mean > 0.86 {
		t.Errorf("coherence dimension = %+v, want count 2 mean ~0.85", coh)
	}
	if safety := judge.Dimensions["safety"]; safety.Count != 2 || safety.Mean != 1.0 {
		t.Errorf("safety dimension = %+v, want count 2 mean 1.0", safety)
	}
}

func TestExperimentResultsDelegationDepthFilter(t *testing.T) {
	router, exps, evals := newResultsAdmin(t)
	seedExperiment(t, exps, "exp_f")
	put := func(depth uint32) {
		if _, err := evals.Put(context.Background(), evalstore.EvalResult{
			TenantID: "acme", SessionID: "s", ExperimentID: "exp_f",
			VariantID: "treatment", Scorer: "judge", Score: scorePtr(0.5),
			DelegationDepth: depth,
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	put(0)
	put(0)
	put(1)

	judgeCount := func(query string) int {
		rr := doAdminReq(t, router.Handler(), http.MethodGet,
			"/v1/admin/experiments/exp_f/results?tenantId=acme&"+query, nil, withAdminPrincipal)
		if rr.Code != http.StatusOK {
			t.Fatalf("results (%s): status %d", query, rr.Code)
		}
		var res admin.ExperimentResults
		_ = json.Unmarshal(rr.Body.Bytes(), &res)
		for _, v := range res.Variants {
			if v.VariantID == "treatment" {
				return v.Scorers["judge"].Count
			}
		}
		return -1
	}
	if got := judgeCount("delegation_depth=0"); got != 2 {
		t.Errorf("delegation_depth=0 → judge count %d, want 2", got)
	}
	if got := judgeCount("delegation_depth=1"); got != 1 {
		t.Errorf("delegation_depth=1 → judge count %d, want 1", got)
	}
}

func TestExperimentResultsBreakdownByDelegationDepth(t *testing.T) {
	router, exps, evals := newResultsAdmin(t)
	seedExperiment(t, exps, "exp_bd")
	put := func(depth uint32) {
		if _, err := evals.Put(context.Background(), evalstore.EvalResult{
			TenantID: "acme", SessionID: "s", ExperimentID: "exp_bd",
			VariantID: "treatment", Scorer: "judge", Score: scorePtr(0.7),
			DelegationDepth: depth,
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	put(0)
	put(0)
	put(1)

	rr := doAdminReq(t, router.Handler(), http.MethodGet,
		"/v1/admin/experiments/exp_bd/results?tenantId=acme&breakdown_by=delegation_depth",
		nil, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("results: status %d, body %s", rr.Code, rr.Body.String())
	}
	var res admin.ExperimentResults
	_ = json.Unmarshal(rr.Body.Bytes(), &res)
	var treatment admin.VariantResults
	for _, v := range res.Variants {
		if v.VariantID == "treatment" {
			treatment = v
		}
	}
	if treatment.BreakdownBy != "delegation_depth" {
		t.Errorf("breakdownBy = %q, want delegation_depth", treatment.BreakdownBy)
	}
	if treatment.Scorers != nil {
		t.Error("a breakdown_by response must not carry the flat scorers block")
	}
	if len(treatment.Breakdowns) != 2 {
		t.Fatalf("breakdowns = %d, want 2 (depths 0 and 1)", len(treatment.Breakdowns))
	}
	if treatment.Breakdowns[0].BucketValue != float64(0) || treatment.Breakdowns[1].BucketValue != float64(1) {
		t.Errorf("bucket order = [%v %v], want ascending [0 1]",
			treatment.Breakdowns[0].BucketValue, treatment.Breakdowns[1].BucketValue)
	}
	if treatment.Breakdowns[0].Scorers["judge"].Count != 2 {
		t.Errorf("depth-0 bucket judge count = %d, want 2", treatment.Breakdowns[0].Scorers["judge"].Count)
	}
	if treatment.SampleCount != treatment.Breakdowns[0].SampleCount+treatment.Breakdowns[1].SampleCount {
		t.Error("variant sampleCount must equal the sum across breakdown buckets")
	}
}

func TestExperimentResultsRejectsBadBreakdownBy(t *testing.T) {
	router, exps, _ := newResultsAdmin(t)
	seedExperiment(t, exps, "exp_bb")
	rr := doAdminReq(t, router.Handler(), http.MethodGet,
		"/v1/admin/experiments/exp_bb/results?tenantId=acme&breakdown_by=nonsense",
		nil, withAdminPrincipal)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("malformed breakdown_by: status %d, want 400", rr.Code)
	}
}

func TestExperimentResultsRejectsBadFilter(t *testing.T) {
	router, exps, _ := newResultsAdmin(t)
	seedExperiment(t, exps, "exp_bad")
	rr := doAdminReq(t, router.Handler(), http.MethodGet,
		"/v1/admin/experiments/exp_bad/results?tenantId=acme&delegation_depth=notanumber",
		nil, withAdminPrincipal)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("malformed delegation_depth: status %d, want 400", rr.Code)
	}
}

// TestExperimentResultsRejectsBreakdownFilterOverlap_spec_10_7_952 pins
// §10.7 line 952: breakdown_by is not combinable with the equality /
// exclusion filter on the same field — the combination would collapse to
// a degenerate single-bucket response, so it is rejected with 400
// INVALID_QUERY_PARAMS. F-10.7.10.
func TestExperimentResultsRejectsBreakdownFilterOverlap_spec_10_7_952(t *testing.T) {
	router, exps, _ := newResultsAdmin(t)
	seedExperiment(t, exps, "exp_ov")
	cases := []string{
		"delegation_depth=0&breakdown_by=delegation_depth",
		"inherited=false&breakdown_by=inherited",
		"exclude_post_conclusion=true&breakdown_by=submitted_after_conclusion",
	}
	for _, q := range cases {
		rr := doAdminReq(t, router.Handler(), http.MethodGet,
			"/v1/admin/experiments/exp_ov/results?tenantId=acme&"+q, nil, withAdminPrincipal)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", q, rr.Code)
			continue
		}
		var resp map[string]any
		_ = json.Unmarshal(rr.Body.Bytes(), &resp)
		errBlk, _ := resp["error"].(map[string]any)
		if errBlk["code"] != "INVALID_QUERY_PARAMS" {
			t.Errorf("%s: code = %v, want INVALID_QUERY_PARAMS", q, errBlk["code"])
		}
	}
}

// TestExperimentResultsAllowsBreakdownWithDifferentFilter_spec_10_7_952
// pins that filtering one field and breaking down by a different field is
// permitted — only the same-field overlap is rejected. F-10.7.10.
func TestExperimentResultsAllowsBreakdownWithDifferentFilter_spec_10_7_952(t *testing.T) {
	router, exps, _ := newResultsAdmin(t)
	seedExperiment(t, exps, "exp_ok")
	rr := doAdminReq(t, router.Handler(), http.MethodGet,
		"/v1/admin/experiments/exp_ok/results?tenantId=acme&delegation_depth=0&breakdown_by=inherited",
		nil, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Errorf("filter on one field + breakdown on another: status %d, want 200, body %s",
			rr.Code, rr.Body.String())
	}
}

// TestExperimentResultsPerDimensionCount_spec_10_7_1088 pins §10.7 line
// 1088: a dimension's count equals the number of EvalResult rows that
// supplied a non-null value for that dimension, which may be lower than
// the enclosing scorer's count when some rows omit the dimension.
// F-10.7.9.
func TestExperimentResultsPerDimensionCount_spec_10_7_1088(t *testing.T) {
	router, exps, evals := newResultsAdmin(t)
	seedExperiment(t, exps, "exp_dim")
	put := func(sessionID string, scores map[string]float64) {
		if _, err := evals.Put(context.Background(), evalstore.EvalResult{
			TenantID: "acme", SessionID: sessionID, ExperimentID: "exp_dim",
			VariantID: "treatment", Scorer: "judge", Score: scorePtr(0.5), Scores: scores,
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	put("s1", map[string]float64{"coherence": 0.9, "relevance": 0.8})
	put("s2", map[string]float64{"coherence": 0.7}) // omits relevance
	rr := doAdminReq(t, router.Handler(), http.MethodGet,
		"/v1/admin/experiments/exp_dim/results?tenantId=acme", nil, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("results: status %d, body %s", rr.Code, rr.Body.String())
	}
	var res admin.ExperimentResults
	_ = json.Unmarshal(rr.Body.Bytes(), &res)
	var judge admin.ScorerStats
	for _, v := range res.Variants {
		if v.VariantID == "treatment" {
			judge = v.Scorers["judge"]
		}
	}
	if judge.Count != 2 {
		t.Errorf("scorer count = %d, want 2", judge.Count)
	}
	if judge.Dimensions["coherence"].Count != 2 {
		t.Errorf("coherence count = %d, want 2 (both rows supplied it)", judge.Dimensions["coherence"].Count)
	}
	if judge.Dimensions["relevance"].Count != 1 {
		t.Errorf("relevance count = %d, want 1 (one row omitted it)", judge.Dimensions["relevance"].Count)
	}
}

func TestExperimentResultsNotFound(t *testing.T) {
	router, _, _ := newResultsAdmin(t)
	rr := doAdminReq(t, router.Handler(), http.MethodGet,
		"/v1/admin/experiments/absent/results?tenantId=acme", nil, withAdminPrincipal)
	if rr.Code != http.StatusNotFound {
		t.Errorf("results for unknown experiment: status %d, want 404", rr.Code)
	}
}

// fakeAggReader wraps a real evalstore.Store and adds the §10.7
// materialized-view read path, returning canned aggregates distinct
// from the base-table rows so a test can tell which path served the
// response. spec: §10.7 lines 954, 1088. F-10.7.12.
type fakeAggReader struct {
	evalstore.Store
	aggregates map[string]evalstore.VariantAggregate
	reads      int
	refreshed  int
}

func (f *fakeAggReader) AggregatesByExperiment(_ context.Context, _, _ string) (map[string]evalstore.VariantAggregate, error) {
	f.reads++
	return f.aggregates, nil
}

func (f *fakeAggReader) RefreshAggregates(context.Context) error { f.refreshed++; return nil }

func newMatviewAdmin(t *testing.T, agg *fakeAggReader, enabled bool) (*admin.Router, experimentstore.Store) {
	t.Helper()
	exps := experimentstore.NewMemory()
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC) },
	}).WithExperiments(exps).WithEvalResults(agg).WithEvalAggregateView(enabled)
	return router, exps
}

// spec: §10.7 line 1088 — an unfiltered, no-breakdown request is served
// from the lenny_eval_aggregates matview when the view is enabled.
func TestExperimentResultsRoutesUnfilteredToMatview_spec_10_7_1088(t *testing.T) {
	agg := &fakeAggReader{
		Store: evalstore.NewMemory(0, nil),
		aggregates: map[string]evalstore.VariantAggregate{
			"treatment": {
				VariantID: "treatment", SampleCount: 5,
				Scorers: map[string]evalstore.ScorerAggregate{
					"judge": {
						Count: 3, Mean: 0.9, P50: 0.9, P95: 0.95,
						Dimensions: map[string]evalstore.ScorerAggregate{
							"coherence": {Count: 2, Mean: 0.8, P50: 0.8, P95: 0.85},
						},
					},
				},
			},
		},
	}
	router, exps := newMatviewAdmin(t, agg, true)
	seedExperiment(t, exps, "exp_1")
	// Base-table rows say something different (mean 0.1); the matview path
	// must win.
	seedEval(t, agg.Store, "s9", "exp_1", "treatment", "judge", 0.1)

	rr := doAdminReq(t, router.Handler(), http.MethodGet,
		"/v1/admin/experiments/exp_1/results?tenantId=acme", nil, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
	}
	if agg.reads != 1 {
		t.Fatalf("matview AggregatesByExperiment calls = %d, want 1", agg.reads)
	}
	var res admin.ExperimentResults
	_ = json.Unmarshal(rr.Body.Bytes(), &res)
	var treatment admin.VariantResults
	for _, v := range res.Variants {
		if v.VariantID == "treatment" {
			treatment = v
		}
	}
	if treatment.SampleCount != 5 {
		t.Errorf("sampleCount = %d, want 5 (matview value, not the base-table 1)", treatment.SampleCount)
	}
	st := treatment.Scorers["judge"]
	if st.Count != 3 || st.Mean < 0.89 || st.Mean > 0.91 {
		t.Errorf("judge stats = %+v, want count 3 mean ~0.9 from the matview", st)
	}
	if st.Dimensions["coherence"].Count != 2 {
		t.Errorf("coherence dimension not projected from the matview: %+v", st.Dimensions)
	}
}

// spec: §10.7 line 954 — a filtered or broken-down request bypasses the
// matview and recomputes from the base table.
func TestExperimentResultsFilteredBypassesMatview_spec_10_7_954(t *testing.T) {
	agg := &fakeAggReader{
		Store:      evalstore.NewMemory(0, nil),
		aggregates: map[string]evalstore.VariantAggregate{"treatment": {VariantID: "treatment", SampleCount: 999}},
	}
	router, exps := newMatviewAdmin(t, agg, true)
	seedExperiment(t, exps, "exp_1")
	seedEval(t, agg.Store, "s1", "exp_1", "treatment", "judge", 0.4)

	for _, query := range []string{
		"?tenantId=acme&inherited=true",
		"?tenantId=acme&delegation_depth=0",
		"?tenantId=acme&exclude_post_conclusion=true",
		"?tenantId=acme&breakdown_by=inherited",
	} {
		agg.reads = 0
		rr := doAdminReq(t, router.Handler(), http.MethodGet,
			"/v1/admin/experiments/exp_1/results"+query, nil, withAdminPrincipal)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: status %d, body %s", query, rr.Code, rr.Body.String())
		}
		if agg.reads != 0 {
			t.Errorf("%s: matview was consulted (reads=%d), want base-table path", query, agg.reads)
		}
	}
}

// spec: §10.7 line 1088 — with the matview disabled, even an unfiltered
// request aggregates on read from the base table.
func TestExperimentResultsMatviewDisabledUsesBaseTable_spec_10_7_1088(t *testing.T) {
	agg := &fakeAggReader{
		Store:      evalstore.NewMemory(0, nil),
		aggregates: map[string]evalstore.VariantAggregate{"treatment": {VariantID: "treatment", SampleCount: 999}},
	}
	router, exps := newMatviewAdmin(t, agg, false)
	seedExperiment(t, exps, "exp_1")
	seedEval(t, agg.Store, "s1", "exp_1", "treatment", "judge", 0.4)

	rr := doAdminReq(t, router.Handler(), http.MethodGet,
		"/v1/admin/experiments/exp_1/results?tenantId=acme", nil, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
	}
	if agg.reads != 0 {
		t.Errorf("matview consulted while disabled (reads=%d)", agg.reads)
	}
}

// spec: §10.7 line 950 — a malformed filter is a 400 on the matview path
// too (validated before the read path is chosen).
func TestExperimentResultsMalformedFilterRejected_spec_10_7_950(t *testing.T) {
	agg := &fakeAggReader{Store: evalstore.NewMemory(0, nil)}
	router, exps := newMatviewAdmin(t, agg, true)
	seedExperiment(t, exps, "exp_1")

	rr := doAdminReq(t, router.Handler(), http.MethodGet,
		"/v1/admin/experiments/exp_1/results?tenantId=acme&inherited=maybe", nil, withAdminPrincipal)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("malformed inherited: status %d, want 400", rr.Code)
	}
	if agg.reads != 0 {
		t.Errorf("matview consulted on a malformed request (reads=%d)", agg.reads)
	}
}
