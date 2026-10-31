// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract test for §15.2.1 REST ↔ MCP parity on the §10.2
// session-creation caller check. POST /v1/sessions and MCP
// lenny/create_session refuse an impersonation bearer with the same
// FORBIDDEN code and category.
package rest_mcp_consistency_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/gateway/callercheck"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
	"github.com/lennylabs/lenny/tests/testinfra/ownerprincipal"
)

// impersonationTokens reads every jti as a live impersonation row.
type impersonationTokens struct{}

func (impersonationTokens) Get(_ context.Context, tenantID, jti string) (issuedtokenstore.IssuedToken, error) {
	return issuedtokenstore.IssuedToken{JTI: jti, TenantID: tenantID, Subject: "alice@acme.com", ActSubject: "bob@platform"}, nil
}

// countingAudit accepts every rejection row and counts them.
type countingAudit struct{ rows int }

func (c *countingAudit) Append(context.Context, string, string, json.RawMessage, time.Time) (audit.Row, error) {
	c.rows++
	return audit.Row{}, nil
}

// asImpersonationBearer attaches alice's impersonation bearer principal to
// every request, standing in for the auth middleware.
func asImpersonationBearer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := authmw.Principal{Subject: "alice@acme.com", TenantID: "acme", JTI: "jti-impersonation"}
		next.ServeHTTP(w, r.WithContext(authmw.WithPrincipal(r.Context(), p)))
	})
}

// newCallerCheckServers stands up REST and MCP surfaces over one store
// whose caller check reads every bearer as an impersonation bearer. MCP
// lenny/create_session routes through the session server, as the gateway
// wires it.
func newCallerCheckServers(t *testing.T) (rest, mcpURL string, store sessionstore.Store, rec *countingAudit) {
	t.Helper()
	store = memstore.New()
	rec = &countingAudit{}
	srv := sessionserver.New(store, sessionserver.Options{
		DevTenantHeader: true,
		Executor:        executor.NewEchoExecutor(),
		CallerCheck:     callercheck.Deps{IssuedTokens: impersonationTokens{}, Audit: rec},
	})
	t.Cleanup(srv.Close)
	tsREST := httptest.NewServer(asImpersonationBearer(srv.Handler()))
	t.Cleanup(tsREST.Close)
	mcpSrv := mcp.NewServer()
	mcptools.Register(mcpSrv, mcptools.Deps{
		DevTenantFallback: true,
		Store:             store,
		SessionCreator:    srv,
		SessionService:    srv,
		Executor:          executor.NewEchoExecutor(),
		Clock:             func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:            func() string { return "sess_mcp" },
		TenantID:          "acme",
	})
	tsMCP := httptest.NewServer(asImpersonationBearer(mcpSrv.Handler()))
	t.Cleanup(tsMCP.Close)
	return tsREST.URL, tsMCP.URL, store, rec
}

// spec: §15.2.1 (REST/MCP Consistency Contract), §10.2 (Signing topology)
// diagnosis: POST /v1/sessions and lenny/create_session answer an
// impersonation bearer with different codes or categories, or one of them
// creates a session. Both must refuse with FORBIDDEN in the same category,
// each after writing one rejection row, and no session row may exist.
func TestCreateSessionCallerCheckParity_spec_15_2_1(t *testing.T) {
	restURL, mcpURL, store, rec := newCallerCheckServers(t)

	resp, raw := postJSON(t, restURL+"/v1/sessions", "acme", []byte(`{"runtimeRef":"echo"}`))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST /v1/sessions = %d, want 403; body %s", resp.StatusCode, raw)
	}
	var restEnv struct {
		Error capEnvelope `json:"error"`
	}
	if err := json.Unmarshal(raw, &restEnv); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	mcpEnv := mcpCapEnvelope(t, mcpURL, "lenny/create_session", map[string]any{"runtimeRef": "echo"})

	if restEnv.Error.Code != "FORBIDDEN" || mcpEnv.Code != "FORBIDDEN" {
		t.Errorf("codes REST=%q MCP=%q, want FORBIDDEN on both", restEnv.Error.Code, mcpEnv.Code)
	}
	if restEnv.Error.Category != mcpEnv.Category || restEnv.Error.Retryable != mcpEnv.Retryable {
		t.Errorf("REST (%q, %v) and MCP (%q, %v) category/retryable differ",
			restEnv.Error.Category, restEnv.Error.Retryable, mcpEnv.Category, mcpEnv.Retryable)
	}
	if rec.rows != 2 {
		t.Errorf("rejection rows = %d, want one per refused request (2)", rec.rows)
	}
	rows, err := store.List(context.Background(), "acme", sessionstore.ListFilter{})
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("sessions = %d, want 0", len(rows))
	}
}

// newOwnershipParityServers stands up REST and MCP surfaces over one store
// holding alice's running session sess_alice, both authenticated as bob,
// who does not own it. The MCP session tools dispatch through the session
// server, as the gateway wires them.
func newOwnershipParityServers(t *testing.T) (rest, mcpURL string) {
	t.Helper()
	store := memstore.New()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_alice", TenantID: "acme", UserID: ownerprincipal.Alice, RuntimeRef: "echo",
		State: session.StateRunning, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	srv := sessionserver.New(store, sessionserver.Options{DevTenantHeader: true, Executor: executor.NewEchoExecutor()})
	t.Cleanup(srv.Close)
	const bob = "bob@acme.com"
	tsREST := httptest.NewServer(ownerprincipal.Wrap(srv.Handler(), bob))
	t.Cleanup(tsREST.Close)
	mcpSrv := mcp.NewServer()
	mcptools.Register(mcpSrv, mcptools.Deps{
		DevTenantFallback: true,
		Store:             store,
		SessionCreator:    srv,
		SessionService:    srv,
		Executor:          executor.NewEchoExecutor(),
		Clock:             func() time.Time { return now },
		TenantID:          "acme",
	})
	tsMCP := httptest.NewServer(ownerprincipal.Wrap(mcpSrv.Handler(), bob))
	t.Cleanup(tsMCP.Close)
	return tsREST.URL, tsMCP.URL
}

// restNotFoundEnvelope sends method to url and decodes its error envelope,
// failing unless the status is 404.
func restNotFoundEnvelope(t *testing.T, method, url string) capEnvelope {
	t.Helper()
	req, _ := http.NewRequest(method, url, nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	var env struct {
		Error capEnvelope `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode %s %s: %v", method, url, err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("%s %s = %d %+v, want 404", method, url, resp.StatusCode, env.Error)
	}
	return env.Error
}

// spec: §15.2.1 (REST/MCP Consistency Contract), §10.2 (Session ownership) rule 7
// diagnosis: a user that does not own a session receives different answers
// from a REST session route and the MCP tool that mirrors it. Both must
// answer the non-owner with the RESOURCE_NOT_FOUND a missing session gets,
// in the same category, so neither surface reveals that the session exists
// or lets the non-owner read or act on it.
func TestNonOwnerNotFoundParity_spec_10_2(t *testing.T) {
	restURL, mcpURL := newOwnershipParityServers(t)
	for _, c := range []struct {
		method, path, tool string
	}{
		{http.MethodGet, "/v1/sessions/sess_alice", "lenny/get_session_status"},
		{http.MethodPost, "/v1/sessions/sess_alice/terminate", "lenny/terminate_session"},
	} {
		t.Run(c.tool, func(t *testing.T) {
			rest := restNotFoundEnvelope(t, c.method, restURL+c.path)
			mcpEnv := mcpCapEnvelope(t, mcpURL, c.tool, map[string]any{"sessionId": "sess_alice"})
			if rest.Code != "RESOURCE_NOT_FOUND" || mcpEnv.Code != rest.Code {
				t.Fatalf("codes REST=%q MCP=%q, want RESOURCE_NOT_FOUND on both", rest.Code, mcpEnv.Code)
			}
			if rest.Category != mcpEnv.Category || rest.Retryable != mcpEnv.Retryable {
				t.Errorf("REST (%q, %v) and MCP (%q, %v) category/retryable differ",
					rest.Category, rest.Retryable, mcpEnv.Category, mcpEnv.Retryable)
			}
		})
	}
}
