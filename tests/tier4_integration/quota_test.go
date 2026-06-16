// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §11.2 quota path end to end through the
// real cmd/lenny-gateway binary running against a Redis container. It
// proves two §11.2 properties:
//
//   - TestQuotaEnforcement — the Redis-backed per-tenant token counter
//     is wired into the §4.8 admission path: the gateway's QuotaEvaluator
//     reads the live Redis window counter and rejects a session create
//     once the recorded usage reaches the tenant's tokenQuotaPerWindow.
//   - TestQuotaRecovery — the §11.2 Redis MAX-rule reconciliation
//     (quota.ReconcileMax) protects budget enforcement: a stale Postgres
//     checkpoint that would restore a counter below the actual
//     accumulated usage is reconciled to MAX(in_memory, checkpoint), and
//     the gateway enforces the budget against the reconciled value.
//
// This file converts the TestQuotaEnforcement and TestQuotaRecovery
// scaffolds (formerly skipped in scaffolds_test.go) into real
// integration tests.
package tier4_integration_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/quota"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// spec: 11.2 (Redis-backed per-tenant token counter on the admission path)
// diagnosis: the §11.2 Redis token counter was not wired into the
// gateway admission path — the QuotaEvaluator read no live counter, so
// a tenant's recorded token usage had no effect on session creation
// and the per-tenant token budget was unenforceable end to end.
func TestQuotaEnforcement(t *testing.T) {
	gateway.SkipUnlessAvailable(t)

	rd := containers.StartRedis(t, containers.RedisOptions{})
	// allow-all noEnvironmentPolicy so the §10.6 / §11.1 environment gate
	// admits a no-environment session create and the §4.8 QuotaEvaluator
	// is the gate under test on the create path.
	gw := gateway.StartWith(t, "--dev-mode", "--redis-url=redis://"+rd.Addr+"/0",
		"--no-environment-policy", "allow-all")
	c := policyClient{t: t, base: gw.BaseURL()}
	ctx := context.Background()

	const tenant = "acme"
	const user = "alice@acme.com"
	const limit = 10_000
	code, _ := c.do(http.MethodPost, "/v1/admin/tenants", "platform", "ops@acme.com", "platform-admin",
		map[string]any{"id": tenant, "displayName": "Acme Corp", "tokenQuotaPerWindow": limit})
	if code != http.StatusCreated {
		t.Fatalf("create tenant: status %d", code)
	}

	// tokenQuotaPerWindow is the tenant-scope limit, so the usage the
	// QuotaEvaluator checks lives in the tenant-rollup window, not any
	// per-user window.
	key := tenantQuotaWindowKey(tenant, time.Now())

	// ---- well under the limit: the create is admitted ----
	if err := rd.Client.Set(ctx, key, 2_000, time.Hour).Err(); err != nil {
		t.Fatalf("seed counter: %v", err)
	}
	if code, body := c.do(http.MethodPost, "/v1/sessions", tenant, user, "",
		map[string]any{"runtimeRef": "claude-code", "userId": user}); code != http.StatusCreated {
		t.Fatalf("create at 20%% of budget: status %d (%v), want 201", code, body)
	}

	// ---- just below the limit (soft-warning band): still admitted ----
	// §11.2: soft warnings fire at 80%; the request is admitted up to
	// 100%. 9_500 of 10_000 is in the soft-warning band.
	if err := rd.Client.Set(ctx, key, 9_500, time.Hour).Err(); err != nil {
		t.Fatalf("seed counter: %v", err)
	}
	if code, body := c.do(http.MethodPost, "/v1/sessions", tenant, user, "",
		map[string]any{"runtimeRef": "claude-code", "userId": user}); code != http.StatusCreated {
		t.Fatalf("create at 95%% of budget: status %d (%v); §11.2 admits up to 100%%", code, body)
	}

	// ---- at the limit: the create is rejected ----
	if err := rd.Client.Set(ctx, key, limit, time.Hour).Err(); err != nil {
		t.Fatalf("seed counter: %v", err)
	}
	code, rejected := c.do(http.MethodPost, "/v1/sessions", tenant, user, "",
		map[string]any{"runtimeRef": "claude-code", "userId": user})
	if code != http.StatusTooManyRequests {
		t.Fatalf("create at 100%% of budget: status %d (%v); §11.2 hard-limits at 100%%", code, rejected)
	}
	errBody, _ := rejected["error"].(map[string]any)
	if errBody == nil || errBody["code"] != "QUOTA_EXCEEDED" {
		t.Errorf("rejection code = %v, want QUOTA_EXCEEDED", rejected)
	}

	// ---- back under the limit: enforcement releases ----
	// The gateway enforces against the live counter, so dropping the
	// recorded usage immediately re-admits creates.
	if err := rd.Client.Set(ctx, key, 1_000, time.Hour).Err(); err != nil {
		t.Fatalf("reset counter: %v", err)
	}
	if code, body := c.do(http.MethodPost, "/v1/sessions", tenant, user, "",
		map[string]any{"runtimeRef": "claude-code", "userId": user}); code != http.StatusCreated {
		t.Fatalf("create after the counter dropped: status %d (%v), want 201", code, body)
	}
}

// spec: 11.2 (Redis MAX-rule reconciliation on counter recovery)
// diagnosis: the §11.2 MAX rule was not applied when restoring a token
// counter — a stale Postgres checkpoint (up to quotaSyncIntervalSeconds
// old) restored a counter below the actual accumulated usage, silently
// un-enforcing a budget violation that occurred during the fail-open
// window. quota.ReconcileMax(in_memory, checkpoint) is the §11.2 rule
// that prevents the regression; this test drives the reconciled
// counter through the live gateway and asserts the budget still holds.
func TestQuotaRecovery(t *testing.T) {
	gateway.SkipUnlessAvailable(t)

	rd := containers.StartRedis(t, containers.RedisOptions{})
	// allow-all noEnvironmentPolicy so the §10.6 / §11.1 environment gate
	// admits a no-environment session create and the §4.8 QuotaEvaluator
	// enforcing the reconciled counter is the gate under test.
	gw := gateway.StartWith(t, "--dev-mode", "--redis-url=redis://"+rd.Addr+"/0",
		"--no-environment-policy", "allow-all")
	c := policyClient{t: t, base: gw.BaseURL()}
	ctx := context.Background()

	const tenant = "acme"
	const user = "alice@acme.com"
	const limit = 8_000
	code, _ := c.do(http.MethodPost, "/v1/admin/tenants", "platform", "ops@acme.com", "platform-admin",
		map[string]any{"id": tenant, "displayName": "Acme Corp", "tokenQuotaPerWindow": limit})
	if code != http.StatusCreated {
		t.Fatalf("create tenant: status %d", code)
	}

	// tokenQuotaPerWindow is the tenant-scope limit, so the reconciled
	// usage the QuotaEvaluator checks lives in the tenant-rollup window.
	key := tenantQuotaWindowKey(tenant, time.Now())

	// Accumulated usage at the moment a Redis outage begins: the tenant
	// has consumed its whole budget. This is the in-memory counter the
	// surviving gateway replica tracked during the fail-open window.
	const accumulated = int64(8_000)
	if err := rd.Client.Set(ctx, key, accumulated, time.Hour).Err(); err != nil {
		t.Fatalf("seed accumulated usage: %v", err)
	}

	// The last durable Postgres checkpoint is stale — it predates the
	// fail-open window's consumption and records far less usage.
	const staleCheckpoint = int64(1_500)

	// §11.2 Redis recovery: the gateway restores each counter to
	// MAX(in_memory_counter, postgres_checkpoint). Applying the stale
	// checkpoint naively would reset the counter to 1_500 and let the
	// over-budget tenant create sessions again; the MAX rule keeps it
	// at the true accumulated value.
	restored := quota.ReconcileMax(accumulated, staleCheckpoint)
	if restored != accumulated {
		t.Fatalf("ReconcileMax(%d, %d) = %d, want %d (§11.2 MAX rule must keep the higher value)",
			accumulated, staleCheckpoint, restored, accumulated)
	}
	if err := rd.Client.Set(ctx, key, restored, time.Hour).Err(); err != nil {
		t.Fatalf("write reconciled counter: %v", err)
	}

	// The gateway enforces against the reconciled counter: the
	// over-budget tenant is still rejected. Had the stale checkpoint
	// won, this create would have been admitted.
	code, rejected := c.do(http.MethodPost, "/v1/sessions", tenant, user, "",
		map[string]any{"runtimeRef": "claude-code", "userId": user})
	if code != http.StatusTooManyRequests {
		t.Fatalf("create after MAX-rule recovery: status %d (%v); the reconciled counter must keep the budget enforced", code, rejected)
	}
	errBody, _ := rejected["error"].(map[string]any)
	if errBody == nil || errBody["code"] != "QUOTA_EXCEEDED" {
		t.Errorf("post-recovery rejection code = %v, want QUOTA_EXCEEDED", rejected)
	}

	// Control: if the stale checkpoint had won (no MAX rule), the
	// counter would sit at 1_500 and the create would be admitted —
	// confirming the MAX rule is the load-bearing reconciliation step.
	if err := rd.Client.Set(ctx, key, staleCheckpoint, time.Hour).Err(); err != nil {
		t.Fatalf("write stale checkpoint: %v", err)
	}
	if code, body := c.do(http.MethodPost, "/v1/sessions", tenant, user, "",
		map[string]any{"runtimeRef": "claude-code", "userId": user}); code != http.StatusCreated {
		t.Fatalf("create against the stale-checkpoint counter: status %d (%v); a 1_500/8_000 window is under budget", code, body)
	}
}
