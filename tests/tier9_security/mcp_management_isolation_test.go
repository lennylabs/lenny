// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security test for cross-tenant isolation through the §25.12 MCP
// Management Server, one row of the §12.9.1 cross-store tenant-isolation
// matrix.
//
// §12.9.1 (TESTING.md) names the MCP management server among the code
// paths that must fail every cross-tenant read and write with the
// documented isolation error: "for each store and each operation,
// attempt cross-tenant reads and writes through every code path (REST,
// MCP, OpenAI Completions, OpenAI Responses, admin API, MCP management
// server, audit query, drift detection, lenny-ops endpoints). Every
// attempt must fail with the documented isolation error."
//
// §25 states the resource-tenancy layer explicitly: "Scopes do not
// replace tenancy — a tenant-admin caller is still constrained to its
// tenant regardless of scope. Scopes restrict actions; tenancy restricts
// resources. Both are enforced independently." §25.12's REST-layer RBAC
// (layer 3) realizes this on the MCP path: "Every MCP tool invocation
// that passes the scope check is translated into a REST call against
// lenny-ops or the gateway admin API. That REST call passes through the
// standard OIDC/JWT middleware and role-based authorization check." The
// tenant boundary therefore holds when a tool call is driven through
// /mcp/management exactly as it holds on a direct REST call.
//
// The concrete tenant-scoped write exercised here is §25.5 webhook event
// subscription creation: "A tenant-admin may only create subscriptions
// with tenantFilter: {their-tenant}. Attempts to use a different tenant
// or wildcard return 403 SUBSCRIPTION_TENANT_FORBIDDEN." A tenant-admin
// authenticated for tenant acme invokes the create tool through
// /mcp/management with tenantFilter set to a foreign tenant (globex); the
// underlying REST handler, reached through the in-process MCP replay,
// must reject it with that isolation error rather than write a
// subscription for globex.
//
// This runs the production lenny-ops management server in process (no
// Kind), matching the existing §25.12 authorization-sweep test in this
// package: the isolation boundary being verified is a code path (the MCP
// replay forwards the caller's verified tenant to the REST handler),
// which the real opsserver plus the real event-subscription handler
// exercise faithfully without a live Postgres.
//
// spec: §12.9.1 (cross-tenant isolation through the MCP management
// server), §25.12 (REST-layer RBAC — the MCP replay passes through the
// OIDC/role authorization check), §25.5 (tenant-admin cross-tenant
// subscription create returns 403 SUBSCRIPTION_TENANT_FORBIDDEN).

package tier9_security_test

import (
	"context"
	"net/http"
	"net/netip"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/ops/eventsubscription"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
)

// isoStubResolver resolves every host to a fixed public address so the
// §25.5 webhook SSRF validator's DNS step runs deterministically in
// process without real name resolution. The isolation check under test
// runs after URL validation, so the callback URL must first pass the
// SSRF gate for the tenant boundary to be the operative rejection.
type isoStubResolver struct{}

func (isoStubResolver) LookupNetIP(_ context.Context, _, _ string) ([]netip.Addr, error) {
	return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
}

// isoTenantRegistry confirms every tenant the auth extractor asks about.
// Multi-tenant auth (needed so the caller's tenant claim is honored rather
// than collapsed to the single-tenant default) requires a registry; the
// only claim exercised is the caller's own tenant, so a permissive
// registry suffices.
type isoTenantRegistry struct{}

func (isoTenantRegistry) IsRegistered(string) (bool, error) { return true, nil }

// newIsolationMgmtServer builds a production lenny-ops management server
// with the §25.4 OIDC auth + role gate wired against an HMAC verifier and
// the §25.5 event-subscription service backed by an in-memory store. The
// returned signer mints the tenant-scoped bearers the test authenticates
// with. The rate limiter is given a high budget so it never trips.
func newIsolationMgmtServer(t *testing.T) (*opsserver.Server, *jwt.HMACSigner) {
	t.Helper()
	signer := jwt.NewHMACSigner("ops-iso-test", []byte("ops-iso-secret"))
	svc := eventsubscription.NewService(eventsubscription.NewMemoryStore())
	svc.SSRF = eventsubscription.NewSSRFValidator(eventsubscription.SSRFConfig{Resolver: isoStubResolver{}})
	srv := opsserver.New(opsserver.Options{
		EventSubscriptions: svc,
		Auth: &opsserver.AuthConfig{
			Options: authmw.Options{
				Verifier: signer,
				// Honor the bearer's tenant claim so a tenant-admin for acme
				// is scoped to acme rather than the single-tenant default;
				// the tenancy boundary is the control under test.
				MultiTenant: true,
				Registry:    isoTenantRegistry{},
			},
			RateLimiter: opsserver.NewRateLimiter(1000, 1000),
		},
	})
	return srv, signer
}

// tenantAdminBearer signs a bearer for a tenant-admin scoped to the named
// tenant, carrying no scope claim so the §25.1 scope gate defers to the
// role ceiling and the tenancy layer is the operative control.
func tenantAdminBearer(t *testing.T, signer *jwt.HMACSigner, sub, tenant string) string {
	t.Helper()
	tok, err := signer.Sign(jwt.Claims{Subject: sub, TenantID: tenant, Roles: []auth.Role{auth.RoleTenantAdmin}})
	if err != nil {
		t.Fatalf("sign tenant-admin token: %v", err)
	}
	return tok
}

// createSubscriptionBody builds a §25.12 tools/call for the event-
// subscription create tool with a webhook callback and the given
// tenantFilter.
func createSubscriptionBody(tenantFilter string) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name": "lenny_event_subscription_create",
			"arguments": map[string]any{
				"callbackUrl":  "https://acme.example/webhook",
				"types":        []string{"dev.lenny.alert_fired"},
				"tenantFilter": tenantFilter,
			},
		},
	}
}

// toolResultIsError reports the isError flag and the concatenated content
// text of a §25.12 tools/call result envelope. The REST body of the
// underlying call (including any error envelope) rides in the content
// text, so a rejection surfaces its canonical error code there.
func toolResultIsError(t *testing.T, resp map[string]any) (bool, string) {
	t.Helper()
	if rpcErr, ok := resp["error"].(map[string]any); ok && rpcErr != nil {
		t.Fatalf("tools/call returned a JSON-RPC protocol error, not a tool result: %v", rpcErr)
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("tools/call response carries no result object: %v", resp)
	}
	isErr, _ := result["isError"].(bool)
	var text strings.Builder
	if content, ok := result["content"].([]any); ok {
		for _, item := range content {
			if m, ok := item.(map[string]any); ok {
				if s, ok := m["text"].(string); ok {
					text.WriteString(s)
				}
			}
		}
	}
	return isErr, text.String()
}

// spec: 12.9.1, 25.12, 25.5
// diagnosis: cross-tenant isolation through the §25.12 MCP management
// server did not hold. A tenant-admin authenticated for tenant acme
// invokes the §25.5 event-subscription create tool through
// /mcp/management with tenantFilter set to a foreign tenant (globex). The
// §25.12 REST-layer RBAC forwards the caller's verified tenant to the
// replayed REST handler, which per §25.5 must reject the cross-tenant
// write with 403 SUBSCRIPTION_TENANT_FORBIDDEN. A tool result with
// isError:false — or an error code other than SUBSCRIPTION_TENANT_
// FORBIDDEN — means the MCP management path let a tenant-admin write a
// resource scoped to another tenant, the §12.9.1 "MCP management server"
// isolation row the matrix requires. The own-tenant positive control
// proves the tool path itself works for the caller, so the cross-tenant
// rejection is the tenancy layer and not an unrelated failure.
func TestMCPManagementCrossTenantWriteRejected_spec_12_9_1(t *testing.T) {
	srv, signer := newIsolationMgmtServer(t)
	bearer := tenantAdminBearer(t, signer, "carol@acme.com", "acme")
	hdr := map[string]string{"Authorization": "Bearer " + bearer}

	// Positive control: the tenant-admin creates a subscription for its
	// OWN tenant through /mcp/management. This must succeed so a later
	// cross-tenant rejection is attributable to the tenancy layer rather
	// than to the tool being unreachable for this caller.
	code, resp := postManagementRPC(t, srv, hdr, createSubscriptionBody("acme"))
	if code != http.StatusOK {
		t.Fatalf("own-tenant tools/call HTTP status = %d, want 200 (JSON-RPC envelope); resp=%v", code, resp)
	}
	if isErr, text := toolResultIsError(t, resp); isErr {
		t.Fatalf("own-tenant subscription create was rejected; the tool path is not reachable for the tenant-admin caller: %s", text)
	}

	// Cross-tenant write: the same tenant-admin (acme) invokes the create
	// tool targeting a foreign tenant (globex). §25.5 requires a 403
	// SUBSCRIPTION_TENANT_FORBIDDEN, surfaced as an isError tool result.
	code, resp = postManagementRPC(t, srv, hdr, createSubscriptionBody("globex"))
	if code != http.StatusOK {
		t.Fatalf("cross-tenant tools/call HTTP status = %d, want 200 (the isolation error rides in the JSON-RPC result, not the HTTP status); resp=%v", code, resp)
	}
	isErr, text := toolResultIsError(t, resp)
	if !isErr {
		t.Fatalf("cross-tenant subscription create was NOT rejected: a tenant-admin for acme wrote a subscription scoped to globex through /mcp/management; result text=%s", text)
	}
	if !strings.Contains(text, eventsubscription.ErrCodeTenantForbidden) {
		t.Errorf("cross-tenant rejection error code = %q, want %s (the documented §25.5 isolation error)", text, eventsubscription.ErrCodeTenantForbidden)
	}
}
