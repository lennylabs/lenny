// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract tests for the §25.4 idempotency-key replay contract as
// it applies to the §25.8 platform-upgrade lifecycle endpoints
// (proceed/pause/rollback). §25.4 classifies these as multi-phase
// long-running operations: a replayed key returns the cached response
// without re-executing (so the upgrade state machine advances once per
// distinct key), and the record lives under the 7d long-running TTL
// rather than the 24h standard window because the agent may pause for
// days between steps.
package ops_endpoints_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/opsidem"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
	"github.com/lennylabs/lenny/pkg/ops/upgradeservice"
	"github.com/lennylabs/lenny/pkg/upgrade"
	"github.com/lennylabs/lenny/tests/testinfra/clockstep"
)

// startUpgradeKeyed starts a §25.8 upgrade carrying the required §25.4
// idempotency key, failing the test when the start is not accepted.
func startUpgradeKeyed(t *testing.T, srv *opsserver.Server, key string) {
	t.Helper()
	rec, body := request(t, srv, http.MethodPost, "/v1/admin/platform/upgrade/start",
		map[string]string{opsidem.HeaderName: key}, map[string]any{"version": "1.6.0"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("keyed start = %d, want 202; body=%v", rec.Code, body)
	}
}

// statusPhase reads GET /upgrade/status and returns the reported phase
// name, failing the test when the status call does not succeed.
func statusPhase(t *testing.T, srv *opsserver.Server) string {
	t.Helper()
	rec, body := request(t, srv, http.MethodGet, "/v1/admin/platform/upgrade/status", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", rec.Code, body)
	}
	return statusPhaseString(t, body)
}

// statusPhaseString extracts the machine-readable phase from an upgrade
// status/lifecycle response, preferring the top-level "phase" field and
// falling back to progress.currentStep.
func statusPhaseString(t *testing.T, body map[string]any) string {
	t.Helper()
	if p, ok := body["phase"].(string); ok {
		return p
	}
	if cs, ok := upgradeProgressField(t, body)["currentStep"].(string); ok {
		return cs
	}
	t.Fatalf("response carries no phase/currentStep: %v", body)
	return ""
}

// TestPlatformUpgradeProceedReplayDoesNotDoubleAdvance pins the §25.4
// single-advance replay guarantee on POST /upgrade/proceed: a proceed
// carrying an Idempotency-Key advances exactly one phase, and replaying
// the same key returns the cached response (X-Lenny-Idempotent-Replay)
// without advancing the state machine a second time. A distinct key on a
// later proceed advances again, confirming the guard is per-key and not
// a global stall.
//
// spec: 25.4 (a completed idempotency key replays the stored response
// without re-executing; keyed by (key, caller_id)), 25.8 (POST
// /v1/admin/platform/upgrade/proceed advances one phase)
// diagnosis: A replayed proceed re-executed the §25.8 state machine and
// advanced a second phase, so a benign network retry of a proceed
// skipped a phase the operator meant to inspect. The §25.4 replay path
// is not wired on the upgrade lifecycle endpoints.
func TestPlatformUpgradeProceedReplayDoesNotDoubleAdvance(t *testing.T) {
	srv := opsserver.New(opsserver.Options{
		Upgrade:     upgradeservice.New(upgradeservice.Options{Store: upgradeservice.NewMemoryStore()}),
		Idempotency: opsidem.NewMemoryStore(),
		Production:  true,
	})
	startUpgradeKeyed(t, srv, "start-key")

	// First proceed advances Preflight -> OpsRoll.
	rec, body := request(t, srv, http.MethodPost, "/v1/admin/platform/upgrade/proceed",
		map[string]string{opsidem.HeaderName: "proceed-1"}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("first proceed = %d, want 200; body=%v", rec.Code, body)
	}
	if got := statusPhaseString(t, body); got != string(upgrade.OpsRoll) {
		t.Fatalf("first proceed phase = %q, want %q", got, upgrade.OpsRoll)
	}

	// Replay the SAME key: the middleware returns the cached response and
	// must NOT re-run the state machine.
	rec, body = request(t, srv, http.MethodPost, "/v1/admin/platform/upgrade/proceed",
		map[string]string{opsidem.HeaderName: "proceed-1"}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("replayed proceed = %d, want 200; body=%v", rec.Code, body)
	}
	if rec.Header().Get("X-Lenny-Idempotent-Replay") != "true" {
		t.Errorf("replay missing X-Lenny-Idempotent-Replay header")
	}
	if got := statusPhaseString(t, body); got != string(upgrade.OpsRoll) {
		t.Errorf("replay body phase = %q, want the cached %q (no advance)", got, upgrade.OpsRoll)
	}
	// The authoritative state machine advanced exactly once.
	if got := statusPhase(t, srv); got != string(upgrade.OpsRoll) {
		t.Errorf("after replay the state machine is at %q, want %q — the replay double-advanced", got, upgrade.OpsRoll)
	}

	// A distinct key advances again: the guard is per-key, not a stall.
	rec, body = request(t, srv, http.MethodPost, "/v1/admin/platform/upgrade/proceed",
		map[string]string{opsidem.HeaderName: "proceed-2"}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("second-key proceed = %d, want 200; body=%v", rec.Code, body)
	}
	if got := statusPhase(t, srv); got != string(upgrade.CRDUpdate) {
		t.Errorf("after a distinct-key proceed the state machine is at %q, want %q", got, upgrade.CRDUpdate)
	}
}

// TestPlatformUpgradeProceedKeyHonoredUnderLongRunningTTL pins the §25.4
// long-running (7d) TTL classification on the upgrade lifecycle: a
// proceed key replayed 25 hours later — past the 24h standard window but
// within the 7d long-running window — is still honored (replays, no
// re-execution). Were proceed misclassified as a standard 24h mutation,
// the record would have expired and the replay would re-run the state
// machine, advancing a second phase.
//
// spec: 25.4 (two TTL classes: standard 24h, long-running 7d for
// multi-phase operations including upgrade proceed/pause/rollback where
// the agent may pause between steps; the endpoint picks the TTL by a
// static classification)
// diagnosis: An upgrade proceed key expired before 7d, so a retry after
// a multi-day operator pause re-executed and skipped a phase. The §25.4
// long-running TTL class is not applied to the §25.8 lifecycle endpoints.
func TestPlatformUpgradeProceedKeyHonoredUnderLongRunningTTL(t *testing.T) {
	origin := time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC)
	clk := clockstep.New(origin)
	srv := opsserver.New(opsserver.Options{
		Upgrade:        upgradeservice.New(upgradeservice.Options{Store: upgradeservice.NewMemoryStore()}),
		Idempotency:    opsidem.NewMemoryStore(),
		Production:     true,
		IdempotencyNow: clk.Now,
		// Leave the TTLs at the §25.4 built-ins (24h standard, 7d
		// long-running) so 25h is a genuine crossing of the standard window.
	})
	startUpgradeKeyed(t, srv, "start-key")

	rec, body := request(t, srv, http.MethodPost, "/v1/admin/platform/upgrade/proceed",
		map[string]string{opsidem.HeaderName: "proceed-lr"}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("first proceed = %d, want 200; body=%v", rec.Code, body)
	}
	if got := statusPhaseString(t, body); got != string(upgrade.OpsRoll) {
		t.Fatalf("first proceed phase = %q, want %q", got, upgrade.OpsRoll)
	}

	// Advance past the 24h standard TTL, still within the 7d long-running
	// window, then replay the same key.
	clk.Advance(25 * time.Hour)

	rec, body = request(t, srv, http.MethodPost, "/v1/admin/platform/upgrade/proceed",
		map[string]string{opsidem.HeaderName: "proceed-lr"}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("replay after 25h = %d, want 200; body=%v", rec.Code, body)
	}
	if rec.Header().Get("X-Lenny-Idempotent-Replay") != "true" {
		t.Errorf("replay after 25h missing X-Lenny-Idempotent-Replay — the long-running key expired at the 24h standard window")
	}
	if got := statusPhaseString(t, body); got != string(upgrade.OpsRoll) {
		t.Errorf("replay after 25h phase = %q, want the cached %q (no advance)", got, upgrade.OpsRoll)
	}
	if got := statusPhase(t, srv); got != string(upgrade.OpsRoll) {
		t.Errorf("after a 25h replay the state machine is at %q, want %q — the key was not honored under the 7d TTL", got, upgrade.OpsRoll)
	}
}
