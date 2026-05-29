// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/experiment"
	"github.com/lennylabs/lenny/pkg/gateway/evalstore"
	"github.com/lennylabs/lenny/pkg/gateway/experimentstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// spec: §10.7 built-in eval endpoint — POST /v1/sessions/{id}/eval.

func evalServer(t *testing.T, maxPerSession int, seed ...sessionstore.Session) (http.Handler, evalstore.Store) {
	t.Helper()
	store := memstore.New()
	for _, s := range seed {
		if err := store.Create(context.Background(), s); err != nil {
			t.Fatalf("seed session %q: %v", s.ID, err)
		}
	}
	evals := evalstore.NewMemory(maxPerSession, nil)
	srv := sessionserver.New(store, sessionserver.Options{Evals: evals})
	return srv.Handler(), evals
}

func evalServerWithExperiments(t *testing.T, exps experimentstore.Store, seed ...sessionstore.Session) (http.Handler, evalstore.Store) {
	t.Helper()
	store := memstore.New()
	for _, s := range seed {
		if err := store.Create(context.Background(), s); err != nil {
			t.Fatalf("seed session %q: %v", s.ID, err)
		}
	}
	evals := evalstore.NewMemory(0, nil)
	srv := sessionserver.New(store, sessionserver.Options{Evals: evals, Experiments: exps})
	return srv.Handler(), evals
}

func evalSession(id string, state session.State) sessionstore.Session {
	return sessionstore.Session{ID: id, TenantID: "default", UserID: "alice", State: state}
}

// concludedExperiment seeds a valid §10.7 experiment in the given status.
func seedExperiment(t *testing.T, exps experimentstore.Store, id string, status experiment.Status) {
	t.Helper()
	if err := exps.Create(context.Background(), experimentstore.Experiment{
		ID: id, TenantID: "default", Status: status, BaseRuntime: "claude",
		Variants:      []experimentstore.Variant{{ID: "treatment", Weight: 0.5}},
		TargetingMode: experiment.TargetingPercentage,
		Sticky:        experiment.StickyUser,
		Propagation:   experiment.PropagationInherit,
	}); err != nil {
		t.Fatalf("seed experiment %q: %v", id, err)
	}
}

func postEval(t *testing.T, h http.Handler, sessionID string, body sessionserver.EvalRequest) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sessionID+"/eval", bytes.NewReader(b))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func evalScore(v float64) *float64 { return &v }

func TestEvalIngestsScore(t *testing.T) {
	h, evals := evalServer(t, 0, evalSession("sess_1", session.StateRunning))
	rr := postEval(t, h, "sess_1", sessionserver.EvalRequest{Scorer: "llm-judge", Score: evalScore(0.82)})
	if rr.Code != http.StatusCreated {
		t.Fatalf("eval: status %d, body %s", rr.Code, rr.Body.String())
	}
	n, _ := evals.CountBySession(context.Background(), "default", "sess_1")
	if n != 1 {
		t.Errorf("stored %d eval results, want 1", n)
	}
	var resp sessionserver.EvalResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.ID == "" || resp.Scorer != "llm-judge" {
		t.Errorf("response = %+v, want a stored eval result", resp)
	}
}

func TestEvalAcceptsCompletedAndFailedSessions(t *testing.T) {
	h, _ := evalServer(t, 0,
		evalSession("done", session.StateCompleted),
		evalSession("broke", session.StateFailed))
	for _, id := range []string{"done", "broke"} {
		rr := postEval(t, h, id, sessionserver.EvalRequest{Scorer: "s", Score: evalScore(0.5)})
		if rr.Code != http.StatusCreated {
			t.Errorf("eval against %s: status %d, want 201", id, rr.Code)
		}
	}
}

func TestEvalRejectsMissingScorer(t *testing.T) {
	h, _ := evalServer(t, 0, evalSession("sess_1", session.StateRunning))
	rr := postEval(t, h, "sess_1", sessionserver.EvalRequest{Score: evalScore(0.5)})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("missing scorer: status %d, want 400", rr.Code)
	}
}

func TestEvalRejectsNoScore(t *testing.T) {
	h, _ := evalServer(t, 0, evalSession("sess_1", session.StateRunning))
	rr := postEval(t, h, "sess_1", sessionserver.EvalRequest{Scorer: "s"})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("no score or scores: status %d, want 400", rr.Code)
	}
}

func TestEvalAcceptsScoresOnly(t *testing.T) {
	h, _ := evalServer(t, 0, evalSession("sess_1", session.StateRunning))
	rr := postEval(t, h, "sess_1", sessionserver.EvalRequest{
		Scorer: "multi", Scores: map[string]float64{"coherence": 0.9},
	})
	if rr.Code != http.StatusCreated {
		t.Errorf("scores-only submission: status %d, want 201", rr.Code)
	}
}

func TestEvalRejectsIneligibleSession(t *testing.T) {
	h, _ := evalServer(t, 0, evalSession("cancelled", session.StateCancelled))
	rr := postEval(t, h, "cancelled", sessionserver.EvalRequest{Scorer: "s", Score: evalScore(0.5)})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("eval against a cancelled session: status %d, want 422", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "SESSION_NOT_EVAL_ELIGIBLE") {
		t.Errorf("rejection should carry SESSION_NOT_EVAL_ELIGIBLE: %s", rr.Body.String())
	}
}

func TestEvalSessionNotFound(t *testing.T) {
	h, _ := evalServer(t, 0)
	rr := postEval(t, h, "ghost", sessionserver.EvalRequest{Scorer: "s", Score: evalScore(0.5)})
	if rr.Code != http.StatusNotFound {
		t.Errorf("eval against an unknown session: status %d, want 404", rr.Code)
	}
}

func TestEvalQuotaExceeded(t *testing.T) {
	h, _ := evalServer(t, 1, evalSession("sess_1", session.StateRunning))
	if rr := postEval(t, h, "sess_1", sessionserver.EvalRequest{Scorer: "s", Score: evalScore(0.5)}); rr.Code != http.StatusCreated {
		t.Fatalf("first eval: status %d", rr.Code)
	}
	rr := postEval(t, h, "sess_1", sessionserver.EvalRequest{Scorer: "s", Score: evalScore(0.6)})
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("over-quota eval: status %d, want 429", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "EVAL_QUOTA_EXCEEDED") {
		t.Errorf("rejection should carry EVAL_QUOTA_EXCEEDED: %s", rr.Body.String())
	}
}

func TestEvalUnavailableWithoutStore(t *testing.T) {
	store := memstore.New()
	_ = store.Create(context.Background(), evalSession("sess_1", session.StateRunning))
	srv := sessionserver.New(store, sessionserver.Options{}) // no Evals wired
	rr := postEval(t, srv.Handler(), "sess_1", sessionserver.EvalRequest{Scorer: "s", Score: evalScore(0.5)})
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("eval with no store: status %d, want 503", rr.Code)
	}
}

// spec: §10.7 lines 892-928 — the POST /v1/sessions/{id}/eval response
// carries experimentId, variantId, delegationDepth, inherited, and
// submittedAfterConclusion when the session has an experiment context,
// so callers can round-trip the stored record without a follow-up GET.
func TestEvalResponseCarriesExperimentAttribution_spec_10_7(t *testing.T) {
	enrolled := sessionstore.Session{
		ID: "sess_attr", TenantID: "default", UserID: "alice", State: session.StateRunning,
		ExperimentContext: &sessionstore.ExperimentContext{
			ExperimentID: "exp-42",
			VariantID:    "variant-b",
			Inherited:    true,
		},
	}
	h, _ := evalServer(t, 0, enrolled)
	rr := postEval(t, h, "sess_attr", sessionserver.EvalRequest{Scorer: "s", Score: evalScore(0.5)})
	if rr.Code != http.StatusCreated {
		t.Fatalf("eval: status %d, body %s", rr.Code, rr.Body.String())
	}
	var resp sessionserver.EvalResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ExperimentID != "exp-42" {
		t.Errorf("experimentId = %q, want exp-42", resp.ExperimentID)
	}
	if resp.VariantID != "variant-b" {
		t.Errorf("variantId = %q, want variant-b", resp.VariantID)
	}
	if !resp.Inherited {
		t.Errorf("inherited = false, want true")
	}
	// Unenrolled session: attribution fields are omitted (zero values).
	h2, _ := evalServer(t, 0, evalSession("sess_unenrolled", session.StateRunning))
	rr2 := postEval(t, h2, "sess_unenrolled", sessionserver.EvalRequest{Scorer: "s", Score: evalScore(0.5)})
	var resp2 sessionserver.EvalResponse
	_ = json.Unmarshal(rr2.Body.Bytes(), &resp2)
	if resp2.ExperimentID != "" || resp2.VariantID != "" {
		t.Errorf("unenrolled response carries attribution: %+v", resp2)
	}
	if strings.Contains(rr2.Body.String(), `"experimentId"`) || strings.Contains(rr2.Body.String(), `"variantId"`) {
		t.Errorf("unenrolled response should omit empty attribution fields: %s", rr2.Body.String())
	}
}

// TestEvalPopulatesDelegationDepth_spec_10_7_905 pins §10.7 lines
// 868/905: the eval endpoint copies the session's stamped
// delegation_depth onto the EvalResult so the Results API
// `?delegation_depth=` filter and `?breakdown_by=delegation_depth`
// operate on truthful data. F-10.7.5.
func TestEvalPopulatesDelegationDepth_spec_10_7_905(t *testing.T) {
	deep := sessionstore.Session{
		ID: "sess_deep", TenantID: "default", UserID: "alice",
		State: session.StateRunning, DelegationDepth: 3,
	}
	h, evals := evalServer(t, 0, deep)
	rr := postEval(t, h, "sess_deep", sessionserver.EvalRequest{Scorer: "s", Score: evalScore(0.5)})
	if rr.Code != http.StatusCreated {
		t.Fatalf("eval: status %d, body %s", rr.Code, rr.Body.String())
	}
	var resp sessionserver.EvalResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.DelegationDepth != 3 {
		t.Errorf("response delegationDepth = %d, want 3", resp.DelegationDepth)
	}
	rows, _ := evals.ListBySession(context.Background(), "default", "sess_deep")
	if len(rows) != 1 || rows[0].DelegationDepth != 3 {
		t.Errorf("stored delegation_depth = %+v, want one row at depth 3", rows)
	}
}

// TestEvalRootSessionDelegationDepthZero_spec_10_7_905 pins that a root
// session yields depth 0 and the response omits the zero field. F-10.7.5.
func TestEvalRootSessionDelegationDepthZero_spec_10_7_905(t *testing.T) {
	h, evals := evalServer(t, 0, evalSession("sess_root", session.StateRunning))
	rr := postEval(t, h, "sess_root", sessionserver.EvalRequest{Scorer: "s", Score: evalScore(0.5)})
	if rr.Code != http.StatusCreated {
		t.Fatalf("eval: status %d, body %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), `"delegationDepth"`) {
		t.Errorf("root session response should omit zero delegationDepth: %s", rr.Body.String())
	}
	rows, _ := evals.ListBySession(context.Background(), "default", "sess_root")
	if len(rows) != 1 || rows[0].DelegationDepth != 0 {
		t.Errorf("stored delegation_depth = %+v, want one row at depth 0", rows)
	}
}

// TestEvalSubmittedAfterConclusion_spec_10_7_907 pins §10.7 lines
// 907/937: an eval submitted against a session whose attributed
// experiment has concluded is stored with the original attribution and
// flagged submitted_after_conclusion=true. F-10.7.5.
func TestEvalSubmittedAfterConclusion_spec_10_7_907(t *testing.T) {
	exps := experimentstore.NewMemory()
	seedExperiment(t, exps, "exp-done", experiment.StatusConcluded)
	enrolled := sessionstore.Session{
		ID: "sess_post", TenantID: "default", UserID: "alice", State: session.StateRunning,
		ExperimentContext: &sessionstore.ExperimentContext{ExperimentID: "exp-done", VariantID: "treatment"},
	}
	h, evals := evalServerWithExperiments(t, exps, enrolled)
	rr := postEval(t, h, "sess_post", sessionserver.EvalRequest{Scorer: "s", Score: evalScore(0.5)})
	if rr.Code != http.StatusCreated {
		t.Fatalf("eval: status %d, body %s", rr.Code, rr.Body.String())
	}
	var resp sessionserver.EvalResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if !resp.SubmittedAfterConclusion {
		t.Errorf("submittedAfterConclusion = false, want true (experiment concluded)")
	}
	if resp.ExperimentID != "exp-done" {
		t.Errorf("experimentId = %q, want exp-done (attribution preserved post-conclusion)", resp.ExperimentID)
	}
	rows, _ := evals.ListBySession(context.Background(), "default", "sess_post")
	if len(rows) != 1 || !rows[0].SubmittedAfterConclusion {
		t.Errorf("stored submitted_after_conclusion = %+v, want one flagged row", rows)
	}
}

// TestEvalNotSubmittedAfterConclusionWhenActive_spec_10_7_907 pins that
// an active experiment's evals are not flagged. F-10.7.5.
func TestEvalNotSubmittedAfterConclusionWhenActive_spec_10_7_907(t *testing.T) {
	exps := experimentstore.NewMemory()
	seedExperiment(t, exps, "exp-live", experiment.StatusActive)
	enrolled := sessionstore.Session{
		ID: "sess_live", TenantID: "default", UserID: "alice", State: session.StateRunning,
		ExperimentContext: &sessionstore.ExperimentContext{ExperimentID: "exp-live", VariantID: "treatment"},
	}
	h, _ := evalServerWithExperiments(t, exps, enrolled)
	rr := postEval(t, h, "sess_live", sessionserver.EvalRequest{Scorer: "s", Score: evalScore(0.5)})
	var resp sessionserver.EvalResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.SubmittedAfterConclusion {
		t.Errorf("submittedAfterConclusion = true, want false (experiment active)")
	}
	if strings.Contains(rr.Body.String(), `"submittedAfterConclusion"`) {
		t.Errorf("active-experiment response should omit the false flag: %s", rr.Body.String())
	}
}

// TestEvalSubmittedAfterConclusionUnresolvedIsFalse_spec_10_7_907 pins
// that an unenrolled session and an unwired experiment store both leave
// the flag false rather than erroring — the eval is still stored.
// F-10.7.5.
func TestEvalSubmittedAfterConclusionUnresolvedIsFalse_spec_10_7_907(t *testing.T) {
	// Unenrolled session, experiment store wired.
	exps := experimentstore.NewMemory()
	h, _ := evalServerWithExperiments(t, exps, evalSession("sess_plain", session.StateRunning))
	rr := postEval(t, h, "sess_plain", sessionserver.EvalRequest{Scorer: "s", Score: evalScore(0.5)})
	if rr.Code != http.StatusCreated {
		t.Fatalf("unenrolled eval: status %d, body %s", rr.Code, rr.Body.String())
	}
	// Enrolled in a concluded experiment, but the experiment store is not
	// wired: the handler must fail open (flag false), not panic.
	enrolled := sessionstore.Session{
		ID: "sess_nostore", TenantID: "default", UserID: "alice", State: session.StateRunning,
		ExperimentContext: &sessionstore.ExperimentContext{ExperimentID: "exp-x", VariantID: "treatment"},
	}
	h2, evals := evalServer(t, 0, enrolled)
	rr2 := postEval(t, h2, "sess_nostore", sessionserver.EvalRequest{Scorer: "s", Score: evalScore(0.5)})
	if rr2.Code != http.StatusCreated {
		t.Fatalf("no-store eval: status %d, body %s", rr2.Code, rr2.Body.String())
	}
	rows, _ := evals.ListBySession(context.Background(), "default", "sess_nostore")
	if len(rows) != 1 || rows[0].SubmittedAfterConclusion {
		t.Errorf("no-store enrolled eval should leave flag false: %+v", rows)
	}
}
