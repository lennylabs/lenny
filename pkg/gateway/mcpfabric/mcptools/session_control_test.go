// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
)

// spec: §15.2 — cancel_session force-cancels a session, marking
// it cancelled (the REST DELETE /v1/sessions/{id} edge). F-27.5.3.
func TestCancelSessionTool_marksCancelled(t *testing.T) {
	srv, store := newMCP(t)
	mkSession(t, store, "sess_x", session.StateRunning, "")

	resp := call(t, srv.Handler(), "lenny/cancel_session", `{"sessionId":"sess_x"}`)
	text := resultText(t, resp)
	if !strings.Contains(text, "cancelled") {
		t.Errorf("cancel_session result = %q, want cancelled", text)
	}
	row, err := store.Get(context.Background(), "acme", "sess_x")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if row.State != session.StateCancelled {
		t.Errorf("session state = %q, want cancelled", row.State)
	}
}

// spec: §27.6 — the playground_client_closed reason is a
// best-effort hint. A hint on an already-terminal session is an
// idempotent no-op rather than an INVALID_STATE_TRANSITION error, because
// the dropped-frame fallback is the §27.6 idle-timeout path. F-27.6.5.
func TestCancelSessionTool_bestEffortHintOnTerminalIsNoOp(t *testing.T) {
	srv, store := newMCP(t)
	mkSession(t, store, "sess_term", session.StateCompleted, "")

	resp := call(t, srv.Handler(), "lenny/cancel_session",
		`{"sessionId":"sess_term","reason":"playground_client_closed"}`)
	// Must not be an error result; the hint is accepted.
	text := resultText(t, resp)
	if !strings.Contains(text, "accepted") {
		t.Errorf("best-effort cancel on terminal session = %q, want accepted no-op", text)
	}
	// The session state is untouched.
	row, _ := store.Get(context.Background(), "acme", "sess_term")
	if row.State != session.StateCompleted {
		t.Errorf("terminal session state changed to %q", row.State)
	}
}

// spec: §27.6 — a best-effort hint for a session the gateway no
// longer knows about (already reclaimed) is accepted, not RESOURCE_NOT_FOUND.
// F-27.6.5.
func TestCancelSessionTool_bestEffortHintOnUnknownIsAccepted(t *testing.T) {
	srv, _ := newMCP(t)
	resp := call(t, srv.Handler(), "lenny/cancel_session",
		`{"sessionId":"gone","reason":"playground_client_closed"}`)
	text := resultText(t, resp)
	if !strings.Contains(text, "accepted") {
		t.Errorf("best-effort cancel on unknown session = %q, want accepted", text)
	}
}

// spec: §15.1 — without the best-effort reason, cancel_session on a
// terminal session returns INVALID_STATE_TRANSITION exactly as the REST
// DELETE does, so the generic §15.2 tool keeps its strict semantics.
func TestCancelSessionTool_strictTerminalRejected(t *testing.T) {
	srv, store := newMCP(t)
	mkSession(t, store, "sess_term2", session.StateCancelled, "")

	resp := call(t, srv.Handler(), "lenny/cancel_session", `{"sessionId":"sess_term2"}`)
	result, _ := resp["result"].(map[string]any)
	env := readLennyErrorEnvelope(t, result)
	if env["code"] != "INVALID_STATE_TRANSITION" {
		t.Errorf("error code = %v, want INVALID_STATE_TRANSITION", env["code"])
	}
}
