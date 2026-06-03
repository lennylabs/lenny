// SPDX-License-Identifier: MIT

package opsserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/common/scopes"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/ops/operations"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
)

// meConfigFixture is the shared §25.4 me platform context for the tests.
func meConfigFixture() *opsserver.MeConfig {
	return &opsserver.MeConfig{
		InstallationID:           "inst-abc",
		Version:                  "1.5.0",
		Tier:                     "tier2",
		Namespace:                "lenny-system",
		OpsServiceURL:            "https://ops.example.com",
		GatewayURL:               "https://lenny-gateway:8443",
		Issuer:                   "https://auth.example.com",
		TokenRefreshBeforeExpiry: "60s",
		Capabilities: opsserver.Capabilities{
			PrometheusAvailable: true,
			LockMemoryTier:      "single-replica-only",
			TenantFiltering:     true,
			OpsReplicas:         1,
		},
	}
}

// meServer wires a dev-mode (no auth gate) Server so the tests inject the
// principal on the request context, matching the existing opsserver test
// convention.
func meServer() (*opsserver.Server, *captureAudit) {
	audit := &captureAudit{}
	srv := opsserver.New(opsserver.Options{
		Audit: audit,
		Me:    meConfigFixture(),
	})
	return srv, audit
}

// spec §25.4 lines 1577-1623: GET /v1/admin/me returns the identity,
// authorization (with authorizedLockScopes + subjectToGuards), platform
// context, capabilities, and discovery links for a platform-admin.
func TestMePlatformAdmin(t *testing.T) {
	srv, audit := meServer()
	p := platformAdmin("sa-watchdog")
	before := metricFamilyTotal(t, "lenny_ops_me_requests_total")
	rec, body := getAuthed(t, srv, "/v1/admin/me", &p)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	identity := body["identity"].(map[string]any)
	if identity["sub"] != "sa-watchdog" {
		t.Errorf("identity.sub = %v, want sa-watchdog", identity["sub"])
	}
	if identity["issuer"] != "https://auth.example.com" {
		t.Errorf("identity.issuer = %v", identity["issuer"])
	}

	authz := body["authorization"].(map[string]any)
	if authz["tenantScope"] != "*" {
		t.Errorf("tenantScope = %v, want * for platform-admin", authz["tenantScope"])
	}
	if authz["scope"] != "tools:*" {
		t.Errorf("scope = %v, want tools:* for an absent scope claim", authz["scope"])
	}
	lockScopes := authz["authorizedLockScopes"].([]any)
	if len(lockScopes) == 0 {
		t.Error("authorizedLockScopes must be populated for platform-admin")
	}
	guards := authz["subjectToGuards"].(map[string]any)
	if _, ok := guards["confirmRequiredFor"]; !ok {
		t.Error("subjectToGuards.confirmRequiredFor missing")
	}

	platform := body["platform"].(map[string]any)
	if platform["installationId"] != "inst-abc" || platform["version"] != "1.5.0" {
		t.Errorf("platform block = %v", platform)
	}

	caps := body["capabilities"].(map[string]any)
	if caps["prometheusAvailable"] != true {
		t.Errorf("capabilities.prometheusAvailable = %v, want true", caps["prometheusAvailable"])
	}
	if caps["mcpManagementServer"] != true {
		t.Errorf("capabilities.mcpManagementServer = %v, want true (server builds the MCP registry)", caps["mcpManagementServer"])
	}

	links := body["links"].(map[string]any)
	if links["authorizedTools"] != "/v1/admin/me/authorized-tools" {
		t.Errorf("links.authorizedTools = %v", links["authorizedTools"])
	}
	if _, ok := links["myRecentAudit"]; !ok {
		t.Error("links.myRecentAudit must be present for an identified caller")
	}

	// Dev mode wires no rate limiter, so the (omitempty) rateLimits block
	// is absent; the authed surface is covered by TestMeRateLimitsSurface.
	if _, ok := body["rateLimits"]; ok {
		t.Error("rateLimits must be omitted when no limiter is wired")
	}

	if !audit.has("identity.discovered") {
		t.Error("identity.discovered audit event not recorded on first call")
	}
	if after := metricFamilyTotal(t, "lenny_ops_me_requests_total"); after <= before {
		t.Errorf("me requests metric did not increment: %v -> %v", before, after)
	}
}

// spec §25.4 lines 1606-1611: through the real auth gate the rateLimits
// block surfaces the caller's token-bucket balance so an agent can
// self-pace.
func TestMeRateLimitsSurface(t *testing.T) {
	signer := jwt.NewHMACSigner("ops-test", []byte("ops-test-secret"))
	srv := opsserver.New(opsserver.Options{
		Me: meConfigFixture(),
		Auth: &opsserver.AuthConfig{
			Options:     authmw.Options{Verifier: signer},
			RateLimiter: opsserver.NewRateLimiter(20, 50),
		},
	})
	tok, err := signer.Sign(jwt.Claims{Subject: "sa-pacer", TenantID: "default", Roles: []auth.Role{auth.RolePlatformAdmin}})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/me", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	rl, ok := body["rateLimits"].(map[string]any)
	if !ok {
		t.Fatalf("rateLimits missing: %v", body["rateLimits"])
	}
	if rl["burst"].(float64) != 50 {
		t.Errorf("rateLimits.burst = %v, want 50", rl["burst"])
	}
	if rl["requestsPerSecond"].(float64) != 20 {
		t.Errorf("rateLimits.requestsPerSecond = %v, want 20", rl["requestsPerSecond"])
	}
	if rl["currentTokensAvailable"].(float64) > 50 {
		t.Errorf("currentTokensAvailable = %v, want <= burst", rl["currentTokensAvailable"])
	}
}

// spec §25.4 line 1647: a tenant-admin gets tenant-scoped values rather
// than the platform wildcards.
func TestMeTenantAdminScoped(t *testing.T) {
	srv, _ := meServer()
	p := tenantAdmin("ta-1", "t-12345")
	_, body := getAuthed(t, srv, "/v1/admin/me", &p)
	authz := body["authorization"].(map[string]any)
	if authz["tenantScope"] != "t-12345" {
		t.Errorf("tenantScope = %v, want t-12345", authz["tenantScope"])
	}
	lockScopes := authz["authorizedLockScopes"].([]any)
	for _, s := range lockScopes {
		if s == "platform:*" {
			t.Error("tenant-admin must not see the platform:* lock scope")
		}
	}
}

// spec §25.4 line 1641: identity.discovered is emitted at most once per
// caller subject.
func TestMeIdentityDiscoveredDeduplicated(t *testing.T) {
	srv, audit := meServer()
	p := platformAdmin("sa-dedup")
	getAuthed(t, srv, "/v1/admin/me", &p)
	getAuthed(t, srv, "/v1/admin/me", &p)
	count := 0
	for _, e := range audit.events {
		if e == "identity.discovered" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("identity.discovered emitted %d times, want 1", count)
	}
}

// spec §25.4 line 1575: /v1/admin/me/authorized-tools returns the MCP
// tool inventory pre-filtered to the caller; a scope-narrowed caller sees
// only tools their scope authorizes.
func TestAuthorizedTools(t *testing.T) {
	srv, _ := meServer()
	p := platformAdmin("sa-admin")
	rec, body := getAuthed(t, srv, "/v1/admin/me/authorized-tools", &p)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	tools, ok := body["tools"].([]any)
	if !ok || len(tools) == 0 {
		t.Fatalf("tools = %v, want a populated inventory", body["tools"])
	}

	// A caller whose scope claim authorizes only one narrow scope sees a
	// strictly smaller inventory.
	narrowSet, err := scopes.Parse("tools:locks:write")
	if err != nil {
		t.Fatalf("parse scope: %v", err)
	}
	narrow := authmw.Principal{
		Subject: "sa-narrow",
		Roles:   []auth.Role{auth.RolePlatformAdmin},
		Scopes:  narrowSet,
	}
	_, body2 := getAuthed(t, srv, "/v1/admin/me/authorized-tools", &narrow)
	narrowed, _ := body2["tools"].([]any)
	if len(narrowed) >= len(tools) {
		t.Errorf("scope-narrowed inventory (%d) must be smaller than the full one (%d)", len(narrowed), len(tools))
	}
}

// spec §25.4 line 1575: /v1/admin/me/operations is the actor=me alias and
// returns only the caller's own operations.
func TestMeOperationsAlias(t *testing.T) {
	audit := &captureAudit{}
	src := fakeSource{
		kinds: []operations.Kind{operations.KindRemediationLock},
		ops: []operations.Operation{
			opOf("lock-mine", operations.KindRemediationLock, operations.StatusHeld, "sa-me", ""),
			opOf("lock-theirs", operations.KindRemediationLock, operations.StatusHeld, "sa-other", ""),
		},
	}
	srv := opsserver.New(opsserver.Options{
		Inventory: operations.New(src),
		Audit:     audit,
		Me:        &opsserver.MeConfig{Version: "1.5.0"},
	})
	p := platformAdmin("sa-me")
	rec, body := getAuthed(t, srv, "/v1/admin/me/operations", &p)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	ops, _ := body["operations"].([]any)
	if len(ops) != 1 {
		t.Fatalf("operations = %d, want 1 (own only)", len(ops))
	}
	if ops[0].(map[string]any)["operationId"] != "lock-mine" {
		t.Errorf("operationId = %v, want lock-mine", ops[0].(map[string]any)["operationId"])
	}
}

// spec §25.4 line 1668: when no inventory is wired the /me/operations
// alias returns an empty page rather than a 404.
func TestMeOperationsAliasEmptyWithoutInventory(t *testing.T) {
	srv, _ := meServer()
	p := platformAdmin("sa-me")
	rec, body := getAuthed(t, srv, "/v1/admin/me/operations", &p)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	ops, _ := body["operations"].([]any)
	if len(ops) != 0 {
		t.Errorf("operations = %d, want empty page", len(ops))
	}
}
