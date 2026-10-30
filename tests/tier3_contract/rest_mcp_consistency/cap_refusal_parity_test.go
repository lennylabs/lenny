// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract test for §15.2.1 REST ↔ MCP parity on the resume
// admission refusal. A message or a resume call to a suspended session at
// a full concurrent-session limit is refused on both surfaces with the
// same error envelope a refused session creation carries.
package rest_mcp_consistency_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/tests/testinfra/ownerprincipal"
)

// capEnvelope is the projection of an error envelope the parity test
// compares.
type capEnvelope struct {
	Code      string         `json:"code"`
	Category  string         `json:"category"`
	Retryable bool           `json:"retryable"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details"`
}

// newCapLimitedServers stands up REST and MCP surfaces over one store for
// tenant acme with a concurrent-session quota of one, held by a running
// session, and a suspended target. The MCP surface dispatches
// lenny/resume_session through the REST route and runs lenny/send_message
// with the session server as its delivery seam.
func newCapLimitedServers(t *testing.T) (*httptest.Server, *httptest.Server) {
	t.Helper()
	ctx := context.Background()
	store := memstore.New()
	tenants := tenantstore.NewMemory()
	if err := tenants.Create(ctx, tenantstore.Tenant{ID: "acme", MaxConcurrentSessions: 1}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	for _, row := range []sessionstore.Session{
		{ID: "sess_running", TenantID: "acme", UserID: ownerprincipal.Alice, RuntimeRef: "echo", State: session.StateRunning},
		{ID: "sess_suspended", TenantID: "acme", UserID: ownerprincipal.Alice, RuntimeRef: "echo", State: session.StateSuspended, PodAssignment: "pod-a"},
	} {
		if err := store.Create(ctx, row); err != nil {
			t.Fatalf("seed %s: %v", row.ID, err)
		}
	}
	exec := executor.NewEchoExecutor()
	rest := sessionserver.New(store, sessionserver.Options{DevTenantHeader: true, Executor: exec, Tenants: tenants})
	t.Cleanup(rest.Close)
	// spec: §10.2 (Session ownership) — REST requests run as the seeded rows' owner.
	tsREST := httptest.NewServer(ownerprincipal.Wrap(rest.Handler(), ownerprincipal.Alice))
	t.Cleanup(tsREST.Close)
	mcpSrv := mcp.NewServer()
	mcptools.Register(mcpSrv, mcptools.Deps{
		DevTenantFallback: true,
		Store:             store,
		SessionService:    rest,
		Delivery:          rest,
		Executor:          exec,
		Clock:             func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:            func() string { return "msg_cap" },
		TenantID:          "acme",
	})
	tsMCP := httptest.NewServer(ownerprincipal.Wrap(mcpSrv.Handler(), ownerprincipal.Alice))
	t.Cleanup(tsMCP.Close)
	return tsREST, tsMCP
}

// restCapEnvelope posts to path and decodes the 429 envelope.
func restCapEnvelope(t *testing.T, url string, body []byte) capEnvelope {
	t.Helper()
	resp, raw := postJSON(t, url, "acme", body)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("POST %s = %d, want 429; body %s", url, resp.StatusCode, raw)
	}
	var env struct {
		Error capEnvelope `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return env.Error
}

// mcpCapEnvelope calls tool and decodes its lenny/error envelope.
func mcpCapEnvelope(t *testing.T, url, tool string, args map[string]any) capEnvelope {
	t.Helper()
	res := mcpCall(t, url+"/mcp", tool, args)
	if res["isError"] != true {
		t.Fatalf("%s = %v, want a tool error", tool, res)
	}
	contents, _ := res["content"].([]any)
	for _, c := range contents {
		block, _ := c.(map[string]any)
		if block["type"] != "lenny/error" {
			continue
		}
		text, _ := block["text"].(string)
		var env capEnvelope
		if err := json.Unmarshal([]byte(text), &env); err != nil {
			t.Fatalf("decode %s: %v", text, err)
		}
		return env
	}
	t.Fatalf("%s result has no lenny/error block: %v", tool, res)
	return capEnvelope{}
}

// spec: §15.2.1 (REST/MCP Consistency Contract), §11.1 (Concurrent-session count)
// diagnosis: the REST and MCP surfaces refuse a resume of a suspended
// session at a full concurrent-session limit with different envelopes, or
// one surface admits it. POST /messages, POST /resume, lenny/send_message,
// and lenny/resume_session must each carry QUOTA_EXCEEDED with the same
// category, retryable flag, message, and the limit and active details a
// refused creation carries.
func TestResumeCapRefusalParity_spec_15_2_1(t *testing.T) {
	tsREST, tsMCP := newCapLimitedServers(t)
	base := tsREST.URL + "/v1/sessions/sess_suspended"
	got := map[string]capEnvelope{
		"POST /messages": restCapEnvelope(t, base+"/messages",
			[]byte(`{"messages":[{"role":"user","content":"x"}]}`)),
		"POST /resume":         restCapEnvelope(t, base+"/resume", nil),
		"lenny/send_message":   mcpCapEnvelope(t, tsMCP.URL, "lenny/send_message", map[string]any{"to": "sess_suspended", "message": "x"}),
		"lenny/resume_session": mcpCapEnvelope(t, tsMCP.URL, "lenny/resume_session", map[string]any{"sessionId": "sess_suspended"}),
	}
	want := capEnvelope{
		Code:    "QUOTA_EXCEEDED",
		Message: "the tenant has reached its concurrent-session limit",
		Details: map[string]any{"limit": float64(1), "active": float64(1)},
	}
	ref := got["POST /resume"]
	want.Category, want.Retryable = ref.Category, ref.Retryable
	for surface, env := range got {
		if !reflect.DeepEqual(env, want) {
			t.Errorf("%s envelope = %+v, want %+v", surface, env, want)
		}
	}
}
