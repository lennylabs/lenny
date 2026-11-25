// SPDX-License-Identifier: MIT

//go:build integration

// Integration test: the §12.8 user erasure waits for the terminal pipeline
// of every session it drains, so the session.completed billing event the
// pipeline appends after the pod release lands before the job pseudonymizes
// the user's billing events. Driven through the real cmd/lenny-gateway binary
// against a live Postgres, with a short terminal pipeline budget so the test
// can wait one whole budget after the job completes.
package integration_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// pipelineTestSealSeconds and pipelineTestReleaseAllowance set the gateway's
// terminal pipeline budget for the test to their sum. The seal window stays
// above the default usage quiescence timeout, which the gateway requires.
const (
	pipelineTestSealSeconds      = 8
	pipelineTestReleaseAllowance = 2 * time.Second
	pipelineTestBudget           = pipelineTestSealSeconds*time.Second + pipelineTestReleaseAllowance
)

// spec: §12.8 (Lifecycle fence and drain), §12.8 (Compliance Interfaces),
// §7.1 (Pipeline-pending session)
// diagnosis: a failure means the erasure job pseudonymized and verified the
// user's billing events while a drained session's terminal pipeline could
// still append its session.completed event with the raw user id: the gateway
// does not write the pipeline mark (sessions.terminal_pipeline_done_at,
// migration 0204) after the billing step, or the erasure job's
// store_deleting gate does not wait on it for sessions with and without a
// pod. A raw row found only after the budget wait means the pipeline wrote
// after the job completed.
func TestErasureLeavesNoRawUserIDInTheBillingEventsOfDrainedSessions(t *testing.T) {
	t.Parallel()
	gateway.SkipUnlessAvailable(t)

	pg := sharedDatabase(t)
	gw := gateway.StartWith(t, "--no-environment-policy", "allow-all", "--dev-mode",
		"--postgres-dsn="+pg.DSN, "--postgres-billing-audit-ddl-dsn="+pg.DSN, "--tenant-deletion-interval=1s",
		"--workspace-seal-max-duration-seconds=8", "--stream-failure-report-timeout=2s")
	base := gw.BaseURL()

	const alice, bob = "alice@acme.com", "bob@acme.com"
	code, _ := erasureAdminDo(t, base, http.MethodPost, "/v1/admin/bootstrap", "platform-admin", map[string]any{
		"tenants": []map[string]any{{"id": "acme", "displayName": "Acme Corp"}},
		"runtimes": []map[string]any{{
			"name": "echo", "image": "lenny/echo@sha256:abc", "labels": map[string]string{"tier": "test"},
		}},
		"users": []map[string]any{
			{"subject": alice, "tenantId": "acme", "email": alice, "roles": []string{"tenant-admin"}},
			{"subject": bob, "tenantId": "acme", "email": bob, "roles": []string{"tenant-admin"}},
		},
	})
	if code != http.StatusOK {
		t.Fatalf("bootstrap: status %d", code)
	}
	create := func(user, path string) string {
		t.Helper()
		code, created := erasureDoAs(t, base, http.MethodPost, path, user, "", map[string]any{"runtimeRef": "echo"})
		sid, _ := created["id"].(string)
		if code != http.StatusCreated || sid == "" {
			t.Fatalf("%s for %s: %d (%v)", path, user, code, created)
		}
		return sid
	}
	// A started session and a session that was created but never started,
	// so the drain covers a session with and without a running pod.
	aliceRunning := create(alice, "/v1/sessions/start")
	aliceCreated := create(alice, "/v1/sessions")
	create(bob, "/v1/sessions/start")
	for _, sid := range []string{aliceRunning, aliceCreated} {
		var pod string
		if err := pg.Pool.QueryRow(t.Context(), `SELECT pod_assignment FROM sessions WHERE id = $1::uuid`, sid).Scan(&pod); err != nil {
			t.Fatalf("read session %s: %v", sid, err)
		}
		t.Logf("session %s pod_assignment=%q", sid, pod)
	}
	rawRows := func(user string) int {
		return dbCountErasure(t, pg, `SELECT COUNT(*) FROM billing_events WHERE tenant_id = 'acme' AND user_id = $1`, user)
	}
	if n := rawRows(alice); n == 0 {
		t.Fatalf("precondition: alice has no billing events before the erasure")
	}

	code, resp := erasureAdminDo(t, base, http.MethodPost, "/v1/admin/users/"+alice+"/erase", "platform-admin",
		map[string]any{"tenantId": "acme"})
	jobID, _ := resp["jobId"].(string)
	if code != http.StatusAccepted || jobID == "" {
		t.Fatalf("erase %s: status %d (%v)", alice, code, resp)
	}
	awaitErasureCompleted(t, base, jobID, 30*time.Second)
	completedAt := time.Now()

	if n := rawRows(alice); n != 0 {
		t.Errorf("%d billing event(s) carry the erased user's raw id when the job completed, want 0", n)
	}
	for _, sid := range []string{aliceRunning, aliceCreated} {
		if n := dbCountErasure(t, pg, `SELECT COUNT(*) FROM billing_events WHERE session_id = $1::uuid AND event_type = 'session.completed'`, sid); n != 1 {
			t.Errorf("session %s has %d session.completed billing events when the job completed, want 1 written before the pseudonymization", sid, n)
		}
	}

	// No step of a drained session's pipeline writes after its budget, so a
	// raw row that has not landed one budget after completion never will.
	time.Sleep(time.Until(completedAt.Add(pipelineTestBudget + time.Second)))
	if n := rawRows(alice); n != 0 {
		t.Errorf("%d billing event(s) carry the erased user's raw id one terminal pipeline budget after the job completed, want 0", n)
	}
	if n := rawRows(bob); n == 0 {
		t.Error("another user's billing events lost their user id, want them untouched")
	}
}
