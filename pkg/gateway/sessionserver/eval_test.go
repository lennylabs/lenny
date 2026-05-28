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
	"github.com/lennylabs/lenny/pkg/gateway/evalstore"
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

func evalSession(id string, state session.State) sessionstore.Session {
	return sessionstore.Session{ID: id, TenantID: "default", UserID: "alice", State: state}
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
