// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract tests for the §25.8 platform-upgrade orchestration
// endpoints served by lenny-ops: the mutating lifecycle surface
// (start/proceed/pause/rollback/verify) and GET /status. The suite pins
// the wire contract an agent depends on: the §25.2 canonical `progress`
// object (currentStep as the machine-readable phase name, the step
// counts), the §25.2 canonical error envelope on the lifecycle error
// paths, and the §25.4 idempotency-key requirement on the non-convergent
// start endpoint.
package ops_endpoints_test

import (
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/opsidem"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
	"github.com/lennylabs/lenny/pkg/ops/upgradeservice"
	"github.com/lennylabs/lenny/pkg/upgrade"
)

// upgradeOpsServer builds a §25.8 lenny-ops Server with only the
// platform-upgrade orchestrator wired (an in-memory singleton store), so
// the contract suite drives the lifecycle endpoints in isolation.
func upgradeOpsServer(t *testing.T) *opsserver.Server {
	t.Helper()
	return opsserver.New(opsserver.Options{
		Upgrade: upgradeservice.New(upgradeservice.Options{Store: upgradeservice.NewMemoryStore()}),
	})
}

// upgradeProgressField extracts the §25.2 progress object from an
// upgrade status/lifecycle response body, failing when it is absent.
func upgradeProgressField(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	prog, ok := body["progress"].(map[string]any)
	if !ok {
		t.Fatalf("response is missing the §25.2 progress object: %v", body)
	}
	return prog
}

// TestPlatformUpgradeProgressCurrentStepIsPhaseName pins the §25.2/§25.8
// progress contract on POST /upgrade/start and GET /upgrade/status: the
// progress object reports currentStep as the machine-readable phase name
// (a string), totalSteps as 7, and completedSteps as the 1-based step
// index. An agent driving the upgrade keys its phase logic on the
// currentStep identifier, so a numeric currentStep breaks it.
//
// spec: 25.2 (progress envelope — currentStep is the machine-readable
// step identifier, e.g. "OpsRoll"), 25.8 (upgrade progress — currentStep
// is the phase name; totalSteps is 7)
// diagnosis: The §25.8 upgrade status endpoint serialized
// progress.currentStep as a value other than the phase-name string the
// §25.2 canonical envelope requires. An agent that reads currentStep to
// decide whether it may roll back cannot map an integer to a phase.
func TestPlatformUpgradeProgressCurrentStepIsPhaseName(t *testing.T) {
	srv := upgradeOpsServer(t)

	rec, body := request(t, srv, http.MethodPost, "/v1/admin/platform/upgrade/start", nil,
		map[string]any{"version": "1.6.0"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("start status = %d, want 202; body=%v", rec.Code, body)
	}
	assertJSONContentType(t, rec)
	prog := upgradeProgressField(t, body)
	if cs, ok := prog["currentStep"].(string); !ok || cs != string(upgrade.Preflight) {
		t.Errorf("start progress.currentStep = %v (type %T), want the phase name %q",
			prog["currentStep"], prog["currentStep"], upgrade.Preflight)
	}
	if total, _ := prog["totalSteps"].(float64); total != float64(upgrade.TotalSteps) {
		t.Errorf("progress.totalSteps = %v, want %d", prog["totalSteps"], upgrade.TotalSteps)
	}
	if completed, _ := prog["completedSteps"].(float64); completed != 1 {
		t.Errorf("progress.completedSteps = %v, want 1 for Preflight", prog["completedSteps"])
	}

	// GET /status echoes the same canonical progress object.
	rec, body = request(t, srv, http.MethodGet, "/v1/admin/platform/upgrade/status", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if cs, ok := upgradeProgressField(t, body)["currentStep"].(string); !ok || cs != string(upgrade.Preflight) {
		t.Errorf("status progress.currentStep = %v (type %T), want %q",
			body["progress"], body["progress"], upgrade.Preflight)
	}
}

// TestPlatformUpgradeProceedAdvancesPhase confirms POST /upgrade/proceed
// advances the phase and the progress object's currentStep tracks the
// new phase name across the state machine.
//
// spec: 25.8 (POST /v1/admin/platform/upgrade/proceed advances one phase)
// diagnosis: A proceed did not advance the §25.8 phase, or the progress
// object's currentStep did not follow the new phase. An agent stepping
// the upgrade relies on the reported phase to decide the next call.
func TestPlatformUpgradeProceedAdvancesPhase(t *testing.T) {
	srv := upgradeOpsServer(t)
	request(t, srv, http.MethodPost, "/v1/admin/platform/upgrade/start", nil,
		map[string]any{"version": "1.6.0"})

	rec, body := request(t, srv, http.MethodPost, "/v1/admin/platform/upgrade/proceed", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("proceed status = %d, want 200; body=%v", rec.Code, body)
	}
	if body["phase"] != string(upgrade.OpsRoll) {
		t.Errorf("phase = %v, want %q", body["phase"], upgrade.OpsRoll)
	}
	if cs, _ := upgradeProgressField(t, body)["currentStep"].(string); cs != string(upgrade.OpsRoll) {
		t.Errorf("progress.currentStep = %v, want %q after proceed", cs, upgrade.OpsRoll)
	}
}

// TestPlatformUpgradePauseReportsPaused confirms POST /upgrade/pause
// carries the operator justification and marks the upgrade paused.
//
// spec: 25.8 (POST /v1/admin/platform/upgrade/pause)
// diagnosis: A pause did not record the awaiting-proceed state or the
// operator reason. The Operations Inventory reports the upgrade as
// paused off this flag; a lost flag hides the stall.
func TestPlatformUpgradePauseReportsPaused(t *testing.T) {
	srv := upgradeOpsServer(t)
	request(t, srv, http.MethodPost, "/v1/admin/platform/upgrade/start", nil,
		map[string]any{"version": "1.6.0"})

	rec, body := request(t, srv, http.MethodPost, "/v1/admin/platform/upgrade/pause", nil,
		map[string]any{"reason": "await maintenance window"})
	if rec.Code != http.StatusOK {
		t.Fatalf("pause status = %d, want 200; body=%v", rec.Code, body)
	}
	if body["paused"] != true {
		t.Errorf("paused = %v, want true", body["paused"])
	}
	if body["reason"] != "await maintenance window" {
		t.Errorf("reason = %v, want the operator justification echoed", body["reason"])
	}
}

// TestPlatformUpgradeErrorEnvelope confirms the §25.8 lifecycle error
// paths carry the §25.2 canonical error envelope. A proceed with no
// active upgrade is 409 UPGRADE_NOT_IN_PROGRESS (PERMANENT), a verify
// outside the Verification phase is 409, and a rollback past the
// SchemaMigration point of no return is 409 UPGRADE_ROLLBACK_UNAVAILABLE.
//
// spec: 25.2 (canonical error envelope), 25.8 (upgrade error codes and
// the rollback point of no return at SchemaMigration)
// diagnosis: A §25.8 lifecycle endpoint returned an error whose body is
// not the §25.2 envelope or whose code/category is wrong. An agent's
// retry and rollback logic keys on the code and category.
func TestPlatformUpgradeErrorEnvelope(t *testing.T) {
	// Proceed with no upgrade → 409 UPGRADE_NOT_IN_PROGRESS.
	srv := upgradeOpsServer(t)
	rec, body := request(t, srv, http.MethodPost, "/v1/admin/platform/upgrade/proceed", nil, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("proceed with no upgrade = %d, want 409", rec.Code)
	}
	errObj := errorEnvelope(t, body)
	for _, field := range []string{"code", "category", "message", "documentationUrl"} {
		if _, ok := errObj[field]; !ok {
			t.Errorf("error envelope is missing the %q field", field)
		}
	}
	if errObj["code"] != upgradeservice.CodeNoUpgrade {
		t.Errorf("code = %v, want %q", errObj["code"], upgradeservice.CodeNoUpgrade)
	}
	if errObj["category"] != "PERMANENT" || errObj["retryable"] != false {
		t.Errorf("category=%v retryable=%v, want PERMANENT/false", errObj["category"], errObj["retryable"])
	}

	// Verify outside the Verification phase → 409.
	srv = upgradeOpsServer(t)
	request(t, srv, http.MethodPost, "/v1/admin/platform/upgrade/start", nil, map[string]any{"version": "1.6.0"})
	rec, body = request(t, srv, http.MethodPost, "/v1/admin/platform/upgrade/verify", nil, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("verify at Preflight = %d, want 409", rec.Code)
	}
	if errorEnvelope(t, body)["category"] != "PERMANENT" {
		t.Errorf("verify wrong-phase category = %v, want PERMANENT", body["error"])
	}

	// Rollback past SchemaMigration → 409 UPGRADE_ROLLBACK_UNAVAILABLE.
	// Advance Preflight→OpsRoll→CRDUpdate→SchemaMigration (3 proceeds).
	for i := 0; i < 3; i++ {
		request(t, srv, http.MethodPost, "/v1/admin/platform/upgrade/proceed", nil, nil)
	}
	rec, body = request(t, srv, http.MethodPost, "/v1/admin/platform/upgrade/rollback", nil,
		map[string]any{"reason": "abort"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("late rollback = %d, want 409", rec.Code)
	}
	if errorEnvelope(t, body)["code"] != upgradeservice.CodeNotRollbackable {
		t.Errorf("late rollback code = %v, want %q", body["error"], upgradeservice.CodeNotRollbackable)
	}
}

// TestPlatformUpgradeStatusNoUpgrade confirms GET /upgrade/status with no
// upgrade ever started is 404 UPGRADE_NOT_IN_PROGRESS in the canonical
// envelope, distinct from the 409 the mutating endpoints return.
//
// spec: 25.8 (GET /v1/admin/platform/upgrade/status — 404 when no
// upgrade has been recorded)
// diagnosis: The status endpoint returned the wrong status or a
// non-canonical body when no upgrade exists. An agent polling status
// before starting an upgrade must distinguish "none" from an error.
func TestPlatformUpgradeStatusNoUpgrade(t *testing.T) {
	srv := upgradeOpsServer(t)
	rec, body := request(t, srv, http.MethodGet, "/v1/admin/platform/upgrade/status", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status with no upgrade = %d, want 404", rec.Code)
	}
	if errorEnvelope(t, body)["code"] != upgradeservice.CodeNoUpgrade {
		t.Errorf("code = %v, want %q", body["error"], upgradeservice.CodeNoUpgrade)
	}
}

// TestPlatformUpgradeStartRequiresIdempotencyKey confirms the §25.4
// idempotency-key requirement on the non-convergent POST /upgrade/start:
// at the Tier 2/3 (production) posture a start with no Idempotency-Key
// header is rejected 400 IDEMPOTENCY_KEY_REQUIRED in the canonical
// envelope, and a start carrying the header is accepted.
//
// spec: 25.4 (required-key endpoints reject a missing Idempotency-Key at
// Tier 2/3; upgrade start is a required-key endpoint)
// diagnosis: The §25.8 start endpoint accepted a mutating start without
// an idempotency key at production tier, or rejected one that carried a
// key. Without the key gate a retried start can launch a second upgrade.
func TestPlatformUpgradeStartRequiresIdempotencyKey(t *testing.T) {
	srv := opsserver.New(opsserver.Options{
		Upgrade:     upgradeservice.New(upgradeservice.Options{Store: upgradeservice.NewMemoryStore()}),
		Idempotency: opsidem.NewMemoryStore(),
		Production:  true,
	})

	// No Idempotency-Key header → 400 IDEMPOTENCY_KEY_REQUIRED.
	rec, body := request(t, srv, http.MethodPost, "/v1/admin/platform/upgrade/start", nil,
		map[string]any{"version": "1.6.0"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("keyless start = %d, want 400; body=%v", rec.Code, body)
	}
	if errorEnvelope(t, body)["code"] != "IDEMPOTENCY_KEY_REQUIRED" {
		t.Errorf("code = %v, want IDEMPOTENCY_KEY_REQUIRED", body["error"])
	}

	// With the header the start is accepted.
	rec, body = request(t, srv, http.MethodPost, "/v1/admin/platform/upgrade/start",
		map[string]string{opsidem.HeaderName: "start-key-01"}, map[string]any{"version": "1.6.0"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("keyed start = %d, want 202; body=%v", rec.Code, body)
	}
	if body["phase"] != string(upgrade.Preflight) {
		t.Errorf("phase = %v, want %q", body["phase"], upgrade.Preflight)
	}
}

// TestPlatformUpgradeStatusReportsCanonicalProgressEnvelope pins the
// §25.2/§25.8 progress-envelope contract on GET /upgrade/status: the
// response's progress object is the full §25.2 canonical envelope, not
// only the descriptive phase/step fields. The upgrade orchestrator is
// operator-paced (§25.8): a freshly started upgrade is immediately
// "awaiting the first proceed", the same paused-steady-state the §25.8
// line 1733 canonical example depicts (a GatewayRoll upgrade with
// currentStepDetail "Waiting for operator to call /upgrade/proceed").
// In that state the envelope reports a numeric percent derived from the
// step count and a lastProgressAt timestamp, while etaSeconds,
// etaMethod, and stalledForSeconds carry the "no estimate" values the
// same canonical example shows (etaMethod "none", etaSeconds and
// stalledForSeconds null) — present as envelope keys rather than
// omitted.
//
// spec: 25.8 ("GET /v1/admin/platform/upgrade/status and the Operations
// Inventory (Section 25.4) return the canonical progress envelope
// (Section 25.2)"; the line 1733 example: a paused platform_upgrade
// operation with progress.percent 65, progress.etaSeconds null,
// progress.etaMethod "none", progress.lastProgressAt populated,
// progress.stalledForSeconds null), 25.2 (percent — "Operations with
// discrete steps use completedSteps / totalSteps * 100"; lastProgressAt
// — "when progress last advanced"; stalledForSeconds — "populated when
// now() - lastProgressAt exceeds the operation kind's expected cadence
// ... null when advancing normally")
// diagnosis: The direct upgrade-status endpoint served only the bare
// phase/step-count fields instead of the full §25.2 canonical progress
// envelope. An agent that reads percent, etaSeconds, etaMethod, or
// stalledForSeconds off this endpoint (rather than the Operations
// Inventory) sees them silently absent instead of populated with the
// canonical envelope's "no estimate yet" representation.
func TestPlatformUpgradeStatusReportsCanonicalProgressEnvelope(t *testing.T) {
	srv := upgradeOpsServer(t)
	request(t, srv, http.MethodPost, "/v1/admin/platform/upgrade/start", nil,
		map[string]any{"version": "1.6.0"})

	rec, body := request(t, srv, http.MethodGet, "/v1/admin/platform/upgrade/status", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", rec.Code, body)
	}
	prog := upgradeProgressField(t, body)

	percent, ok := prog["percent"].(float64)
	if !ok {
		t.Fatalf("progress.percent missing or not numeric: %v", prog["percent"])
	}
	wantPercent := 1.0 / float64(upgrade.TotalSteps) * 100
	if percent < wantPercent-0.01 || percent > wantPercent+0.01 {
		t.Errorf("progress.percent = %v, want ~%v (completedSteps/totalSteps*100 for step 1 of %d)",
			percent, wantPercent, upgrade.TotalSteps)
	}

	if method, _ := prog["etaMethod"].(string); method != "none" {
		t.Errorf("progress.etaMethod = %q, want %q while awaiting the first proceed", method, "none")
	}
	if v, ok := prog["etaSeconds"]; !ok || v != nil {
		t.Errorf("progress.etaSeconds = %v (present=%v), want the key present and null while awaiting proceed", v, ok)
	}
	if v, ok := prog["stalledForSeconds"]; !ok || v != nil {
		t.Errorf("progress.stalledForSeconds = %v (present=%v), want the key present and null", v, ok)
	}

	lastProgressAt, ok := prog["lastProgressAt"].(string)
	if !ok || lastProgressAt == "" {
		t.Errorf("progress.lastProgressAt missing or empty: %v", prog["lastProgressAt"])
	}
}
