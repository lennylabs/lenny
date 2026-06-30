// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/experiment"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/experimentstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

// spec: §10.7 / §15.1 experiment admin CRUD.

func newExperimentAdmin(t *testing.T) (*admin.Router, experimentstore.Store, *recordingAudit) {
	t.Helper()
	exps := experimentstore.NewMemory()
	audit := &recordingAudit{}
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC) },
		Audit: audit,
	}).WithExperiments(exps)
	return router, exps, audit
}

func validExperimentPayload(id string) admin.ExperimentPayload {
	return admin.ExperimentPayload{
		ID:          id,
		TenantID:    "acme",
		Status:      "active",
		BaseRuntime: "claude-worker",
		Variants: []admin.ExperimentVariant{
			{ID: "treatment", Runtime: "claude-worker-v2", Pool: "cw-v2-pool", Weight: 0.1},
		},
		Targeting:   admin.ExperimentTargeting{Mode: "percentage", Sticky: "user"},
		Propagation: admin.ExperimentPropagation{ChildSessions: "inherit"},
	}
}

func doAdminReq(t *testing.T, h http.Handler, method, path string, body any, as func(*http.Request) *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req := as(httptest.NewRequest(method, path, rdr))
	if method == http.MethodPut && req.Header.Get("If-Match") == "" {
		// spec: §15.1 lines 1207-1211 — pool and experiment PUTs enforce
		// If-Match. Cases that do not exercise the precondition directly
		// reach the handler by carrying the resource's current ETag, fetched
		// via a GET against the same resource. A test that drives the
		// precondition sets If-Match explicitly and is left untouched.
		if getPath := adminETagGetPath(path); getPath != "" {
			g := as(httptest.NewRequest(http.MethodGet, getPath, nil))
			grr := httptest.NewRecorder()
			h.ServeHTTP(grr, g)
			if etag := grr.Header().Get("ETag"); etag != "" {
				req.Header.Set("If-Match", etag)
			}
		}
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// injectAdminIfMatch fills in the §15.1 If-Match header on a pre-built
// admin PUT request from the resource's current ETag, so a test that is
// not exercising the precondition itself still reaches the handler. It is
// a no-op for a non-PUT request, a request that already carries If-Match,
// or a path with no ETag-bearing GET (adminETagGetPath returns ""). The
// GET reuses the request's context so it carries the same authenticated
// principal. spec: §15.1 lines 1207-1211.
func injectAdminIfMatch(t *testing.T, h http.Handler, req *http.Request) {
	t.Helper()
	if req.Method != http.MethodPut || req.Header.Get("If-Match") != "" {
		return
	}
	getPath := adminETagGetPath(req.URL.RequestURI())
	if getPath == "" {
		return
	}
	g := httptest.NewRequest(http.MethodGet, getPath, nil).WithContext(req.Context())
	grr := httptest.NewRecorder()
	h.ServeHTTP(grr, g)
	if etag := grr.Header().Get("ETag"); etag != "" {
		req.Header.Set("If-Match", etag)
	}
}

// adminETagGetPath maps a PUT path to the GET path that returns the
// resource's current ETag, or "" when the route does not enforce
// If-Match. Pools read straight off the same path; experiments need the
// tenantId query (the platform-admin GET requires it) so the lookup
// resolves the tenant the fixtures use. spec: §15.1 lines 1207-1211.
func adminETagGetPath(putPath string) string {
	if strings.HasPrefix(putPath, "/v1/admin/pools/") {
		if strings.HasSuffix(putPath, "/warm-count") {
			return ""
		}
		return putPath
	}
	if strings.HasPrefix(putPath, "/v1/admin/experiments/") {
		name := strings.TrimPrefix(putPath, "/v1/admin/experiments/")
		if i := strings.IndexByte(name, '?'); i >= 0 {
			name = name[:i]
		}
		if name == "" {
			return ""
		}
		return "/v1/admin/experiments/" + name + "?tenantId=acme"
	}
	if strings.HasPrefix(putPath, "/v1/admin/delegation-policies/") {
		name := strings.TrimPrefix(putPath, "/v1/admin/delegation-policies/")
		if i := strings.IndexByte(name, '?'); i >= 0 {
			name = name[:i]
		}
		if name == "" {
			return ""
		}
		// The GET resolves the tenant from the principal (the as func is
		// applied to the GET request), so no tenant query is needed.
		return "/v1/admin/delegation-policies/" + name
	}
	if strings.HasPrefix(putPath, "/v1/admin/interceptors/") {
		name := strings.TrimPrefix(putPath, "/v1/admin/interceptors/")
		if i := strings.IndexByte(name, '?'); i >= 0 {
			name = name[:i]
		}
		if name == "" {
			return ""
		}
		// Interceptors are platform-scoped, so the GET reads off the same
		// path with no tenant query.
		return "/v1/admin/interceptors/" + name
	}
	// Custom roles live under the tenant-scoped path
	// /v1/admin/tenants/{id}/roles/{name}; the GET reads off the same path
	// (the tenant is resolved from the path segment, so no query is needed).
	if strings.HasPrefix(putPath, "/v1/admin/tenants/") && strings.Contains(putPath, "/roles/") {
		if i := strings.IndexByte(putPath, '?'); i >= 0 {
			return putPath[:i]
		}
		return putPath
	}
	// The tenant resource PUT and its rbac-config / elicitation-content-
	// integrity sub-resource PUTs all carry the tenant row's version as
	// their entity tag, and each sub-resource GET reads off the same path,
	// so the lookup returns the PUT path verbatim (query stripped).
	if strings.HasPrefix(putPath, "/v1/admin/tenants/") {
		p := putPath
		if i := strings.IndexByte(p, '?'); i >= 0 {
			p = p[:i]
		}
		rest := strings.TrimPrefix(p, "/v1/admin/tenants/")
		// A bare {id} or the rbac-config / elicitation-content-integrity
		// sub-resource; other tenant sub-paths are not If-Match PUTs.
		if rest != "" && (!strings.Contains(rest, "/") ||
			strings.HasSuffix(p, "/rbac-config") ||
			strings.HasSuffix(p, "/elicitation-content-integrity")) {
			return p
		}
		return ""
	}
	// Runtimes are platform-global; the GET reads by name. Only the
	// top-level resource PUT enforces If-Match (the /tenant-access and
	// /regenerate-cards sub-routes are POST/DELETE), so a name carrying a
	// further path segment is not an ETag-bearing PUT.
	if strings.HasPrefix(putPath, "/v1/admin/runtimes/") {
		name := etagResourceName(putPath, "/v1/admin/runtimes/")
		if name == "" {
			return ""
		}
		return "/v1/admin/runtimes/" + name
	}
	// Environments and users are tenant-scoped under acme in the fixtures.
	// A platform-admin GET requires the tenantId query; a tenant-admin
	// principal ignores it and resolves from the principal, so the query is
	// harmless either way.
	for _, prefix := range []string{"/v1/admin/environments/", "/v1/admin/users/"} {
		if strings.HasPrefix(putPath, prefix) {
			name := strings.TrimPrefix(putPath, prefix)
			if i := strings.IndexByte(name, '?'); i >= 0 {
				name = name[:i]
			}
			if name == "" {
				return ""
			}
			return prefix + name + "?tenantId=acme"
		}
	}
	// External adapters are platform-global; the GET reads by name with no
	// tenant query. Only the top-level resource PUT enforces If-Match (the
	// /validate sub-route is a POST), so a name carrying a further path
	// segment is not an ETag-bearing PUT.
	if strings.HasPrefix(putPath, "/v1/admin/external-adapters/") {
		name := etagResourceName(putPath, "/v1/admin/external-adapters/")
		if name == "" {
			return ""
		}
		return "/v1/admin/external-adapters/" + name
	}
	// Connectors are tenant-scoped; a platform-admin GET with no tenant_id
	// query resolves the row across tenants by id, so the bare path suffices.
	if strings.HasPrefix(putPath, "/v1/admin/connectors/") {
		name := etagResourceName(putPath, "/v1/admin/connectors/")
		if name == "" {
			return ""
		}
		return "/v1/admin/connectors/" + name
	}
	// Credential pools are tenant-scoped; a platform-admin GET requires the
	// tenantId query, so resolve against the acme fixture tenant. The
	// per-credential sub-resource PUTs (.../credentials/{id}) are §24.5
	// operations outside the §15.1 If-Match contract — etagResourceName
	// returns "" for any path carrying a further segment.
	if strings.HasPrefix(putPath, "/v1/admin/credential-pools/") {
		name := etagResourceName(putPath, "/v1/admin/credential-pools/")
		if name == "" {
			return ""
		}
		return "/v1/admin/credential-pools/" + name + "?tenantId=acme"
	}
	return ""
}

// etagResourceName extracts the single resource segment after prefix in a
// PUT path, dropping any query string. It returns "" when the segment is
// empty or carries a further `/` (a sub-resource path, which the §15.1
// If-Match contract does not cover).
func etagResourceName(putPath, prefix string) string {
	name := strings.TrimPrefix(putPath, prefix)
	if i := strings.IndexByte(name, '?'); i >= 0 {
		name = name[:i]
	}
	if name == "" || strings.Contains(name, "/") {
		return ""
	}
	return name
}

func TestCreateExperiment(t *testing.T) {
	router, exps, audit := newExperimentAdmin(t)
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/experiments",
		validExperimentPayload("exp_1"), withAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rr.Code, rr.Body.String())
	}
	got, err := exps.Get(context.Background(), "acme", "exp_1")
	if err != nil {
		t.Fatalf("Get after create: %v", err)
	}
	if got.BaseRuntime != "claude-worker" || got.Status != experiment.StatusActive {
		t.Errorf("stored experiment = %+v", got)
	}
	if snap := audit.snapshot(); len(snap) != 1 || snap[0].Type != "admin.experiment.created" {
		t.Errorf("audit: %+v", snap)
	}
}

func TestCreateExperimentRejectsReservedVariant(t *testing.T) {
	router, _, _ := newExperimentAdmin(t)
	body := validExperimentPayload("exp_bad")
	body.Variants = []admin.ExperimentVariant{{ID: "control", Weight: 0.1}}
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/experiments", body, withAdminPrincipal)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("reserved variant id: status %d, want 422", rr.Code)
	}
}

func TestCreateExperimentRejectsDuplicate(t *testing.T) {
	router, _, _ := newExperimentAdmin(t)
	doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/experiments",
		validExperimentPayload("exp_dup"), withAdminPrincipal)
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/experiments",
		validExperimentPayload("exp_dup"), withAdminPrincipal)
	if rr.Code != http.StatusConflict {
		t.Errorf("duplicate create: status %d, want 409", rr.Code)
	}
}

func TestListExperiments(t *testing.T) {
	router, _, _ := newExperimentAdmin(t)
	doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/experiments",
		validExperimentPayload("exp_a"), withAdminPrincipal)
	doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/experiments",
		validExperimentPayload("exp_b"), withAdminPrincipal)

	rr := doAdminReq(t, router.Handler(), http.MethodGet, "/v1/admin/experiments?tenantId=acme",
		nil, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("list: status %d", rr.Code)
	}
	var resp struct {
		Experiments []admin.ExperimentPayload `json:"items"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.Experiments) != 2 {
		t.Errorf("list returned %d experiments, want 2", len(resp.Experiments))
	}
}

func TestGetExperimentNotFound(t *testing.T) {
	router, _, _ := newExperimentAdmin(t)
	rr := doAdminReq(t, router.Handler(), http.MethodGet, "/v1/admin/experiments/absent?tenantId=acme",
		nil, withAdminPrincipal)
	if rr.Code != http.StatusNotFound {
		t.Errorf("get unknown: status %d, want 404", rr.Code)
	}
}

func TestUpdateExperimentPreservesStatus(t *testing.T) {
	router, exps, _ := newExperimentAdmin(t)
	doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/experiments",
		validExperimentPayload("exp_u"), withAdminPrincipal)
	// Transition to paused, then PUT with status "active" in the body.
	doAdminReq(t, router.Handler(), http.MethodPatch, "/v1/admin/experiments/exp_u?tenantId=acme",
		admin.PatchExperimentRequest{Status: ptr("paused")}, withAdminPrincipal)

	body := validExperimentPayload("exp_u")
	body.Status = "active" // PUT must not transition status
	body.BaseRuntime = "claude-worker-next"
	rr := doAdminReq(t, router.Handler(), http.MethodPut, "/v1/admin/experiments/exp_u",
		body, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("update: status %d, body %s", rr.Code, rr.Body.String())
	}
	got, _ := exps.Get(context.Background(), "acme", "exp_u")
	if got.BaseRuntime != "claude-worker-next" {
		t.Errorf("PUT did not update baseRuntime: %q", got.BaseRuntime)
	}
	if got.Status != experiment.StatusPaused {
		t.Errorf("PUT changed status to %q — status transitions go through PATCH only", got.Status)
	}
}

func TestPatchExperimentTransitionsStatus(t *testing.T) {
	router, exps, audit := newExperimentAdmin(t)
	doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/experiments",
		validExperimentPayload("exp_p"), withAdminPrincipal)

	rr := doAdminReq(t, router.Handler(), http.MethodPatch, "/v1/admin/experiments/exp_p?tenantId=acme",
		admin.PatchExperimentRequest{Status: ptr("paused")}, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("patch: status %d, body %s", rr.Code, rr.Body.String())
	}
	got, _ := exps.Get(context.Background(), "acme", "exp_p")
	if got.Status != experiment.StatusPaused {
		t.Errorf("status = %q, want paused", got.Status)
	}
	found := false
	for _, ev := range audit.snapshot() {
		if ev.Type == "experiment.status_changed" {
			found = true
		}
	}
	if !found {
		t.Error("a status transition must emit experiment.status_changed")
	}
}

func TestPatchExperimentRejectsInvalidTransition(t *testing.T) {
	router, _, _ := newExperimentAdmin(t)
	doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/experiments",
		validExperimentPayload("exp_c"), withAdminPrincipal)
	// active → concluded is valid; concluded → active is not.
	doAdminReq(t, router.Handler(), http.MethodPatch, "/v1/admin/experiments/exp_c?tenantId=acme",
		admin.PatchExperimentRequest{Status: ptr("concluded")}, withAdminPrincipal)
	rr := doAdminReq(t, router.Handler(), http.MethodPatch, "/v1/admin/experiments/exp_c?tenantId=acme",
		admin.PatchExperimentRequest{Status: ptr("active")}, withAdminPrincipal)
	if rr.Code != http.StatusConflict {
		t.Errorf("concluded → active: status %d, want 409", rr.Code)
	}
}

// spec: §10.7 line 1094 — concluded experiments are immutable; delete
// is permitted only after the operator transitions the experiment to
// `concluded` via PATCH. F-10.7.17.
func TestDeleteExperiment(t *testing.T) {
	router, exps, _ := newExperimentAdmin(t)
	doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/experiments",
		validExperimentPayload("exp_d"), withAdminPrincipal)
	doAdminReq(t, router.Handler(), http.MethodPatch, "/v1/admin/experiments/exp_d?tenantId=acme",
		map[string]any{"status": "concluded"}, withAdminPrincipal)

	rr := doAdminReq(t, router.Handler(), http.MethodDelete, "/v1/admin/experiments/exp_d?tenantId=acme",
		nil, withAdminPrincipal)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete: status %d, body %s", rr.Code, rr.Body.String())
	}
	if _, err := exps.Get(context.Background(), "acme", "exp_d"); err == nil {
		t.Error("experiment still present after delete")
	}
}

// spec: §10.7 line 1094 — deleting an active or paused experiment
// would orphan its variant pool and enrolled-session eval attribution;
// the handler must reject with 409 INVALID_STATE_TRANSITION until the
// operator PATCHes to `concluded`. F-10.7.17.
func TestDeleteExperimentRejectsActive(t *testing.T) {
	router, exps, _ := newExperimentAdmin(t)
	doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/experiments",
		validExperimentPayload("exp_active"), withAdminPrincipal)

	rr := doAdminReq(t, router.Handler(), http.MethodDelete, "/v1/admin/experiments/exp_active?tenantId=acme",
		nil, withAdminPrincipal)
	if rr.Code != http.StatusConflict {
		t.Fatalf("delete active: status %d, want 409", rr.Code)
	}
	var resp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if errBlk, _ := resp["error"].(map[string]any); errBlk["code"] != "INVALID_STATE_TRANSITION" {
		t.Errorf("delete active: code %v, want INVALID_STATE_TRANSITION", errBlk["code"])
	}
	if _, err := exps.Get(context.Background(), "acme", "exp_active"); err != nil {
		t.Error("experiment removed despite reject")
	}
}

// spec: §10.7 line 703 / §15.1 line 1005 — reserved-identifier
// violation surfaces as 422 RESERVED_IDENTIFIER with details.field +
// details.value, distinct from VALIDATION_ERROR. F-10.7.11.
func TestCreateExperimentReservedIdentifierCode(t *testing.T) {
	router, _, _ := newExperimentAdmin(t)
	body := validExperimentPayload("exp_resv")
	body.Variants = []admin.ExperimentVariant{{ID: "control", Weight: 0.1}}
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/experiments", body, withAdminPrincipal)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("reserved: status %d, want 422", rr.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	errBlk, _ := resp["error"].(map[string]any)
	if errBlk["code"] != "RESERVED_IDENTIFIER" {
		t.Fatalf("code = %v, want RESERVED_IDENTIFIER", errBlk["code"])
	}
	details, _ := errBlk["details"].(map[string]any)
	if details["field"] != "variants[0].id" {
		t.Errorf("details.field = %v, want variants[0].id", details["field"])
	}
	if details["value"] != "control" {
		t.Errorf("details.value = %v, want \"control\"", details["value"])
	}
}

// spec: §4.6.2 line 545 — Σ variant_weights ≥ 1 surfaces as 422
// INVALID_VARIANT_WEIGHTS, distinct from VALIDATION_ERROR. F-10.7.11.
func TestCreateExperimentInvalidVariantWeightsCode(t *testing.T) {
	router, _, _ := newExperimentAdmin(t)
	body := validExperimentPayload("exp_weights")
	body.Variants = []admin.ExperimentVariant{
		{ID: "a", Weight: 0.5},
		{ID: "b", Weight: 0.5},
	}
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/experiments", body, withAdminPrincipal)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("sum>=1: status %d, want 422", rr.Code)
	}
	var resp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	errBlk, _ := resp["error"].(map[string]any)
	if errBlk["code"] != "INVALID_VARIANT_WEIGHTS" {
		t.Errorf("code = %v, want INVALID_VARIANT_WEIGHTS", errBlk["code"])
	}
}

func TestExperimentRequiresAdmin(t *testing.T) {
	router, _, _ := newExperimentAdmin(t)
	asPlainUser := func(req *http.Request) *http.Request {
		ctx := authmw.WithPrincipal(req.Context(), authmw.Principal{
			Subject: "alice@acme.com", TenantID: "acme",
			Roles: []pkgauth.Role{pkgauth.RoleUser},
		})
		return req.WithContext(ctx)
	}
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/experiments",
		validExperimentPayload("exp_x"), asPlainUser)
	if rr.Code != http.StatusForbidden {
		t.Errorf("plain user create: status %d, want 403", rr.Code)
	}
}

func TestExperimentTenantAdminScoped(t *testing.T) {
	router, exps, _ := newExperimentAdmin(t)
	// A tenant-admin omits tenantId; the handler derives it from the token.
	body := validExperimentPayload("exp_ta")
	body.TenantID = ""
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/experiments",
		body, withTenantAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Fatalf("tenant-admin create: status %d, body %s", rr.Code, rr.Body.String())
	}
	if _, err := exps.Get(context.Background(), "acme", "exp_ta"); err != nil {
		t.Errorf("tenant-admin's experiment not stored under its tenant: %v", err)
	}
}

func ptr(s string) *string { return &s }

// weightedPayload builds a §10.7 experiment payload with a single
// variant of the given weight on the given base runtime.
func weightedPayload(id, baseRuntime string, weight float64) admin.ExperimentPayload {
	p := validExperimentPayload(id)
	p.BaseRuntime = baseRuntime
	p.Variants = []admin.ExperimentVariant{{ID: "treatment", Weight: weight}}
	return p
}

// TestCreateExperimentRejectsCrossExperimentWeights_spec_4_6_2 pins
// §4.6.2 line 545: Σ variant_weights across all active experiments on
// the same base runtime must stay < 1, rejected at admission with
// INVALID_VARIANT_WEIGHTS. F-10.7.8.
func TestCreateExperimentRejectsCrossExperimentWeights_spec_4_6_2(t *testing.T) {
	router, _, _ := newExperimentAdmin(t)
	if rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/experiments",
		weightedPayload("exp_a", "claude-worker", 0.6), withAdminPrincipal); rr.Code != http.StatusCreated {
		t.Fatalf("first experiment: status %d, body %s", rr.Code, rr.Body.String())
	}
	// A second active experiment on the same base would push the aggregate
	// to 1.1 — the base pool would have no control-group remainder.
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/experiments",
		weightedPayload("exp_b", "claude-worker", 0.5), withAdminPrincipal)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("cross-experiment Σ≥1: status %d, want 422, body %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	errBlk, _ := resp["error"].(map[string]any)
	if errBlk["code"] != "INVALID_VARIANT_WEIGHTS" {
		t.Errorf("code = %v, want INVALID_VARIANT_WEIGHTS", errBlk["code"])
	}
}

// TestCreateExperimentCrossWeightsScopedToBaseRuntime_spec_4_6_2 pins
// that experiments on different base runtimes do not aggregate. F-10.7.8.
func TestCreateExperimentCrossWeightsScopedToBaseRuntime_spec_4_6_2(t *testing.T) {
	router, _, _ := newExperimentAdmin(t)
	doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/experiments",
		weightedPayload("exp_a", "claude-worker", 0.6), withAdminPrincipal)
	// Same weight, different base runtime: admitted (disjoint pools).
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/experiments",
		weightedPayload("exp_b", "gemini-worker", 0.6), withAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Fatalf("different base runtime: status %d, want 201, body %s", rr.Code, rr.Body.String())
	}
}

// TestCreateExperimentCrossWeightsIgnoresInactiveSibling_spec_4_6_2 pins
// that only active experiments contribute to the aggregate — a paused
// sibling diverts no traffic. F-10.7.8.
func TestCreateExperimentCrossWeightsIgnoresInactiveSibling_spec_4_6_2(t *testing.T) {
	router, _, _ := newExperimentAdmin(t)
	paused := weightedPayload("exp_a", "claude-worker", 0.6)
	paused.Status = "paused"
	if rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/experiments",
		paused, withAdminPrincipal); rr.Code != http.StatusCreated {
		t.Fatalf("paused experiment: status %d, body %s", rr.Code, rr.Body.String())
	}
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/experiments",
		weightedPayload("exp_b", "claude-worker", 0.6), withAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Fatalf("active sibling with paused peer: status %d, want 201, body %s", rr.Code, rr.Body.String())
	}
}

// TestCreateExperimentDryRun_spec_15_1_1140 pins §15.1 line 1140:
// ?dryRun=true performs validation, returns the computed representation
// with X-Dry-Run: true, but persists nothing and emits no audit event.
// F-10.7.15.
func TestCreateExperimentDryRun_spec_15_1_1140(t *testing.T) {
	router, exps, audit := newExperimentAdmin(t)
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/experiments?dryRun=true",
		validExperimentPayload("exp_dry"), withAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Fatalf("dryRun create: status %d, want 201, body %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-Dry-Run") != "true" {
		t.Errorf("X-Dry-Run = %q, want true", rr.Header().Get("X-Dry-Run"))
	}
	if _, err := exps.Get(context.Background(), "acme", "exp_dry"); !errors.Is(err, experimentstore.ErrNotFound) {
		t.Errorf("dryRun create persisted the experiment: err=%v", err)
	}
	if snap := audit.snapshot(); len(snap) != 0 {
		t.Errorf("dryRun create emitted audit events: %+v", snap)
	}
}

// TestCreateExperimentDryRunStillValidates_spec_15_1_1140 pins that the
// dry-run path runs full validation (reserved variant id rejected) and
// persists nothing on failure. F-10.7.15.
func TestCreateExperimentDryRunStillValidates_spec_15_1_1140(t *testing.T) {
	router, exps, _ := newExperimentAdmin(t)
	body := validExperimentPayload("exp_bad_dry")
	body.Variants = []admin.ExperimentVariant{{ID: "control", Weight: 0.1}}
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/experiments?dryRun=true",
		body, withAdminPrincipal)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("dryRun invalid: status %d, want 422, body %s", rr.Code, rr.Body.String())
	}
	if _, err := exps.Get(context.Background(), "acme", "exp_bad_dry"); !errors.Is(err, experimentstore.ErrNotFound) {
		t.Errorf("failed dryRun persisted the experiment: err=%v", err)
	}
}

// TestUpdateExperimentDryRun_spec_15_1_1140 pins that PUT ?dryRun=true
// validates and previews without mutating the stored definition. F-10.7.15.
func TestUpdateExperimentDryRun_spec_15_1_1140(t *testing.T) {
	router, exps, _ := newExperimentAdmin(t)
	if rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/experiments",
		validExperimentPayload("exp_up"), withAdminPrincipal); rr.Code != http.StatusCreated {
		t.Fatalf("seed create: status %d, body %s", rr.Code, rr.Body.String())
	}
	changed := validExperimentPayload("exp_up")
	changed.Variants = []admin.ExperimentVariant{{ID: "treatment", Weight: 0.42}}
	rr := doAdminReq(t, router.Handler(), http.MethodPut, "/v1/admin/experiments/exp_up?dryRun=true",
		changed, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("dryRun update: status %d, want 200, body %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-Dry-Run") != "true" {
		t.Errorf("X-Dry-Run = %q, want true", rr.Header().Get("X-Dry-Run"))
	}
	stored, err := exps.Get(context.Background(), "acme", "exp_up")
	if err != nil {
		t.Fatalf("Get after dryRun update: %v", err)
	}
	if len(stored.Variants) != 1 || stored.Variants[0].Weight != 0.1 {
		t.Errorf("dryRun update mutated the stored definition: %+v", stored.Variants)
	}
}
