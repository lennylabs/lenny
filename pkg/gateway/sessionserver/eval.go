// SPDX-License-Identifier: MIT

package sessionserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/evalstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
)

// EvalRequest is the §10.7 POST /v1/sessions/{id}/eval body. At least
// one of Score or Scores must be provided.
type EvalRequest struct {
	Scorer   string             `json:"scorer"`
	Score    *float64           `json:"score,omitempty"`
	Scores   map[string]float64 `json:"scores,omitempty"`
	Metadata map[string]any     `json:"metadata,omitempty"`
}

// EvalResponse echoes the stored §10.7 EvalResult. spec: §10.7 lines
// 892-928 — the response carries the experiment attribution and the
// delegation/inherited/submittedAfterConclusion flags the gateway
// populates from the session's experiment context so callers see the
// effective record without an additional GET. Empty attribution fields
// are omitted for unenrolled sessions.
type EvalResponse struct {
	ID                       string             `json:"id"`
	SessionID                string             `json:"sessionId"`
	ExperimentID             string             `json:"experimentId,omitempty"`
	VariantID                string             `json:"variantId,omitempty"`
	Scorer                   string             `json:"scorer"`
	Score                    *float64           `json:"score,omitempty"`
	Scores                   map[string]float64 `json:"scores,omitempty"`
	Metadata                 map[string]any     `json:"metadata,omitempty"`
	DelegationDepth          uint32             `json:"delegationDepth,omitempty"`
	Inherited                bool               `json:"inherited,omitempty"`
	SubmittedAfterConclusion bool               `json:"submittedAfterConclusion,omitempty"`
	CreatedAt                string             `json:"createdAt"`
}

// evalEligible reports whether a session in state st accepts eval
// submissions. §10.7 accepts active (running), completed, and failed
// sessions; cancelled and expired sessions are rejected.
func evalEligible(st session.State) bool {
	return st == session.StateRunning || st == session.StateCompleted || st == session.StateFailed
}

// handleEval implements POST /v1/sessions/{id}/eval — the §10.7
// built-in eval-score ingestion endpoint. It validates the submission,
// enforces the §10.7 per-session storage bound, and stores an
// EvalResult.
//
// Experiment attribution (experiment_id, variant_id) and the
// delegation-depth / inherited fields are auto-populated from the
// session's experiment context; that context is set by the
// ExperimentRouter, which is not yet built, so v1 stores results with
// empty attribution.
func (s *Server) handleEval(w http.ResponseWriter, r *http.Request) {
	if s.evals == nil {
		s.writeError(w, http.StatusServiceUnavailable, "EVAL_UNAVAILABLE",
			"the built-in eval endpoint is not configured", nil)
		return
	}
	tenantID := s.resolveTenant(r)
	id := r.PathValue("id")

	var req EvalRequest
	body := jsonReader(w, r)
	defer body.Close()
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON", nil)
		return
	}
	if req.Scorer == "" {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "scorer is required",
			map[string]any{"fields": []map[string]string{{"field": "scorer"}}})
		return
	}
	if req.Score == nil && len(req.Scores) == 0 {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"at least one of score or scores must be provided",
			map[string]any{"fields": []map[string]string{{"field": "score"}}})
		return
	}

	sess, err := s.store.Get(r.Context(), tenantID, id)
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
			return
		}
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if !evalEligible(sess.State) {
		s.writeError(w, http.StatusUnprocessableEntity, "SESSION_NOT_EVAL_ELIGIBLE",
			"eval submissions are accepted only for running, completed, or failed sessions", nil)
		return
	}

	// §10.7 gateway auto-association: copy the session's experiment
	// context onto the eval result so the results API can aggregate by
	// variant. An unenrolled session leaves the attribution empty.
	result := evalstore.EvalResult{
		TenantID:  tenantID,
		SessionID: id,
		Scorer:    req.Scorer,
		Score:     req.Score,
		Scores:    req.Scores,
		Metadata:  req.Metadata,
	}
	if ec := sess.ExperimentContext; ec != nil {
		result.ExperimentID = ec.ExperimentID
		result.VariantID = ec.VariantID
		result.Inherited = ec.Inherited
	}
	stored, err := s.evals.Put(r.Context(), result)
	if err != nil {
		if errors.Is(err, evalstore.ErrQuotaExceeded) {
			s.writeError(w, http.StatusTooManyRequests, "EVAL_QUOTA_EXCEEDED",
				"the session has reached its eval-result storage limit", nil)
			return
		}
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(EvalResponse{
		ID:                       stored.ID,
		SessionID:                stored.SessionID,
		ExperimentID:             stored.ExperimentID,
		VariantID:                stored.VariantID,
		Scorer:                   stored.Scorer,
		Score:                    stored.Score,
		Scores:                   stored.Scores,
		Metadata:                 stored.Metadata,
		DelegationDepth:          stored.DelegationDepth,
		Inherited:                stored.Inherited,
		SubmittedAfterConclusion: stored.SubmittedAfterConclusion,
		CreatedAt:                stored.CreatedAt.UTC().Format(time.RFC3339Nano),
	})
}
