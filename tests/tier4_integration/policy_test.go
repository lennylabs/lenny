// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §11 / §4.8 policy interceptor chain end
// to end through the real cmd/lenny-gateway binary. It exercises the
// built-in §4.8 QuotaEvaluator (priority 200, PostAuth) wired onto the
// session-creation admission path: a tenant whose §11.2 token budget is
// exhausted is rejected with QUOTA_EXCEEDED, and the rejection writes
// the §16.7 `interceptor.rejected` row to the per-tenant audit chain.
//
// This file converts the TestPolicyGate and TestPolicyAudit scaffolds
// (formerly skipped in scaffolds_test.go) into real integration tests.
// The gateway runs against a Redis container — the §11.2 token-usage
// counter is Redis-backed, and the QuotaEvaluator is registered only
// when --redis-url is set. The tenant registry and the audit hash
// chain are the gateway's in-memory backends (no --postgres-dsn), so
// the per-tenant tokenQuotaPerWindow set through the admin API and the
// `interceptor.rejected` audit row are both reachable in one process.
package tier4_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// policyClient drives the gateway subprocess with the dev-mode auth
// headers a tier-4 test needs.
type policyClient struct {
	t    *testing.T
	base string
}

func (c policyClient) do(method, path, tenant, user, roles string, body any) (int, map[string]any) {
	c.t.Helper()
	var reader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, reader)
	if err != nil {
		c.t.Fatalf("new request %s %s: %v", method, path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if tenant != "" {
		req.Header.Set("X-Lenny-Tenant-ID", tenant)
	}
	if user != "" {
		req.Header.Set("X-Lenny-User-ID", user)
	}
	if roles != "" {
		req.Header.Set("X-Lenny-Roles", roles)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return resp.StatusCode, out
}

// quotaWindowKey returns the §12.4 Redis key for the (tenant, user)
// hourly token-usage window containing now. It mirrors the key
// construction in pkg/gateway/quotastore so a test can seed the
// counter the QuotaEvaluator reads.
func quotaWindowKey(tenant, user string, now time.Time) string {
	label := "hourly-" + now.UTC().Format("2006010215")
	return "t:" + tenant + ":quota:tokens:" + user + ":" + label
}

// spec: 11 / 4.8 (policy interceptor chain through the gateway subprocess)
// diagnosis: the §4.8 QuotaEvaluator was not wired onto the
// session-creation admission path. cmd/lenny-gateway constructed the
// gateway with an empty interceptor.NewChain(), so the §11 policy gate
// never ran — a tenant over its §11.2 token budget could still create
// a session, and a tenant under budget saw no difference.
func TestPolicyGate(t *testing.T) {
	gateway.SkipUnlessAvailable(t)

	rd := containers.StartRedis(t, containers.RedisOptions{})
	gw := gateway.StartWith(t, "--dev-mode", "--redis-url=redis://"+rd.Addr+"/0")
	c := policyClient{t: t, base: gw.BaseURL()}
	ctx := context.Background()

	// A tenant with a small §11.2 per-window token budget. The
	// QuotaEvaluator enforces it hierarchically against the Redis
	// token-usage counter.
	const tenant = "acme"
	const user = "alice@acme.com"
	code, body := c.do(http.MethodPost, "/v1/admin/tenants", "platform", "ops@acme.com", "platform-admin",
		map[string]any{"id": tenant, "displayName": "Acme Corp", "tokenQuotaPerWindow": 1000})
	if code != http.StatusCreated {
		t.Fatalf("create tenant: status %d (%v)", code, body)
	}
	if got, _ := body["tokenQuotaPerWindow"].(float64); got != 1000 {
		t.Fatalf("tenant tokenQuotaPerWindow round-trip = %v, want 1000", body["tokenQuotaPerWindow"])
	}

	// ---- under budget: the policy chain admits the session create ----
	if err := rd.Client.Set(ctx, quotaWindowKey(tenant, user, time.Now()), 100, time.Hour).Err(); err != nil {
		t.Fatalf("seed under-budget counter: %v", err)
	}
	code, created := c.do(http.MethodPost, "/v1/sessions", tenant, user, "",
		map[string]any{"runtimeRef": "claude-code", "userId": user})
	if code != http.StatusCreated {
		t.Fatalf("under-budget create: status %d (%v); the QuotaEvaluator must admit a tenant within its token budget", code, created)
	}
	if created["state"] != "created" {
		t.Errorf("under-budget create state = %v, want created", created["state"])
	}

	// ---- over budget: the policy chain rejects with QUOTA_EXCEEDED ----
	// Drive the recorded window usage to the configured limit so
	// quota.HierarchicalCheck reports the tenant scope hard-exceeded.
	if err := rd.Client.Set(ctx, quotaWindowKey(tenant, user, time.Now()), 1000, time.Hour).Err(); err != nil {
		t.Fatalf("seed over-budget counter: %v", err)
	}
	code, rejected := c.do(http.MethodPost, "/v1/sessions", tenant, user, "",
		map[string]any{"runtimeRef": "claude-code", "userId": user})
	if code != http.StatusTooManyRequests {
		t.Fatalf("over-budget create: status %d (%v); a tenant at its §11.2 token limit must be rejected 429", code, rejected)
	}
	errBody, _ := rejected["error"].(map[string]any)
	if errBody == nil {
		t.Fatalf("over-budget rejection has no error envelope: %v", rejected)
	}
	if errBody["code"] != "QUOTA_EXCEEDED" {
		t.Errorf("over-budget rejection code = %v, want QUOTA_EXCEEDED", errBody["code"])
	}

	// ---- a tenant with no token budget is unaffected by the gate ----
	const freeTenant = "globex"
	code, _ = c.do(http.MethodPost, "/v1/admin/tenants", "platform", "ops@acme.com", "platform-admin",
		map[string]any{"id": freeTenant, "displayName": "Globex"})
	if code != http.StatusCreated {
		t.Fatalf("create no-budget tenant: status %d", code)
	}
	// Even with recorded usage, no configured limit means no rejection.
	if err := rd.Client.Set(ctx, quotaWindowKey(freeTenant, user, time.Now()), 9_000_000, time.Hour).Err(); err != nil {
		t.Fatalf("seed no-budget counter: %v", err)
	}
	code, _ = c.do(http.MethodPost, "/v1/sessions", freeTenant, user, "",
		map[string]any{"runtimeRef": "claude-code", "userId": user})
	if code != http.StatusCreated {
		t.Errorf("no-budget tenant create: status %d, want 201; a tenant with tokenQuotaPerWindow=0 has no token cap", code)
	}
}

// spec: 11.7 / 11 (policy-decision audit through the gateway subprocess)
// diagnosis: a §4.8 interceptor-chain REJECT did not emit the §16.7
// `interceptor.rejected` audit row to the per-tenant hash chain. The
// policy gate must record every rejection synchronously (§11.7
// gateway-originated write) so the §25.9 audit-query surface and any
// SIEM consumer see the policy decision.
func TestPolicyAudit(t *testing.T) {
	gateway.SkipUnlessAvailable(t)

	rd := containers.StartRedis(t, containers.RedisOptions{})
	gw := gateway.StartWith(t, "--dev-mode", "--redis-url=redis://"+rd.Addr+"/0")
	c := policyClient{t: t, base: gw.BaseURL()}
	ctx := context.Background()

	const tenant = "acme"
	const user = "alice@acme.com"
	code, _ := c.do(http.MethodPost, "/v1/admin/tenants", "platform", "ops@acme.com", "platform-admin",
		map[string]any{"id": tenant, "displayName": "Acme Corp", "tokenQuotaPerWindow": 500})
	if code != http.StatusCreated {
		t.Fatalf("create tenant: status %d", code)
	}

	// Exhaust the §11.2 token budget so the next create is rejected.
	if err := rd.Client.Set(ctx, quotaWindowKey(tenant, user, time.Now()), 500, time.Hour).Err(); err != nil {
		t.Fatalf("seed exhausted counter: %v", err)
	}
	code, rejected := c.do(http.MethodPost, "/v1/sessions", tenant, user, "",
		map[string]any{"runtimeRef": "claude-code", "userId": user})
	if code != http.StatusTooManyRequests {
		t.Fatalf("create over budget: status %d (%v), want 429", code, rejected)
	}

	// The rejection must have written an `interceptor.rejected` audit
	// row to the tenant's §11.7 hash chain. A platform-admin reads it
	// back through the §25.9 audit-query surface.
	code, audit := c.do(http.MethodGet, "/v1/admin/audit-events?tenantId="+tenant,
		"platform", "ops@acme.com", "platform-admin", nil)
	if code != http.StatusOK {
		t.Fatalf("query audit events: status %d (%v)", code, audit)
	}
	events, _ := audit["auditEvents"].([]any)
	var rejectionRow map[string]any
	for _, e := range events {
		row, _ := e.(map[string]any)
		if row != nil && row["eventType"] == "interceptor.rejected" {
			rejectionRow = row
			break
		}
	}
	if rejectionRow == nil {
		t.Fatalf("no interceptor.rejected audit row on the %q chain; the policy gate did not record the rejection (events: %v)", tenant, events)
	}

	// The audit payload carries the rejecting decision context.
	payloadRaw, _ := json.Marshal(rejectionRow["payload"])
	var payload map[string]any
	if err := json.Unmarshal(payloadRaw, &payload); err != nil {
		t.Fatalf("audit row payload is not JSON: %v", err)
	}
	if payload["caller_tenant_id"] != tenant {
		t.Errorf("audit payload caller_tenant_id = %v, want %s", payload["caller_tenant_id"], tenant)
	}
	if payload["caller_sub"] != user {
		t.Errorf("audit payload caller_sub = %v, want %s", payload["caller_sub"], user)
	}
	if payload["error_code"] != "QUOTA_EXCEEDED" {
		t.Errorf("audit payload error_code = %v, want QUOTA_EXCEEDED", payload["error_code"])
	}
	if reason, _ := payload["reason"].(string); reason == "" {
		t.Error("audit payload must carry the rejection reason")
	}

	// §11.7: the chain stays verifiable after the policy-rejection write.
	code, verify := c.do(http.MethodGet, "/v1/admin/audit-events/verify?tenantId="+tenant,
		"platform", "ops@acme.com", "platform-admin", nil)
	if code != http.StatusOK {
		t.Fatalf("verify audit chain: status %d (%v)", code, verify)
	}
	if verify["integrity"] != "verified" {
		t.Errorf("audit chain integrity after the interceptor.rejected write = %v, want verified", verify["integrity"])
	}
}
