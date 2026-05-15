// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/usagestore"
)

// spec: §15.1 GET /v1/usage.

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
	req := httptest.NewRequest(http.MethodGet, "/v1/usage", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
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

func TestUsageEmptyWhenUnwired(t *testing.T) {
	srv := sessionserver.New(memstore.New(), sessionserver.Options{})
	req := httptest.NewRequest(http.MethodGet, "/v1/usage", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
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

	req := httptest.NewRequest(http.MethodGet, "/v1/usage", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	var report usagestore.Report
	_ = json.Unmarshal(rr.Body.Bytes(), &report)
	if report.TotalSessions != 5 {
		t.Errorf("acme-scoped usage: got %d, want 5 (globex must not leak)", report.TotalSessions)
	}
}
