// SPDX-License-Identifier: MIT

package opsserver_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/doctor"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
)

// postRaw issues a POST with an unparsed body so a malformed-JSON case
// can be exercised.
func postRaw(t *testing.T, srv *opsserver.Server, path, body string) (*httptest.ResponseRecorder, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec, rec.Body.String()
}

// fakeDoctorRem is a test Remediator driving the orchestrator behind the
// §25.6 run endpoint.
type fakeDoctorRem struct {
	detected  []doctor.Detected
	detectErr error
	applied   []string
}

func (f *fakeDoctorRem) Detect(context.Context) ([]doctor.Detected, error) {
	return f.detected, f.detectErr
}

func (f *fakeDoctorRem) Apply(_ context.Context, d doctor.Detected) error {
	f.applied = append(f.applied, d.Code)
	return nil
}

func doctorServer(rem doctor.Remediator) *opsserver.Server {
	var svc doctor.Service
	if o := doctor.New(rem, doctor.Config{}); o != nil {
		svc = o
	}
	return opsserver.New(opsserver.Options{Doctor: svc})
}

// spec: §25.6 line 2941 — POST without ?fix=true is read-only and reports
// detected fixable findings without applying.
func TestDiagnosticsRun_ReadOnly_spec_25_6_2941(t *testing.T) {
	rem := &fakeDoctorRem{detected: []doctor.Detected{
		{Code: doctor.FindingCoreDNSStuckEndpoint, Resource: "kube-system/coredns"},
	}}
	srv := doctorServer(rem)
	rec, body := doJSON(t, srv, http.MethodPost, "/v1/admin/diagnostics/run", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%v", rec.Code, body)
	}
	if body["fix"] != false {
		t.Fatalf("fix=%v want false", body["fix"])
	}
	findings, _ := body["findings"].([]any)
	if len(findings) != 1 {
		t.Fatalf("findings=%v", findings)
	}
	if len(rem.applied) != 0 {
		t.Fatalf("read-only must not apply")
	}
}

// spec: §25.6 lines 2943-2982 — POST ?fix=true applies remediations and
// returns the §25.2 progress envelope with the applied count.
func TestDiagnosticsRun_Fix_spec_25_6_2943(t *testing.T) {
	rem := &fakeDoctorRem{detected: []doctor.Detected{
		{Code: doctor.FindingCertManagerExpiring, Resource: "lenny-system/tls"},
	}}
	srv := doctorServer(rem)
	rec, body := doJSON(t, srv, http.MethodPost, "/v1/admin/diagnostics/run?fix=true", nil,
		map[string]any{"findings": []string{doctor.FindingCertManagerExpiring}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%v", rec.Code, body)
	}
	if body["fix"] != true {
		t.Fatalf("fix=%v want true", body["fix"])
	}
	if body["appliedCount"].(float64) != 1 {
		t.Fatalf("appliedCount=%v", body["appliedCount"])
	}
	if len(rem.applied) != 1 || rem.applied[0] != doctor.FindingCertManagerExpiring {
		t.Fatalf("applied=%v", rem.applied)
	}
	prog, ok := body["progress"].(map[string]any)
	if !ok || prog["percent"].(float64) != 100 {
		t.Fatalf("progress=%v", body["progress"])
	}
	if body["operationId"] == "" {
		t.Fatalf("missing operationId")
	}
}

// A nil orchestrator reports the endpoint unavailable rather than 404.
func TestDiagnosticsRun_Unavailable(t *testing.T) {
	srv := opsserver.New(opsserver.Options{}) // no Doctor
	rec, body := doJSON(t, srv, http.MethodPost, "/v1/admin/diagnostics/run", nil, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503; body=%v", rec.Code, body)
	}
}

// A detect failure surfaces as a transient 503 so an agent retries
// rather than treating the cluster as healthy.
func TestDiagnosticsRun_DetectError_503(t *testing.T) {
	rem := &fakeDoctorRem{detectErr: errors.New("kube API unreachable")}
	srv := doctorServer(rem)
	rec, _ := doJSON(t, srv, http.MethodPost, "/v1/admin/diagnostics/run?fix=true", nil, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503", rec.Code)
	}
}

// A malformed JSON body is a 400 VALIDATION_ERROR.
func TestDiagnosticsRun_BadBody_400(t *testing.T) {
	rem := &fakeDoctorRem{}
	srv := doctorServer(rem)
	rec, _ := postRaw(t, srv, "/v1/admin/diagnostics/run", "{not json")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", rec.Code)
	}
}
