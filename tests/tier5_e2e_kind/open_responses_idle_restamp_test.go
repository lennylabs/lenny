// SPDX-License-Identifier: MIT

//go:build e2e_kind

package tier5_e2e_kind_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
	"github.com/lennylabs/lenny/tests/testinfra/sessiondriver"
)

const (
	// restampPool is the pool backing openAIConformanceRuntime, whose
	// sessions the Open Responses adapter holds across turns.
	restampPool = "echo-pool-sidecar"
	// restampInterval spaces the warm turns inside the idle bound.
	restampInterval = 25 * time.Second
	// restampTurns is the number of warm continuations; restampTurns *
	// restampInterval is more than twice the idle bound idleKindBound.
	restampTurns = 6
)

// restampTurn posts one Open Responses turn continuing prev and returns the
// response id, failing unless the turn answered 200.
func restampTurn(t *testing.T, d *sessiondriver.Driver, tenant, prev, input string) string {
	t.Helper()
	body := map[string]any{"model": openAIConformanceRuntime, "input": input, "user": "alice"}
	if prev != "" {
		body["previous_response_id"] = prev
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	status, out := t5PostJSON(t, d.BaseURL(), "/v1/responses", tenant, raw)
	var resp struct {
		ID string `json:"id"`
	}
	if status != http.StatusOK || json.Unmarshal(out, &resp) != nil || resp.ID == "" {
		t.Fatalf("turn %q after %q: status %d body %s", input, prev, status, out)
	}
	return resp.ID
}

// lastAgentActivity reads the session row's last_agent_activity_at as
// epoch seconds, or 0 when it is unset or unreadable.
func lastAgentActivity(t *testing.T, c *kind.Cluster, pgIP, sessionID string) float64 {
	t.Helper()
	sql := fmt.Sprintf("SELECT COALESCE(EXTRACT(EPOCH FROM last_agent_activity_at), 0) FROM sessions WHERE id = '%s';", sessionID)
	out := strings.TrimSpace(t5RunPsqlQuery(t, c, pgIP, "t5-restamp-activity", sql))
	var v float64
	if _, err := fmt.Sscanf(out, "%g", &v); err != nil {
		t.Logf("last_agent_activity_at of %s unreadable (%q): %v", sessionID, out, err)
		return 0
	}
	return v
}

// spec: 15 (Open Responses conversation sessions), 6.2 (Pod State Machine), 4.7.1 (Role and Gateway RPC Contract)
// diagnosis: warm Open Responses turns spaced inside the idle bound, over a
// span longer than it, did not keep the held session running on its pod:
// the session idle-suspended or moved pods, a continuation went cold onto a
// new session, or last_agent_activity_at never advanced. Warm turns write no
// idle stamp; the adapter counts each CH-MSGSOCK message write as activity
// under its session seal, and the idle suspension moves the idle clock to
// the adapter's answer instead of suspending. Check sealForIdleSuspension in
// pkg/gateway/sessionserver/suspend_release.go, the adapter's activity
// window, and the activity stamper wiring in cmd/lenny-gateway.
func TestWarmResponsesTurnsKeepTheHeldSessionRunningPastTheIdleBound_spec_15(t *testing.T) {
	d := sessiondriver.New(t, sessiondriver.Options{HTTPTimeout: 30 * time.Second})
	c := d.Cluster()
	requirePoolReadyPods(t, c, restampPool, 2)
	pgIP := t5DataStorePodIP(t, c, "postgres")
	if pgIP == "" {
		t.Skip("blocked: the e2e Postgres pod is not running, so last_agent_activity_at cannot be read")
	}
	tightenIdleBound(t, d, restampPool)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	tenant := newIdleTenant(ctx, t, d, "or-restamp")

	first := restampTurn(t, d, tenant, "", "turn-1")
	sessionID, ordinal, ok := strings.Cut(first, ".")
	if !ok || ordinal != "1" {
		t.Fatalf("first response id %q, want <session>.1", first)
	}
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer ccancel()
		_ = d.Terminate(cctx, tenant, sessionID)
	})
	sess, err := d.GetSession(ctx, tenant, sessionID)
	if err != nil || sess.State != "running" || sess.PodAssignment == "" {
		t.Fatalf("held session after the first turn = %+v (%v), want running on a pod", sess, err)
	}
	pod := sess.PodAssignment
	before := lastAgentActivity(t, c, pgIP, sessionID)
	start := time.Now()

	prev := first
	for n := 2; n <= restampTurns+1; n++ {
		time.Sleep(restampInterval)
		next := restampTurn(t, d, tenant, prev, fmt.Sprintf("turn-%d", n))
		if want := fmt.Sprintf("%s.%d", sessionID, n); next != want {
			t.Fatalf("turn %d after %s of warm turns = %q, want %q (warm on the held session)",
				n, time.Since(start).Round(time.Second), next, want)
		}
		prev = next
	}
	if span := time.Since(start); span <= idleKindBound*time.Second {
		t.Fatalf("the warm turns spanned %s, want longer than the idle bound of %ds", span, idleKindBound)
	}
	sess, err = d.GetSession(ctx, tenant, sessionID)
	if err != nil || sess.State != "running" || sess.PodAssignment != pod {
		t.Fatalf("held session after the warm turns = %+v (%v), want running on %s", sess, err, pod)
	}
	if after := lastAgentActivity(t, c, pgIP, sessionID); after <= before {
		t.Errorf("last_agent_activity_at = %v, want it advanced past %v by the idle clock's adapter answer", after, before)
	}
}
