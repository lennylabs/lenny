// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract tests for the §25.4 escalation endpoints — create,
// list, and status update — served by lenny-ops.
package ops_endpoints_test

import (
	"net/http"
	"testing"
)

// TestCreateEscalationContract confirms POST /v1/admin/escalations
// records an escalation and returns the §25.4 Escalation shape with the
// persistence tier and the emission flag.
//
// spec: 25.4 (POST /v1/admin/escalations — Escalation response shape)
// diagnosis: Creating an escalation returned a body missing a §25.4
// Escalation field — id, status, severity, persistence, or emitted. An
// agent reads persistence to know the durability of the record and
// emitted to know whether subscribers were notified.
func TestCreateEscalationContract(t *testing.T) {
	srv := opsServer(t)
	rec, body := request(t, srv, http.MethodPost, "/v1/admin/escalations",
		map[string]string{"X-Lenny-Caller": "prod-watchdog"},
		map[string]any{
			"severity": "critical",
			"summary":  "warm pool exhausted; three scaling attempts failed",
		})
	// §25.4: the in-memory Tier 3 buffer accepts with 202.
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 for the buffered-memory tier; body=%v", rec.Code, body)
	}
	assertJSONContentType(t, rec)
	for _, field := range []string{"id", "severity", "source", "summary", "status", "persistence", "emitted", "createdAt"} {
		if _, ok := body[field]; !ok {
			t.Errorf("Escalation response is missing the %q field", field)
		}
	}
	if body["status"] != "open" {
		t.Errorf("status = %v, want open on a fresh escalation", body["status"])
	}
	if body["severity"] != "critical" {
		t.Errorf("severity = %v, want critical", body["severity"])
	}
	// §25.4: the in-memory tier reports the buffered-memory persistence.
	if body["persistence"] != "buffered-memory" {
		t.Errorf("persistence = %v, want buffered-memory", body["persistence"])
	}
	// §25.4: the response carries the X-Lenny-Persistence header for the
	// non-Tier-1 path.
	if rec.Header().Get("X-Lenny-Persistence") != "buffered-memory" {
		t.Errorf("X-Lenny-Persistence header = %q, want buffered-memory", rec.Header().Get("X-Lenny-Persistence"))
	}
}

// TestCreateEscalationRejectsBadSeverity confirms an invalid severity
// is rejected with a PERMANENT error.
//
// spec: 25.4 (escalation severity — critical, warning, info)
// diagnosis: Creating an escalation with an out-of-enum severity
// succeeded. §25.4 defines a closed severity set; an unrecognized
// severity would break subscriber routing.
func TestCreateEscalationRejectsBadSeverity(t *testing.T) {
	srv := opsServer(t)
	rec, body := request(t, srv, http.MethodPost, "/v1/admin/escalations", nil,
		map[string]any{"severity": "emergency", "summary": "x"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a bad severity", rec.Code)
	}
	if errorEnvelope(t, body)["category"] != "PERMANENT" {
		t.Error("a bad severity should be a PERMANENT error")
	}
}

// TestListEscalationsFilterContract confirms GET /v1/admin/escalations
// honors the §25.4 status filter.
//
// spec: 25.4 (GET /v1/admin/escalations — status filter)
// diagnosis: The escalation list ignored the status filter and returned
// resolved escalations under ?status=open. An agent triaging open
// escalations needs the filter to exclude already-handled ones.
func TestListEscalationsFilterContract(t *testing.T) {
	srv := opsServer(t)
	_, toResolve := request(t, srv, http.MethodPost, "/v1/admin/escalations", nil,
		map[string]any{"severity": "critical", "summary": "resolved-one"})
	request(t, srv, http.MethodPost, "/v1/admin/escalations", nil,
		map[string]any{"severity": "warning", "summary": "open-one"})
	id, _ := toResolve["id"].(string)
	request(t, srv, http.MethodPut, "/v1/admin/escalations/"+id, nil,
		map[string]any{"status": "resolved"})

	rec, body := request(t, srv, http.MethodGet, "/v1/admin/escalations?status=open", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	items, _ := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("?status=open returned %d escalations, want 1", len(items))
	}
	only, _ := items[0].(map[string]any)
	if only["summary"] != "open-one" {
		t.Errorf("filtered escalation = %v, want open-one", only["summary"])
	}
}

// TestUpdateEscalationStatusContract confirms PUT /v1/admin/-
// escalations/{id} moves an escalation through the §25.4 lifecycle and
// stamps the lifecycle timestamps.
//
// spec: 25.4 (PUT /v1/admin/escalations/{id} — status update)
// diagnosis: Updating an escalation did not change status or did not
// stamp acknowledgedAt/resolvedAt. The lifecycle timestamps are the
// record of when an operator responded.
func TestUpdateEscalationStatusContract(t *testing.T) {
	srv := opsServer(t)
	_, esc := request(t, srv, http.MethodPost, "/v1/admin/escalations", nil,
		map[string]any{"severity": "info", "summary": "x"})
	id, _ := esc["id"].(string)

	rec, body := request(t, srv, http.MethodPut, "/v1/admin/escalations/"+id, nil,
		map[string]any{"status": "acknowledged"})
	if rec.Code != http.StatusOK {
		t.Fatalf("acknowledge status = %d, want 200; body=%v", rec.Code, body)
	}
	if body["status"] != "acknowledged" {
		t.Errorf("status = %v, want acknowledged", body["status"])
	}
	if body["acknowledgedAt"] == nil {
		t.Error("acknowledgedAt was not stamped on the acknowledged escalation")
	}

	rec, body = request(t, srv, http.MethodPut, "/v1/admin/escalations/"+id, nil,
		map[string]any{"status": "resolved"})
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve status = %d, want 200", rec.Code)
	}
	if body["resolvedAt"] == nil {
		t.Error("resolvedAt was not stamped on the resolved escalation")
	}
}

// TestUpdateUnknownEscalationContract confirms an unknown escalation id
// returns the §25.4 ESCALATION_NOT_FOUND error.
//
// spec: 25.4 (ESCALATION_NOT_FOUND — 404 PERMANENT)
// diagnosis: Updating an unknown escalation returned a non-404 or the
// wrong code. An agent distinguishes "no such escalation" from a
// transient failure by this code.
func TestUpdateUnknownEscalationContract(t *testing.T) {
	srv := opsServer(t)
	rec, body := request(t, srv, http.MethodPut, "/v1/admin/escalations/esc-absent", nil,
		map[string]any{"status": "resolved"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if errorEnvelope(t, body)["code"] != "ESCALATION_NOT_FOUND" {
		t.Errorf("error code = %v, want ESCALATION_NOT_FOUND", errorEnvelope(t, body)["code"])
	}
}
