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

func TestExperimentResultsNotFound(t *testing.T) {
	router, _, _ := newResultsAdmin(t)
	rr := doAdminReq(t, router.Handler(), http.MethodGet,
		"/v1/admin/experiments/absent/results?tenantId=acme", nil, withAdminPrincipal)
	if rr.Code != http.StatusNotFound {
		t.Errorf("results for unknown experiment: status %d, want 404", rr.Code)
	}
}
