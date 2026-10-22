// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the credential posture of the real
// cmd/lenny-gateway binary. Outside dev mode the gateway refuses a request
// that carries no bearer with 401 UNAUTHORIZED on every authenticated
// route, even when the request names a tenant through X-Lenny-Tenant-ID,
// and it keeps the spec's public routes reachable without credentials. In
// dev mode the X-Lenny-Tenant-ID dev header still names the tenant.
// F-10.2.21.
package tier4_integration_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// productionPostureFlags start the gateway outside dev mode. --dev-mode=false
// overrides the harness's LENNY_DEV_MODE=true default, and the OIDC issuer
// and client id are the §10.3 required platform keys outside dev mode.
var productionPostureFlags = []string{
	"--dev-mode=false",
	"--oidc-issuer-url=https://idp.acme.test",
	"--oidc-client-id=lenny-gateway",
}

// postureResponse is one recorded gateway response.
type postureResponse struct {
	status int
	code   string
	body   string
}

// postureDo issues one request with the given headers and decodes the
// error code from the canonical envelope when there is one.
func postureDo(t *testing.T, method, url string, headers map[string]string, body string) postureResponse {
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
	return postureResponse{status: resp.StatusCode, code: env.Error.Code, body: string(raw)}
}

// spec: §10.2 (Authentication), §15.1 (UNAUTHORIZED error code)
// diagnosis: a failure means the gateway binary outside dev mode admits a
// request with no bearer, so an unauthenticated caller reaches the session,
// admin, MCP, credential, or OpenAI-dialect handlers and can name the
// tenant it reads through X-Lenny-Tenant-ID. Check that buildHTTPSurface
// calls applyAuthPostureOptions with --dev-mode and that the route is not in
// publicRoutePatterns.
func TestProductionGatewayRefusesUnauthenticatedTenantHeader(t *testing.T) {
	gateway.SkipUnlessAvailable(t)
	gw := gateway.StartWith(t, append([]string{"--no-environment-policy", "allow-all"}, productionPostureFlags...)...)
	base := gw.BaseURL()
	forged := map[string]string{
		"X-Lenny-Tenant-ID": auth.DefaultTenantID,
		"X-Lenny-User-ID":   "mallory@globex.com",
		"X-Lenny-Roles":     "platform-admin",
	}
	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, "/v1/sessions", ""},
		{http.MethodPost, "/v1/sessions", `{"runtime":"echo"}`},
		{http.MethodGet, "/v1/sessions/sess-1", ""},
		{http.MethodGet, "/v1/runtimes", ""},
		{http.MethodGet, "/v1/admin/tenants", ""},
		{http.MethodGet, "/v1/admin/me", ""},
		{http.MethodGet, "/v1/admin/health", ""},
		{http.MethodPost, "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`},
		{http.MethodGet, "/v1/credentials", ""},
		{http.MethodPost, "/v1/chat/completions", `{"model":"echo","messages":[{"role":"user","content":"hi"}]}`},
	} {
		got := postureDo(t, c.method, base+c.path, forged, c.body)
		if got.status != http.StatusUnauthorized || got.code != "UNAUTHORIZED" {
			t.Errorf("%s %s with dev headers and no bearer: status %d code %q, want 401 UNAUTHORIZED (%s)",
				c.method, c.path, got.status, got.code, got.body)
		}
	}

	// A bearer the gateway did not sign is refused, not ignored.
	now := time.Now()
	tok, err := jwt.NewHMACSigner("forger", []byte("forger-secret-0123456789abcdef")).Sign(jwt.Claims{
		Subject: "mallory@globex.com", TenantID: auth.DefaultTenantID, Roles: []auth.Role{auth.RolePlatformAdmin},
		Typ: auth.TokenUserBearer, IssuedAt: now.Unix(), Expiry: now.Add(time.Minute).Unix(),
	})
	if err != nil {
		t.Fatalf("sign forged bearer: %v", err)
	}
	if got := postureDo(t, http.MethodGet, base+"/v1/sessions", map[string]string{"Authorization": "Bearer " + tok}, ""); got.status != http.StatusUnauthorized {
		t.Errorf("forged bearer: status %d code %q, want 401", got.status, got.code)
	}
}

// spec: §10.2 (Authentication), §15.1 (OpenAPI spec endpoint), §5.1
// (publishedMetadata visibility levels)
// diagnosis: a failure means requiring authentication outside dev mode
// also closed a route the specification serves without credentials, so a
// kubelet probe, a Prometheus scrape, an SDK generator, or a public
// metadata fetch is refused. Check publicRoutePatterns in
// cmd/lenny-gateway/authposture.go.
func TestProductionGatewayKeepsPublicRoutesPublic(t *testing.T) {
	gateway.SkipUnlessAvailable(t)
	gw := gateway.StartWith(t, append([]string{"--no-environment-policy", "allow-all"}, productionPostureFlags...)...)
	base := gw.BaseURL()
	for _, path := range []string{"/healthz", "/metrics", "/openapi.json", "/openapi.yaml", "/v1/openapi.json"} {
		if got := postureDo(t, http.MethodGet, base+path, nil, ""); got.status != http.StatusOK {
			t.Errorf("GET %s without credentials: status %d, want 200 (%s)", path, got.status, got.body)
		}
	}
	// /readyz answers from the readiness verdict, never from the auth gate.
	if got := postureDo(t, http.MethodGet, base+"/readyz", nil, ""); got.status == http.StatusUnauthorized {
		t.Errorf("GET /readyz without credentials: status 401, want the readiness verdict")
	}
	// The §5.1 public metadata route reaches its handler, which answers an
	// absent runtime with its own 404.
	if got := postureDo(t, http.MethodGet, base+"/v1/runtimes/absent/meta/agent-card", nil, ""); got.status != http.StatusNotFound || got.code != "RESOURCE_NOT_FOUND" {
		t.Errorf("GET public metadata without credentials: status %d code %q, want 404 RESOURCE_NOT_FOUND", got.status, got.code)
	}
}

// spec: §17.4 (dev mode), §18 Phase 4 (the minimal gateway reads the tenant
// from X-Lenny-Tenant-ID)
// diagnosis: a failure means the F-10.2.21 posture leaked into dev mode,
// so the dev-header transport the local development and integration
// suites rely on no longer names the request's tenant.
func TestDevGatewayHonoursTenantHeader(t *testing.T) {
	gateway.SkipUnlessAvailable(t)
	gw := gateway.StartWith(t, "--no-environment-policy", "allow-all", "--dev-mode")
	got := postureDo(t, http.MethodGet, gw.BaseURL()+"/v1/admin/me", map[string]string{
		"X-Lenny-Tenant-ID": "acme",
		"X-Lenny-User-ID":   "alice@acme.com",
	}, "")
	if got.status != http.StatusOK {
		t.Fatalf("dev header request: status %d, want 200 (%s)", got.status, got.body)
	}
	var me struct {
		TenantID string `json:"tenantId"`
	}
	if err := json.Unmarshal([]byte(got.body), &me); err != nil || me.TenantID != "acme" {
		t.Fatalf("dev header request: tenantId %q (decode err %v), want acme", me.TenantID, err)
	}
}
