// SPDX-License-Identifier: MIT

package opsserver_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/escalation"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
)

// escalationServer returns a Server with an escalation service wired
// over the in-memory Tier 3 buffer.
func escalationServer() *opsserver.Server {
	return opsserver.New(opsserver.Options{Escalations: escalation.NewService(nil)})
}

func TestCreateEscalationReturns202ForMemoryTier(t *testing.T) {
	srv := escalationServer()
	rec, body := doJSON(t, srv, http.MethodPost, "/v1/admin/escalations",
		map[string]string{"X-Lenny-Caller": "watchdog"},
		map[string]any{"severity": "critical", "summary": "warm pool exhausted, scaling failed"})
	// §25.4: the in-memory Tier 3 buffer returns 202 Accepted with the
	// X-Lenny-Persistence header.
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%v", rec.Code, body)
	}
	if rec.Header().Get("X-Lenny-Persistence") != "buffered-memory" {
		t.Errorf("X-Lenny-Persistence = %q, want buffered-memory", rec.Header().Get("X-Lenny-Persistence"))
	}
	if body["status"] != "open" || body["source"] != "watchdog" {
		t.Errorf("escalation = %v, want status=open source=watchdog", body)
	}
}

func TestCreateEscalationTier3IncludesDurabilityWarning(t *testing.T) {
	srv := escalationServer()
	rec, body := doJSON(t, srv, http.MethodPost, "/v1/admin/escalations", nil,
		map[string]any{"severity": "warning", "summary": "credential pool degraded"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
	// §25.4 lines 2388-2394: the Tier 3 response carries a durability
	// warning in the body alongside the escalation fields.
	warning, _ := body["warning"].(string)
	if warning == "" {
		t.Fatal("Tier 3 response missing the durability warning")
	}
	for _, want := range []string{"memory only", "escalation_created"} {
		if !strings.Contains(warning, want) {
			t.Errorf("warning %q missing %q", warning, want)
		}
	}
	// The escalation fields are still present at the top level.
	if body["persistence"] != "buffered-memory" || body["id"] == nil {
		t.Errorf("response = %v, want the escalation fields plus warning", body)
	}
}

func TestCreateEscalationRejectsBadSeverity(t *testing.T) {
	srv := escalationServer()
	rec, body := doJSON(t, srv, http.MethodPost, "/v1/admin/escalations", nil,
		map[string]any{"severity": "catastrophic", "summary": "x"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	errObj, _ := body["error"].(map[string]any)
	if errObj["code"] != "ESCALATION_INVALID" {
		t.Errorf("error code = %v, want ESCALATION_INVALID", errObj["code"])
	}
}

func TestListEscalationsFiltersByStatus(t *testing.T) {
	srv := escalationServer()
	_, a := doJSON(t, srv, http.MethodPost, "/v1/admin/escalations", nil,
		map[string]any{"severity": "critical", "summary": "to-resolve"})
	doJSON(t, srv, http.MethodPost, "/v1/admin/escalations", nil,
		map[string]any{"severity": "warning", "summary": "stays-open"})
	id, _ := a["id"].(string)
	doJSON(t, srv, http.MethodPut, "/v1/admin/escalations/"+id, nil, map[string]any{"status": "resolved"})

	rec, body := doJSON(t, srv, http.MethodGet, "/v1/admin/escalations?status=open", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	items, _ := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("status=open returned %d escalations, want 1", len(items))
	}
	// The §25.4 list carries the canonical pagination envelope.
	if _, ok := body["pagination"]; !ok {
		t.Error("the escalation list is missing the pagination envelope")
	}
}

func TestUpdateEscalationToResolved(t *testing.T) {
	srv := escalationServer()
	_, esc := doJSON(t, srv, http.MethodPost, "/v1/admin/escalations", nil,
		map[string]any{"severity": "info", "summary": "x"})
	id, _ := esc["id"].(string)
	rec, body := doJSON(t, srv, http.MethodPut, "/v1/admin/escalations/"+id, nil,
		map[string]any{"status": "acknowledged"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", rec.Code, body)
	}
	if body["status"] != "acknowledged" || body["acknowledgedAt"] == nil {
		t.Errorf("escalation = %v, want status=acknowledged with a timestamp", body)
	}
}

func TestUpdateUnknownEscalationReturns404(t *testing.T) {
	srv := escalationServer()
	rec, body := doJSON(t, srv, http.MethodPut, "/v1/admin/escalations/esc-ghost", nil,
		map[string]any{"status": "resolved"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	errObj, _ := body["error"].(map[string]any)
	if errObj["code"] != "ESCALATION_NOT_FOUND" {
		t.Errorf("error code = %v, want ESCALATION_NOT_FOUND", errObj["code"])
	}
}

func TestEscalationsUnavailableWithoutService(t *testing.T) {
	srv := opsserver.New(opsserver.Options{})
	rec, _ := doJSON(t, srv, http.MethodGet, "/v1/admin/escalations", nil, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 with no escalation service configured", rec.Code)
	}
}
