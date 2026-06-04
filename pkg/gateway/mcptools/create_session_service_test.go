// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// parseToolJSON decodes the JSON body a §8.5 tool emits as a single
// text-content block.
func parseToolJSON(t *testing.T, text string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("tool payload not JSON: %v\n%s", err, text)
	}
	return out
}

// fakeSessionCreator records the request lenny/create_session forwards and
// returns a canned response or a typed ServiceError, so the routing and
// error-projection behaviour of the tool can be asserted without the full
// §15.1 gateway. spec: §15.2.1 rule 1 line 1380. F-15.2.4.
type fakeSessionCreator struct {
	gotTenant string
	gotReq    sessionserver.CreateSessionRequest
	resp      sessionserver.CreateSessionResponse
	err       *sessionserver.ServiceError
}

func (f *fakeSessionCreator) CreateSessionService(_ context.Context, tenantID string, req sessionserver.CreateSessionRequest) (sessionserver.CreateSessionResponse, *sessionserver.ServiceError) {
	f.gotTenant = tenantID
	f.gotReq = req
	return f.resp, f.err
}

func newMCPWithCreator(t *testing.T, creator mcptools.SessionCreator) *mcp.Server {
	t.Helper()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:          memstore.New(),
		SessionCreator: creator,
		Executor:       executor.NewEchoExecutor(),
		Clock:          func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:         func() string { return "sess_mcp" },
		TenantID:       "acme",
	})
	return srv
}

// TestCreateSessionRoutesThroughSharedService_spec_15_2_1_1380 verifies
// the §15.2.1 rule-1 fix: when a SessionCreator is wired, the
// lenny/create_session tool forwards the request (runtimeRef, userId,
// environment) under the caller-resolved tenant to the shared §15.1
// service rather than writing the session row directly, and projects the
// service response onto the {sessionId, state, uploadToken} envelope —
// returning the same `created` state and §7.1 uploadToken the REST surface
// returns. F-15.2.4.
func TestCreateSessionRoutesThroughSharedService_spec_15_2_1_1380(t *testing.T) {
	creator := &fakeSessionCreator{}
	creator.resp.ID = "sess_created"
	creator.resp.State = "created"
	creator.resp.UploadToken = "tok_abc123"

	srv := newMCPWithCreator(t, creator)
	resp := call(t, srv.Handler(), "lenny/create_session",
		`{"runtimeRef":"echo","userId":"alice@acme.com","environment":"team-x"}`)

	// The request reached the shared service with the resolved tenant.
	if creator.gotTenant != "acme" {
		t.Errorf("forwarded tenant: got %q, want %q", creator.gotTenant, "acme")
	}
	if creator.gotReq.RuntimeRef != "echo" {
		t.Errorf("forwarded runtimeRef: got %q", creator.gotReq.RuntimeRef)
	}
	if creator.gotReq.UserID != "alice@acme.com" {
		t.Errorf("forwarded userId: got %q", creator.gotReq.UserID)
	}
	if creator.gotReq.Environment != "team-x" {
		t.Errorf("forwarded environment: got %q", creator.gotReq.Environment)
	}

	// The tool projects the §15.1 response onto the stable MCP envelope.
	payload := parseToolJSON(t, resultText(t, resp))
	if payload["sessionId"] != "sess_created" {
		t.Errorf("sessionId: got %v", payload["sessionId"])
	}
	if payload["state"] != "created" {
		t.Errorf("state: got %v, want created (REST/MCP parity)", payload["state"])
	}
	if payload["uploadToken"] != "tok_abc123" {
		t.Errorf("uploadToken: got %v (must carry the §7.1 token)", payload["uploadToken"])
	}
}

// TestCreateSessionSurfacesServiceError_spec_15_2_1_1384 verifies that a
// gate rejection from the shared service (e.g. the §11.2 quota gate)
// surfaces on the MCP wire as an error result carrying the canonical
// lenny code and details, so a client applies one error-handling strategy
// across REST and MCP. Before the fix the MCP path ran no gates and could
// never return QUOTA_EXCEEDED. spec: §15.2.1 rule 1/3; F-15.2.4.
func TestCreateSessionSurfacesServiceError_spec_15_2_1_1384(t *testing.T) {
	creator := &fakeSessionCreator{
		err: &sessionserver.ServiceError{
			HTTPStatus: 429,
			Code:       "QUOTA_EXCEEDED",
			Category:   "POLICY",
			Message:    "tenant session quota exhausted",
			Retryable:  false,
			Details:    map[string]any{"limit": float64(5)},
		},
	}
	srv := newMCPWithCreator(t, creator)
	resp := call(t, srv.Handler(), "lenny/create_session", `{"runtimeRef":"echo"}`)

	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("expected isError result, got %+v", resp)
	}
	env := readLennyErrorEnvelope(t, result)
	if env["code"] != "QUOTA_EXCEEDED" {
		t.Errorf("code: got %v, want QUOTA_EXCEEDED", env["code"])
	}
	// The §15.2.1 classifier assigns the category from the code on the MCP
	// side, matching the REST envelope for the same code.
	if env["category"] != "POLICY" {
		t.Errorf("category: got %v, want POLICY", env["category"])
	}
	details, _ := env["details"].(map[string]any)
	if details["limit"] != float64(5) {
		t.Errorf("details.limit: got %v, want 5", details["limit"])
	}
}

// TestCreateSessionLegacyPathWhenNoCreator_spec_15_2_4 confirms the
// minimal in-process gateway (no SessionCreator wired) still creates a
// session through the legacy direct-store path so the tool stays usable.
// F-15.2.4.
func TestCreateSessionLegacyPathWhenNoCreator_spec_15_2_4(t *testing.T) {
	srv, store := newMCP(t)
	resp := call(t, srv.Handler(), "lenny/create_session", `{"runtimeRef":"echo","userId":"alice"}`)
	payload := parseToolJSON(t, resultText(t, resp))
	if payload["sessionId"] != "sess_mcp" {
		t.Fatalf("legacy create sessionId: got %v", payload["sessionId"])
	}
	if _, err := store.Get(context.Background(), "acme", "sess_mcp"); err != nil {
		t.Fatalf("legacy create did not persist row: %v", err)
	}
}
