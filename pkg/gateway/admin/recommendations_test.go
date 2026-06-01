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

// unavailableReader is a recommendations.MetricReader that reports its
// backing source as unreachable so the §25.3 disableOnPrometheusOutage
// path can be exercised end-to-end through the admin handler.
type unavailableReader struct{}

func (unavailableReader) GaugeValue(string, map[string]string) (float64, bool)   { return 0, false }
func (unavailableReader) CounterValue(string, map[string]string) (float64, bool) { return 0, false }
func (unavailableReader) HistogramQuantile(string, map[string]string, float64) (float64, bool) {
	return 0, false
}
func (unavailableReader) WindowedRate(string, map[string]string, time.Duration) (float64, bool) {
	return 0, false
}
func (unavailableReader) Available() bool { return false }

// TestRecommendationsEndpointUnknownCategory_spec_25_3_624 asserts an
// unrecognised ?category= returns 400 UNKNOWN_RECOMMENDATION_CATEGORY
// rather than a flat INTERNAL_ERROR.
func TestRecommendationsEndpointUnknownCategory_spec_25_3_624(t *testing.T) {
	store := recommendations.NewWindowStore(7 * 24 * time.Hour)
	router := newRecommendationsAdmin(t, store)

	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet,
		"/v1/admin/recommendations?category=scaling_things", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d, want 400", rr.Code)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body.Error.Code != "UNKNOWN_RECOMMENDATION_CATEGORY" {
		t.Errorf("error code = %q, want UNKNOWN_RECOMMENDATION_CATEGORY", body.Error.Code)
	}
}

// TestRecommendationsEndpointUnavailable_spec_25_3_625 asserts the
// disableOnPrometheusOutage opt-out surfaces as 503
// RECOMMENDATIONS_UNAVAILABLE through the admin handler.
func TestRecommendationsEndpointUnavailable_spec_25_3_625(t *testing.T) {
	svc := recommendations.NewCapacityServiceWithConfig(unavailableReader{},
		recommendations.Config{DisableOnPrometheusOutage: true})
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithRecommendations(svc)

	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/recommendations", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: %d, want 503", rr.Code)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body.Error.Code != "RECOMMENDATIONS_UNAVAILABLE" {
		t.Errorf("error code = %q, want RECOMMENDATIONS_UNAVAILABLE", body.Error.Code)
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
