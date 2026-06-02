// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	"github.com/lennylabs/lenny/pkg/preflight"
)

// spec: §15.1 line 890 — POST /v1/admin/preflight returns the full
// connectivity report; an all-pass report yields passed=true.
func TestPreflightEndpoint_AllPass(t *testing.T) {
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{}).
		WithPreflight(admin.InfraPreflightFunc(func(context.Context) []preflight.CheckResult {
			return []preflight.CheckResult{
				{Name: "postgres-connectivity", Decision: preflight.Decision{Passed: true, Reason: "Postgres reachable; schema version: 116"}},
				{Name: "redis-connectivity", Decision: preflight.Decision{Passed: true, Reason: "Redis reachable"}},
			}
		}))

	rr := preflightReq(t, router.Handler())
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rr.Code, rr.Body.String())
	}
	var resp admin.PreflightResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Passed {
		t.Errorf("expected passed=true, got %+v", resp)
	}
	if len(resp.Checks) != 2 {
		t.Fatalf("want 2 checks, got %d", len(resp.Checks))
	}
}

// spec: §15.1 line 890 — a failing probe is reported as passed=false in
// the body with HTTP 200 (the probe ran; the negative finding is the
// payload), so the CLI can exit non-zero.
func TestPreflightEndpoint_FailureReportedInBody(t *testing.T) {
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{}).
		WithPreflight(admin.InfraPreflightFunc(func(context.Context) []preflight.CheckResult {
			return []preflight.CheckResult{
				{Name: "postgres-connectivity", Decision: preflight.Decision{Passed: false, Reason: "POSTGRES_UNREACHABLE: connection refused"}},
			}
		}))

	rr := preflightReq(t, router.Handler())
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	var resp admin.PreflightResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Passed {
		t.Error("expected passed=false on an unreachable backend")
	}
	if resp.Checks[0].Reason == "" || resp.Checks[0].Passed {
		t.Errorf("check not reported as failed: %+v", resp.Checks[0])
	}
}

// Without a wired preflighter the route stays unregistered (404), so a
// gateway with no backends does not advertise a probe it cannot run.
func TestPreflightEndpoint_Unwired404(t *testing.T) {
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{})
	rr := preflightReq(t, router.Handler())
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404 when unwired, got %d", rr.Code)
	}
}

func preflightReq(t *testing.T, h http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/preflight", nil))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}
