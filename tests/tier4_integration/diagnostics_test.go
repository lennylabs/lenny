//go:build integration

// SPDX-License-Identifier: MIT

// Tier-4 integration test: the real cmd/lenny-ops binary running against a
// live Postgres and Redis, driving the §25.6 diagnostic endpoints a DevOps
// agent uses in place of psql and kubectl. It seeds a failed session and a
// warm pool with a per-state pod breakdown into the real platform tables,
// then walks the agent journey over HTTP — GET
// /v1/admin/diagnostics/sessions/{id} for the structured cause chain and GET
// /v1/admin/diagnostics/pools/{name} for the pod-count breakdown — asserting
// the responses are computed from live store data rather than a stub. This
// exercises the surface above the tier-2 Postgres-reader component test and
// the tier-3 contract test with a stubbed DataSource, neither of which runs
// the cmd/lenny-ops composition root end to end against real backends.
package tier4_integration_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/lennylabs/lenny/pkg/gateway/storage/pgtenant"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/opsprocess"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// TestDiagnosticsDevOpsJourneyAgainstLiveStoresE2E boots cmd/lenny-ops
// against a real Postgres and Redis, seeds a budget-terminated session and a
// warm pool whose pods span every state, then drives the §25.6 session and
// pool diagnostic endpoints and asserts each answer is derived from the live
// store.
//
// spec: §25.6 line 2824 ("GET /v1/admin/diagnostics/sessions/{id} |
// Structured cause chain for a session"), line 2896 ("Builds cause chain by
// cross-referencing ...") and line 2890 (the cause chain cross-references the
// session's terminal failure, so a budget termination surfaces BUDGET_EXPIRED
// from session state alone); §25.6 line 2825 ("GET
// /v1/admin/diagnostics/pools/{name} | Pool bottleneck analysis") and line
// 2899 ("Reads agent_pod_state table grouped by state → pod count
// breakdown"); §25.6 line 2882 (a diagnosis served from a fallback source
// carries the degradation envelope, and lenny-ops without a Kubernetes
// connection cannot enrich pod signals).
//
// diagnosis: a failure means the cmd/lenny-ops composition root did not
// thread the live Postgres store through the §25.6 DiagnosticService. Either
// the session cause chain was not built from the seeded failure_reason, the
// pool pod-count breakdown did not reflect the seeded agent_pod_state rows,
// or the partial-result degradation envelope was absent when no Kubernetes
// connection was configured — any of which shows the diagnostic endpoints
// diverged from §25.6 when driven against real backends rather than a stub.
func TestDiagnosticsDevOpsJourneyAgainstLiveStoresE2E(t *testing.T) {
	opsprocess.SkipUnlessAvailable(t)

	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx := context.Background()

	// §25.6: seed a failed session and its warm pool through the real
	// platform tables. sessions and agent_pod_state carry the
	// lenny_tenant_guard trigger, so the writes run inside a tenant-scoped
	// transaction that sets app.current_tenant (§4.2 / §12.3).
	if _, err := pg.Pool.Exec(ctx,
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x00')`, "acme"); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}

	const sessID = "22222222-2222-2222-2222-222222222222"
	const pool = "default-gvisor"
	// Each pod state maps onto one §25.6 PodCountBreakdown bucket; the
	// claimed pod is the failed session's own pod.
	pods := []struct {
		id, state, sess string
	}{
		{"diag-pod-claimed", "claimed", sessID},
		{"diag-pod-idle", "idle", ""},
		{"diag-pod-warming", "warming", ""},
		{"diag-pod-failed", "failed", ""},
	}
	if err := pgtenant.InTx(ctx, pg.Pool, "acme", func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO sessions (id, tenant_id, state, runtime_ref, pool_ref, root_session_id,
				failure_class, failure_reason)
			 VALUES ($1, 'acme', 'failed', 'claude', $2, $1, 'runtime_crash', 'budget_exceeded')`,
			sessID, pool); err != nil {
			return err
		}
		for _, p := range pods {
			var sess any
			if p.sess != "" {
				sess = p.sess
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO agent_pod_state (pod_id, pool_id, state, tenant_id, session_id,
					isolation_profile, execution_mode, resource_version, node_name)
				 VALUES ($1, $2, $3, 'acme', $4, 'sandboxed', 'session', 1, 'node-a')`,
				p.id, pool, p.state, sess); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed session + pods: %v", err)
	}

	ops := opsprocess.StartWith(
		t,
		"--postgres-dsn="+pg.DSN,
		"--redis-url=redis://"+rd.Addr+"/0",
		"--redis-allow-insecure",
	)
	base := ops.BaseURL()
	client := http.DefaultClient

	get := func(path string) (int, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, base+path, nil)
		// The dev / unauthenticated surface honours the X-Lenny-* headers;
		// a watchdog agent presents platform-admin.
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		req.Header.Set("X-Lenny-User-ID", "alice@acme.com")
		req.Header.Set("X-Lenny-Roles", "platform-admin")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var out map[string]any
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &out)
		}
		return resp.StatusCode, out
	}

	// ---- session diagnosis: structured cause chain from live data ----
	//
	// The session is served from Postgres; lenny-ops in the compose stack has
	// no Kubernetes connection, so the pod-signal enrichment degrades and the
	// diagnosis returns 207 DIAGNOSTICS_PARTIAL with the canonical envelope.
	code, sess := get("/v1/admin/diagnostics/sessions/" + sessID)
	if code != http.StatusMultiStatus {
		t.Fatalf("GET diagnostics/sessions: status %d (want 207 partial), body %v", code, sess)
	}
	if got, _ := sess["state"].(string); got != "failed" {
		t.Errorf("session state = %q, want failed", got)
	}
	if got, _ := sess["runtime"].(string); got != "claude" {
		t.Errorf("session runtime = %q, want claude", got)
	}
	if got, _ := sess["pool"].(string); got != pool {
		t.Errorf("session pool = %q, want %q", got, pool)
	}
	// spec: §25.6 line 2890 — the cause chain cross-references the session's
	// terminal failure, so a budget_exceeded reason surfaces BUDGET_EXPIRED
	// even with no pod signal available.
	chain, _ := sess["causeChain"].([]any)
	if len(chain) == 0 {
		t.Fatalf("session causeChain is empty; expected a BUDGET_EXPIRED entry from live data: %v", sess)
	}
	foundBudget := false
	for _, e := range chain {
		m, _ := e.(map[string]any)
		if cat, _ := m["category"].(string); cat == "BUDGET_EXPIRED" {
			foundBudget = true
		}
	}
	if !foundBudget {
		t.Errorf("session causeChain has no BUDGET_EXPIRED entry: %v", chain)
	}
	// The partial-result envelope reports the K8s pod signals could not be
	// read against the live compose stack.
	deg, _ := sess["degradation"].(map[string]any)
	if deg == nil {
		t.Fatalf("session diagnosis missing degradation envelope: %v", sess)
	}
	if !containsString(deg["unavailableFields"], "causeChain.podSignals") {
		t.Errorf("degradation.unavailableFields = %v, want it to include causeChain.podSignals", deg["unavailableFields"])
	}

	// ---- pool diagnosis: pod-count breakdown grouped by state ----
	//
	// spec: §25.6 line 2899 — the breakdown is read from agent_pod_state
	// grouped by state. Each seeded pod lands in its matching bucket.
	code, pd := get("/v1/admin/diagnostics/pools/" + pool)
	if code != http.StatusOK && code != http.StatusMultiStatus {
		t.Fatalf("GET diagnostics/pools: status %d, body %v", code, pd)
	}
	if got, _ := pd["pool"].(string); got != pool {
		t.Errorf("pool = %q, want %q", got, pool)
	}
	counts, _ := pd["podCounts"].(map[string]any)
	if counts == nil {
		t.Fatalf("pool diagnosis missing podCounts: %v", pd)
	}
	for bucket, want := range map[string]float64{"idle": 1, "warming": 1, "claimed": 1, "failed": 1} {
		if got, _ := counts[bucket].(float64); got != want {
			t.Errorf("podCounts.%s = %v, want %v (live agent_pod_state breakdown): %v", bucket, got, want, counts)
		}
	}

	// ---- diagnostics latency histogram: scraped and labelled per endpoint ----
	//
	// spec: §25.6 line 2932 — the metrics catalog lists
	// `lenny_diagnostics_request_duration_seconds` as a Histogram labelled
	// `endpoint`, "Per-diagnostic-endpoint latency". The session and pool
	// diagnostic calls above each ran against this same lenny-ops process,
	// so the real /metrics text exposition it serves must carry one
	// completed observation per endpoint short name.
	resp, err := client.Get(base + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	metricsBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /metrics status = %d, want 200; body:\n%s", resp.StatusCode, metricsBody)
	}
	samples := parseGaugeSamples(t, string(metricsBody))
	for _, endpoint := range []string{"session", "pool"} {
		key := `lenny_diagnostics_request_duration_seconds_count{endpoint="` + endpoint + `"}`
		got, ok := samples[key]
		if !ok {
			t.Errorf("/metrics did not expose %s (§25.6 line 2932); the diagnostics histogram is not on the scraped registry for endpoint %q", key, endpoint)
			continue
		}
		if got < 1 {
			t.Errorf("%s = %v, want >= 1 (one diagnostic call was made against endpoint %q)", key, got, endpoint)
		}
	}
}

// containsString reports whether the decoded JSON array v holds the string s.
func containsString(v any, s string) bool {
	arr, ok := v.([]any)
	if !ok {
		return false
	}
	for _, e := range arr {
		if str, _ := e.(string); str == s {
			return true
		}
	}
	return false
}
