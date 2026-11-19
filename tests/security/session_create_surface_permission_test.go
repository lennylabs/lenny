// SPDX-License-Identifier: MIT

//go:build security

// Security-tier tests for the session creation surfaces of the §10.2
// permission matrix against a real cmd/lenny-gateway over Postgres: a
// tenant-viewer or billing-viewer bearer is refused session creation with
// FORBIDDEN on REST, on both MCP create tools, and on the OpenAI Chat
// Completions adapter, and no refused call leaves a session row.
package security_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
)

// chatCompletion posts a Chat Completions request for the echo runtime to
// the gateway as sub in acme with role, and returns the status and the
// error code of the adapter's error envelope.
func (g *liveGateway) chatCompletion(sub string, role pkgauth.Role) (int, string, []byte) {
	g.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, g.base+"/v1/chat/completions",
		bytes.NewReader([]byte(`{"model":"echo","messages":[{"role":"user","content":"hi"}]}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+g.bearer(liveTenant, sub, role))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		g.t.Fatalf("POST /v1/chat/completions: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &env)
	return resp.StatusCode, env.Error.Code, raw
}

// sessionsOf counts the acme session rows owned by sub.
func (g *liveGateway) sessionsOf(sub string) int {
	g.t.Helper()
	return g.pgExec(`SELECT count(*) FROM sessions WHERE tenant_id = $1 AND user_id = $2`, liveTenant, sub)
}

// spec: §10.2 (Authorization and RBAC) Session creation surfaces, §15.2.1 (REST/MCP Consistency Contract), §15 (Built-in adapter single-shot compute model)
// diagnosis: a status other than 403 or a code other than FORBIDDEN on any
// surface means a role the "Create / cancel own sessions" row refuses can
// create a session through that surface, so the surface skips the
// permission check REST applies. A session row for the refused subject
// means the check ran after a store write. A FORBIDDEN for the user role
// means the check refuses a role the row grants.
func TestSessionCreationSurfacesRefuseRolesWithoutTheCreatePermission(t *testing.T) {
	g := startLiveGateway(t)
	for _, role := range []pkgauth.Role{pkgauth.RoleTenantViewer, pkgauth.RoleBillingViewer} {
		sub := "erin-" + string(role) + "@acme.com"
		t.Run(string(role)+"/rest", func(t *testing.T) {
			for _, path := range []string{"/v1/sessions", "/v1/sessions/start"} {
				code, body := g.rest(sub, role, http.MethodPost, path, map[string]any{"runtimeRef": "echo"})
				errBody, _ := body["error"].(map[string]any)
				if code != http.StatusForbidden || errBody["code"] != "FORBIDDEN" {
					t.Errorf("POST %s as %s = %d %v, want 403 FORBIDDEN", path, role, code, body)
				}
			}
		})
		t.Run(string(role)+"/mcp", func(t *testing.T) {
			for _, tool := range []string{"lenny/create_session", "lenny/create_and_start_session"} {
				isErr, code, text := g.tool(sub, role, tool, map[string]any{"runtimeRef": "echo"})
				if !isErr || code != "FORBIDDEN" {
					t.Errorf("%s as %s = (isError %v, code %q, %s), want FORBIDDEN", tool, role, isErr, code, text)
				}
			}
		})
		t.Run(string(role)+"/chat-completions", func(t *testing.T) {
			status, code, raw := g.chatCompletion(sub, role)
			if status != http.StatusForbidden || code != "FORBIDDEN" {
				t.Errorf("POST /v1/chat/completions as %s = %d %q, want 403 FORBIDDEN; body %s", role, status, code, raw)
			}
		})
		if n := g.sessionsOf(sub); n != 0 {
			t.Errorf("sessions owned by refused %s subject = %d, want 0", role, n)
		}
	}
	t.Run("user/mcp-create-session-admitted", func(t *testing.T) {
		if _, code, text := g.tool("alice@acme.com", pkgauth.RoleUser, "lenny/create_session", map[string]any{"runtimeRef": "echo"}); code == "FORBIDDEN" {
			t.Errorf("lenny/create_session as user was refused with FORBIDDEN (%s); the row grants user", text)
		}
	})
}
