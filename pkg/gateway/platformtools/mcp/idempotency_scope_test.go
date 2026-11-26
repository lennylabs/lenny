// SPDX-License-Identifier: MIT

package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/platformtools/mcp"
	"github.com/lennylabs/lenny/pkg/idempotency"
	"github.com/lennylabs/lenny/pkg/idempotency/idempotencymw"
)

// Test request headers that carry the principal scope in this package's
// idempotency tests. The gateway resolves the scope from the auth
// middleware's principal; these tests stand in for it with headers.
const (
	testSubjectHeader = "X-Test-Subject"
	testSessionHeader = "X-Test-Session"
)

// headerPrincipal resolves the tenant from X-Lenny-Tenant-ID, the subject
// from X-Test-Subject (alice when the header is absent), and the bound
// session from X-Test-Session.
func headerPrincipal(r *http.Request) (string, string, string) {
	subject := r.Header.Get(testSubjectHeader)
	if subject == "" {
		subject = "alice"
	}
	return r.Header.Get("X-Lenny-Tenant-ID"), subject, r.Header.Get(testSessionHeader)
}

// aliceScope is the record key alice in acme, bound to no session, files
// the stored key value under.
func aliceScope(value string) idempotency.Key {
	return idempotency.Key{TenantID: "acme", Subject: "alice", Value: value}
}

// rpcAs POSTs an MCP JSON-RPC request in acme as subject, bound to session
// when it is not empty.
func rpcAs(t *testing.T, h http.Handler, subject, session, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set(testSubjectHeader, subject)
	if session != "" {
		req.Header.Set(testSessionHeader, session)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// countingTool registers name on s with a handler that answers each call
// with a distinct handle, and returns the call counter.
func countingTool(s *mcp.Server, name string) *atomic.Int32 {
	var calls atomic.Int32
	s.RegisterTool(mcp.Tool{Name: name, InputSchema: json.RawMessage(`{}`)},
		func(_ context.Context, _ json.RawMessage) (mcp.ToolResult, error) {
			n := calls.Add(1)
			return mcp.ToolResult{Content: []mcp.ToolContent{{Type: "text", Text: fmt.Sprintf(`{"handle":"h-%d"}`, n)}}}, nil
		})
	return &calls
}

// TestMCPIdempotencyKeyFromTwoSubjectsDoesNotShareAResult checks that bob's
// lenny/create_session call with alice's idempotencyKey and arguments runs
// the tool rather than replaying alice's result, and that alice's retry
// still replays hers.
//
// spec: §11.5 (Idempotency)
func TestMCPIdempotencyKeyFromTwoSubjectsDoesNotShareAResult(t *testing.T) {
	s := mcp.NewServer()
	calls := countingTool(s, "lenny/create_session")
	s.SetIdempotency(mcp.IdempotencyConfig{
		Store:                idempotencymw.NewMemoryStore(),
		PrincipalFromRequest: headerPrincipal,
		Tools:                map[string]bool{"lenny/create_session": true},
	})
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"lenny/create_session","arguments":{"runtimeRef":"echo","idempotencyKey":"k1"}}}`

	alice := rpcAs(t, s.Handler(), "alice", "", body)
	bob := rpcAs(t, s.Handler(), "bob", "", body)
	if calls.Load() != 2 {
		t.Fatalf("tool ran %d times for two subjects, want 2", calls.Load())
	}
	if alice.Body.String() == bob.Body.String() {
		t.Fatalf("bob received alice's result %s", bob.Body.String())
	}
	if retry := rpcAs(t, s.Handler(), "alice", "", body); retry.Body.String() != alice.Body.String() || calls.Load() != 2 {
		t.Errorf("alice's retry = %s after %d calls, want her result %s after 2", retry.Body.String(), calls.Load(), alice.Body.String())
	}
}

// TestMCPDelegateTaskFromTwoSessionsOfOneSubjectDoesNotShareAChildHandle
// checks that lenny/delegate_task with one idempotencyKey and one set of
// arguments, called from alice's session s1 and from her session s2,
// creates a child for each session, and each session's retry replays its
// own child handle.
//
// spec: §11.5 (Idempotency)
func TestMCPDelegateTaskFromTwoSessionsOfOneSubjectDoesNotShareAChildHandle(t *testing.T) {
	s := mcp.NewServer()
	calls := countingTool(s, "lenny/delegate_task")
	s.SetIdempotency(mcp.IdempotencyConfig{
		Store:                idempotencymw.NewMemoryStore(),
		PrincipalFromRequest: headerPrincipal,
		Tools:                map[string]bool{"lenny/delegate_task": true},
	})
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"lenny/delegate_task","arguments":{"runtimeRef":"echo","idempotencyKey":"child-1"}}}`

	s1 := rpcAs(t, s.Handler(), "alice", "s1", body)
	s2 := rpcAs(t, s.Handler(), "alice", "s2", body)
	if calls.Load() != 2 {
		t.Fatalf("tool ran %d times for two sessions, want 2", calls.Load())
	}
	if s1.Body.String() == s2.Body.String() {
		t.Fatalf("session s2 received session s1's child handle %s", s2.Body.String())
	}
	if retry := rpcAs(t, s.Handler(), "alice", "s1", body); retry.Body.String() != s1.Body.String() {
		t.Errorf("s1 retry = %s, want %s", retry.Body.String(), s1.Body.String())
	}
	if retry := rpcAs(t, s.Handler(), "alice", "s2", body); retry.Body.String() != s2.Body.String() {
		t.Errorf("s2 retry = %s, want %s", retry.Body.String(), s2.Body.String())
	}
	if calls.Load() != 2 {
		t.Errorf("retries ran the tool again: %d calls, want 2", calls.Load())
	}
}

// TestMCPIdempotencyClaimsUnderThePrincipalScope checks that the hook files
// the record under the principal's tenant, subject, and bound session with
// the tool-namespaced key, and under no narrower scope.
//
// spec: §11.5 (Idempotency)
func TestMCPIdempotencyClaimsUnderThePrincipalScope(t *testing.T) {
	store := idempotencymw.NewMemoryStore()
	s := mcp.NewServer()
	countingTool(s, "lenny/delegate_task")
	s.SetIdempotency(mcp.IdempotencyConfig{
		Store:                store,
		PrincipalFromRequest: headerPrincipal,
		Tools:                map[string]bool{"lenny/delegate_task": true},
	})
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"lenny/delegate_task","arguments":{"idempotencyKey":"child-1"}}}`
	_ = rpcAs(t, s.Handler(), "alice", "s1", body)

	ctx := context.Background()
	want := idempotency.Key{TenantID: "acme", Subject: "alice", SessionID: "s1", Value: "mcp/lenny/delegate_task/child-1"}
	if _, found, err := store.Get(ctx, want); err != nil || !found {
		t.Fatalf("record under %+v: found=%v err=%v, want found", want, found, err)
	}
	unbound := want
	unbound.SessionID = ""
	if _, found, _ := store.Get(ctx, unbound); found {
		t.Errorf("a record exists under alice bound to no session, want none")
	}
}

// TestMCPIdempotencyRefusesAKeyedCallWithNoSubject checks that a keyed tool
// call whose principal has no subject is refused with UNAUTHORIZED, reason
// principal_subject_required, and that the tool does not run.
//
// spec: §11.5 (Idempotency)
func TestMCPIdempotencyRefusesAKeyedCallWithNoSubject(t *testing.T) {
	s := mcp.NewServer()
	calls := countingTool(s, "lenny/create_session")
	s.SetIdempotency(mcp.IdempotencyConfig{
		Store:                idempotencymw.NewMemoryStore(),
		PrincipalFromRequest: func(*http.Request) (string, string, string) { return "acme", "", "" },
		Tools:                map[string]bool{"lenny/create_session": true},
	})
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"lenny/create_session","arguments":{"idempotencyKey":"k1"}}}`
	r := rpcAs(t, s.Handler(), "", "", body)

	if calls.Load() != 0 {
		t.Errorf("tool ran %d times, want 0", calls.Load())
	}
	if !bytes.Contains(r.Body.Bytes(), []byte(`"UNAUTHORIZED"`)) || !bytes.Contains(r.Body.Bytes(), []byte("principal_subject_required")) {
		t.Errorf("response = %s, want UNAUTHORIZED with reason principal_subject_required", r.Body.String())
	}
}
