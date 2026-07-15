//go:build contract

// SPDX-License-Identifier: MIT

// Tier-3 contract tests for the §25.3 gateway-side ops endpoints served
// from pkg/gateway/externalapi/admin. The suite drives the production
// admin Router via httptest and pins the wire format of the four public
// read endpoints — capacity recommendations, the event buffer, the
// compiled-in version, and the redacted running config — plus the two
// §25.3 recommendation error envelopes, against the §25.2 canonical
// schema. A field rename or an envelope regression in any of these
// handlers surfaces here as a contract violation rather than shipping
// unguarded.
//
// spec: §25.3 line 556 — "GET /v1/admin/recommendations | Prioritized
// recommendations. Optional ?category= filter."
// spec: §25.3 line 621 — "UNKNOWN_RECOMMENDATION_CATEGORY | PERMANENT |
// 400 | Unrecognized category filter".
// spec: §25.3 line 622 — "RECOMMENDATIONS_UNAVAILABLE | TRANSIENT | 503".
// spec: §25.3 line 632 — "GET /v1/admin/platform/version | Compiled-in
// version info (gateway.version, gitCommit, buildDate, goVersion)".
// spec: §25.3 line 633 / 637 — "GET /v1/admin/platform/config |
// Effective running configuration (secrets redacted)" and "Secret
// values are redacted to \"***\"."
// spec: §25.3 line 729 — "GET /v1/admin/events/buffer | Recent events
// from in-memory buffer."
// spec: §25.2 — the canonical degradation envelope, the pagination
// envelope, and the error-response envelope every operability endpoint
// returns by reference.
package gateway_ops_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/operability/recommendations"
	"github.com/lennylabs/lenny/pkg/ops/conventions"
)

// adminPrincipal authenticates the request as a §10.2 platform-admin so
// the §25.3 endpoints' role gate admits it. The four endpoints are all
// platform-admin-gated reads.
func adminPrincipal(req *http.Request) *http.Request {
	return req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject:  "sa-prod-watchdog-01",
		TenantID: "platform",
		Roles:    []pkgauth.Role{pkgauth.RolePlatformAdmin},
	}))
}

// fixedClock pins the router's clock so the suite is deterministic.
func fixedClock() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

// get issues an authenticated GET against the admin Router and returns
// the recorder plus the raw decoded body, so a test can assert both the
// production-type decode and the exact JSON field names.
func get(t *testing.T, router *admin.Router, url string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, adminPrincipal(httptest.NewRequest(http.MethodGet, url, nil)))
	var raw map[string]any
	if rr.Body.Len() > 0 {
		if err := json.Unmarshal(rr.Body.Bytes(), &raw); err != nil {
			t.Fatalf("decode %s body as JSON object: %v (body=%s)", url, err, rr.Body.String())
		}
	}
	return rr, raw
}

// unavailableReader is a recommendations.MetricReader whose backing
// source reports unreachable, exercising the §25.3
// disableOnPrometheusOutage 503 path through the admin handler.
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

// TestRecommendationsResponseWireContract pins the §25.3
// GET /v1/admin/recommendations envelope: a top-level `recommendations`
// array of triggered entries and the §25.2 canonical `degradation`
// envelope carrying `level` and `thresholdSource`. With a rule's window
// holding data the envelope reports `healthy` with the gateway's
// compiled-in-defaults threshold source.
//
// spec: §25.3 line 556, §25.2 (canonical degradation envelope),
// §25.13 line 4848 (gateway runs the compiled-in defaults).
// diagnosis: a failure means the gateway recommendations endpoint's
// response envelope drifted from the §25.2 canonical schema — a renamed
// or dropped `recommendations`/`degradation` field, or a wrong
// thresholdSource — so an agent parsing the documented contract breaks.
func TestRecommendationsResponseWireContract(t *testing.T) {
	store := recommendations.NewWindowStore(7 * 24 * time.Hour)
	// A high credential-pool utilisation trips the CredentialPoolUndersized
	// rule so the response carries a triggered entry.
	store.Record("lenny_credential_pool_utilization", nil, 0.90)
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{Clock: fixedClock}).
		WithRecommendations(recommendations.NewCapacityService(store))

	rr, raw := get(t, router, "/v1/admin/recommendations")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	// The canonical field names are present under the documented keys.
	if _, ok := raw["recommendations"].([]any); !ok {
		t.Fatalf("body missing top-level `recommendations` array: %v", raw)
	}
	deg, ok := raw["degradation"].(map[string]any)
	if !ok {
		t.Fatalf("body missing §25.2 `degradation` envelope: %v", raw)
	}
	if deg["level"] != string(conventions.DegradationHealthy) {
		t.Errorf("degradation.level = %v, want %q", deg["level"], conventions.DegradationHealthy)
	}
	if deg["thresholdSource"] != string(conventions.ThresholdSourceCompiledInDefaults) {
		t.Errorf("degradation.thresholdSource = %v, want %q",
			deg["thresholdSource"], conventions.ThresholdSourceCompiledInDefaults)
	}

	// The production type round-trips and the triggered rule is present.
	var resp recommendations.RecommendationsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode RecommendationsResponse: %v", err)
	}
	found := false
	for _, rec := range resp.Recommendations {
		if rec.Rule == "CredentialPoolUndersized" {
			found = true
			if rec.Category == "" || rec.Priority == "" {
				t.Errorf("triggered recommendation missing category/priority: %+v", rec)
			}
		}
	}
	if !found {
		t.Errorf("recommendations must surface the triggered rule: %+v", resp.Recommendations)
	}
}

// TestRecommendationsPostRestartDegradedEnvelope pins the §25.3
// data-starved envelope: when every ring buffer is empty (for example
// shortly after a gateway restart) the response reports the §25.2
// canonical envelope as `degraded` with a warning, so an agent
// distinguishes a starved response from a healthy platform with no
// capacity issues.
//
// spec: §25.3 (Degradation) — "While every ring buffer is empty, no
// per-category recommendations are generated and the response's
// degradation envelope reports \"level\": \"degraded\" with a warning
// that no rule has data yet."
// diagnosis: a failure means the gateway stamps a healthy envelope on a
// data-starved recommendations response, so an agent cannot tell an
// empty array caused by starved windows from one caused by a healthy
// platform.
func TestRecommendationsPostRestartDegradedEnvelope(t *testing.T) {
	store := recommendations.NewWindowStore(7 * 24 * time.Hour) // no samples recorded
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{Clock: fixedClock}).
		WithRecommendations(recommendations.NewCapacityService(store))

	rr, raw := get(t, router, "/v1/admin/recommendations")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	deg, ok := raw["degradation"].(map[string]any)
	if !ok {
		t.Fatalf("body missing §25.2 `degradation` envelope: %v", raw)
	}
	if deg["level"] != string(conventions.DegradationDegraded) {
		t.Errorf("empty-window degradation.level = %v, want %q", deg["level"], conventions.DegradationDegraded)
	}
	warnings, ok := deg["warnings"].([]any)
	if !ok || len(warnings) == 0 {
		t.Errorf("data-starved response must carry a degradation warning, got %v", deg["warnings"])
	}
}

// TestRecommendationsDegradedToHealthyOnFirstSampleTransition pins the
// §25.2 canonical-envelope state transition against one long-lived
// service instance: the same WindowStore and CapacityService serve the
// endpoint twice, first while every ring buffer is empty and again
// immediately after one rule's source metric records a sample. The
// envelope must flip from degraded to healthy across that single
// session, distinct from the sibling tests that only pin each state in
// isolation against a freshly constructed service.
//
// spec: §25.2 (Omitted when healthy) — "an endpoint whose data quality
// also depends on in-process history reports \"level\": \"degraded\"
// while it holds no history at all and returns to \"level\": \"healthy\"
// once any of its rules records a sample".
// diagnosis: a failure means the recommendations handler's degradation
// envelope does not actually flip when the backing store gains its
// first sample — a stale-envelope, memoization, or marshal/omitempty
// regression that a fresh-per-test harness would not catch because it
// never observes the same service instance across the transition.
func TestRecommendationsDegradedToHealthyOnFirstSampleTransition(t *testing.T) {
	store := recommendations.NewWindowStore(7 * 24 * time.Hour)
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{Clock: fixedClock}).
		WithRecommendations(recommendations.NewCapacityService(store))

	// Before any sample is recorded, the store is empty and the envelope
	// reports degraded.
	rr, raw := get(t, router, "/v1/admin/recommendations")
	if rr.Code != http.StatusOK {
		t.Fatalf("pre-sample: status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	deg, ok := raw["degradation"].(map[string]any)
	if !ok {
		t.Fatalf("pre-sample: body missing §25.2 `degradation` envelope: %v", raw)
	}
	if deg["level"] != string(conventions.DegradationDegraded) {
		t.Fatalf("pre-sample: degradation.level = %v, want %q", deg["level"], conventions.DegradationDegraded)
	}
	if recs, ok := raw["recommendations"].([]any); !ok || len(recs) != 0 {
		t.Errorf("pre-sample: recommendations = %v, want an empty array", raw["recommendations"])
	}

	// Record one sample for a rule's source metric, at a value that
	// stays under the rule's threshold so the transition under test is
	// isolated to the envelope, not a newly triggered recommendation.
	store.Record("lenny_gateway_cpu_utilization_ratio", nil, 0.10)

	// The same router/service/store now reports healthy on the very next
	// request.
	rr, raw = get(t, router, "/v1/admin/recommendations")
	if rr.Code != http.StatusOK {
		t.Fatalf("post-sample: status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	deg, ok = raw["degradation"].(map[string]any)
	if !ok {
		t.Fatalf("post-sample: body missing §25.2 `degradation` envelope: %v", raw)
	}
	if deg["level"] != string(conventions.DegradationHealthy) {
		t.Errorf("post-sample: degradation.level = %v, want %q", deg["level"], conventions.DegradationHealthy)
	}
	if _, present := deg["warnings"]; present {
		t.Errorf("post-sample: degradation.warnings should be omitted once healthy, got %v", deg["warnings"])
	}

	// The production type round-trips both states faithfully.
	var resp recommendations.RecommendationsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode RecommendationsResponse: %v", err)
	}
	if resp.Degradation == nil || resp.Degradation.Level != conventions.DegradationHealthy {
		t.Errorf("decoded response degradation = %+v, want healthy", resp.Degradation)
	}
}

// TestRecommendationsUnknownCategoryErrorEnvelope pins the §25.3
// UNKNOWN_RECOMMENDATION_CATEGORY 400 against the §25.2 canonical error
// envelope: a `code`/`category`/`message`/`retryable` object under a
// top-level `error` key, classified PERMANENT and non-retryable.
//
// spec: §25.3 line 621 — "UNKNOWN_RECOMMENDATION_CATEGORY | PERMANENT |
// 400 | Unrecognized category filter".
// spec: §25.2 (error response envelope) — PERMANENT 4xx errors are not
// retryable.
// diagnosis: a failure means an unrecognised ?category= no longer maps
// to the documented 400 UNKNOWN_RECOMMENDATION_CATEGORY envelope, so an
// agent cannot distinguish a bad filter from a transient outage.
func TestRecommendationsUnknownCategoryErrorEnvelope(t *testing.T) {
	store := recommendations.NewWindowStore(7 * 24 * time.Hour)
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{Clock: fixedClock}).
		WithRecommendations(recommendations.NewCapacityService(store))

	rr, raw := get(t, router, "/v1/admin/recommendations?category=not_a_real_category")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", rr.Code, rr.Body.String())
	}
	env := errorEnvelope(t, raw)
	if env["code"] != "UNKNOWN_RECOMMENDATION_CATEGORY" {
		t.Errorf("error.code = %v, want UNKNOWN_RECOMMENDATION_CATEGORY", env["code"])
	}
	if env["category"] != string(conventions.CategoryPermanent) {
		t.Errorf("error.category = %v, want %q", env["category"], conventions.CategoryPermanent)
	}
	if env["retryable"] != false {
		t.Errorf("error.retryable = %v, want false for a PERMANENT error", env["retryable"])
	}
}

// TestRecommendationsUnavailableErrorEnvelope pins the §25.3
// RECOMMENDATIONS_UNAVAILABLE 503 against the §25.2 canonical error
// envelope: classified TRANSIENT and retryable. It is returned only
// under the disableOnPrometheusOutage opt-out when the source is down.
//
// spec: §25.3 line 622 — "RECOMMENDATIONS_UNAVAILABLE | TRANSIENT |
// 503 | Returned only when ops.recommendations.disableOnPrometheusOutage:
// true and Prometheus is unreachable."
// spec: §25.2 (error response envelope) — TRANSIENT 5xx errors are
// retryable.
// diagnosis: a failure means the disableOnPrometheusOutage opt-out no
// longer surfaces as the documented 503 RECOMMENDATIONS_UNAVAILABLE
// transient envelope, so an agent would not know to retry with backoff.
func TestRecommendationsUnavailableErrorEnvelope(t *testing.T) {
	svc := recommendations.NewCapacityServiceWithConfig(unavailableReader{},
		recommendations.Config{DisableOnPrometheusOutage: true})
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{Clock: fixedClock}).
		WithRecommendations(svc)

	rr, raw := get(t, router, "/v1/admin/recommendations")
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body=%s)", rr.Code, rr.Body.String())
	}
	env := errorEnvelope(t, raw)
	if env["code"] != "RECOMMENDATIONS_UNAVAILABLE" {
		t.Errorf("error.code = %v, want RECOMMENDATIONS_UNAVAILABLE", env["code"])
	}
	if env["category"] != string(conventions.CategoryTransient) {
		t.Errorf("error.category = %v, want %q", env["category"], conventions.CategoryTransient)
	}
	if env["retryable"] != true {
		t.Errorf("error.retryable = %v, want true for a TRANSIENT error", env["retryable"])
	}
}

// TestEventBufferPageWireContract pins the §25.3
// GET /v1/admin/events/buffer response: a top-level `events` array and
// the §25.2 canonical `pagination` envelope carrying `cursor`,
// `hasMore`, and the buffer's `cursorKind` of `buffer-seq`.
//
// spec: §25.3 line 729 — "GET /v1/admin/events/buffer | Recent events
// from in-memory buffer."
// spec: §25.3 (Gateway Event Buffer, BufferedEventPage) and §25.2
// (pagination envelope) — the cursor is the monotonic id of the last
// event returned and hasMore reports whether more remain.
// diagnosis: a failure means the event-buffer endpoint's page envelope
// drifted from the §25.2 pagination schema — a renamed events/cursor/
// hasMore field or a wrong cursorKind — so lenny-ops's buffer-fallback
// poller breaks.
func TestEventBufferPageWireContract(t *testing.T) {
	buf := eventbuffer.NewEventBuffer(0)
	buf.Append(events.OperationalEvent{
		ID: "alert_fired", SpecVersion: "1.0.2", Type: "dev.lenny.alert_fired",
		Severity: "critical", Time: fixedClock(),
	})
	buf.Append(events.OperationalEvent{
		ID: "pool_state_changed", SpecVersion: "1.0.2", Type: "dev.lenny.pool_state_changed",
		Severity: "info", Time: fixedClock(),
	})
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{Clock: fixedClock}).
		WithEventBuffer(buf)

	rr, raw := get(t, router, "/v1/admin/events/buffer")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	if _, ok := raw["events"].([]any); !ok {
		t.Fatalf("body missing top-level `events` array: %v", raw)
	}
	pag, ok := raw["pagination"].(map[string]any)
	if !ok {
		t.Fatalf("body missing §25.2 `pagination` envelope: %v", raw)
	}
	// cursor is a JSON number (monotonic buffer id); the second appended
	// event holds id 2, so the page's cursor is 2.
	if cur, _ := pag["cursor"].(float64); cur != 2 {
		t.Errorf("pagination.cursor = %v, want 2", pag["cursor"])
	}
	if pag["cursorKind"] != "buffer-seq" {
		t.Errorf("pagination.cursorKind = %v, want buffer-seq", pag["cursorKind"])
	}
	if _, ok := pag["hasMore"]; !ok {
		t.Errorf("pagination missing `hasMore` field: %v", pag)
	}

	// The production type round-trips faithfully.
	var page events.BufferedEventPage
	if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode BufferedEventPage: %v", err)
	}
	if len(page.Events) != 2 || page.Pagination.Cursor != 2 {
		t.Errorf("page: %d events, cursor %d; want 2, 2", len(page.Events), page.Pagination.Cursor)
	}
}

// TestPlatformVersionWireContract pins the §25.3
// GET /v1/admin/platform/version response fields: the gateway's own
// compiled-in metadata under gatewayVersion, gitCommit, buildDate, and
// goVersion.
//
// spec: §25.3 line 632 — "Compiled-in version info (gateway.version,
// gitCommit, buildDate, goVersion)".
// diagnosis: a failure means the platform version endpoint's field set
// drifted from the documented contract, so lenny-ops's version
// aggregation (§25.8), which decodes gatewayVersion from this body,
// breaks.
func TestPlatformVersionWireContract(t *testing.T) {
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{Clock: fixedClock}).
		WithPlatformInfo(admin.PlatformInfo{
			Version:   "1.2.3",
			GitCommit: "abc123",
			BuildDate: "2026-01-01",
		}, nil)

	rr, raw := get(t, router, "/v1/admin/platform/version")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	for _, field := range []string{"gatewayVersion", "gitCommit", "buildDate", "goVersion"} {
		if v, ok := raw[field].(string); !ok || v == "" {
			t.Errorf("platform/version missing non-empty %q field: %v", field, raw)
		}
	}
	if raw["gatewayVersion"] != "1.2.3" || raw["gitCommit"] != "abc123" {
		t.Errorf("platform/version values: %v", raw)
	}
}

// TestPlatformConfigRedactionWireContract pins the §25.3
// GET /v1/admin/platform/config response: a `config` array of key/value
// entries with every secret-bearing value redacted to "***" and
// non-secret values passed through.
//
// spec: §25.3 line 633 / line 637 — "Effective running configuration
// (secrets redacted)" and "Secret values are redacted to \"***\"."
// diagnosis: a failure means the platform config endpoint stopped
// redacting a secret-bearing value or dropped the documented config
// envelope, either leaking secret material or breaking a config reader.
func TestPlatformConfigRedactionWireContract(t *testing.T) {
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{Clock: fixedClock}).
		WithPlatformInfo(admin.PlatformInfo{}, map[string]string{
			"gateway.addr":      ":8080",
			"jwt.signingSecret": "super-secret-value",
			"redis.password":    "hunter2",
			"oauth.clientId":    "public-client-id",
		})

	rr, raw := get(t, router, "/v1/admin/platform/config")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	entries, ok := raw["config"].([]any)
	if !ok {
		t.Fatalf("body missing top-level `config` array: %v", raw)
	}
	got := map[string]string{}
	for _, e := range entries {
		m, ok := e.(map[string]any)
		if !ok {
			t.Fatalf("config entry is not a key/value object: %v", e)
		}
		got[m["key"].(string)] = m["value"].(string)
	}
	if got["gateway.addr"] != ":8080" {
		t.Errorf("non-secret value should pass through: %q", got["gateway.addr"])
	}
	for _, secretKey := range []string{"jwt.signingSecret", "redis.password"} {
		if got[secretKey] != "***" {
			t.Errorf("%s must be redacted to ***, got %q", secretKey, got[secretKey])
		}
	}
}

// errorEnvelope extracts the §25.2 canonical error envelope's inner
// object, failing the test when the body is not an error envelope.
func errorEnvelope(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	obj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("response is not a §25.2 error envelope: %v", body)
	}
	return obj
}
