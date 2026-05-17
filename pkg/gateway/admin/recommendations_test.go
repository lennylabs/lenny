// SPDX-License-Identifier: MIT

package admin_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/recommendations"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

func newRecommendationsAdmin(t *testing.T, store *recommendations.WindowStore) *admin.Router {
	t.Helper()
	return admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithRecommendations(recommendations.NewCapacityService(store))
}

func TestRecommendationsEndpointSurfacesTriggeredRule(t *testing.T) {
	store := recommendations.NewWindowStore(7 * 24 * time.Hour)
	store.Record("lenny_credential_pool_utilization", nil, 0.90)
	router := newRecommendationsAdmin(t, store)

	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/recommendations", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp recommendations.RecommendationsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	found := false
	for _, rec := range resp.Recommendations {
		if rec.Rule == "CredentialPoolUndersized" {
			found = true
		}
	}
	if !found {
		t.Errorf("endpoint must surface the triggered rule: %+v", resp.Recommendations)
	}
}

func TestRecommendationsEndpointCategoryFilter(t *testing.T) {
	store := recommendations.NewWindowStore(7 * 24 * time.Hour)
	store.Record("lenny_credential_pool_utilization", nil, 0.90)
	router := newRecommendationsAdmin(t, store)

	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet,
		"/v1/admin/recommendations?category=gateway_scaling", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var resp recommendations.RecommendationsResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	// Credential utilisation is high, but the filter selects only the
	// gateway_scaling category, which has no triggered rule.
	if len(resp.Recommendations) != 0 {
		t.Errorf("category filter: got %+v, want empty", resp.Recommendations)
	}
}

func TestRecommendationsEndpointRequiresAdmin(t *testing.T) {
	store := recommendations.NewWindowStore(7 * 24 * time.Hour)
	router := newRecommendationsAdmin(t, store)

	// No admin principal on the request — the endpoint must reject it.
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/recommendations", nil)
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden && rr.Code != http.StatusUnauthorized {
		t.Errorf("recommendations without an admin principal: status %d, want 401/403", rr.Code)
	}
}
