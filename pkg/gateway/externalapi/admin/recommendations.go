// SPDX-License-Identifier: MIT

package admin

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/gateway/operability/recommendations"
)

// WithRecommendations wires the §25.3 GET /v1/admin/recommendations
// capacity-recommendations endpoint onto the Router. Call before
// Handler() so the mux picks it up.
func (r *Router) WithRecommendations(svc RecommendationService) *Router {
	r.recommendations = svc
	return r
}

// handleRecommendations implements GET /v1/admin/recommendations — the
// §25.3 capacity-recommendations endpoint. An optional ?category=
// query narrows the result to one §25.3 recommendation category.
func (r *Router) handleRecommendations(w http.ResponseWriter, req *http.Request) {
	var category *string
	if c := req.URL.Query().Get("category"); c != "" {
		category = &c
	}
	resp, err := r.recommendations.GetRecommendations(req.Context(), category)
	if err != nil {
		// spec: §25.3 lines 622-625 — map the service's typed errors to
		// the §25.3 error-code table rather than a flat INTERNAL_ERROR.
		switch {
		case errors.Is(err, recommendations.ErrUnknownCategory):
			writeError(w, http.StatusBadRequest, "UNKNOWN_RECOMMENDATION_CATEGORY", err.Error(), nil)
		case errors.Is(err, recommendations.ErrRecommendationsUnavailable):
			writeError(w, http.StatusServiceUnavailable, "RECOMMENDATIONS_UNAVAILABLE", err.Error(), nil)
		default:
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
