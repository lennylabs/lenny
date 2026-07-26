// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §27.2 layer-4 Ready-gate self-heal and
// the deadlock-avoidance guarantee for non-playground routes, exercised
// through the real cmd/lenny-gateway binary against a real Postgres
// container (not the fake in-memory tenant registry the component-level
// pkg/gateway/mcpfabric/playground/playground_test.go suite uses).
//
// The lenny-bootstrap Job (helm.sh/hook: post-install,post-upgrade)
// polls GET /healthz before it seeds the devTenantId row, so this test
// simulates the bootstrap-ordering window by starting the gateway with
// playground.authMode=dev bound to a devTenantId that does not exist in
// Postgres yet, then seeding it after the fact via the same
// POST /v1/admin/bootstrap upsert path the real bootstrap flow uses
// (tests/tier4_integration/gateway_postgres_e2e_test.go exercises the
// identical Postgres-persistence wiring), rather than a fake tenant
// registry.
package tier4_integration_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// spec: §27.3 — "if the tenant id is well-formed yet absent from
// Postgres at startup the gateway process starts normally and only the
// /playground/* route family returns 503
// LENNY_PLAYGROUND_DEV_TENANT_NOT_SEEDED ... All non-playground routes
// (admin API, /healthz, /v1/sessions, MCP endpoints) remain fully
// available ... The gateway re-checks tenant existence on every
// /playground/* request ..., so the 503 self-heals the instant the
// bootstrap Job commits the tenant row; no gateway restart or rollout
// is required."
//
// diagnosis: a failure here means either (a) the Ready-gate rejected a
// request to a non-playground route (admin API, /healthz, /v1/sessions)
// while the configured devTenantId was still unseeded, which would
// deadlock the real lenny-bootstrap Job's GET /healthz poll against a
// tenant row that Job itself is responsible for creating, or (b) the
// gateway kept a negative cache of the "not seeded" lookup so that
// /playground/* kept returning 503 after the tenant row was seeded
// without a process restart, contradicting the "no gateway restart or
// rollout is required" guarantee. As of this writing the test cannot
// even reach these assertions: cmd/lenny-gateway crash-loops the
// instant --playground-enabled is set (any playground.authMode) because
// pkg/gateway/mcpfabric/playground/metrics.go registers the
// lenny_playground_page_views_total counter with the camelCase label
// "authMode", which pkg/observability/metrics's §16.1.1 snake_case
// validator rejects as a fatal registration error
// (cmd/lenny-gateway/httpsurface.go's log.Fatalf on playground.NewMetrics).
// This is the same already-tracked defect blocking T-27.2.2, T-27.5.1,
// T-27.5.2, T-27.5.3, T-27.6.1, T-27.6.2, T-27.8.1, and the tier9/tier5
// playground suites (BUILD-GAPS.md §16.1 Metrics Finding 8). Unskip once
// that defect is resolved.
func TestPlaygroundDevReadyGateSelfHealsAgainstRealPostgres(t *testing.T) {
	t.Skip("blocked by the pre-existing camelCase \"authMode\" playground-metrics label defect (BUILD-GAPS.md §16.1 Metrics Finding 8), which crash-loops any live cmd/lenny-gateway process with playground.enabled=true before this test's Ready-gate assertions can run")

	gateway.SkipUnlessAvailable(t)

	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})

	// devTenantId is well-formed (passes the startup format gate) but not
	// yet present in Postgres: the bootstrap-ordering window this test
	// targets.
	const devTenantID = "playground-dev-tenant"

	gw := gateway.StartWith(
		t,
		"--dev-mode",
		"--postgres-dsn="+pg.DSN,
		"--playground-enabled",
		"--playground-auth-mode", "dev",
		"--playground-dev-tenant-id", devTenantID,
	)
	base := gw.BaseURL()
	client := http.DefaultClient

	do := func(method, path, roles string, body any) (int, map[string]any) {
		t.Helper()
		var reader io.Reader
		if body != nil {
			b, err := json.Marshal(body)
			if err != nil {
				t.Fatalf("marshal body: %v", err)
			}
			reader = bytes.NewReader(b)
		}
		req, err := http.NewRequest(method, base+path, reader)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		req.Header.Set("X-Lenny-User-ID", "alice@acme.com")
		if roles != "" {
			req.Header.Set("X-Lenny-Roles", roles)
		}
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
		return resp.StatusCode, out
	}

	// Seed an unrelated tenant ("acme") through the same admin-bootstrap
	// upsert path the real lenny-bootstrap Job uses, so /v1/sessions has
	// a registered tenant to create against. This proves the
	// non-playground routes are unaffected by the *unrelated* devTenantId
	// gate, not merely that they don't 404.
	code, _ := do(http.MethodPost, "/v1/admin/bootstrap", "platform-admin", map[string]any{
		"tenants": []map[string]any{{"id": "acme", "displayName": "Acme Corp"}},
		"runtimes": []map[string]any{{
			"name":  "echo",
			"image": "lenny/echo@sha256:abc",
			"labels": map[string]string{
				"tier": "test",
			},
		}},
	})
	if code != http.StatusOK {
		t.Fatalf("admin bootstrap (acme): status %d, want 200 (admin API must stay available during the bootstrap-ordering window)", code)
	}

	// spec: §27.3 — "if the tenant id is well-formed yet absent from
	// Postgres at startup the gateway process starts normally and only
	// the /playground/* route family returns 503
	// LENNY_PLAYGROUND_DEV_TENANT_NOT_SEEDED ... All non-playground
	// routes (admin API, /healthz, /v1/sessions, MCP endpoints) remain
	// fully available so that the lenny-bootstrap Job ... is not
	// deadlocked against a startup gate that would itself require the
	// bootstrap Job to have run first."
	healthResp, err := client.Get(base + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	healthResp.Body.Close()
	if healthResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /healthz status = %d, want 200 while devTenantId %q is unseeded (the lenny-bootstrap Job's readiness poll must not deadlock)", healthResp.StatusCode, devTenantID)
	}

	code, created := do(http.MethodPost, "/v1/sessions/start", "", map[string]any{
		"runtimeRef": "echo",
		"userId":     "alice@acme.com",
	})
	if code != http.StatusCreated {
		t.Fatalf("POST /v1/sessions/start status = %d, want 201 while devTenantId %q is unseeded (non-playground session creation must stay available)", code, devTenantID)
	}
	if _, ok := created["id"].(string); !ok {
		t.Fatalf("create session response missing id: %+v", created)
	}

	// spec: §27.3 — "the gateway starts normally and serves all
	// non-playground routes, but every request to /playground/* returns
	// 503 LENNY_PLAYGROUND_DEV_TENANT_NOT_SEEDED with Retry-After: 5
	// until the tenant row appears."
	pgResp, err := client.Get(base + "/playground/")
	if err != nil {
		t.Fatalf("GET /playground/ (pre-seed): %v", err)
	}
	pgBody, _ := io.ReadAll(pgResp.Body)
	pgResp.Body.Close()
	if pgResp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("GET /playground/ (pre-seed) status = %d, want 503\nbody: %s", pgResp.StatusCode, pgBody)
	}
	if got := pgResp.Header.Get("Retry-After"); got != "5" {
		t.Fatalf("GET /playground/ (pre-seed) Retry-After = %q, want \"5\"", got)
	}
	var pgErr struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(pgBody, &pgErr); err != nil {
		t.Fatalf("decode 503 body: %v (body: %s)", err, pgBody)
	}
	if pgErr.Code != "LENNY_PLAYGROUND_DEV_TENANT_NOT_SEEDED" {
		t.Fatalf("GET /playground/ (pre-seed) error code = %q, want LENNY_PLAYGROUND_DEV_TENANT_NOT_SEEDED", pgErr.Code)
	}

	// The lenny-bootstrap Job commits the devTenantId row. No gateway
	// restart or rollout happens between the two /playground/ requests:
	// the same running process is reused, only the Postgres tenant table
	// gains a row.
	code, _ = do(http.MethodPost, "/v1/admin/bootstrap", "platform-admin", map[string]any{
		"tenants": []map[string]any{{"id": devTenantID, "displayName": "Playground Dev Tenant"}},
	})
	if code != http.StatusOK {
		t.Fatalf("admin bootstrap (%s): status %d, want 200", devTenantID, code)
	}

	// spec: §27.3 — "The gateway re-checks tenant existence on every
	// /playground/* request ..., so the 503 self-heals the instant the
	// bootstrap Job commits the tenant row; no gateway restart or
	// rollout is required."
	deadline := time.Now().Add(10 * time.Second)
	var lastStatus int
	for time.Now().Before(deadline) {
		resp, err := client.Get(base + "/playground/")
		if err != nil {
			t.Fatalf("GET /playground/ (post-seed): %v", err)
		}
		lastStatus = resp.StatusCode
		resp.Body.Close()
		if lastStatus == http.StatusOK {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if lastStatus != http.StatusOK {
		t.Fatalf("GET /playground/ (post-seed) status = %d, want 200 once devTenantId %q is seeded, with no gateway restart", lastStatus, devTenantID)
	}
}
