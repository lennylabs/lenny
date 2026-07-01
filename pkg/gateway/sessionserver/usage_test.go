// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/billing/usagestore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// spec: §15.1 GET /v1/usage.

// withUsageViewer attaches a principal that holds the §10.2 view_usage
// permission so the GET /v1/usage gate admits the request.
func withUsageViewer(req *http.Request) *http.Request {
	return req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject: "viewer@acme.com", TenantID: "acme",
		Roles: []pkgauth.Role{pkgauth.RoleTenantAdmin},
	}))
}

func TestUsageRecordsSessionCreation(t *testing.T) {
	usage := usagestore.NewMemory()
	var n int
	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		IDFunc: func() string { n++; return "sess_u" + string(rune('0'+n)) },
		Usage:  usage,
	})
	handler := srv.Handler()

	// Create two sessions through the same server.
	for i := 0; i < 2; i++ {
		body, _ := json.Marshal(sessionserver.CreateSessionRequest{RuntimeRef: "echo"})
		req := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(body))
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusCreated {
			t.Fatalf("create %d: %d, body=%s", i, rr.Code, rr.Body.String())
		}
	}

	// GET /v1/usage reports 2 sessions for acme.
	req := withUsageViewer(httptest.NewRequest(http.MethodGet, "/v1/usage", nil))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("usage: %d", rr.Code)
	}
	var report usagestore.Report
	_ = json.Unmarshal(rr.Body.Bytes(), &report)
	if report.TotalSessions != 2 {
		t.Errorf("totalSessions: got %d, want 2", report.TotalSessions)
	}
	if len(report.ByRuntime) != 1 || report.ByRuntime[0].Runtime != "echo" {
		t.Errorf("byRuntime: %+v", report.ByRuntime)
	}
}

// TestUsageLabelFilter_spec_14_106 drives the §14 line 106 label-scoped
// usage report end to end: two sessions are created with distinct labels
// and GET /v1/usage?label=team=search returns only the matching session's
// usage. F-14.1.13.
func TestUsageLabelFilter_spec_14_106(t *testing.T) {
	usage := usagestore.NewMemory()
	var n int
	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		IDFunc: func() string { n++; return "sess_ul" + string(rune('0'+n)) },
		Usage:  usage,
	})
	handler := srv.Handler()

	create := func(labels map[string]string) {
		body, _ := json.Marshal(sessionserver.CreateSessionRequest{RuntimeRef: "echo", Labels: labels})
		req := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(body))
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusCreated {
			t.Fatalf("create: %d, body=%s", rr.Code, rr.Body.String())
		}
	}
	create(map[string]string{"team": "search"})
	create(map[string]string{"team": "ads"})

	// Filtered: only the search session's usage.
	req := withUsageViewer(httptest.NewRequest(http.MethodGet, "/v1/usage?label=team=search", nil))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("filtered usage: %d", rr.Code)
	}
	var report usagestore.Report
	_ = json.Unmarshal(rr.Body.Bytes(), &report)
	if report.TotalSessions != 1 {
		t.Errorf("team=search usage: got %d sessions, want 1", report.TotalSessions)
	}

	// Unfiltered: both sessions.
	req = withUsageViewer(httptest.NewRequest(http.MethodGet, "/v1/usage", nil))
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	_ = json.Unmarshal(rr.Body.Bytes(), &report)
	if report.TotalSessions != 2 {
		t.Errorf("unfiltered usage: got %d sessions, want 2", report.TotalSessions)
	}
}

func TestUsageRejectsCallerWithoutViewUsage(t *testing.T) {
	// §10.2 grants the `user` role no view_usage permission, so a plain
	// user cannot read the usage report.
	srv := sessionserver.New(memstore.New(), sessionserver.Options{Usage: usagestore.NewMemory()})
	req := httptest.NewRequest(http.MethodGet, "/v1/usage", nil)
	req = req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject: "alice@acme.com", TenantID: "acme",
		Roles: []pkgauth.Role{pkgauth.RoleUser},
	}))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("user role on /v1/usage: got %d, want 403", rr.Code)
	}
}

func TestUsageEmptyWhenUnwired(t *testing.T) {
	srv := sessionserver.New(memstore.New(), sessionserver.Options{})
	req := withUsageViewer(httptest.NewRequest(http.MethodGet, "/v1/usage", nil))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("usage unwired: %d", rr.Code)
	}
	var report usagestore.Report
	_ = json.Unmarshal(rr.Body.Bytes(), &report)
	if report.TotalSessions != 0 {
		t.Errorf("unwired usage should be empty: %+v", report)
	}
}

func TestUsageTenantScoped(t *testing.T) {
	usage := usagestore.NewMemory()
	srv := sessionserver.New(memstore.New(), sessionserver.Options{Usage: usage})

	// Seed usage for two tenants directly.
	_ = usage.Record(nil, usagestore.Record{TenantID: "acme", Sessions: 5})
	_ = usage.Record(nil, usagestore.Record{TenantID: "globex", Sessions: 9})

	req := withUsageViewer(httptest.NewRequest(http.MethodGet, "/v1/usage", nil))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("acme usage: %d", rr.Code)
	}
	var report usagestore.Report
	_ = json.Unmarshal(rr.Body.Bytes(), &report)
	if report.TotalSessions != 5 {
		t.Errorf("acme-scoped usage: got %d, want 5 (globex must not leak)", report.TotalSessions)
	}
}
