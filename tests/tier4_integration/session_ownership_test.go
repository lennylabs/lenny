// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §10.2 session ownership rule through the real
// cmd/lenny-gateway binary. alice's session is invisible to bob, a `user`,
// over REST get, REST list, and MCP attach_session, both the JSON snapshot
// and the Streamable HTTP SSE channel. The SSE channel's authorizer is wired
// in cmd/lenny-gateway, outside the session route table and the MCP tool
// handlers, so this binary-level test is the one that reaches it.

package tier4_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// ownershipCaller is a dev-header principal of the live gateway. An empty
// user sends X-Lenny-User-ID with no value.
type ownershipCaller struct {
	user, roles string
}

var (
	ownAlice  = ownershipCaller{user: "alice@acme.com", roles: "user"}
	ownBob    = ownershipCaller{user: "bob@acme.com", roles: "user"}
	ownViewer = ownershipCaller{user: "bob@acme.com", roles: "tenant-viewer"}
	ownNobody = ownershipCaller{roles: "user"}
)

// request builds a request against the live gateway as c in the MCP
// tenant, bounded by ctx.
func (c ownershipCaller) request(ctx context.Context, t *testing.T, method, url string, body []byte) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", mcpTenant)
	req.Header.Set("X-Lenny-User-ID", c.user)
	req.Header.Set("X-Lenny-Roles", c.roles)
	return req
}

// rest sends one REST request as c and returns the status and body.
func (c ownershipCaller) rest(t *testing.T, base, method, path string, body []byte) (int, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := http.DefaultClient.Do(c.request(ctx, t, method, base+path, body))
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

// attachBody is the JSON-RPC tools/call body of attach_session on id.
func attachBody(id string) []byte {
	return []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + mcp.AttachToolName +
		`","arguments":{"sessionId":"` + id + `"}}}`)
}

// attachStream opens the attach_session SSE channel as c and returns the
// response's content type and, when the gateway answered JSON, the tool
// error it carried. An SSE response is closed after its headers arrive.
func (c ownershipCaller) attachStream(t *testing.T, base, id string) (string, *mcp.ToolError) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req := c.request(ctx, t, http.MethodPost, base+"/mcp", attachBody(id))
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("attach_session SSE: %v", err)
	}
	defer resp.Body.Close()
	ct := resp.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "text/event-stream") {
		return ct, nil
	}
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		// The auth chain refused the request before MCP dispatch, with
		// the §15.1 REST error envelope.
		var env struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatalf("decode attach_session refusal %d %s: %v", resp.StatusCode, raw, err)
		}
		return ct, &mcp.ToolError{Code: env.Error.Code, Msg: env.Error.Message}
	}
	_, te, err := mcp.DecodeToolCallResponse(raw)
	if err != nil {
		t.Fatalf("decode attach_session response %s: %v", raw, err)
	}
	return ct, te
}

// spec: §10.2 (Session ownership) rules 3, 4, 6, and 7; §15.2 (MCP API)
// diagnosis: the live gateway let a user that does not own a session see
// it. bob, a `user`, must receive RESOURCE_NOT_FOUND on REST get, must not
// list the session, and must receive RESOURCE_NOT_FOUND from MCP
// attach_session both as a JSON snapshot and on the SSE channel before any
// SSE byte; a tenant-viewer opens the SSE stream; and a dev-header request
// with an empty X-Lenny-User-ID receives UNAUTHORIZED on it. A failure on
// the SSE cases points at the attach authorizer in
// cmd/lenny-gateway/mcpsurface.go, which no other test reaches.
func TestSessionOwnershipThroughTheGateway_spec_10_2(t *testing.T) {
	gateway.SkipUnlessAvailable(t)
	gw := gateway.StartWith(t, "--no-environment-policy", "allow-all", "--dev-mode")
	base := gw.BaseURL()

	code, raw := ownAlice.rest(t, base, http.MethodPost, "/v1/sessions/start", []byte(`{"runtimeRef":"echo","userId":"bob@acme.com"}`))
	if code != http.StatusCreated {
		t.Fatalf("alice create+start = %d %s", code, raw)
	}
	var created struct {
		ID     string `json:"id"`
		UserID string `json:"userId"`
	}
	if err := json.Unmarshal(raw, &created); err != nil || created.ID == "" {
		t.Fatalf("decode create %s: %v", raw, err)
	}
	if created.UserID != "" && created.UserID != ownAlice.user {
		t.Errorf("session owner = %q, want %s; the body userId set it", created.UserID, ownAlice.user)
	}

	if code, raw := ownBob.rest(t, base, http.MethodGet, "/v1/sessions/"+created.ID, nil); code != http.StatusNotFound || !bytes.Contains(raw, []byte("RESOURCE_NOT_FOUND")) {
		t.Errorf("bob GET alice's session = %d %s, want 404 RESOURCE_NOT_FOUND", code, raw)
	}
	if _, raw := ownBob.rest(t, base, http.MethodGet, "/v1/sessions", nil); bytes.Contains(raw, []byte(created.ID)) {
		t.Errorf("bob's list includes alice's session: %s", raw)
	}

	code, raw = ownBob.rest(t, base, http.MethodPost, "/mcp", attachBody(created.ID))
	if code != http.StatusOK {
		t.Fatalf("bob attach_session snapshot = %d %s", code, raw)
	}
	if _, te, err := mcp.DecodeToolCallResponse(raw); err != nil || te == nil || te.Code != "RESOURCE_NOT_FOUND" {
		t.Errorf("bob attach_session snapshot = %v (err %v), want RESOURCE_NOT_FOUND", te, err)
	}

	for _, c := range []struct {
		name   string
		caller ownershipCaller
		code   string
	}{
		{"bob/user", ownBob, "RESOURCE_NOT_FOUND"},
		{"empty X-Lenny-User-ID", ownNobody, "UNAUTHORIZED"},
	} {
		ct, te := c.caller.attachStream(t, base, created.ID)
		if strings.HasPrefix(ct, "text/event-stream") || te == nil || te.Code != c.code {
			t.Errorf("%s attach_session SSE: content type %q, error %v; want a JSON %s before any SSE byte", c.name, ct, te, c.code)
		}
	}
	if ct, te := ownViewer.attachStream(t, base, created.ID); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("bob/tenant-viewer attach_session SSE: content type %q, error %v; want the event stream", ct, te)
	}
	if ct, te := ownAlice.attachStream(t, base, created.ID); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("alice attach_session SSE: content type %q, error %v; want the event stream", ct, te)
	}
}
