// SPDX-License-Identifier: MIT

package opsserver_test

import (
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/coordination"
	"github.com/lennylabs/lenny/pkg/ops/escalation"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
)

// spec: §25.17 lines 5185-5186, 5264 — a watchdog that sends the
// X-Lenny-Operation-ID header on every call expects every recorded
// resource to carry that operationId so "show every action taken by
// this remediation effort" resolves. The handlers fall back to the
// correlation-context operationId when the request body omits it.
func TestAcquireLockFallsBackToOperationIDHeader_spec_25_17(t *testing.T) {
	srv := opsserver.New(opsserver.Options{Locks: coordination.NewMemStore()})
	rec, body := doJSON(t, srv, http.MethodPost, "/v1/admin/remediation-locks",
		map[string]string{
			"X-Lenny-Caller":       "watchdog",
			"X-Lenny-Operation-ID": "550e8400-corr",
		},
		// body intentionally omits operationId
		map[string]any{"scope": "pool:default-gvisor", "operation": "scale", "ttlSeconds": 300})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%v", rec.Code, body)
	}
	if body["operationId"] != "550e8400-corr" {
		t.Errorf("operationId = %v, want 550e8400-corr (from X-Lenny-Operation-ID header)", body["operationId"])
	}
}

// The body operationId wins over the correlation header when both are
// present: the explicit body field is the caller's deliberate choice.
func TestAcquireLockBodyOperationIDWinsOverHeader_spec_25_17(t *testing.T) {
	srv := opsserver.New(opsserver.Options{Locks: coordination.NewMemStore()})
	rec, body := doJSON(t, srv, http.MethodPost, "/v1/admin/remediation-locks",
		map[string]string{
			"X-Lenny-Caller":       "watchdog",
			"X-Lenny-Operation-ID": "header-op",
		},
		map[string]any{"scope": "pool:p", "operation": "scale", "ttlSeconds": 300, "operationId": "body-op"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%v", rec.Code, body)
	}
	if body["operationId"] != "body-op" {
		t.Errorf("operationId = %v, want body-op (body field wins)", body["operationId"])
	}
}

// An escalation created without an operationId in the body inherits the
// correlation-context operationId, so the §25.17 failure-path escalation
// joins the rest of the remediation effort's audit trail.
func TestCreateEscalationFallsBackToOperationIDHeader_spec_25_17(t *testing.T) {
	srv := opsserver.New(opsserver.Options{Escalations: escalation.NewService(nil)})
	rec, body := doJSON(t, srv, http.MethodPost, "/v1/admin/escalations",
		map[string]string{
			"X-Lenny-Caller":       "watchdog",
			"X-Lenny-Operation-ID": "550e8400-esc",
		},
		map[string]any{"severity": "critical", "summary": "warm pool exhausted, scaling failed"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%v", rec.Code, body)
	}
	if body["operationId"] != "550e8400-esc" {
		t.Errorf("operationId = %v, want 550e8400-esc (from X-Lenny-Operation-ID header)", body["operationId"])
	}
}
