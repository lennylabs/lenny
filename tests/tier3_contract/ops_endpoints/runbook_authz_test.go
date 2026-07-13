// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract test pinning the §25.4 access-control contract of the
// §25.7 lenny-ops runbook-discovery endpoints (Path A index and the
// structured-steps and full-markdown hops). The runbook routes register
// on the same lenny-ops mux as every other operability endpoint, so the
// §25.4 OIDC authentication + platform-admin/tenant-admin role gate and
// the §25.1 per-route scope gate wrap them. Nothing else in the suite
// drives the runbook routes through the authenticated surface, so a
// routing change that registered a runbook handler outside the
// authenticated mux would expose operational topology (component names,
// alert triggers, remediation commands) without failing a test. This
// test drives the composed surface through httptest with a real bearer.
package ops_endpoints_test

import (
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
)

// runbookAuthServer builds a §25 lenny-ops Server with the §25.7 runbook
// index wired behind the real §25.4 OIDC authentication + role gate (an
// HMAC bearer verifier), so a caller's JWT reaches the runbook routes
// only after passing the production authentication path. The rate
// limiter is given a high budget so it never trips in these auth tests.
func runbookAuthServer(t *testing.T) (*opsserver.Server, *jwt.HMACSigner) {
	t.Helper()
	signer := jwt.NewHMACSigner("ops-runbook-authz-test", []byte("ops-runbook-authz-secret"))
	srv := opsserver.New(opsserver.Options{
		Runbooks: stubRunbookSource{},
		Auth: &opsserver.AuthConfig{
			Options:     authmw.Options{Verifier: signer},
			RateLimiter: opsserver.NewRateLimiter(1000, 1000),
		},
	})
	return srv, signer
}

// signRunbookToken signs a bearer carrying sub sa-prod-watchdog-01, the
// given role, and the given RFC 9068 space-separated scope claim. An
// empty scope yields an absent scope claim (the no-narrowing case that
// defers to the role ceiling).
func signRunbookToken(t *testing.T, signer *jwt.HMACSigner, role auth.Role, scope string) string {
	t.Helper()
	tok, err := signer.Sign(jwt.Claims{
		Subject:  "sa-prod-watchdog-01",
		TenantID: "default",
		Roles:    []auth.Role{role},
		Scope:    scope,
	})
	if err != nil {
		t.Fatalf("sign runbook token (role=%s scope=%q): %v", role, scope, err)
	}
	return tok
}

// runbookAuthPaths are the three §25.7 runbook-discovery routes: the Path
// A index, the structured-steps hop, and the full-markdown hop. The
// {name} routes address the contract suite's seeded warm-pool runbook.
var runbookAuthPaths = []string{
	"/v1/admin/runbooks",
	"/v1/admin/runbooks/warm-pool-exhaustion/steps",
	"/v1/admin/runbooks/warm-pool-exhaustion",
}

// bearerHeader wraps a token in the Authorization header map the request
// helper applies. A nil map (no header) drives the unauthenticated case.
func bearerHeader(token string) map[string]string {
	if token == "" {
		return nil
	}
	return map[string]string{"Authorization": "Bearer " + token}
}

// spec: §25.4 (line 1567: "Requires platform-admin or tenant-admin role
// on all endpoints. No anonymous access except /healthz."), §25.1 (line
// 89: a request for a tool not permitted by any scope returns 403
// SCOPE_FORBIDDEN; lines 92-94: the admin-API middleware checks scopes
// before routing to the handler), §25.7 (the runbook-discovery
// endpoints).
//
// diagnosis: the §25.7 runbook-discovery routes are not gated by the
// §25.4 authentication + role gate and the §25.1 scope gate. An
// anonymous caller, a caller lacking the platform-admin/tenant-admin
// role, or a scope-narrowed caller without tools:runbooks:read reaches
// the runbook index (operational topology: component names, alert
// triggers, remediation commands) instead of receiving 401/403 with the
// canonical error envelope. A failure means a routing change moved the
// runbook handlers outside the authenticated mux, or the scope gate does
// not resolve the runbook routes' tools:runbooks:read scope.
func TestRunbookEndpointsRequireAuthenticatedScopedPrincipal(t *testing.T) {
	srv, signer := runbookAuthServer(t)

	for _, path := range runbookAuthPaths {
		// 1) No bearer: the operability surface admits no anonymous request.
		rec, body := request(t, srv, http.MethodGet, path, bearerHeader(""), nil)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s unauthenticated: status %d, want 401; body=%s", path, rec.Code, rec.Body.String())
		} else if code := errorEnvelope(t, body)["code"]; code != "UNAUTHORIZED" {
			t.Errorf("GET %s unauthenticated: error code %v, want UNAUTHORIZED", path, code)
		}

		// 2) Verified bearer lacking platform-admin/tenant-admin: the §25.4
		//    role gate rejects with 403 FORBIDDEN before the handler runs.
		nonAdmin := signRunbookToken(t, signer, auth.RoleUser, "")
		rec, body = request(t, srv, http.MethodGet, path, bearerHeader(nonAdmin), nil)
		if rec.Code != http.StatusForbidden {
			t.Errorf("GET %s as non-admin: status %d, want 403; body=%s", path, rec.Code, rec.Body.String())
		} else if code := errorEnvelope(t, body)["code"]; code != "FORBIDDEN" {
			t.Errorf("GET %s as non-admin: error code %v, want FORBIDDEN", path, code)
		}

		// 3) platform-admin whose scope claim is narrowed away from
		//    tools:runbooks:read: the §25.1 scope gate rejects with 403
		//    SCOPE_FORBIDDEN naming the required scope, before the handler
		//    runs. The role would otherwise permit the read; the scope
		//    narrows below the role ceiling.
		narrowed := signRunbookToken(t, signer, auth.RolePlatformAdmin, "tools:health:read")
		rec, body = request(t, srv, http.MethodGet, path, bearerHeader(narrowed), nil)
		if rec.Code != http.StatusForbidden {
			t.Errorf("GET %s scope-narrowed: status %d, want 403; body=%s", path, rec.Code, rec.Body.String())
		} else {
			env := errorEnvelope(t, body)
			if code := env["code"]; code != "SCOPE_FORBIDDEN" {
				t.Errorf("GET %s scope-narrowed: error code %v, want SCOPE_FORBIDDEN", path, code)
			}
			if details, _ := env["details"].(map[string]any); details["requiredScope"] != "tools:runbooks:read" {
				t.Errorf("GET %s scope-narrowed: details.requiredScope = %v, want tools:runbooks:read", path, details["requiredScope"])
			}
		}

		// 4) platform-admin carrying tools:runbooks:read: both gates admit
		//    the request and the handler serves the runbook (200, not
		//    401/403).
		scoped := signRunbookToken(t, signer, auth.RolePlatformAdmin, "tools:runbooks:read")
		rec, _ = request(t, srv, http.MethodGet, path, bearerHeader(scoped), nil)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s with tools:runbooks:read: status %d, want 200; body=%s", path, rec.Code, rec.Body.String())
		}
	}
}
