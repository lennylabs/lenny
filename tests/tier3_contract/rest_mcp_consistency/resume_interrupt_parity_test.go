// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract test for §15.2.1 REST ↔ MCP parity on the resume and
// interrupt transitions. Each admitted pre-state is seeded into two
// identical stores; the REST route runs against one and the MCP tool against
// the other, and both must make the same transition and answer the same
// session body. The Go SDK's MCP InterruptSession decodes that body.
package rest_mcp_consistency_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/sdks/client/go/lenny"
	"github.com/lennylabs/lenny/tests/testinfra/ownerprincipal"
)

const paritySessionID = "00000000-0000-4000-8000-0000000000b1"

// paritySurface is one store with its REST server and its MCP server.
type paritySurface struct {
	store sessionstore.Store
	rest  *httptest.Server
	mcp   *httptest.Server
}

// newParitySurface seeds one session in state st and serves it over REST
// and MCP, with the MCP tools dispatching through the REST route.
func newParitySurface(t *testing.T, st session.State) *paritySurface {
	t.Helper()
	store := memstore.New()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: paritySessionID, TenantID: "acme", UserID: "alice@acme.com", RuntimeRef: "echo",
		State: st, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	exec := executor.NewEchoExecutor()
	srv := sessionserver.New(store, sessionserver.Options{DevTenantHeader: true, Executor: exec})
	t.Cleanup(srv.Close)
	// spec: §10.2 (Session ownership) — REST requests run as the seeded rows' owner.
	rest := httptest.NewServer(ownerprincipal.Wrap(srv.Handler(), ownerprincipal.Alice))
	t.Cleanup(rest.Close)
	mcpSrv := mcp.NewServer()
	mcptools.Register(mcpSrv, mcptools.Deps{
		DevTenantFallback: true,
		Store:             store, SessionService: srv, Delivery: srv, Executor: exec,
		Clock: func() time.Time { return now }, TenantID: "acme",
	})
	m := httptest.NewServer(ownerprincipal.Wrap(mcpSrv.Handler(), ownerprincipal.Alice))
	t.Cleanup(m.Close)
	return &paritySurface{store: store, rest: rest, mcp: m}
}

// comparableBody drops the fields that carry the write's wall-clock time.
func comparableBody(body map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range body {
		if strings.HasSuffix(k, "At") {
			continue
		}
		out[k] = v
	}
	return out
}

// spec: §15.2.1 (REST/MCP Consistency Contract), §15.1 (REST API), §7.3
// (Retry and Resume)
// diagnosis: lenny/resume_session or lenny/interrupt_session made a
// different transition than POST /resume or POST /interrupt from the same
// pre-state, or answered a different session body. Both tools dispatch
// through the REST route, so a difference means the tool reads its result
// from somewhere else, such as a stale row or a hand-built status object.
func TestResumeAndInterruptToolsMatchREST_spec_15_2_1(t *testing.T) {
	for _, tc := range []struct {
		pre      session.State
		endpoint string
		tool     string
		want     session.State
	}{
		{session.StateAwaitingClientAction, "resume", "lenny/resume_session", session.StateResumePending},
		{session.StateSuspended, "resume", "lenny/resume_session", session.StateRunning},
		{session.StateRunning, "interrupt", "lenny/interrupt_session", session.StateSuspended},
	} {
		t.Run(tc.tool+" from "+string(tc.pre), func(t *testing.T) {
			viaREST := newParitySurface(t, tc.pre)
			viaMCP := newParitySurface(t, tc.pre)

			resp, raw := postJSON(t, viaREST.rest.URL+"/v1/sessions/"+paritySessionID+"/"+tc.endpoint, "acme", nil)
			if resp.StatusCode != 200 {
				t.Fatalf("POST /%s = %d %s", tc.endpoint, resp.StatusCode, raw)
			}
			var restBody map[string]any
			if err := json.Unmarshal(raw, &restBody); err != nil {
				t.Fatalf("decode REST body: %v", err)
			}
			mcpBody := mcpToolPayload(t, mcpCall(t, viaMCP.mcp.URL+"/mcp", tc.tool, map[string]any{"sessionId": paritySessionID}))

			if restBody["state"] != string(tc.want) {
				t.Errorf("REST state = %v, want %s", restBody["state"], tc.want)
			}
			if got, want := comparableBody(mcpBody), comparableBody(restBody); !reflect.DeepEqual(got, want) {
				t.Errorf("MCP body differs from REST body:\nMCP  %v\nREST %v", got, want)
			}
			for name, s := range map[string]*paritySurface{"REST": viaREST, "MCP": viaMCP} {
				row, err := s.store.Get(context.Background(), "acme", paritySessionID)
				if err != nil || row.State != tc.want {
					t.Errorf("%s stored state = %q (err %v), want %s", name, row.State, err, tc.want)
				}
			}
		})
	}
}

// spec: §15.2.1 (REST/MCP Consistency Contract), §15.1 (REST API), §7.2
// (Session state machine)
// diagnosis: POST /interrupt or lenny/interrupt_session admitted a session
// waiting in input_required, or the two surfaces refused it with different
// codes. The §15.1 interrupt row admits running alone, because suspending a
// session whose lenny/request_input wait stays live strands the wait; both
// surfaces must refuse with INVALID_STATE_TRANSITION and leave the row as it
// was.
func TestInterruptToolAndRESTRefuseInputRequired_spec_15_2_1(t *testing.T) {
	viaREST := newParitySurface(t, session.StateInputRequired)
	viaMCP := newParitySurface(t, session.StateInputRequired)

	resp, raw := postJSON(t, viaREST.rest.URL+"/v1/sessions/"+paritySessionID+"/interrupt", "acme", nil)
	if resp.StatusCode != 409 {
		t.Fatalf("POST /interrupt = %d %s, want 409", resp.StatusCode, raw)
	}
	if got := decodeRESTError(t, raw); got.Code != "INVALID_STATE_TRANSITION" {
		t.Errorf("REST code = %q, want INVALID_STATE_TRANSITION", got.Code)
	}
	res := mcpCall(t, viaMCP.mcp.URL+"/mcp", "lenny/interrupt_session", map[string]any{"sessionId": paritySessionID})
	if got := decodeMCPError(t, res); got.Code != "INVALID_STATE_TRANSITION" {
		t.Errorf("MCP code = %q, want INVALID_STATE_TRANSITION", got.Code)
	}
	for name, s := range map[string]*paritySurface{"REST": viaREST, "MCP": viaMCP} {
		row, err := s.store.Get(context.Background(), "acme", paritySessionID)
		if err != nil || row.State != session.StateInputRequired {
			t.Errorf("%s stored state = %q (err %v), want input_required", name, row.State, err)
		}
	}
}

// spec: §15.2.1 (REST/MCP Consistency Contract), §15.2 (MCP API)
// diagnosis: the Go SDK's MCPClient.InterruptSession did not return the
// session's id and state, so it decodes the tool result into the wrong
// shape or the tool no longer answers the session body.
func TestGoSDKMCPInterruptSessionReturnsTheRow_spec_15_2_1(t *testing.T) {
	s := newParitySurface(t, session.StateRunning)
	c, err := lenny.New(s.mcp.URL, lenny.WithTenant("acme"))
	if err != nil {
		t.Fatalf("lenny.New: %v", err)
	}
	got, err := c.MCP().InterruptSession(context.Background(), paritySessionID)
	if err != nil {
		t.Fatalf("InterruptSession: %v", err)
	}
	if got.ID != paritySessionID || got.State != lenny.State(session.StateSuspended) {
		t.Errorf("InterruptSession = id %q state %q, want %s suspended", got.ID, got.State, paritySessionID)
	}
}
