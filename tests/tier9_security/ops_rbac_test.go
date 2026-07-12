// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security tests for the §25.4 lenny-ops authentication, role
// ceiling, RFC 9068 scope-claim narrowing, and remediation-lock
// scope-based authorization.
//
// The §25.4 "Authentication" section requires platform-admin or
// tenant-admin on every operability endpoint with no anonymous access
// except /healthz, layers the optional RFC 9068 scope claim below the
// role ceiling, and returns 403 LOCK_SCOPE_FORBIDDEN when a tenant-admin
// touches a platform-scoped remediation lock. The existing lenny-ops
// coverage exercises these controls one of two ways: the auth unit test
// drives real bearer JWTs but only against a single health endpoint, and
// the lock scope test drives the LOCK_SCOPE_FORBIDDEN control through the
// X-Lenny-Role dev header rather than a verified JWT. Neither drives the
// lock, escalation, and operations endpoint families through the genuine
// OIDC middleware with real minted tokens.
//
// These tests boot the real *opsserver.Server with a production
// AuthConfig (an HMAC JWT verifier standing in for the OIDC verifier —
// the e2e Kind cluster runs the gateway/ops surface in dev mode with no
// OIDC provider, so the JWT-verified authorization path is unreachable
// end-to-end there) and mint real bearer tokens carrying roles, tenant,
// and RFC 9068 scope claims. Every request runs through the same
// authentication, role-gate, scope-enforce, and lock-scope-authorization
// code a Bearer-JWT caller exercises against the deployed service. The
// network reachability layer (§13.2 NetworkPolicy, Ingress routing) is a
// separate concern that does not affect which of 401 / 403 / admitted an
// authenticated request receives.
//
// spec: §25.4 (lenny-ops authentication, role ceiling, scope narrowing,
// lock-scope authorization).

package tier9_security_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/ops/coordination"
	"github.com/lennylabs/lenny/pkg/ops/escalation"
	"github.com/lennylabs/lenny/pkg/ops/operations"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
)

// opsRBACSource is an empty operations.Source so the §25.4 Operations
// Inventory endpoints (GET /v1/admin/operations) register and can be
// driven through the auth gate. It returns no kinds and no operations;
// the authorization decision is made before the handler queries it.
type opsRBACSource struct{}

func (opsRBACSource) Kinds() []operations.Kind { return nil }
func (opsRBACSource) List(context.Context, operations.Filter) ([]operations.Operation, error) {
	return nil, nil
}

// authedOpsServer returns a real *opsserver.Server wired with the §25.4
// OIDC auth + role gate against an HMAC verifier, plus the remediation
// lock, escalation, and operations surfaces so all three endpoint
// families are reachable once a request passes authorization. The rate
// limiter budget is high so it never trips in these authorization tests.
func authedOpsServer() (*opsserver.Server, *jwt.HMACSigner) {
	signer := jwt.NewHMACSigner("ops-rbac-test", []byte("ops-rbac-test-secret"))
	srv := opsserver.New(opsserver.Options{
		Locks:       coordination.NewMemStore(),
		Escalations: escalation.NewService(nil),
		Inventory:   operations.New(opsRBACSource{}),
		Auth: &opsserver.AuthConfig{
			Options:     authmw.Options{Verifier: signer},
			RateLimiter: opsserver.NewRateLimiter(1000, 1000),
		},
	})
	return srv, signer
}

// mintOpsRBACToken signs a bearer carrying sub, tenant, the RFC 9068
// scope claim, and the given roles. An empty scope leaves the claim
// absent (the no-narrowing case that defers to the role ceiling).
func mintOpsRBACToken(t *testing.T, signer *jwt.HMACSigner, sub, tenant, scope string, roles ...auth.Role) string {
	t.Helper()
	tok, err := signer.Sign(jwt.Claims{Subject: sub, TenantID: tenant, Scope: scope, Roles: roles})
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return tok
}

// opsErrorCode extracts {error:{code}} from a §25.4 error envelope.
func opsErrorCode(t *testing.T, body string) string {
	t.Helper()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("decode error body %q: %v", body, err)
	}
	return env.Error.Code
}

// opsReadEndpoints are the read endpoints across the lock, escalation,
// and operations families. Authentication and the role ceiling are
// enforced identically on each before any handler runs.
var opsReadEndpoints = []struct {
	name   string
	method string
	path   string
}{
	{"remediation-locks list", http.MethodGet, "/v1/admin/remediation-locks"},
	{"escalations list", http.MethodGet, "/v1/admin/escalations"},
	{"operations list", http.MethodGet, "/v1/admin/operations"},
}

// spec: §25.4 line 1567 — "Requires platform-admin or tenant-admin role
// on all endpoints. No anonymous access except /healthz." A request with
// no bearer is rejected 401 on every operability endpoint.
//
// diagnosis: the §25.4 lenny-ops auth middleware admitted an
// unauthenticated caller on a non-/healthz endpoint. Either the OIDC
// gate is not wired onto the lock, escalation, or operations family, or
// it fails open when the Authorization header is absent, exposing every
// operability surface to anonymous callers.
func TestOpsAnonymousRejectedOnEveryEndpoint_spec_25_4_1567(t *testing.T) {
	srv, _ := authedOpsServer()
	for _, ep := range opsReadEndpoints {
		t.Run(ep.name, func(t *testing.T) {
			req := httptest.NewRequest(ep.method, ep.path, nil)
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("anonymous %s %s: status = %d, want 401; body=%s",
					ep.method, ep.path, rec.Code, rec.Body.String())
			}
		})
	}
	// The acquire (write) endpoint is likewise closed to anonymous callers.
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/remediation-locks",
		bytes.NewReader([]byte(`{"scope":"pool:p","operation":"scale","ttlSeconds":300}`)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous POST remediation-locks: status = %d, want 401; body=%s",
			rec.Code, rec.Body.String())
	}
}

// spec: §25.4 line 1567 — /healthz is the sole exemption from the auth
// requirement so the kubelet can probe an unauthenticated liveness path.
//
// diagnosis: the §25.4 /healthz exemption regressed — the auth gate now
// blocks the one endpoint the spec exempts, which breaks the kubelet
// liveness probe and would flap lenny-ops out of readiness.
func TestOpsHealthzAnonymousAdmitted_spec_25_4_1567(t *testing.T) {
	srv, _ := authedOpsServer()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
		t.Fatalf("anonymous /healthz gated: status = %d; body=%s", rec.Code, rec.Body.String())
	}
}

// spec: §25.4 line 1567 — the role ceiling admits only platform-admin
// and tenant-admin. A verified bearer whose only role is below the
// ceiling is rejected 403 on every operability endpoint even though
// authentication succeeded.
//
// diagnosis: the §25.4 role ceiling did not hold — a verified bearer
// whose only role is below platform-admin/tenant-admin reached an
// operability endpoint. The role gate either omits an endpoint family or
// admits a role the ceiling excludes.
func TestOpsRoleCeilingRejectsNonAdmin_spec_25_4_1567(t *testing.T) {
	srv, signer := authedOpsServer()
	tok := mintOpsRBACToken(t, signer, "bob@acme.com", "acme", "", auth.RoleUser)
	for _, ep := range opsReadEndpoints {
		t.Run(ep.name, func(t *testing.T) {
			req := httptest.NewRequest(ep.method, ep.path, nil)
			req.Header.Set("Authorization", "Bearer "+tok)
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("non-admin %s %s: status = %d, want 403; body=%s",
					ep.method, ep.path, rec.Code, rec.Body.String())
			}
		})
	}
}

// spec: §25.4 line 1567 — platform-admin passes the role gate (neither
// 401 nor 403) on every operability endpoint.
//
// diagnosis: the §25.4 role ceiling is over-restrictive — a
// platform-admin, which the spec admits on all endpoints, was denied.
// The role gate rejects an identity the ceiling grants, locking
// operators out of the operability surface.
func TestOpsRoleCeilingAdmitsPlatformAdmin_spec_25_4_1567(t *testing.T) {
	srv, signer := authedOpsServer()
	tok := mintOpsRBACToken(t, signer, "alice@acme.com", "platform", "", auth.RolePlatformAdmin)
	for _, ep := range opsReadEndpoints {
		t.Run(ep.name, func(t *testing.T) {
			req := httptest.NewRequest(ep.method, ep.path, nil)
			req.Header.Set("Authorization", "Bearer "+tok)
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
				t.Fatalf("platform-admin %s %s blocked: status = %d; body=%s",
					ep.method, ep.path, rec.Code, rec.Body.String())
			}
		})
	}
}

// spec: §25.4 line 2128 — "tenant-admin attempts on platform-scoped
// locks return 403 LOCK_SCOPE_FORBIDDEN." A verified tenant-admin JWT
// acquiring a platform-scoped lock is denied by the scope-based lock
// authorization; the same JWT acquiring a lock on a pool in its own
// tenant passes the control.
//
// diagnosis: the §25.4 lock-scope authorization did not return 403
// LOCK_SCOPE_FORBIDDEN for a tenant-admin acquiring a platform-scoped
// lock. A tenant admin can block a platform upgrade or restore, or the
// control over-blocks a lock in the caller's own tenant.
func TestOpsTenantAdminPlatformLockForbidden_spec_25_4_2128(t *testing.T) {
	srv, signer := authedOpsServer()
	tenantAdmin := mintOpsRBACToken(t, signer, "carol@acme.com", "acme", "", auth.RoleTenantAdmin)

	// Platform-scoped lock: denied 403 LOCK_SCOPE_FORBIDDEN.
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/remediation-locks",
		bytes.NewReader([]byte(`{"scope":"restore:platform","operation":"restore","ttlSeconds":300}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tenantAdmin)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("tenant-admin platform lock: status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	if code := opsErrorCode(t, rec.Body.String()); code != "LOCK_SCOPE_FORBIDDEN" {
		t.Fatalf("tenant-admin platform lock: error code = %q, want LOCK_SCOPE_FORBIDDEN; body=%s",
			code, rec.Body.String())
	}

	// A pool in the caller's own tenant is authorized (the scope control
	// admits, the acquire creates the lock). This pins that the denial is
	// scope-specific, not a blanket tenant-admin lock rejection.
	req = httptest.NewRequest(http.MethodPost, "/v1/admin/remediation-locks",
		bytes.NewReader([]byte(`{"scope":"pool:acme-default","operation":"scale","ttlSeconds":300}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tenantAdmin)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code == http.StatusForbidden {
		t.Fatalf("tenant-admin own-tenant pool lock unexpectedly forbidden: body=%s", rec.Body.String())
	}
}

// spec: §25.4 lines 62, 1569 — the RFC 9068 scope claim narrows the
// caller's effective surface below the role ceiling. A platform-admin
// token whose scope claim grants only tools:locks:read is rejected 403
// SCOPE_FORBIDDEN when it attempts a write (lock acquire requires
// tools:locks:write), while the same role with no scope claim, or with
// the matching write scope, passes the gate.
//
// diagnosis: the §25.4 RFC 9068 scope-claim narrowing did not hold — a
// platform-admin token scoped to tools:locks:read reached a write, so
// the scope claim fails to narrow the effective surface below the role
// ceiling, or it over-narrows and denies a read its scope grants.
func TestOpsScopeClaimNarrowsBelowRoleCeiling_spec_25_4_1569(t *testing.T) {
	srv, signer := authedOpsServer()

	// Narrowed to read-only: the write (acquire) is denied SCOPE_FORBIDDEN
	// even though the platform-admin role would otherwise permit it.
	narrowed := mintOpsRBACToken(t, signer, "alice@acme.com", "platform", "tools:locks:read", auth.RolePlatformAdmin)
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/remediation-locks",
		bytes.NewReader([]byte(`{"scope":"pool:p","operation":"scale","ttlSeconds":300}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+narrowed)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("scope-narrowed acquire: status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	if code := opsErrorCode(t, rec.Body.String()); code != "SCOPE_FORBIDDEN" {
		t.Fatalf("scope-narrowed acquire: error code = %q, want SCOPE_FORBIDDEN; body=%s",
			code, rec.Body.String())
	}

	// The same narrowed token reaches the read endpoint its scope grants:
	// the scope gate admits (a read within tools:locks:read).
	req = httptest.NewRequest(http.MethodGet, "/v1/admin/remediation-locks", nil)
	req.Header.Set("Authorization", "Bearer "+narrowed)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code == http.StatusForbidden {
		t.Fatalf("scope-narrowed read denied by scope gate: body=%s", rec.Body.String())
	}

	// A matching write scope passes the gate and reaches the acquire
	// handler (non-403).
	matching := mintOpsRBACToken(t, signer, "alice@acme.com", "platform", "tools:locks:write", auth.RolePlatformAdmin)
	req = httptest.NewRequest(http.MethodPost, "/v1/admin/remediation-locks",
		bytes.NewReader([]byte(`{"scope":"pool:p2","operation":"scale","ttlSeconds":300}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+matching)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code == http.StatusForbidden {
		t.Fatalf("matching-scope acquire rejected by scope gate: body=%s", rec.Body.String())
	}
}
