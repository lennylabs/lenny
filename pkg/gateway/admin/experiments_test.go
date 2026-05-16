// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/experiment"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/experimentstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
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

func doExperiment(t *testing.T, h http.Handler, method, path string, body any, as func(*http.Request) *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req := as(httptest.NewRequest(method, path, rdr))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestCreateExperiment(t *testing.T) {
	router, exps, audit := newExperimentAdmin(t)
	rr := doExperiment(t, router.Handler(), http.MethodPost, "/v1/admin/experiments",
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
	rr := doExperiment(t, router.Handler(), http.MethodPost, "/v1/admin/experiments", body, withAdminPrincipal)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("reserved variant id: status %d, want 422", rr.Code)
	}
}

func TestCreateExperimentRejectsDuplicate(t *testing.T) {
	router, _, _ := newExperimentAdmin(t)
	doExperiment(t, router.Handler(), http.MethodPost, "/v1/admin/experiments",
		validExperimentPayload("exp_dup"), withAdminPrincipal)
	rr := doExperiment(t, router.Handler(), http.MethodPost, "/v1/admin/experiments",
		validExperimentPayload("exp_dup"), withAdminPrincipal)
	if rr.Code != http.StatusConflict {
		t.Errorf("duplicate create: status %d, want 409", rr.Code)
	}
}

func TestListExperiments(t *testing.T) {
	router, _, _ := newExperimentAdmin(t)
	doExperiment(t, router.Handler(), http.MethodPost, "/v1/admin/experiments",
		validExperimentPayload("exp_a"), withAdminPrincipal)
	doExperiment(t, router.Handler(), http.MethodPost, "/v1/admin/experiments",
		validExperimentPayload("exp_b"), withAdminPrincipal)

	rr := doExperiment(t, router.Handler(), http.MethodGet, "/v1/admin/experiments?tenantId=acme",
		nil, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("list: status %d", rr.Code)
	}
	var resp struct {
		Experiments []admin.ExperimentPayload `json:"experiments"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.Experiments) != 2 {
		t.Errorf("list returned %d experiments, want 2", len(resp.Experiments))
	}
}

func TestGetExperimentNotFound(t *testing.T) {
	router, _, _ := newExperimentAdmin(t)
	rr := doExperiment(t, router.Handler(), http.MethodGet, "/v1/admin/experiments/absent?tenantId=acme",
		nil, withAdminPrincipal)
	if rr.Code != http.StatusNotFound {
		t.Errorf("get unknown: status %d, want 404", rr.Code)
	}
}

func TestUpdateExperimentPreservesStatus(t *testing.T) {
	router, exps, _ := newExperimentAdmin(t)
	doExperiment(t, router.Handler(), http.MethodPost, "/v1/admin/experiments",
		validExperimentPayload("exp_u"), withAdminPrincipal)
	// Transition to paused, then PUT with status "active" in the body.
	doExperiment(t, router.Handler(), http.MethodPatch, "/v1/admin/experiments/exp_u?tenantId=acme",
		admin.PatchExperimentRequest{Status: ptr("paused")}, withAdminPrincipal)

	body := validExperimentPayload("exp_u")
	body.Status = "active" // PUT must not transition status
	body.BaseRuntime = "claude-worker-next"
	rr := doExperiment(t, router.Handler(), http.MethodPut, "/v1/admin/experiments/exp_u",
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
	doExperiment(t, router.Handler(), http.MethodPost, "/v1/admin/experiments",
		validExperimentPayload("exp_p"), withAdminPrincipal)

	rr := doExperiment(t, router.Handler(), http.MethodPatch, "/v1/admin/experiments/exp_p?tenantId=acme",
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
	doExperiment(t, router.Handler(), http.MethodPost, "/v1/admin/experiments",
		validExperimentPayload("exp_c"), withAdminPrincipal)
	// active → concluded is valid; concluded → active is not.
	doExperiment(t, router.Handler(), http.MethodPatch, "/v1/admin/experiments/exp_c?tenantId=acme",
		admin.PatchExperimentRequest{Status: ptr("concluded")}, withAdminPrincipal)
	rr := doExperiment(t, router.Handler(), http.MethodPatch, "/v1/admin/experiments/exp_c?tenantId=acme",
		admin.PatchExperimentRequest{Status: ptr("active")}, withAdminPrincipal)
	if rr.Code != http.StatusConflict {
		t.Errorf("concluded → active: status %d, want 409", rr.Code)
	}
}

func TestDeleteExperiment(t *testing.T) {
	router, exps, _ := newExperimentAdmin(t)
	doExperiment(t, router.Handler(), http.MethodPost, "/v1/admin/experiments",
		validExperimentPayload("exp_d"), withAdminPrincipal)

	rr := doExperiment(t, router.Handler(), http.MethodDelete, "/v1/admin/experiments/exp_d?tenantId=acme",
		nil, withAdminPrincipal)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete: status %d", rr.Code)
	}
	if _, err := exps.Get(context.Background(), "acme", "exp_d"); err == nil {
		t.Error("experiment still present after delete")
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
	rr := doExperiment(t, router.Handler(), http.MethodPost, "/v1/admin/experiments",
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
	rr := doExperiment(t, router.Handler(), http.MethodPost, "/v1/admin/experiments",
		body, withTenantAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Fatalf("tenant-admin create: status %d, body %s", rr.Code, rr.Body.String())
	}
	if _, err := exps.Get(context.Background(), "acme", "exp_ta"); err != nil {
		t.Errorf("tenant-admin's experiment not stored under its tenant: %v", err)
	}
}

func ptr(s string) *string { return &s }
