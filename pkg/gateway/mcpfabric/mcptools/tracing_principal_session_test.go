// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// seedTracingSessions creates two running sessions in tenant acme so a
// tracing registration can be checked against the session it was meant
// to write and against the session it must leave alone.
func seedTracingSessions(t *testing.T, store sessionstore.Store, ids ...string) {
	t.Helper()
	now := time.Now()
	for _, id := range ids {
		if err := store.Create(context.Background(), sessionstore.Session{
			ID: id, TenantID: "acme", State: session.StateRunning,
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
}

// TestSetTracingContextWritesPrincipalSessionNotArgument_spec_8_3
// pins the §8.3 binding rule: the gateway resolves the session a
// tracing registration writes from the authenticated caller's
// principal, so a caller bound to session A cannot register tracing
// context against session B by naming B in the call's arguments.
// spec: §8.3 (tracingContext registration and principal-resolved
// session); §9.1 (in-pod platform tool dispatch under the calling
// session's principal).
func TestSetTracingContextWritesPrincipalSessionNotArgument_spec_8_3(t *testing.T) {
	srv, store := newMCP(t)
	seedTracingSessions(t, store, "sess_a", "sess_b")

	resp := callWithPrincipal(t, srv.Handler(),
		authmw.Principal{TenantID: "acme", Subject: "user_alice", SessionID: "sess_a"},
		"lenny/set_tracing_context",
		`{"sessionId":"sess_b","context":{"trace_id":"t-1"}}`)

	if body := resultText(t, resp); !strings.Contains(body, `"sessionId":"sess_a"`) {
		t.Errorf("response body = %s, want the principal's session sess_a", body)
	}
	a, err := store.Get(context.Background(), "acme", "sess_a")
	if err != nil {
		t.Fatalf("get sess_a: %v", err)
	}
	if a.TracingContext["trace_id"] != "t-1" {
		t.Errorf("sess_a.TracingContext = %v, want trace_id=t-1", a.TracingContext)
	}
	b, err := store.Get(context.Background(), "acme", "sess_b")
	if err != nil {
		t.Fatalf("get sess_b: %v", err)
	}
	if len(b.TracingContext) != 0 {
		t.Errorf("sess_b.TracingContext = %v, want it untouched by the caller bound to sess_a", b.TracingContext)
	}
}

// TestSetTracingContextHonoursArgumentWithoutPrincipalSession_spec_8_3
// pins the fallback §8.3 states for a transport that binds no
// session-scoped principal: the caller-supplied `sessionId` selects the
// session when the principal carries none.
// spec: §8.3; §8.5 (platform tool signature).
func TestSetTracingContextHonoursArgumentWithoutPrincipalSession_spec_8_3(t *testing.T) {
	srv, store := newMCP(t)
	seedTracingSessions(t, store, "sess_a")

	resp := callWithPrincipal(t, srv.Handler(),
		authmw.Principal{TenantID: "acme", Subject: "user_alice"},
		"lenny/set_tracing_context",
		`{"sessionId":"sess_a","context":{"trace_id":"t-2"}}`)

	if body := resultText(t, resp); !strings.Contains(body, `"sessionId":"sess_a"`) {
		t.Errorf("response body = %s, want sess_a", body)
	}
	a, err := store.Get(context.Background(), "acme", "sess_a")
	if err != nil {
		t.Fatalf("get sess_a: %v", err)
	}
	if a.TracingContext["trace_id"] != "t-2" {
		t.Errorf("sess_a.TracingContext = %v, want trace_id=t-2", a.TracingContext)
	}
}

// TestSetTracingContextRefusesUnboundCaller_spec_8_3 pins the guard
// that replaces the argument-level check: a caller with neither a
// principal session nor a `sessionId` argument is refused with the
// §15.2.1 VALIDATION_ERROR / PERMANENT pair rather than reaching the
// store with an empty session id.
// spec: §8.3; §15.2.1 (error category and retryable parity).
func TestSetTracingContextRefusesUnboundCaller_spec_8_3(t *testing.T) {
	srv, store := newMCP(t)
	seedTracingSessions(t, store, "sess_a")

	resp := callWithPrincipal(t, srv.Handler(),
		authmw.Principal{TenantID: "acme", Subject: "user_alice"},
		"lenny/set_tracing_context",
		`{"context":{"trace_id":"t-3"}}`)

	result, _ := resp["result"].(map[string]any)
	env := readLennyErrorEnvelope(t, result)
	if env["code"] != "VALIDATION_ERROR" {
		t.Errorf("envelope.code = %v, want VALIDATION_ERROR", env["code"])
	}
	if env["category"] != "PERMANENT" {
		t.Errorf("envelope.category = %v, want PERMANENT", env["category"])
	}
	if env["retryable"] != false {
		t.Errorf("envelope.retryable = %v, want false", env["retryable"])
	}
	a, err := store.Get(context.Background(), "acme", "sess_a")
	if err != nil {
		t.Fatalf("get sess_a: %v", err)
	}
	if len(a.TracingContext) != 0 {
		t.Errorf("sess_a.TracingContext = %v, want no write from an unbound caller", a.TracingContext)
	}
}
