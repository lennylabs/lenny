// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration tests for Open Responses conversation sessions on the
// lifecycle harness. Each replica's POST /v1/responses is the translator's
// Open Responses handler, which Harness.WireOpenResponses wires as the
// gateway wires it. The binder's hold decision is fixed per test, because the
// gateway's binder adapter reads a runtime registry the harness does not
// seed.
package tier4_integration_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
)

// responsesTurnOn posts one Open Responses turn to replica r and returns the
// response id, failing unless the turn answered 200.
func responsesTurnOn(t *testing.T, h *lifecycleharness.Harness, r *lifecycleharness.Replica, prev, input string) string {
	t.Helper()
	body := map[string]any{"model": lifecycleharness.RuntimeRef, "input": input, "user": "alice@acme.com"}
	if prev != "" {
		body["previous_response_id"] = prev
	}
	code, out := h.Do(r, http.MethodPost, "/v1/responses", body)
	id, _ := out["id"].(string)
	if code != http.StatusOK || id == "" {
		t.Fatalf("turn %q after %q on %s: status %d body %v", input, prev, r.Name, code, out)
	}
	return id
}

// sessionOf returns the session id of response id.
func sessionOf(t *testing.T, id string) string {
	t.Helper()
	dot := strings.LastIndex(id, ".")
	if dot <= 0 {
		t.Fatalf("response id %q is not <session>.<n>", id)
	}
	return id[:dot]
}

// spec: 15 (Open Responses conversation sessions), 6.2 (Pod State Machine), 4.7.1 (Role and Gateway RPC Contract)
// diagnosis: an Open Responses conversation on a held session did not keep
// its session, pod, and coordination across turns: a continuation of the
// newest response bound a new session or a new pod, the runtime did not
// receive each turn's input once, or the session left running between turns.
// Check serveWarm and the hold decision in
// pkg/gateway/environment/translator/open_responses.go.
func TestHeldResponsesSessionServesThreeTurnsOnOnePod_spec_15(t *testing.T) {
	h := newComposeWith(t, lifecycleharness.Options{Replicas: 1, Pods: 4})
	h.WireOpenResponses(true)
	r := h.Replicas[0]

	first := responsesTurnOn(t, h, r, "", "turn-1")
	id := sessionOf(t, first)
	if !strings.HasSuffix(first, ".1") {
		t.Fatalf("first response id %q, want ordinal 1", first)
	}
	pod := h.Row(id).PodAssignment
	if pod == "" {
		t.Fatal("held session names no pod after its first turn")
	}
	holder := leaseHolder(t, h, id)
	prev := first
	for i, input := range []string{"turn-2", "turn-3"} {
		next := responsesTurnOn(t, h, r, prev, input)
		if want := fmt.Sprintf("%s.%d", id, i+2); next != want {
			t.Fatalf("turn %d id = %q, want %q (warm on the same session)", i+2, next, want)
		}
		prev = next
	}
	h.Settle()
	row := h.Row(id)
	if row.State != session.StateRunning || row.PodAssignment != pod {
		t.Errorf("held row: state %q pod %q, want running on %q", row.State, row.PodAssignment, pod)
	}
	if got := leaseHolder(t, h, id); got != holder {
		t.Errorf("coordination lease holder = %q, want unchanged %q", got, holder)
	}
	if len(row.Responses) != 3 {
		t.Errorf("response records = %d, want 3", len(row.Responses))
	}
	for _, input := range []string{"turn-1", "turn-2", "turn-3"} {
		if n := receivedCount(h, id, input); n != 1 {
			t.Errorf("runtime received %q %d times, want once", input, n)
		}
	}
	if _, ok := r.Registry.Get(id); !ok {
		t.Error("held session's binding was released between turns")
	}
}

// spec: 15 (Open Responses conversation sessions), 15 (Built-in adapter single-shot compute model), 6.2 (Pod State Machine)
// diagnosis: an Open Responses session the binder does not hold was not
// released at the end of its turn, or a continuation of its response ran on
// the released session rather than binding a new one. Check serveCold in
// pkg/gateway/environment/translator/open_responses.go.
func TestUnheldResponsesSessionReleasesAfterEachTurn_spec_15(t *testing.T) {
	h := newComposeWith(t, lifecycleharness.Options{Replicas: 1, Pods: 4})
	h.WireOpenResponses(false)
	r := h.Replicas[0]

	first := responsesTurnOn(t, h, r, "", "turn-1")
	firstSession := sessionOf(t, first)
	h.Settle()
	if st := h.Row(firstSession).State; st != session.StateCompleted {
		t.Errorf("first session state = %q, want completed", st)
	}
	if _, ok := r.Registry.Get(firstSession); ok {
		t.Error("first session's binding survived its turn")
	}
	second := responsesTurnOn(t, h, r, first, "turn-2")
	secondSession := sessionOf(t, second)
	if secondSession == firstSession || !strings.HasSuffix(second, ".1") {
		t.Fatalf("continuation id %q, want ordinal 1 on a new session", second)
	}
	row := h.Row(secondSession)
	if row.ContinuationParentID != first {
		t.Errorf("continuation parent = %q, want %q", row.ContinuationParentID, first)
	}
	h.Settle()
	if st := h.Row(secondSession).State; st != session.StateCompleted {
		t.Errorf("second session state = %q, want completed", st)
	}
}
