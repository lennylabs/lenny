// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// callWithPrincipal invokes an MCP tool with the request context
// carrying an authenticated principal so per-call tenant resolution
// (callerTenantID) returns the principal's tenant. F-9.2.13 / F-15.2.15.
func callWithPrincipal(t *testing.T, h http.Handler, p authmw.Principal, tool, args string) map[string]any {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + tool + `","arguments":` + args + `}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader([]byte(body)))
	req = req.WithContext(authmw.WithPrincipal(req.Context(), p))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rr.Body.String())
	}
	return resp
}

// TestCreateSessionStampsPrincipalTenant_spec_15_2_1335 pins
// §9.2 / §16.1 / §15.2 line 1335 end-to-end: a multi-tenant deployment
// calling lenny/create_session via the authenticated MCP transport
// stamps the principal's tenant on the persisted session row — not
// the Register-time fallback. F-9.2.13 / F-15.2.15.
func TestCreateSessionStampsPrincipalTenant_spec_15_2_1335(t *testing.T) {
	store := memstore.New()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:    store,
		Executor: executor.NewEchoExecutor(),
		Clock:    func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:   func() string { return "sess_x" },
		// Register-time fallback is "fallback" — the unique sentinel
		// proves the production handler does NOT use it when an
		// authenticated principal is on the request context.
		TenantID: "fallback",
	})

	callWithPrincipal(t, srv.Handler(),
		authmw.Principal{TenantID: "acme", Subject: "user_alice"},
		"lenny/create_session",
		`{"runtimeRef":"echo"}`)

	row, err := store.Get(context.Background(), "acme", "sess_x")
	if err != nil {
		t.Fatalf("session was not created under tenant acme: %v", err)
	}
	if row.TenantID != "acme" {
		t.Errorf("row.TenantID = %q, want acme (the principal's tenant)", row.TenantID)
	}
	// Defence-in-depth: the same row must not be retrievable under the
	// Register-time fallback tenant.
	if _, err := store.Get(context.Background(), "fallback", "sess_x"); err == nil {
		t.Error("session is reachable under the Register-time fallback tenant; per-request tenant resolution is leaking")
	}
}

// TestCreateSessionFallsBackWhenNoPrincipal_spec_15_2_1335 pins the
// minimal-deployment behaviour: a request with no principal context
// (tests, the dev-headers transport) still creates the session under
// the Register-time fallback so the tool surface stays usable.
// F-9.2.13 / F-15.2.15.
func TestCreateSessionFallsBackWhenNoPrincipal_spec_15_2_1335(t *testing.T) {
	store := memstore.New()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:    store,
		Executor: executor.NewEchoExecutor(),
		Clock:    func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:   func() string { return "sess_x" },
		TenantID: "fallback",
	})

	// No principal context — same path tests and the dev-headers
	// transport take.
	call(t, srv.Handler(), "lenny/create_session", `{"runtimeRef":"echo"}`)

	row, err := store.Get(context.Background(), "fallback", "sess_x")
	if err != nil {
		t.Fatalf("unauthenticated path must fall back to Deps.TenantID: %v", err)
	}
	if row.TenantID != "fallback" {
		t.Errorf("row.TenantID = %q, want fallback", row.TenantID)
	}
}

// TestTenantsAreIsolatedBetweenPrincipals_spec_15_2_1335 pins
// multi-tenant isolation: two principals from different tenants
// each see only their own session even when the IDFunc happens to
// collide. spec: §10.2 tenant boundary; F-9.2.13 / F-15.2.15.
func TestTenantsAreIsolatedBetweenPrincipals_spec_15_2_1335(t *testing.T) {
	store := memstore.New()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:    store,
		Executor: executor.NewEchoExecutor(),
		Clock:    func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		// Unique ids per call so the two sessions do not collide on the
		// store layer.
		IDFunc:   newSequentialIDFunc("sess", 1),
		TenantID: "default",
	})

	callWithPrincipal(t, srv.Handler(),
		authmw.Principal{TenantID: "acme", Subject: "user_alice"},
		"lenny/create_session", `{"runtimeRef":"echo"}`)
	callWithPrincipal(t, srv.Handler(),
		authmw.Principal{TenantID: "globex", Subject: "user_bob"},
		"lenny/create_session", `{"runtimeRef":"echo"}`)

	got, err := listAllSessions(store, "acme")
	if err != nil {
		t.Fatalf("acme list: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("acme should see 1 session, got %d (%+v)", len(got), got)
	} else if got[0].TenantID != "acme" {
		t.Errorf("acme session.TenantID = %q, want acme", got[0].TenantID)
	}
	got2, err := listAllSessions(store, "globex")
	if err != nil {
		t.Fatalf("globex list: %v", err)
	}
	if len(got2) != 1 {
		t.Errorf("globex should see 1 session, got %d (%+v)", len(got2), got2)
	} else if got2[0].TenantID != "globex" {
		t.Errorf("globex session.TenantID = %q, want globex", got2[0].TenantID)
	}
}

// newSequentialIDFunc returns an idFn that emits prefix-N ids with N
// starting at start and incrementing on every call. Used by the
// multi-tenant isolation test so each create_session lands a unique
// row in the store.
func newSequentialIDFunc(prefix string, start int) func() string {
	n := start
	return func() string {
		id := prefix + "_" + itoa(n)
		n++
		return id
	}
}

// listAllSessions reads every session row scoped to tenantID. The test
// uses it to assert tenant isolation; the helper is intentionally a
// thin wrapper so failures pinpoint the boundary, not a generic store
// call.
func listAllSessions(store sessionstore.Store, tenantID string) ([]sessionstore.Session, error) {
	return store.List(context.Background(), tenantID, sessionstore.ListFilter{})
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
