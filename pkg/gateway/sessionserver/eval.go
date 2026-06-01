// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/experiment"
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
	// IdempotencyKey is the optional §10.7 dedup key (≤128 bytes). A
	// repeat submission carrying the same key for the same session within
	// 24h returns 200 OK with the original record rather than inserting a
	// duplicate. spec: §10.7 lines 939-940. F-10.7.4.
	IdempotencyKey string `json:"idempotency_key,omitempty"`
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

// evalSubmittedAfterConclusion reports whether an eval submitted now
// lands after its attributed experiment transitioned to `concluded`.
// spec: §10.7 line 907 / line 937. An unenrolled session, an unwired
// experiment store, or an experiment that has since been deleted yields
// false (the submission is treated as in-window) — the eval is stored
// regardless; only the post-conclusion flag is affected.
func (s *Server) evalSubmittedAfterConclusion(ctx context.Context, tenantID, experimentID string) bool {
	if experimentID == "" || s.experiments == nil {
		return false
	}
	exp, err := s.experiments.Get(ctx, tenantID, experimentID)
	if err != nil {
		return false
	}
	return exp.Status == experiment.StatusConcluded
}

// evalEligible reports whether a session in state st accepts eval
// submissions. §10.7 accepts active (running), completed, and failed
// sessions; cancelled and expired sessions are rejected.
func evalEligible(st session.State) bool {
	return st == session.StateRunning || st == session.StateCompleted || st == session.StateFailed
}

// checkEvalRateLimit enforces the §10.7 line 938 per-session and
// per-tenant eval-submission rate limits. It returns false (after
// writing a 429 with a Retry-After header) when either scope's
// one-minute count exceeds its configured limit, and true otherwise. A
// nil counter or a non-positive limit leaves the corresponding scope
// unlimited. spec: §10.7 line 938. F-10.7.4 / F-11.2.19.
func (s *Server) checkEvalRateLimit(w http.ResponseWriter, r *http.Request, tenantID, sessionID string) bool {
	if s.evalRL == nil {
		return true
	}
	now := s.clock()
	if s.evalPerSessionPerMin > 0 {
		if !s.checkEvalScope(w, r, "eval_session", "eval:s:"+tenantID+":"+sessionID, s.evalPerSessionPerMin, now) {
			return false
		}
	}
	if s.evalPerTenantPerMin > 0 {
		if !s.checkEvalScope(w, r, "eval_tenant", "eval:t:"+tenantID, s.evalPerTenantPerMin, now) {
			return false
		}
	}
	return true
}

// checkEvalScope increments one eval rate-limit scope's per-minute
// counter and writes a 429 (with Retry-After) when the scope is over
// limit. A transient counter error fails open, matching the §11.1
// admission gate, so a Redis blip does not block scoring. spec: §10.7
// line 938. F-10.7.4.
func (s *Server) checkEvalScope(w http.ResponseWriter, r *http.Request, scope, key string, limit int, now time.Time) bool {
	count, err := s.evalRL.Incr(r.Context(), key, now)
	if err != nil {
		if s.rlMetrics != nil {
			s.rlMetrics.IncRateLimitCounterFailure()
		}
		log.Printf("sessionserver: §10.7 %s eval rate-limit counter unavailable key=%q err=%q fail_open=true",
			scope, key, err.Error())
		return true
	}
	if count > limit {
		if s.rlMetrics != nil {
			s.rlMetrics.IncRateLimitRejected(scope)
		}
		retryAfter := 60 - now.Second()
		if retryAfter <= 0 {
			retryAfter = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
		s.writeError(w, http.StatusTooManyRequests, "RATE_LIMITED",
			"the eval-submission rate limit was exceeded",
			map[string]any{"scope": scope, "limitPerMinute": limit})
		return false
	}
	return true
}

// handleEval implements POST /v1/sessions/{id}/eval — the §10.7
// built-in eval-score ingestion endpoint. It validates the submission,
// enforces the §10.7 per-session storage bound, and stores an
// EvalResult.
//
// Experiment attribution (experiment_id, variant_id, inherited) is
// auto-populated from the session's experiment context. delegation_depth
// is copied from the session record (stamped at delegation time per
// §10.7 line 905), and submitted_after_conclusion is computed by
// consulting the attributed experiment's current status per §10.7 lines
// 907 / 937. An unenrolled session leaves the attribution empty and its
// depth at 0. F-10.7.5.
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
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
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
	// §10.7 line 939 — the optional idempotency key is bounded at 128
	// bytes. Reject an oversize key before any store work. F-10.7.4.
	if len(req.IdempotencyKey) > evalstore.MaxIdempotencyKeyBytes {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"idempotency_key must be at most 128 bytes",
			map[string]any{"fields": []map[string]string{{"field": "idempotency_key"}}})
		return
	}

	// §10.7 line 938 — per-session and per-tenant eval-submission rate
	// limits, enforced before the session lookup so a flood against a
	// single session (or across a tenant) is capped regardless of whether
	// the target exists. Excess requests receive 429 with Retry-After.
	// F-10.7.4 / F-11.2.19.
	if !s.checkEvalRateLimit(w, r, tenantID, id) {
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

	// §10.7 line 940 — a repeat submission carrying the same idempotency
	// key for this session within 24h resolves to the original record
	// rather than inserting a duplicate, returning 200 OK. F-10.7.4.
	if req.IdempotencyKey != "" {
		notBefore := s.clock().Add(-evalstore.IdempotencyWindow)
		if prior, ok, err := s.evals.FindByIdempotencyKey(r.Context(), tenantID, id, req.IdempotencyKey, notBefore); err != nil {
			s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
			return
		} else if ok {
			s.writeEvalResponse(w, prior, http.StatusOK)
			return
		}
	}

	// §10.7 gateway auto-association: copy the session's experiment
	// context onto the eval result so the results API can aggregate by
	// variant. An unenrolled session leaves the attribution empty.
	result := evalstore.EvalResult{
		TenantID:       tenantID,
		SessionID:      id,
		Scorer:         req.Scorer,
		Score:          req.Score,
		Scores:         req.Scores,
		Metadata:       req.Metadata,
		IdempotencyKey: req.IdempotencyKey,
		// §10.7 line 905 — delegation_depth is auto-populated from the
		// session's delegation lineage (0 for a root session). The depth
		// is stamped on the session row at delegation time. F-10.7.5.
		DelegationDepth: sess.DelegationDepth,
	}
	if ec := sess.ExperimentContext; ec != nil {
		result.ExperimentID = ec.ExperimentID
		result.VariantID = ec.VariantID
		result.Inherited = ec.Inherited
		// §10.7 lines 907 / 937 — the eval is stored with the session's
		// original attribution regardless of the experiment's status, but
		// the gateway flags submissions that arrive after the experiment
		// concluded so operators can filter them in analysis. F-10.7.5.
		result.SubmittedAfterConclusion = s.evalSubmittedAfterConclusion(r.Context(), tenantID, ec.ExperimentID)
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

	// spec: §16.1 line 164 / §10.7 line 1128 — record one lenny_eval_score
	// observation per submitted eval run so the rollback-trigger safety and
	// mean-score regression queries (rate(_sum)/rate(_count)) resolve per
	// scorer and variant. Only the aggregate score is observed; a submission
	// carrying only the per-dimension scores map has no scalar observation.
	// variant_id is empty for an un-enrolled session. F-10.7.13.
	if s.observeEvalScore != nil && stored.Score != nil {
		s.observeEvalScore(tenantID, stored.Scorer, stored.VariantID, *stored.Score)
	}

	s.writeEvalResponse(w, stored, http.StatusCreated)
}

// writeEvalResponse encodes an EvalResult as the §10.7 EvalResponse with
// the given status. A fresh insert uses 201 Created; an idempotent
// replay of an earlier submission uses 200 OK with the original record.
// spec: §10.7 lines 892-928, 940. F-10.7.4.
func (s *Server) writeEvalResponse(w http.ResponseWriter, stored evalstore.EvalResult, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
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
