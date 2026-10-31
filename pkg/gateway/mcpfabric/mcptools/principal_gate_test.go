// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/ownerprincipal"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// gateServer registers the tool surface over a fresh store with the given
// dev fallback setting.
func gateServer(devFallback bool) (*mcp.Server, sessionstore.Store) {
	store := memstore.New()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		SessionService:    ownerprincipal.SessionAccess{},
		DevTenantFallback: devFallback,
		Store:             store,
		Executor:          executor.NewEchoExecutor(),
		Clock:             func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:            func() string { return "sess_gate" },
		SessionCreator:    newSessionCreator(store, "sess_gate"),
		TenantID:          "default",
	})
	return srv, store
}

// postToolCall posts a tools/call with an optional principal and returns
// the raw response body.
func postToolCall(t *testing.T, h http.Handler, p *authmw.Principal, tool, args string) string {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + tool + `","arguments":` + args + `}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader([]byte(body)))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	if p != nil {
		req = req.WithContext(authmw.WithPrincipal(req.Context(), *p))
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr.Body.String()
}

// TestToolCallWithoutPrincipalRefusedOutsideDevMode asserts that, without
// DevTenantFallback, a tool call carrying no authenticated principal is
// refused with UNAUTHORIZED before the tool runs, on the HTTP transport
// and on the in-process DispatchTool entry point, and that no session is
// created under the Register-time fallback tenant or the tenant the
// client named by header. callerTenantID's `default` fallback is then
// unreachable outside dev mode (F-10.2.21).
//
// spec: §10.2 (Authentication), §15.1 (UNAUTHORIZED error code), §15.2.1
// (REST/MCP consistency).
func TestToolCallWithoutPrincipalRefusedOutsideDevMode(t *testing.T) {
	srv, store := gateServer(false)
	body := postToolCall(t, srv.Handler(), nil, "lenny/create_session", `{"runtimeRef":"echo"}`)
	if !strings.Contains(body, "UNAUTHORIZED") {
		t.Fatalf("HTTP tools/call without a principal: body %s, want UNAUTHORIZED", body)
	}
	res, ok, err := srv.DispatchTool(context.Background(), "lenny/create_session", json.RawMessage(`{"runtimeRef":"echo"}`))
	if !ok || err != nil || !res.IsError {
		t.Fatalf("DispatchTool without a principal: ok %t err %v isError %t, want an isError result", ok, err, res.IsError)
	}
	if b, _ := json.Marshal(res); !strings.Contains(string(b), "UNAUTHORIZED") {
		t.Fatalf("DispatchTool without a principal: result %s, want UNAUTHORIZED", b)
	}
	for _, tenant := range []string{"default", "acme"} {
		if _, err := store.Get(context.Background(), tenant, "sess_gate"); err == nil {
			t.Fatalf("a refused tool call created a session under tenant %s", tenant)
		}
	}

	p := &authmw.Principal{TenantID: "globex", Subject: "alice@acme.com"}
	if body := postToolCall(t, srv.Handler(), p, "lenny/create_session", `{"runtimeRef":"echo"}`); strings.Contains(body, "UNAUTHORIZED") {
		t.Fatalf("tools/call with a principal was refused: %s", body)
	}
	if _, err := store.Get(context.Background(), "globex", "sess_gate"); err != nil {
		t.Fatalf("tools/call with a principal did not create the session under its tenant: %v", err)
	}
}

// TestToolCallWithoutPrincipalAdmittedInDevMode asserts that under
// DevTenantFallback a principal-less tool call passes the pre-dispatch gate
// and reaches the tool, the dev-mode posture. create_session then refuses
// it with the session server's no-subject UNAUTHORIZED, because a session
// owner is the creating principal's subject in every mode, and creates
// nothing. A principal with a subject and no tenant creates the session
// under the Register-time tenant.
//
// spec: §17.4 (dev mode); §10.2 (Session ownership) rule 3.
func TestToolCallWithoutPrincipalAdmittedInDevMode(t *testing.T) {
	srv, store := gateServer(true)
	body := postToolCall(t, srv.Handler(), nil, "lenny/create_session", `{"runtimeRef":"echo"}`)
	if !strings.Contains(body, "principal_subject_required") {
		t.Fatalf("dev-mode principal-less create_session: body %s, want the session server's no-subject refusal", body)
	}
	if _, err := store.Get(context.Background(), "default", "sess_gate"); err == nil {
		t.Fatal("a principal-less create_session created a session")
	}
	p := &authmw.Principal{Subject: "alice@acme.com"}
	if body := postToolCall(t, srv.Handler(), p, "lenny/create_session", `{"runtimeRef":"echo"}`); strings.Contains(body, "UNAUTHORIZED") {
		t.Fatalf("dev-mode tools/call with a subject was refused: %s", body)
	}
	if _, err := store.Get(context.Background(), "default", "sess_gate"); err != nil {
		t.Fatalf("dev-mode tools/call did not create the session under the fallback tenant: %v", err)
	}
}

// TestUnauthorizedToolErrorNamesTheMissingSubject_spec_10_2 pins that a
// session-naming tool's no-subject refusal carries the reason the create
// tools carry, so one condition has one details.reason on the MCP surface.
//
// spec: §10.2 (Session ownership) rule 3
func TestUnauthorizedToolErrorNamesTheMissingSubject_spec_10_2(t *testing.T) {
	srv, _ := gateServer(true)
	p := &authmw.Principal{TenantID: "acme"}
	body := postToolCall(t, srv.Handler(), p, "lenny/get_task_tree", `{"sessionId":"sess_alice"}`)
	if !strings.Contains(body, "UNAUTHORIZED") || !strings.Contains(body, "principal_subject_required") {
		t.Fatalf("body %s, want UNAUTHORIZED with reason principal_subject_required", body)
	}
}
