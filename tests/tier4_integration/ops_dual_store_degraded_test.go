//go:build integration

// SPDX-License-Identifier: MIT

// Tier-4 integration test for the §25.15 "Postgres + Redis both down"
// failure-mode row: the real cmd/lenny-ops binary running against a
// Postgres and a Redis container, with both stores taken down mid-test
// so the composition root exercises its Tier-3 in-memory fall-through
// against genuine connection failures rather than error-returning fakes.
//
// The unit and component suites (pkg/ops/escalation, pkg/ops/coordination,
// pkg/ops/opsserver) cover the tiered create/acquire logic against
// in-memory stores that return the unavailable sentinel. This test adds
// the wiring proof the §25.15 compound claim needs: that the live
// cmd/lenny-ops composition root actually threads the durable Postgres and
// Redis tiers over the in-memory buffer, so when both real backends fail
// together, escalation creation and remediation-lock acquisition degrade to
// the in-memory tier instead of erroring. A regression that failed to wire
// the in-memory fall-through (or that pre-populated no buffer) would pass
// the handler-tier fakes but fail here.
package tier4_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/opsprocess"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// TestOpsDualStoreDegradedInMemoryFallback boots cmd/lenny-ops against real
// Postgres and Redis, proves the durable tiers serve while both stores are
// up, then stops both containers and asserts the §25.15 dual-outage degraded
// mode: escalation creation falls back to the in-memory Tier-3 buffer (202
// Accepted, X-Lenny-Persistence: buffered-memory) and a remediation-lock
// acquire falls back to the in-memory single-replica tier (201 Created,
// lockStore=memory). Both stores are real containers stopped mid-test, so
// the fall-through is driven by genuine connection failures the way a
// production dual outage would drive it.
//
// spec: §25.15 Failure Mode Analysis, "Postgres + Redis both down" row —
// "Core operational loop still functions in degraded mode: ... remediation
// locks in-memory (single-replica only), escalation creation in-memory (202
// Accepted) ...". The single-replica-only in-memory lock tier is the §25.4
// ops.locks.memoryTier default; the single-process opsprocess harness runs
// with no cluster connection, which the policy treats as a single replica
// and grants.
//
// diagnosis: a failure means the cmd/lenny-ops composition root does not
// deliver the §25.15 dual-outage degraded mode against real failing stores.
// Either the escalation service did not fall through to its in-memory Tier-3
// buffer (no 202 / buffered-memory) or the remediation-lock service did not
// fall through to its in-memory single-replica tier (no 201 / lockStore=
// memory) when both Postgres and Redis became unreachable — a wiring
// regression the handler-tier fakes cannot catch because they never exercise
// the live composition root against real backends failing together.
func TestOpsDualStoreDegradedInMemoryFallback(t *testing.T) {
	opsprocess.SkipUnlessAvailable(t)

	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	rd := containers.StartRedis(t, containers.RedisOptions{})

	ops := opsprocess.StartWith(
		t,
		"--postgres-dsn="+pg.DSN,
		"--redis-url=redis://"+rd.Addr+"/0",
		"--redis-allow-insecure",
	)
	base := ops.BaseURL()
	// A bounded client so a would-be hang against a stopped backend surfaces
	// as a test failure rather than stalling the suite; a prompt
	// connection-refused resolves well inside this window.
	client := &http.Client{Timeout: 30 * time.Second}

	// do issues an admin request under the dev platform-admin headers the
	// unauthenticated ops surface honours and returns the status, the
	// response headers, and the decoded top-level JSON object.
	do := func(method, path string, body any) (int, http.Header, map[string]any) {
		t.Helper()
		var reader io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			reader = bytes.NewReader(b)
		}
		req, _ := http.NewRequestWithContext(context.Background(), method, base+path, reader)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		req.Header.Set("X-Lenny-User-ID", "alice@acme.com")
		req.Header.Set("X-Lenny-Roles", "platform-admin")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var out map[string]any
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &out)
		}
		return resp.StatusCode, resp.Header, out
	}

	// ---- precondition: with both stores up the durable tiers serve ----
	// This makes the degraded behavior after the outage attributable to the
	// injected failure rather than to a subsystem that was never wired to a
	// durable store.
	code, hdr, esc := do(http.MethodPost, "/v1/admin/escalations", map[string]any{
		"severity": "critical",
		"summary":  "precondition: escalation lands durably while Postgres is up",
	})
	if code != http.StatusCreated {
		t.Fatalf("precondition POST /v1/admin/escalations: status %d, want 201 (durable Postgres tier); body %v", code, esc)
	}
	if got := hdr.Get("X-Lenny-Persistence"); got != "durable-postgres" {
		t.Fatalf("precondition escalation X-Lenny-Persistence = %q, want durable-postgres", got)
	}

	code, _, lock := do(http.MethodPost, "/v1/admin/remediation-locks", map[string]any{
		"scope":      "pool:acme-precondition",
		"operation":  "scale",
		"ttlSeconds": 300,
	})
	if code != http.StatusCreated {
		t.Fatalf("precondition POST /v1/admin/remediation-locks: status %d, want 201; body %v", code, lock)
	}
	if store, _ := lock["lockStore"].(string); store != "postgres" {
		t.Fatalf("precondition lock.lockStore = %q, want postgres (Tier 1 with Postgres reachable)", store)
	}

	// ---- inject: stop both stores ----
	// Subsequent store operations reach a closed backend (connection
	// refused) rather than returning stale success, so the tiered services
	// must fall through to their in-memory Tier-3 stores.
	rd.Stop(t)
	pg.Stop(t)

	// ---- degraded: escalation creation falls back to in-memory (202) ----
	// §25.15: "escalation creation in-memory (202 Accepted)". The record
	// lands in the Tier-3 buffer, so the create is a 202 with the
	// X-Lenny-Persistence: buffered-memory header and a durability warning.
	code, hdr, esc = do(http.MethodPost, "/v1/admin/escalations", map[string]any{
		"severity": "critical",
		"summary":  "both stores down: escalation must still be recorded in memory",
	})
	if code != http.StatusAccepted {
		t.Fatalf("dual-outage POST /v1/admin/escalations: status %d, want 202 (in-memory Tier-3 buffer); body %v", code, esc)
	}
	if got := hdr.Get("X-Lenny-Persistence"); got != "buffered-memory" {
		t.Errorf("dual-outage escalation X-Lenny-Persistence = %q, want buffered-memory", got)
	}
	if p, _ := esc["persistence"].(string); p != "buffered-memory" {
		t.Errorf("dual-outage escalation.persistence = %q, want buffered-memory; body %v", p, esc)
	}
	if warn, _ := esc["warning"].(string); warn == "" {
		t.Errorf("dual-outage escalation carried no durability warning; body %v", esc)
	}

	// ---- degraded: remediation-lock acquire falls back to in-memory ----
	// §25.15: "remediation locks in-memory (single-replica only)". The
	// single-process harness is a single replica, so the ops.locks.memoryTier
	// single-replica-only default grants the in-memory acquire.
	code, _, lock = do(http.MethodPost, "/v1/admin/remediation-locks", map[string]any{
		"scope":      "pool:acme-degraded",
		"operation":  "scale",
		"ttlSeconds": 300,
	})
	if code != http.StatusCreated {
		t.Fatalf("dual-outage POST /v1/admin/remediation-locks: status %d, want 201 (in-memory single-replica tier); body %v", code, lock)
	}
	if store, _ := lock["lockStore"].(string); store != "memory" {
		t.Errorf("dual-outage lock.lockStore = %q, want memory (Tier 3 with both stores down)", store)
	}
}
