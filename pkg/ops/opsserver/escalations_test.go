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

// TestListEscalationsPaginationReportsMore seeds more escalations than the
// requested page limit and asserts the §25.4 canonical pagination envelope
// reports hasMore=true. The spec's query path requires the envelope's
// hasMore to reflect whether more records exist beyond the page ("hasMore
// reflects whether more records exist"), so a limit smaller than the total
// must not report the page as terminal.
//
// spec: §25.4 lines 2427-2429 (Storage Tiers, Query Path); §25.4 Pagination
// envelope (hasMore reflects whether more records exist).
// diagnosis: a failure means the escalation list envelope hardcodes
// hasMore=false and never advertises additional pages, so an agent paging
// the escalation history can never learn there is more to fetch.
func TestListEscalationsPaginationReportsMore_spec_25_4(t *testing.T) {
	srv := escalationServer()
	for i := 0; i < 3; i++ {
		doJSON(t, srv, http.MethodPost, "/v1/admin/escalations", nil,
			map[string]any{"severity": "warning", "summary": "buffered escalation"})
	}
	rec, body := doJSON(t, srv, http.MethodGet, "/v1/admin/escalations?limit=2", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	items, _ := body["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("page returned %d escalations, want limit=2", len(items))
	}
	pg, _ := body["pagination"].(map[string]any)
	if pg == nil {
		t.Fatal("response missing the pagination envelope")
	}
	if more, _ := pg["hasMore"].(bool); !more {
		t.Errorf("hasMore = %v, want true (3 escalations exist, page limit is 2)", pg["hasMore"])
	}
	// The in-memory Tier 3 buffer is the "none" query-path cursorKind
	// (§25.4 line 2428: limit-only, no continuation cursor).
	if kind, _ := pg["cursorKind"].(string); kind != "none" {
		t.Errorf("cursorKind = %q, want \"none\" for the in-memory buffer path", kind)
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
