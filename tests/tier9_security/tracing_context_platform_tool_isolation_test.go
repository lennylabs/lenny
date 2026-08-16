// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 cross-session isolation for the gateway's
// lenny/set_tracing_context platform tool. §8.3 grants write authority
// over a session's tracing context to that session's own runtime, and a
// pod's local MCP servers never expose another session (§4.7), so the
// session a registration writes is the one the caller's principal is
// bound to (§9.1) rather than the one the call's arguments name.
//
// The suite drives the real gateway MCP surface (`mcptools.Register`
// over the server's HTTP handler, the same registration path the gateway
// binary wires) with the caller authenticated as session A, and asserts
// that a call naming session B in its arguments writes A and leaves B
// untouched.
//
// The write these cases forbid is unrepairable. pkg/delegation/tracing
// merges without overwriting and exposes no delete, so an identifier
// written onto another session stays there for that session's lifetime,
// silently shadows the value the victim later registers for the same
// key, and counts against the §8.3 entry bound for the victim and every
// child it delegates to.
//
// The sibling `tracing_context_session_isolation_test.go` pins the same
// boundary on the adapter's JSONL leg, where the addressing rule drops a
// frame that does not match the stream's own (session, slot) address.
package tier9_security_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// The two sessions the suite spans and the tenant that owns both. Both
// sessions live in one tenant, because the tenant is already resolved
// from the principal: the boundary under test is the one inside a
// tenant, which the tenant check does not cover.
const (
	platformTracingTenant   = "acme"
	platformTracingCaller   = "sess_alice"
	platformTracingVictim   = "sess_bob"
	platformTracingToolName = "lenny/set_tracing_context"
)

// platformTracingSeed is the tracing context session B carries before
// any call in this suite runs. Asserting against a populated row rather
// than an absent one makes a cross-session write visible as an added key
// instead of as the difference between nil and empty.
var platformTracingSeed = map[string]string{"trace_id": "victim-trace"}

// spec: §8.3 (a session's runtime holds write authority over that
// session's tracing context only); §4.7 (a pod's local MCP servers never
// expose other sessions); §9.1 (platform tool dispatch runs under the
// calling session's principal).
// diagnosis: a failure means one session registered tracing identifiers
// onto another session by naming it in the platform tool's arguments.
// The §8.3 merge rule never overwrites and the tree exposes no delete,
// so the injected entries are permanent and unrepairable: the victim's
// own later registration for the same key is accepted and ignored, its
// traces and every descendant's stitch under a value the caller chose,
// and entries injected up to the §8.3 bound make every later
// registration on the victim and its children fail for good.
func TestPlatformTracingToolRefusesToWriteAnotherSession(t *testing.T) {
	handler, store := newPlatformTracingServer(t)

	resp := callPlatformTracingTool(t, handler,
		authmw.Principal{TenantID: platformTracingTenant, Subject: "alice", SessionID: platformTracingCaller},
		`{"sessionId":"`+platformTracingVictim+`","context":{"trace_id":"caller-trace","span_id":"caller-span"}}`)

	// The call succeeds against the caller's own session rather than
	// being refused, and the response names that session.
	body := platformTracingResultText(t, resp)
	if !strings.Contains(body, `"sessionId":"`+platformTracingCaller+`"`) {
		t.Errorf("response body = %s, want the principal's session %s", body, platformTracingCaller)
	}

	requirePlatformTracingContext(t, store, platformTracingCaller, map[string]string{
		"trace_id": "caller-trace",
		"span_id":  "caller-span",
	})
	requirePlatformTracingContext(t, store, platformTracingVictim, platformTracingSeed)
}

// spec: §8.3 (a session's runtime holds write authority over that
// session's tracing context only); §4.7 (a pod's local MCP servers never
// expose other sessions); §9.1 (platform tool dispatch runs under the
// calling session's principal).
// diagnosis: a failure means the tool does not register against the
// session-bound caller's own session when the call carries no session
// argument at all. Either the registration is refused, leaving a
// session-bound runtime unable to record tracing identifiers on itself,
// or it landed on some other session, which the §8.3 merge rule makes
// permanent and unrepairable for that session and every child it
// delegates to.
func TestPlatformTracingToolRegistersTheCallerWithNoSessionArgument(t *testing.T) {
	handler, store := newPlatformTracingServer(t)

	resp := callPlatformTracingTool(t, handler,
		authmw.Principal{TenantID: platformTracingTenant, Subject: "alice", SessionID: platformTracingCaller},
		`{"context":{"trace_id":"caller-trace"}}`)

	body := platformTracingResultText(t, resp)
	if !strings.Contains(body, `"sessionId":"`+platformTracingCaller+`"`) {
		t.Errorf("response body = %s, want the principal's session %s", body, platformTracingCaller)
	}

	requirePlatformTracingContext(t, store, platformTracingCaller, map[string]string{"trace_id": "caller-trace"})
	requirePlatformTracingContext(t, store, platformTracingVictim, platformTracingSeed)
}

// newPlatformTracingServer registers the gateway MCP platform tools over
// a memory session store holding two running sessions in one tenant: the
// caller's session, which starts with no tracing context, and a second
// session carrying a seeded one.
func newPlatformTracingServer(t *testing.T) (http.Handler, sessionstore.Store) {
	t.Helper()
	store := memstore.New()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:    store,
		TenantID: platformTracingTenant,
	})

	now := time.Now()
	seed := func(id string, tc map[string]string) {
		if err := store.Create(context.Background(), sessionstore.Session{
			ID: id, TenantID: platformTracingTenant, UserID: "alice",
			State: session.StateRunning, TracingContext: tc,
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("seed session %s: %v", id, err)
		}
	}
	seed(platformTracingCaller, nil)
	seed(platformTracingVictim, platformTracingSeed)
	return srv.Handler(), store
}

// callPlatformTracingTool drives one tools/call for
// lenny/set_tracing_context against the MCP handler with the given
// authenticated principal and returns the decoded JSON-RPC response.
func callPlatformTracingTool(t *testing.T, h http.Handler, p authmw.Principal, args string) map[string]any {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` +
		platformTracingToolName + `","arguments":` + args + `}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader([]byte(body)))
	req = req.WithContext(authmw.WithPrincipal(req.Context(), p))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode MCP response: %v; body=%s", err, rr.Body.String())
	}
	return resp
}

// platformTracingResultText returns the tool result's text payload and
// fails when the call returned an error result, so a refusal surfaces as
// the refusal rather than as a missing-write assertion further down.
func platformTracingResultText(t *testing.T, resp map[string]any) string {
	t.Helper()
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("response carries no result: %+v", resp)
	}
	if result["isError"] == true {
		t.Fatalf("tools/call returned an error result: %+v", result)
	}
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("tool result carries no content: %+v", result)
	}
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	return text
}

// requirePlatformTracingContext fails when a session's persisted tracing
// context is not exactly want.
func requirePlatformTracingContext(t *testing.T, store sessionstore.Store, sessionID string, want map[string]string) {
	t.Helper()
	row, err := store.Get(context.Background(), platformTracingTenant, sessionID)
	if err != nil {
		t.Fatalf("get session %s: %v", sessionID, err)
	}
	got := row.TracingContext
	if len(got) != len(want) {
		t.Fatalf("session %s tracingContext = %v, want %v", sessionID, got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("session %s tracingContext[%q] = %q, want %q (full context %v)",
				sessionID, k, got[k], v, got)
		}
	}
}
