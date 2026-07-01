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
	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/experiment"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/evalstore"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/experimentstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/policy/ratelimit"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
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

// postEvalAs posts an eval submission carrying an authenticated
// principal so the §10.7 session:eval:write gate evaluates against its
// roles. F-10.7.4.
func postEvalAs(t *testing.T, h http.Handler, sessionID string, body sessionserver.EvalRequest, p authmw.Principal) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sessionID+"/eval", bytes.NewReader(b))
	req = req.WithContext(authmw.WithPrincipal(req.Context(), p))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// evalServerRL builds an eval server with the §10.7 rate-limit options
// wired over an in-memory counter. F-10.7.4 / F-11.2.19.
func evalServerRL(t *testing.T, perSession, perTenant int, seed ...sessionstore.Session) http.Handler {
	t.Helper()
	store := memstore.New()
	for _, s := range seed {
		if err := store.Create(context.Background(), s); err != nil {
			t.Fatalf("seed session %q: %v", s.ID, err)
		}
	}
	srv := sessionserver.New(store, sessionserver.Options{
		Evals:                   evalstore.NewMemory(0, nil),
		EvalRateLimitCounter:    ratelimit.NewMemory(),
		EvalPerSessionPerMinute: perSession,
		EvalPerTenantPerMinute:  perTenant,
	})
	return srv.Handler()
}

// TestEvalRequiresSessionEvalWritePermission_spec_10_7_936 pins §10.7
// line 936: the eval endpoint is gated on session:eval:write. A role
// without it (tenant-viewer) is rejected with 403; a session-owning
// role (user) is admitted. F-10.7.4.
func TestEvalRequiresSessionEvalWritePermission_spec_10_7_936(t *testing.T) {
	h, _ := evalServer(t, 0, evalSession("sess_1", session.StateRunning))
	body := sessionserver.EvalRequest{Scorer: "llm-judge", Score: evalScore(0.5)}

	viewer := authmw.Principal{Subject: "scorer@acme.com", TenantID: "default", Roles: []auth.Role{auth.RoleTenantViewer}}
	if rr := postEvalAs(t, h, "sess_1", body, viewer); rr.Code != http.StatusForbidden {
		t.Fatalf("tenant-viewer eval: status %d, want 403 (lacks session:eval:write)", rr.Code)
	}

	user := authmw.Principal{Subject: "alice@acme.com", TenantID: "default", Roles: []auth.Role{auth.RoleUser}}
	if rr := postEvalAs(t, h, "sess_1", body, user); rr.Code != http.StatusCreated {
		t.Fatalf("user eval: status %d, want 201 (holds session:eval:write)", rr.Code)
	}
}

// TestEvalRateLimitPerSession_spec_10_7_938 pins the per-session
// eval-submission rate limit: a second submission within the same
// minute against a session at limit 1 is rejected with 429 and a
// Retry-After header. F-10.7.4 / F-11.2.19.
func TestEvalRateLimitPerSession_spec_10_7_938(t *testing.T) {
	// Disable the per-tenant scope so only the per-session limit fires.
	h := evalServerRL(t, 1, -1, evalSession("sess_1", session.StateRunning))
	body := sessionserver.EvalRequest{Scorer: "s", Score: evalScore(0.5)}
	if rr := postEval(t, h, "sess_1", body); rr.Code != http.StatusCreated {
		t.Fatalf("first eval: status %d, want 201", rr.Code)
	}
	rr := postEval(t, h, "sess_1", body)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("second eval: status %d, want 429", rr.Code)
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Error("429 response must carry a Retry-After header per §10.7 line 938")
	}
	if !strings.Contains(rr.Body.String(), "RATE_LIMITED") {
		t.Errorf("rejection should carry RATE_LIMITED: %s", rr.Body.String())
	}
}

// TestEvalRateLimitPerTenant_spec_10_7_938 pins the per-tenant
// eval-submission rate limit: a submission against a second session of
// the same tenant trips the tenant scope even though each session is
// under its own limit. F-10.7.4 / F-11.2.19.
func TestEvalRateLimitPerTenant_spec_10_7_938(t *testing.T) {
	// Disable the per-session scope so only the per-tenant limit fires.
	h := evalServerRL(t, -1, 1,
		evalSession("sess_a", session.StateRunning),
		evalSession("sess_b", session.StateRunning))
	body := sessionserver.EvalRequest{Scorer: "s", Score: evalScore(0.5)}
	if rr := postEval(t, h, "sess_a", body); rr.Code != http.StatusCreated {
		t.Fatalf("first tenant eval: status %d, want 201", rr.Code)
	}
	rr := postEval(t, h, "sess_b", body)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("second tenant eval (other session): status %d, want 429", rr.Code)
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Error("per-tenant 429 must carry a Retry-After header")
	}
}

// TestEvalIdempotencyReplay_spec_10_7_940 pins §10.7 line 940: a repeat
// submission carrying the same idempotency key for a session returns
// 200 OK with the original record rather than inserting a duplicate; a
// different key inserts a fresh record. F-10.7.4.
func TestEvalIdempotencyReplay_spec_10_7_940(t *testing.T) {
	h, evals := evalServer(t, 0, evalSession("sess_1", session.StateRunning))

	first := postEval(t, h, "sess_1", sessionserver.EvalRequest{
		Scorer: "llm-judge", Score: evalScore(0.5), IdempotencyKey: "k1",
	})
	if first.Code != http.StatusCreated {
		t.Fatalf("first submission: status %d, want 201", first.Code)
	}
	var firstResp sessionserver.EvalResponse
	_ = json.Unmarshal(first.Body.Bytes(), &firstResp)

	// Replay with the same key but a different score: returns 200 with the
	// ORIGINAL record (id and score unchanged), no second row.
	replay := postEval(t, h, "sess_1", sessionserver.EvalRequest{
		Scorer: "llm-judge", Score: evalScore(0.99), IdempotencyKey: "k1",
	})
	if replay.Code != http.StatusOK {
		t.Fatalf("idempotent replay: status %d, want 200", replay.Code)
	}
	var replayResp sessionserver.EvalResponse
	_ = json.Unmarshal(replay.Body.Bytes(), &replayResp)
	if replayResp.ID != firstResp.ID {
		t.Errorf("replay id = %q, want original %q", replayResp.ID, firstResp.ID)
	}
	if replayResp.Score == nil || *replayResp.Score != 0.5 {
		t.Errorf("replay score = %v, want original 0.5 (no overwrite)", replayResp.Score)
	}
	if n, _ := evals.CountBySession(context.Background(), "default", "sess_1"); n != 1 {
		t.Errorf("stored %d eval results after replay, want 1 (no duplicate)", n)
	}

	// A different key inserts a fresh record.
	other := postEval(t, h, "sess_1", sessionserver.EvalRequest{
		Scorer: "llm-judge", Score: evalScore(0.7), IdempotencyKey: "k2",
	})
	if other.Code != http.StatusCreated {
		t.Fatalf("distinct-key submission: status %d, want 201", other.Code)
	}
	if n, _ := evals.CountBySession(context.Background(), "default", "sess_1"); n != 2 {
		t.Errorf("stored %d eval results, want 2 (distinct keys)", n)
	}
}

// TestEvalRejectsOversizeIdempotencyKey_spec_10_7_939 pins §10.7 line
// 939: an idempotency key over 128 bytes is rejected with 400 before any
// store work. F-10.7.4.
func TestEvalRejectsOversizeIdempotencyKey_spec_10_7_939(t *testing.T) {
	h, evals := evalServer(t, 0, evalSession("sess_1", session.StateRunning))
	rr := postEval(t, h, "sess_1", sessionserver.EvalRequest{
		Scorer: "s", Score: evalScore(0.5),
		IdempotencyKey: strings.Repeat("x", evalstore.MaxIdempotencyKeyBytes+1),
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("oversize idempotency_key: status %d, want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "idempotency_key") {
		t.Errorf("rejection should name the idempotency_key field: %s", rr.Body.String())
	}
	if n, _ := evals.CountBySession(context.Background(), "default", "sess_1"); n != 0 {
		t.Errorf("oversize-key submission stored %d rows, want 0", n)
	}
}

// capturedEvalScore records an ObserveEvalScore hook call.
type capturedEvalScore struct {
	tenantID, scorer, variantID string
	score                       float64
}

// spec: §16.1 line 164 / §10.7 line 1128 — a submitted eval with a scalar
// score records one lenny_eval_score observation labelled by scorer and the
// session's variant. A submission carrying only the per-dimension scores map
// has no scalar observation and must not call the hook. F-10.7.13.
func TestEvalEmitsEvalScoreMetric_spec_16_1_164(t *testing.T) {
	store := memstore.New()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_e", TenantID: "default", UserID: "alice", State: session.StateRunning,
		ExperimentContext: &sessionstore.ExperimentContext{ExperimentID: "exp_a", VariantID: "treatment"},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_d", TenantID: "default", UserID: "alice", State: session.StateRunning,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	var got []capturedEvalScore
	srv := sessionserver.New(store, sessionserver.Options{
		Evals: evalstore.NewMemory(0, nil),
		ObserveEvalScore: func(tenantID, scorer, variantID string, score float64) {
			got = append(got, capturedEvalScore{tenantID, scorer, variantID, score})
		},
	})
	h := srv.Handler()

	if rr := postEval(t, h, "sess_e", sessionserver.EvalRequest{Scorer: "safety", Score: evalScore(0.97)}); rr.Code != http.StatusCreated {
		t.Fatalf("scalar eval: status %d, body %s", rr.Code, rr.Body.String())
	}
	// Dimension-only submission: no scalar score, so no observation.
	if rr := postEval(t, h, "sess_d", sessionserver.EvalRequest{Scorer: "rubric", Scores: map[string]float64{"a": 0.4}}); rr.Code != http.StatusCreated {
		t.Fatalf("dimension eval: status %d, body %s", rr.Code, rr.Body.String())
	}

	if len(got) != 1 {
		t.Fatalf("observations: got %d, want 1 (%+v)", len(got), got)
	}
	want := capturedEvalScore{"default", "safety", "treatment", 0.97}
	if got[0] != want {
		t.Errorf("observation = %+v, want %+v", got[0], want)
	}
}

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
