//go:build component

// SPDX-License-Identifier: MIT

// Tier-4 integration test: the §10.5 RuntimeUpgrade state machine driven
// through the real cmd/lenny-gateway binary's /v1/admin/pools/{name}/upgrade
// endpoints against a live Postgres container (containers.StartPostgres).
// It exercises the operator journey of scaling a pool and then upgrading
// it: Pending -> Expanding -> Draining -> Contracting -> Complete, plus
// pause/resume and both rollback paths, all through the wire the real
// lenny-ctl admin pools upgrade CLI drives (POST .../upgrade/{start,
// proceed,pause,resume,rollback}, GET .../upgrade-status). Before this
// test the linear transition table and rollback rules were exercised only
// at the in-process manager level (runtimeupgrade_test.go) and as single
// 409 samples (admin_conflict_codes_test.go); no test drove the full
// journey through the live HTTP surface against a durable backing store.
package tier4_integration_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// poolUpgradeClient issues admin requests against a running gateway with
// the platform-admin dev-mode role, and decodes the JSON envelope into a
// map so a test can assert on individual wire fields (phase, priorPhase,
// pauseReason, hasPreviousPoolSpec, ...).
type poolUpgradeClient struct {
	t    *testing.T
	base string
}

func (c poolUpgradeClient) do(method, path string, body any) (int, map[string]any) {
	c.t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			c.t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, reader)
	if err != nil {
		c.t.Fatalf("new request %s %s: %v", method, path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set("X-Lenny-User-ID", "alice@acme.com")
	req.Header.Set("X-Lenny-Roles", "platform-admin")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		c.t.Fatalf("read body %s %s: %v", method, path, err)
	}
	var out map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			c.t.Fatalf("decode body %s %s: %v\nbody=%s", method, path, err, raw)
		}
	}
	return resp.StatusCode, out
}

// newPoolUpgradeGateway starts a real Postgres container and the real
// lenny-gateway binary against it, and returns a client plus a helper
// that creates a bootstrapped pool ready for an upgrade journey.
func newPoolUpgradeGateway(t *testing.T) (poolUpgradeClient, func(pool string)) {
	t.Helper()
	gateway.SkipUnlessAvailable(t)

	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	gw := gateway.StartWith(t, "--dev-mode", "--postgres-dsn="+pg.DSN)
	c := poolUpgradeClient{t: t, base: gw.BaseURL()}

	if code, body := c.do(http.MethodPost, "/v1/admin/bootstrap", map[string]any{
		"tenants": []map[string]any{{"id": "acme", "displayName": "Acme Corp"}},
	}); code != http.StatusOK {
		t.Fatalf("bootstrap tenant: status %d body=%v", code, body)
	}

	createPool := func(pool string) {
		t.Helper()
		code, body := c.do(http.MethodPost, "/v1/admin/pools", map[string]any{"name": pool})
		if code != http.StatusCreated {
			t.Fatalf("create pool %s: status %d body=%v", pool, code, body)
		}
	}
	return c, createPool
}

// phaseOf reads the "phase" field an upgrade-transition or upgrade-status
// response carries.
func phaseOf(t *testing.T, body map[string]any) string {
	t.Helper()
	phase, _ := body["phase"].(string)
	if phase == "" {
		t.Fatalf("response carries no phase field: %v", body)
	}
	return phase
}

// spec: §10.5 (`RuntimeUpgrade` State Machine: "Pending -> Expanding ->
// Draining -> Contracting -> Complete") and "GET
// /v1/admin/pools/{name}/upgrade-status ... managed by `lenny-ctl admin
// pools upgrade`"
//
// diagnosis: a failure here means the live admin HTTP surface — the wire
// lenny-ctl admin pools upgrade drives — does not carry a pool through the
// full linear §10.5 upgrade progression against a durable Postgres-backed
// store, or a terminal Complete upgrade still accepts a further proceed.
func TestPoolUpgradeJourney_ExpandDrainContractComplete(t *testing.T) {
	c, createPool := newPoolUpgradeGateway(t)
	const pool = "worker-pool-linear"
	createPool(pool)
	base := "/v1/admin/pools/" + pool + "/upgrade"

	code, body := c.do(http.MethodPost, base+"/start", map[string]any{"newImage": "worker@sha256:v2"})
	if code != http.StatusOK {
		t.Fatalf("start: status %d body=%v", code, body)
	}
	if got := phaseOf(t, body); got != "pending" {
		t.Fatalf("phase after start = %q, want pending", got)
	}

	wantPhases := []string{"expanding", "draining", "contracting", "complete"}
	for _, want := range wantPhases {
		code, body := c.do(http.MethodPost, base+"/proceed", nil)
		if code != http.StatusOK {
			t.Fatalf("proceed to %s: status %d body=%v", want, code, body)
		}
		if got := phaseOf(t, body); got != want {
			t.Fatalf("phase after proceed = %q, want %s", got, want)
		}
	}

	// §10.5: Complete is terminal — "no further state transitions."
	code, body = c.do(http.MethodPost, base+"/proceed", nil)
	if code != http.StatusConflict {
		t.Fatalf("proceed past complete: status %d, want 409; body=%v", code, body)
	}
	if body["error"] == nil {
		t.Fatalf("proceed past complete carries no error envelope: %v", body)
	}

	code, body = c.do(http.MethodGet, base+"-status", nil)
	if code != http.StatusOK {
		t.Fatalf("upgrade-status: status %d body=%v", code, body)
	}
	if got := phaseOf(t, body); got != "complete" {
		t.Fatalf("upgrade-status phase = %q, want complete", got)
	}
}

// spec: §10.5 ("Pause and resume. Any operator can pause the state machine
// at any point before Complete ... Pausing during Expanding halts new pod
// creation at the current count. The pause reason and timestamp are stored
// in the RuntimeUpgrade record" and the state table's Paused row: "Operator
// runs `lenny-ctl admin pools upgrade resume`" exits back to the captured
// state)
//
// diagnosis: a failure here means pause does not halt the live upgrade at
// its current phase with the operator's reason recorded, or resume does not
// restore the exact phase pause captured, over the real HTTP admin surface
// against Postgres.
func TestPoolUpgradeJourney_PauseResume(t *testing.T) {
	c, createPool := newPoolUpgradeGateway(t)
	const pool = "worker-pool-pause"
	createPool(pool)
	base := "/v1/admin/pools/" + pool + "/upgrade"

	if code, body := c.do(http.MethodPost, base+"/start", map[string]any{"newImage": "worker@sha256:v2"}); code != http.StatusOK {
		t.Fatalf("start: status %d body=%v", code, body)
	}
	if code, body := c.do(http.MethodPost, base+"/proceed", nil); code != http.StatusOK || phaseOf(t, body) != "expanding" {
		t.Fatalf("proceed to expanding: status %d body=%v", code, body)
	}

	code, body := c.do(http.MethodPost, base+"/pause", map[string]any{"reason": "investigating a new-pool health regression"})
	if code != http.StatusOK {
		t.Fatalf("pause: status %d body=%v", code, body)
	}
	if got := phaseOf(t, body); got != "paused" {
		t.Fatalf("phase after pause = %q, want paused", got)
	}
	if got, _ := body["priorPhase"].(string); got != "expanding" {
		t.Fatalf("priorPhase after pause = %q, want expanding (§10.5: pausing during Expanding halts at the current count)", got)
	}
	if got, _ := body["pauseReason"].(string); got != "investigating a new-pool health regression" {
		t.Fatalf("pauseReason = %q, want the operator-supplied reason to be stored on the record", got)
	}

	// While paused, proceed is rejected (§10.5: "All state machine
	// activity halts" until resume).
	if code, body := c.do(http.MethodPost, base+"/proceed", nil); code != http.StatusConflict {
		t.Fatalf("proceed while paused: status %d, want 409; body=%v", code, body)
	}

	code, body = c.do(http.MethodPost, base+"/resume", nil)
	if code != http.StatusOK {
		t.Fatalf("resume: status %d body=%v", code, body)
	}
	if got := phaseOf(t, body); got != "expanding" {
		t.Fatalf("phase after resume = %q, want expanding (resume restores the phase pause captured)", got)
	}

	// The state machine still runs to completion after the pause/resume
	// round trip.
	for _, want := range []string{"draining", "contracting", "complete"} {
		code, body := c.do(http.MethodPost, base+"/proceed", nil)
		if code != http.StatusOK {
			t.Fatalf("proceed to %s after resume: status %d body=%v", want, code, body)
		}
		if got := phaseOf(t, body); got != want {
			t.Fatalf("phase after proceed = %q, want %s", got, want)
		}
	}
}

// spec: §10.5 ("Rollback procedure ... From `Expanding`: `lenny-ctl admin
// pools upgrade rollback` -- sets new pool's minWarm to 0, restores full
// routing to old pool, transitions to Paused.")
//
// diagnosis: a failure here means a rollback issued while the live upgrade
// is at Expanding does not transition the record to Paused with a rollback
// reason over the real HTTP admin surface.
func TestPoolUpgradeJourney_RollbackFromExpanding(t *testing.T) {
	c, createPool := newPoolUpgradeGateway(t)
	const pool = "worker-pool-rollback-expanding"
	createPool(pool)
	base := "/v1/admin/pools/" + pool + "/upgrade"

	if code, body := c.do(http.MethodPost, base+"/start", map[string]any{"newImage": "worker@sha256:v2"}); code != http.StatusOK {
		t.Fatalf("start: status %d body=%v", code, body)
	}
	if code, body := c.do(http.MethodPost, base+"/proceed", nil); code != http.StatusOK || phaseOf(t, body) != "expanding" {
		t.Fatalf("proceed to expanding: status %d body=%v", code, body)
	}

	code, body := c.do(http.MethodPost, base+"/rollback", map[string]any{"restoreOldPool": false})
	if code != http.StatusOK {
		t.Fatalf("rollback from expanding: status %d body=%v", code, body)
	}
	if got := phaseOf(t, body); got != "paused" {
		t.Fatalf("phase after rollback from expanding = %q, want paused", got)
	}
	if got, _ := body["pauseReason"].(string); got != "rollback" {
		t.Fatalf("pauseReason after rollback from expanding = %q, want rollback", got)
	}
}

// spec: §10.5 ("From `Draining` or `Contracting`: Rollback is possible if
// the old pool's SandboxTemplate CRD has not yet been deleted.
// `lenny-ctl admin pools upgrade rollback --restore-old-pool` recreates the
// old pool configuration from the stored RuntimeUpgrade.previousPoolSpec
// field and restores routing ... the old pool's spec is always preserved in
// RuntimeUpgrade.previousPoolSpec until the upgrade reaches Complete.")
//
// diagnosis: a failure here means a Draining-phase rollback either succeeds
// without --restore-old-pool (silently discarding the old pool it should
// require restoring) or the live record does not actually carry the
// preserved previousPoolSpec a --restore-old-pool rollback depends on.
func TestPoolUpgradeJourney_RollbackFromDrainingRequiresRestoreOldPool(t *testing.T) {
	c, createPool := newPoolUpgradeGateway(t)
	const pool = "worker-pool-rollback-draining"
	createPool(pool)
	base := "/v1/admin/pools/" + pool + "/upgrade"

	if code, body := c.do(http.MethodPost, base+"/start", map[string]any{"newImage": "worker@sha256:v2"}); code != http.StatusOK {
		t.Fatalf("start: status %d body=%v", code, body)
	}
	if code, body := c.do(http.MethodGet, base+"-status", nil); code != http.StatusOK || body["hasPreviousPoolSpec"] != true {
		t.Fatalf("upgrade-status after start: status %d body=%v, want hasPreviousPoolSpec=true (§10.5: the old pool's spec is always preserved until Complete)", code, body)
	}
	for _, want := range []string{"expanding", "draining"} {
		code, body := c.do(http.MethodPost, base+"/proceed", nil)
		if code != http.StatusOK || phaseOf(t, body) != want {
			t.Fatalf("proceed to %s: status %d body=%v", want, code, body)
		}
	}

	// Without restoreOldPool the rollback is rejected: §10.5 requires the
	// explicit --restore-old-pool flag to recreate the old pool from
	// previousPoolSpec at Draining/Contracting.
	if code, body := c.do(http.MethodPost, base+"/rollback", map[string]any{"restoreOldPool": false}); code != http.StatusConflict {
		t.Fatalf("rollback from draining without restoreOldPool: status %d, want 409; body=%v", code, body)
	}

	code, body := c.do(http.MethodPost, base+"/rollback", map[string]any{"restoreOldPool": true})
	if code != http.StatusOK {
		t.Fatalf("rollback from draining with restoreOldPool: status %d body=%v", code, body)
	}
	if got := phaseOf(t, body); got != "paused" {
		t.Fatalf("phase after rollback from draining = %q, want paused", got)
	}
	if got, _ := body["pauseReason"].(string); got != "rollback (restore-old-pool)" {
		t.Fatalf("pauseReason after restore-old-pool rollback = %q, want %q", got, "rollback (restore-old-pool)")
	}
}
