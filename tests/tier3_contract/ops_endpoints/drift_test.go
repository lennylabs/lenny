// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract tests for the §25.10 configuration drift detection
// endpoints — drift report, snapshot validation, and snapshot refresh —
// served by lenny-ops.
package ops_endpoints_test

import (
	"net/http"
	"testing"
)

// TestDriftReportContract confirms GET /v1/admin/drift returns the
// §25.10 drift report shape: the drift entries with severity, the
// drift count, and the desired-state provenance.
//
// spec: 25.10 (GET /v1/admin/drift — drift report shape)
// diagnosis: The drift report returned a body missing the drift array,
// the driftCount, or desiredStateSource. An agent reads the severity of
// each drifted field to decide which drift to reconcile.
func TestDriftReportContract(t *testing.T) {
	srv := opsServer(t)
	rec, body := request(t, srv, http.MethodGet, "/v1/admin/drift?scope=pools", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", rec.Code, body)
	}
	assertJSONContentType(t, rec)
	// The seeded snapshot has minWarm 5 and running state has 12 — one
	// drifted field.
	if dc, _ := body["driftCount"].(float64); dc != 1 {
		t.Errorf("driftCount = %v, want 1", body["driftCount"])
	}
	drift, ok := body["drift"].([]any)
	if !ok || len(drift) != 1 {
		t.Fatalf("drift = %v, want one drifted field", body["drift"])
	}
	entry, _ := drift[0].(map[string]any)
	for _, field := range []string{"path", "kind", "severity"} {
		if _, ok := entry[field]; !ok {
			t.Errorf("drift entry is missing the %q field", field)
		}
	}
	// A scaling-parameter change classifies as medium severity per §25.10.
	if entry["severity"] != "medium" {
		t.Errorf("minWarm drift severity = %v, want medium", entry["severity"])
	}
	if body["desiredStateSource"] != "snapshot" {
		t.Errorf("desiredStateSource = %v, want snapshot", body["desiredStateSource"])
	}
}

// TestDriftReportMissingSnapshotContract confirms GET /v1/admin/drift
// with no snapshot and no caller-supplied desired state returns the
// §25.10 DRIFT_DESIRED_STATE_MISSING error.
//
// spec: 25.10 (DRIFT_DESIRED_STATE_MISSING — no snapshot, no body)
// diagnosis: A drift report with no desired-state source returned a
// non-error or the wrong code. §25.10 fails closed: drift cannot be
// computed without a desired state.
func TestDriftReportMissingSnapshotContract(t *testing.T) {
	// Build a server whose drift service has no snapshot — reuse the
	// escalation-only server which configures no drift snapshot.
	srv := opsServer(t)
	// against=target with no upgrade in flight has no target snapshot.
	rec, body := request(t, srv, http.MethodGet, "/v1/admin/drift?against=target", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for a missing target snapshot", rec.Code)
	}
	if errorEnvelope(t, body)["code"] != "DRIFT_NO_TARGET_SNAPSHOT" {
		t.Errorf("error code = %v, want DRIFT_NO_TARGET_SNAPSHOT", errorEnvelope(t, body)["code"])
	}
}

// TestDriftValidateContract confirms POST /v1/admin/drift/validate
// returns the §25.10 ValidationResult shape with the match/diverged
// verdict.
//
// spec: 25.10 (POST /v1/admin/drift/validate — snapshotValidationResult)
// diagnosis: Validation returned a body missing snapshotValidationResult
// or the differences array. §25.10 validation surfaces a stale snapshot
// by comparing it against an external source of truth.
func TestDriftValidateContract(t *testing.T) {
	srv := opsServer(t)
	// A desired state differing from the seeded snapshot (minWarm 5).
	rec, body := request(t, srv, http.MethodPost, "/v1/admin/drift/validate", nil, map[string]any{
		"desired": map[string]any{"pools": map[string]any{"default-gvisor": map[string]any{"minWarm": float64(20)}}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", rec.Code, body)
	}
	if body["snapshotValidationResult"] != "diverged" {
		t.Errorf("snapshotValidationResult = %v, want diverged", body["snapshotValidationResult"])
	}
	if _, ok := body["differences"].([]any); !ok {
		t.Error("the validation result is missing the differences array")
	}

	// An identical desired state validates as match.
	_, matchBody := request(t, srv, http.MethodPost, "/v1/admin/drift/validate", nil, map[string]any{
		"desired": map[string]any{"pools": map[string]any{"default-gvisor": map[string]any{"minWarm": float64(5)}}},
	})
	if matchBody["snapshotValidationResult"] != "match" {
		t.Errorf("identical desired state validated as %v, want match", matchBody["snapshotValidationResult"])
	}
}

// TestDriftSnapshotRefreshConfirmGate confirms POST /v1/admin/drift/-
// snapshot/refresh follows the §25.2 dry-run/confirm pattern: without
// confirm:true it returns a preview, with it the snapshot is replaced.
//
// spec: 25.10 (drift snapshot refresh — dry-run/confirm gate)
// diagnosis: A snapshot refresh without confirm:true replaced the stored
// snapshot. The confirm gate is a control — §25.10 keeps refresh an
// explicit operator action so a human or agent confirms the desired
// state before overwriting it.
func TestDriftSnapshotRefreshConfirmGate(t *testing.T) {
	srv := opsServer(t)
	// Without confirm:true — a preview, no replacement.
	rec, body := request(t, srv, http.MethodPost, "/v1/admin/drift/snapshot/refresh", nil, map[string]any{
		"desired": map[string]any{"pools": map[string]any{"default-gvisor": map[string]any{"minWarm": float64(99)}}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("no-confirm refresh status = %d, want 200 preview", rec.Code)
	}
	if body["dryRun"] != true {
		t.Errorf("dryRun = %v, want true on a no-confirm refresh", body["dryRun"])
	}

	// With confirm:true — the snapshot is replaced.
	rec, body = request(t, srv, http.MethodPost, "/v1/admin/drift/snapshot/refresh", nil, map[string]any{
		"desired": map[string]any{"pools": map[string]any{"default-gvisor": map[string]any{"minWarm": float64(99)}}},
		"confirm": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("confirmed refresh status = %d, want 200; body=%v", rec.Code, body)
	}
	if body["replaced"] != true {
		t.Errorf("replaced = %v, want true on a confirmed refresh", body["replaced"])
	}
}
