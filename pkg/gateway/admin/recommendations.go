// SPDX-License-Identifier: MIT

package admin

import (
	"encoding/json"
	"net/http"
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
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
