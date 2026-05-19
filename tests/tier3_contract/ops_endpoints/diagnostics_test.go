// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract tests for the §25.6 diagnostic endpoints — session
// cause chain, pool bottleneck analysis, connectivity, and credential-
// pool diagnosis — served by lenny-ops.
package ops_endpoints_test

import (
	"net/http"
	"testing"
)

// TestDiagnoseSessionContract confirms GET /v1/admin/diagnostics/-
// sessions/{id} returns the §25.6 SessionDiagnosis shape: the cause
// chain, the session identity fields, and a suggestedActions array.
//
// spec: 25.6 (GET /v1/admin/diagnostics/sessions/{id} response shape)
// diagnosis: The session diagnostic returned a body missing the
// causeChain or the identity fields. An agent reads the cause chain to
// decide the remediation, so a malformed diagnosis breaks the
// runbook-driven flow.
func TestDiagnoseSessionContract(t *testing.T) {
	srv := opsServer(t)
	rec, body := request(t, srv, http.MethodGet, "/v1/admin/diagnostics/sessions/sess-known", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", rec.Code, body)
	}
	assertJSONContentType(t, rec)
	if body["sessionId"] != "sess-known" {
		t.Errorf("sessionId = %v, want sess-known", body["sessionId"])
	}
	chain, ok := body["causeChain"].([]any)
	if !ok || len(chain) == 0 {
		t.Fatalf("causeChain = %v, want a non-empty cause chain", body["causeChain"])
	}
	// The proximate cause is level 0 and carries a machine-readable
	// category and a human-readable summary.
	entry, _ := chain[0].(map[string]any)
	if entry["category"] != "OOM_KILLED" {
		t.Errorf("cause category = %v, want OOM_KILLED for an OOM-killed pod", entry["category"])
	}
	if summary, _ := entry["summary"].(string); summary == "" {
		t.Error("the cause-chain entry has no human-readable summary")
	}
	if _, ok := body["suggestedActions"].([]any); !ok {
		t.Error("the session diagnosis is missing the suggestedActions array")
	}
}

// TestDiagnoseSessionNotFoundContract confirms an unknown session id
// returns the §25.6 SESSION_NOT_FOUND error.
//
// spec: 25.6 (SESSION_NOT_FOUND — 404 PERMANENT)
// diagnosis: The session diagnostic returned a non-404 or the wrong
// error code for an unknown session. An agent distinguishes "no such
// session" from a transient failure by this code.
func TestDiagnoseSessionNotFoundContract(t *testing.T) {
	srv := opsServer(t)
	rec, body := request(t, srv, http.MethodGet, "/v1/admin/diagnostics/sessions/sess-absent", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if errorEnvelope(t, body)["code"] != "SESSION_NOT_FOUND" {
		t.Errorf("error code = %v, want SESSION_NOT_FOUND", errorEnvelope(t, body)["code"])
	}
}

// TestDiagnosePoolContract confirms GET /v1/admin/diagnostics/pools/-
// {name} returns the §25.6 PoolDiagnosis shape with the classified
// bottleneck.
//
// spec: 25.6 (GET /v1/admin/diagnostics/pools/{name} response shape)
// diagnosis: The pool diagnostic returned a body missing the bottleneck
// classification or the pod-count breakdown. An agent reads the
// bottleneck category to choose between scaling and escalating.
func TestDiagnosePoolContract(t *testing.T) {
	srv := opsServer(t)
	rec, body := request(t, srv, http.MethodGet, "/v1/admin/diagnostics/pools/default-gvisor", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", rec.Code, body)
	}
	if body["pool"] != "default-gvisor" {
		t.Errorf("pool = %v, want default-gvisor", body["pool"])
	}
	bottleneck, ok := body["bottleneck"].(map[string]any)
	if !ok {
		t.Fatalf("bottleneck = %v, want a classified bottleneck", body["bottleneck"])
	}
	if bottleneck["category"] != "IMAGE_PULL" {
		t.Errorf("bottleneck category = %v, want IMAGE_PULL", bottleneck["category"])
	}
	if _, ok := body["podCounts"].(map[string]any); !ok {
		t.Error("the pool diagnosis is missing the podCounts breakdown")
	}
	if _, ok := body["crdSyncStatus"].(map[string]any); !ok {
		t.Error("the pool diagnosis is missing the crdSyncStatus")
	}
}

// TestDiagnoseConnectivityContract confirms GET /v1/admin/diagnostics/-
// connectivity returns the §25.6 ConnectivityReport shape.
//
// spec: 25.6 (GET /v1/admin/diagnostics/connectivity response shape)
// diagnosis: The connectivity diagnostic returned a body missing the
// dependencies array or the healthy verdict. The connectivity check is
// the §25.6 probe an agent runs to confirm the platform's dependencies
// are reachable.
func TestDiagnoseConnectivityContract(t *testing.T) {
	srv := opsServer(t)
	rec, body := request(t, srv, http.MethodGet, "/v1/admin/diagnostics/connectivity", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", rec.Code, body)
	}
	if _, ok := body["healthy"].(bool); !ok {
		t.Error("the connectivity report is missing the healthy verdict")
	}
	deps, ok := body["dependencies"].([]any)
	if !ok || len(deps) == 0 {
		t.Fatalf("dependencies = %v, want a non-empty dependency list", body["dependencies"])
	}
}

// TestDiagnoseCredentialPoolContract confirms GET /v1/admin/-
// diagnostics/credential-pools/{name} returns the §25.6
// CredentialPoolDiagnosis shape.
//
// spec: 25.6 (GET /v1/admin/diagnostics/credential-pools/{name} shape)
// diagnosis: The credential-pool diagnostic returned a body missing the
// status or the utilization. An agent reads utilization to decide
// whether to add credentials.
func TestDiagnoseCredentialPoolContract(t *testing.T) {
	srv := opsServer(t)
	rec, body := request(t, srv, http.MethodGet, "/v1/admin/diagnostics/credential-pools/anthropic", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", rec.Code, body)
	}
	if body["pool"] != "anthropic" {
		t.Errorf("pool = %v, want anthropic", body["pool"])
	}
	// At 95% utilization the pool diagnoses unhealthy.
	if body["status"] != "unhealthy" {
		t.Errorf("status = %v, want unhealthy at 95%% utilization", body["status"])
	}
	if _, ok := body["utilization"].(float64); !ok {
		t.Error("the credential-pool diagnosis is missing utilization")
	}
}
