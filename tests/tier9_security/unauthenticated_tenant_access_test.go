// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security test for F-10.2.21: an unauthenticated caller cannot
// reach a registered tenant's data on a multi-tenant gateway outside dev
// mode by naming the tenant in the X-Lenny-Tenant-ID header. A dev-mode
// gateway and a production-posture gateway share one Postgres. The dev
// gateway registers the tenant acme and confirms the dev headers reach
// acme's admin record, which establishes that the tenant exists and that
// the header would select it. The production-posture gateway then refuses
// the same header-only requests with 401 UNAUTHORIZED on representative
// session, admin, MCP, and OpenAI-dialect routes.
package tier9_security_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// unauthCall issues one request and returns the status and error code.
func unauthCall(t *testing.T, method, url string, headers map[string]string, body string) (int, string, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &env)
	return resp.StatusCode, env.Error.Code, string(raw)
}

// spec: §10.2 (Authentication; `tenant_id` extraction),
// §13 (Security Model), §15.1 (UNAUTHORIZED error code)
// diagnosis: a failure on the production-posture gateway means an
// unauthenticated request that names a registered tenant through
// X-Lenny-Tenant-ID reached a handler, so tenant isolation rests on a
// client-controlled header (F-10.2.21). A failure on the dev gateway means
// the fixture did not register acme, and the production-posture result
// proves nothing.
func TestUnauthenticatedRequestCannotNameRegisteredTenant(t *testing.T) {
	gateway.SkipUnlessAvailable(t)
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations")})
	shared := []string{"--no-environment-policy", "allow-all", "--tenancy-mode=multi", "--multi-tenant", "--postgres-dsn=" + pg.DSN}

	dev := gateway.StartWith(t, append(shared, "--dev-mode")...)
	platformAdmin := map[string]string{
		"X-Lenny-Tenant-ID": auth.DefaultTenantID,
		"X-Lenny-User-ID":   "carol@acme.com",
		"X-Lenny-Roles":     "platform-admin",
	}
	if code, _, body := unauthCall(t, http.MethodPost, dev.BaseURL()+"/v1/admin/bootstrap", platformAdmin,
		`{"tenants":[{"id":"acme","displayName":"Acme Corp"}]}`); code != http.StatusOK {
		t.Fatalf("dev gateway bootstrap acme: status %d (%s)", code, body)
	}
	acmeHeaders := map[string]string{
		"X-Lenny-Tenant-ID": "acme",
		"X-Lenny-User-ID":   "mallory@globex.com",
		"X-Lenny-Roles":     "tenant-admin",
	}
	// The multi-tenant registry admits the dev header only for a registered
	// tenant, so a 200 naming acme proves the tenant exists.
	code, _, body := unauthCall(t, http.MethodGet, dev.BaseURL()+"/v1/admin/me", acmeHeaders, "")
	var me struct {
		TenantID string `json:"tenantId"`
	}
	if err := json.Unmarshal([]byte(body), &me); code != http.StatusOK || err != nil || me.TenantID != "acme" {
		t.Fatalf("dev gateway: header-only identity read as acme: status %d tenantId %q, want 200 as acme (%s)", code, me.TenantID, body)
	}

	prod := gateway.StartWith(t, append(
		shared,
		"--dev-mode=false",
		"--oidc-issuer-url=https://idp.acme.test",
		"--oidc-client-id=lenny-gateway",
	)...)
	for _, c := range []struct{ name, method, path, body string }{
		{"session list", http.MethodGet, "/v1/sessions", ""},
		{"session read", http.MethodGet, "/v1/sessions/sess-acme-1", ""},
		{"session create", http.MethodPost, "/v1/sessions", `{"runtime":"echo"}`},
		{"admin tenant read", http.MethodGet, "/v1/admin/tenants/acme", ""},
		{"admin identity", http.MethodGet, "/v1/admin/me", ""},
		{"mcp tools/call", http.MethodPost, "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"lenny/list_sessions","arguments":{}}}`},
		{"mcp environment", http.MethodPost, "/mcp/environments/prod", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`},
		{"credentials", http.MethodGet, "/v1/credentials", ""},
		{"openai chat", http.MethodPost, "/v1/chat/completions", `{"model":"echo","messages":[{"role":"user","content":"hi"}]}`},
	} {
		code, errCode, body := unauthCall(t, c.method, prod.BaseURL()+c.path, acmeHeaders, c.body)
		if code != http.StatusUnauthorized || errCode != "UNAUTHORIZED" {
			t.Errorf("%s (%s %s) naming acme without a bearer: status %d code %q, want 401 UNAUTHORIZED (%s)",
				c.name, c.method, c.path, code, errCode, body)
		}
	}
}
