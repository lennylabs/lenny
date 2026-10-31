// SPDX-License-Identifier: MIT

//go:build integration

package tier4_integration_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
)

// spec: 15 (Open Responses conversation sessions), 6.2 (Pod State Machine), 7.2 (Interactive Session Model)
// diagnosis: after a held Open Responses session idle-suspended, a
// continuation of its newest response did not go cold onto a new session
// whose continuation parent is that response: it answered 409 or 202, ran on
// the suspended session through the message-delivery path, resumed it, or
// the new session's runtime did not receive the new input once. The
// suspended session must also hold no pod. Check warmEligible and serveCold
// in pkg/gateway/environment/translator/open_responses.go.
func TestIdleSuspendedResponsesSessionContinuesCold_spec_15(t *testing.T) {
	h := newComposeWith(t, lifecycleharness.Options{Replicas: 1, Pods: 4})
	h.WireOpenResponses(true)
	r := h.Replicas[0]

	first := responsesTurnOn(t, h, r, "", "before-idle")
	held := sessionOf(t, first)
	pod := h.Row(held).PodAssignment
	if pod == "" {
		t.Fatal("held session names no pod after its first turn")
	}
	idleSuspended(t, h, r, held)

	code, body := h.Do(r, http.MethodPost, "/v1/responses", map[string]any{
		"model": lifecycleharness.RuntimeRef, "input": "after-idle", "user": "alice@acme.com",
		"previous_response_id": first,
	})
	next, _ := body["id"].(string)
	if code != http.StatusOK || next == "" {
		t.Fatalf("continuation of the suspended session's response = %d %v, want 200", code, body)
	}
	cold := sessionOf(t, next)
	if cold == held || !strings.HasSuffix(next, ".1") {
		t.Fatalf("continuation id %q, want ordinal 1 on a new session (cold)", next)
	}
	if got := h.Row(cold).ContinuationParentID; got != first {
		t.Errorf("new session's continuation parent = %q, want %q", got, first)
	}
	h.Settle()
	if n := receivedCount(h, cold, "after-idle"); n != 1 {
		t.Errorf("the new session's runtime received the input %d times, want once", n)
	}
	if n := receivedCount(h, held, "after-idle"); n != 0 {
		t.Errorf("the suspended session's runtime received the input %d times, want none", n)
	}
	old := h.Row(held)
	if old.State != session.StateSuspended || old.PodAssignment != "" {
		t.Errorf("superseded session: state %q pod %q, want suspended with no pod", old.State, old.PodAssignment)
	}
	if n := len(old.Responses); n != 1 {
		t.Errorf("superseded session carries %d response records, want only its first turn's", n)
	}
	if n := inboxLen(t, h, held); n != 0 {
		t.Errorf("the suspended session's inbox holds %d entries, want none", n)
	}
	requireEmptyDLQ(t, h, held)
}
