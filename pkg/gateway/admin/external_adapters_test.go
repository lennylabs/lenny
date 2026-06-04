// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/compliance"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/externaladapterstore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// fakeValidator is an injected AdapterValidator for the handler tests:
// it returns a canned report (or error) without driving a real harness.
type fakeValidator struct {
	report compliance.Report
	err    error
	calls  int
}

func (f *fakeValidator) Validate(_ context.Context, _, _ string) (compliance.Report, error) {
	f.calls++
	return f.report, f.err
}

func newExternalAdapterAdmin(t *testing.T, v admin.AdapterValidator) (*admin.Router, *externaladapterstore.Memory, *recordingAudit) {
	t.Helper()
	store := externaladapterstore.NewMemory()
	aud := &recordingAudit{}
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Audit: aud,
	}).WithExternalAdapters(store, v)
	return router, store, aud
}

func eaReq(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		buf = bytes.NewReader(b)
	} else {
		buf = bytes.NewReader(nil)
	}
	req := withAdminPrincipal(httptest.NewRequest(method, path, buf))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func passingReport() compliance.Report {
	return compliance.Report{
		Level:   "standard",
		Checks:  []compliance.Check{{Name: "message_emits_response", Spec: "15.4", Pass: true}},
		Summary: compliance.Summary{Total: 1, Passed: 1, Failed: 0},
	}
}

func failingReport() compliance.Report {
	return compliance.Report{
		Level: "standard",
		Checks: []compliance.Check{
			{Name: "heartbeat_emits_ack", Spec: "15.4", Pass: false, Detail: `ack.type = "nope", want "heartbeat_ack"`},
			{Name: "message_emits_response", Spec: "15.4", Pass: true},
		},
		Summary: compliance.Summary{Total: 2, Passed: 1, Failed: 1},
	}
}

func samplePayload() admin.ExternalAdapterPayload {
	return admin.ExternalAdapterPayload{
		Name:       "acme-a2a",
		Protocol:   "a2a",
		PathPrefix: "/a2a",
		BinaryPath: "/usr/local/bin/acme-a2a",
		Level:      "standard",
	}
}

// spec: §15.1 line 850 / §15 line 1414 — register creates the adapter in
// pending_validation.
func TestCreateExternalAdapterPendingValidation(t *testing.T) {
	router, store, aud := newExternalAdapterAdmin(t, &fakeValidator{report: passingReport()})
	rr := eaReq(t, router.Handler(), http.MethodPost, "/v1/admin/external-adapters", samplePayload())
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	got, err := store.Get(context.Background(), "acme-a2a")
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if got.Status != externaladapterstore.StatusPendingValidation {
		t.Fatalf("status = %q, want pending_validation", got.Status)
	}
	if len(aud.snapshot()) != 1 {
		t.Fatalf("want 1 audit event, got %d", len(aud.snapshot()))
	}
}

// spec: §24.8 line 113 / §15 line 1414 — a passing suite transitions the
// adapter to active.
func TestValidateExternalAdapterPasses(t *testing.T) {
	v := &fakeValidator{report: passingReport()}
	router, store, _ := newExternalAdapterAdmin(t, v)
	if rr := eaReq(t, router.Handler(), http.MethodPost, "/v1/admin/external-adapters", samplePayload()); rr.Code != http.StatusCreated {
		t.Fatalf("create: %d", rr.Code)
	}
	rr := eaReq(t, router.Handler(), http.MethodPost, "/v1/admin/external-adapters/acme-a2a/validate", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("validate status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if v.calls != 1 {
		t.Fatalf("validator called %d times, want 1", v.calls)
	}
	got, _ := store.Get(context.Background(), "acme-a2a")
	if got.Status != externaladapterstore.StatusActive {
		t.Fatalf("status = %q, want active", got.Status)
	}
	if got.LastValidation == nil || got.LastValidation.Passed != 1 {
		t.Fatalf("lastValidation not recorded: %+v", got.LastValidation)
	}
}

// spec: §24.8 line 113 / §15 line 1414 — a failing suite transitions the
// adapter to validation_failed with per-test failure details, and the
// endpoint returns 422.
func TestValidateExternalAdapterFails(t *testing.T) {
	router, store, _ := newExternalAdapterAdmin(t, &fakeValidator{report: failingReport()})
	if rr := eaReq(t, router.Handler(), http.MethodPost, "/v1/admin/external-adapters", samplePayload()); rr.Code != http.StatusCreated {
		t.Fatalf("create: %d", rr.Code)
	}
	rr := eaReq(t, router.Handler(), http.MethodPost, "/v1/admin/external-adapters/acme-a2a/validate", nil)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("validate status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Details struct {
				Status         string `json:"status"`
				LastValidation struct {
					Failed   int `json:"failed"`
					Failures []struct {
						Name   string `json:"name"`
						Detail string `json:"detail"`
					} `json:"failures"`
				} `json:"lastValidation"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error.Code != "ADAPTER_VALIDATION_FAILED" {
		t.Fatalf("code = %q", env.Error.Code)
	}
	if env.Error.Details.LastValidation.Failed != 1 || len(env.Error.Details.LastValidation.Failures) != 1 {
		t.Fatalf("per-test failures missing: %+v", env.Error.Details.LastValidation)
	}
	if env.Error.Details.LastValidation.Failures[0].Name != "heartbeat_emits_ack" {
		t.Fatalf("wrong failing check: %s", env.Error.Details.LastValidation.Failures[0].Name)
	}
	got, _ := store.Get(context.Background(), "acme-a2a")
	if got.Status != externaladapterstore.StatusValidationFailed {
		t.Fatalf("status = %q, want validation_failed", got.Status)
	}
}

// The gate must not fail an adapter it could not test: a missing harness
// returns 503 and leaves the adapter in pending_validation.
func TestValidateExternalAdapterHarnessUnavailable(t *testing.T) {
	// Wrap the sentinel to confirm the handler matches via errors.Is.
	router, store, _ := newExternalAdapterAdmin(t, &fakeValidator{err: fmt.Errorf("compliance.RunSuite: %w", compliance.ErrHarnessNotFound)})
	if rr := eaReq(t, router.Handler(), http.MethodPost, "/v1/admin/external-adapters", samplePayload()); rr.Code != http.StatusCreated {
		t.Fatalf("create: %d", rr.Code)
	}
	rr := eaReq(t, router.Handler(), http.MethodPost, "/v1/admin/external-adapters/acme-a2a/validate", nil)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
	got, _ := store.Get(context.Background(), "acme-a2a")
	if got.Status != externaladapterstore.StatusPendingValidation {
		t.Fatalf("status = %q, want pending_validation (untouched)", got.Status)
	}
}

// With no validator wired the validate endpoint is 503, not a spurious
// failure.
func TestValidateExternalAdapterNoValidator(t *testing.T) {
	router, _, _ := newExternalAdapterAdmin(t, nil)
	if rr := eaReq(t, router.Handler(), http.MethodPost, "/v1/admin/external-adapters", samplePayload()); rr.Code != http.StatusCreated {
		t.Fatalf("create: %d", rr.Code)
	}
	rr := eaReq(t, router.Handler(), http.MethodPost, "/v1/admin/external-adapters/acme-a2a/validate", nil)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
}

func TestValidateExternalAdapterNotFound(t *testing.T) {
	router, _, _ := newExternalAdapterAdmin(t, &fakeValidator{report: passingReport()})
	rr := eaReq(t, router.Handler(), http.MethodPost, "/v1/admin/external-adapters/ghost/validate", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

// spec: §15.1 line 1199 / §15 line 1414 — changing the adapter under test
// resets the gate to pending_validation.
func TestUpdateExternalAdapterResetsValidation(t *testing.T) {
	router, store, _ := newExternalAdapterAdmin(t, &fakeValidator{report: passingReport()})
	if rr := eaReq(t, router.Handler(), http.MethodPost, "/v1/admin/external-adapters", samplePayload()); rr.Code != http.StatusCreated {
		t.Fatalf("create: %d", rr.Code)
	}
	if rr := eaReq(t, router.Handler(), http.MethodPost, "/v1/admin/external-adapters/acme-a2a/validate", nil); rr.Code != http.StatusOK {
		t.Fatalf("validate: %d", rr.Code)
	}
	// Change the binary → must re-enter pending_validation.
	rr := eaReq(t, router.Handler(), http.MethodPut, "/v1/admin/external-adapters/acme-a2a",
		admin.ExternalAdapterPayload{BinaryPath: "/usr/local/bin/acme-a2a-v2"})
	if rr.Code != http.StatusOK {
		t.Fatalf("update status = %d, body=%s", rr.Code, rr.Body.String())
	}
	got, _ := store.Get(context.Background(), "acme-a2a")
	if got.Status != externaladapterstore.StatusPendingValidation {
		t.Fatalf("status = %q, want pending_validation after binary change", got.Status)
	}
	if got.LastValidation != nil {
		t.Fatalf("lastValidation should be cleared on re-validation reset")
	}
}

// Display-name-only update keeps the active status (no re-validation).
func TestUpdateExternalAdapterMetadataKeepsStatus(t *testing.T) {
	router, store, _ := newExternalAdapterAdmin(t, &fakeValidator{report: passingReport()})
	_ = eaReq(t, router.Handler(), http.MethodPost, "/v1/admin/external-adapters", samplePayload())
	_ = eaReq(t, router.Handler(), http.MethodPost, "/v1/admin/external-adapters/acme-a2a/validate", nil)
	rr := eaReq(t, router.Handler(), http.MethodPut, "/v1/admin/external-adapters/acme-a2a",
		admin.ExternalAdapterPayload{DisplayName: "Acme Agent"})
	if rr.Code != http.StatusOK {
		t.Fatalf("update: %d", rr.Code)
	}
	got, _ := store.Get(context.Background(), "acme-a2a")
	if got.Status != externaladapterstore.StatusActive {
		t.Fatalf("status = %q, want active (metadata-only update)", got.Status)
	}
}

func TestListAndDeleteExternalAdapter(t *testing.T) {
	router, _, _ := newExternalAdapterAdmin(t, &fakeValidator{report: passingReport()})
	_ = eaReq(t, router.Handler(), http.MethodPost, "/v1/admin/external-adapters", samplePayload())
	listRR := eaReq(t, router.Handler(), http.MethodGet, "/v1/admin/external-adapters", nil)
	if listRR.Code != http.StatusOK {
		t.Fatalf("list: %d", listRR.Code)
	}
	var list struct {
		ExternalAdapters []admin.ExternalAdapterPayload `json:"items"`
	}
	if err := json.Unmarshal(listRR.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.ExternalAdapters) != 1 {
		t.Fatalf("want 1 adapter, got %d", len(list.ExternalAdapters))
	}
	delRR := eaReq(t, router.Handler(), http.MethodDelete, "/v1/admin/external-adapters/acme-a2a", nil)
	if delRR.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", delRR.Code)
	}
	getRR := eaReq(t, router.Handler(), http.MethodGet, "/v1/admin/external-adapters/acme-a2a", nil)
	if getRR.Code != http.StatusNotFound {
		t.Fatalf("get after delete = %d, want 404", getRR.Code)
	}
}

func TestCreateExternalAdapterValidation(t *testing.T) {
	router, _, _ := newExternalAdapterAdmin(t, &fakeValidator{report: passingReport()})
	bad := samplePayload()
	bad.Level = "" // missing required level
	rr := eaReq(t, router.Handler(), http.MethodPost, "/v1/admin/external-adapters", bad)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

// When the resource is not wired the routes are not registered at all.
func TestExternalAdaptersUnwired404(t *testing.T) {
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	})
	rr := eaReq(t, router.Handler(), http.MethodGet, "/v1/admin/external-adapters", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when unwired", rr.Code)
	}
}
