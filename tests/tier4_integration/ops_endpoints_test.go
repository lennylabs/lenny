//go:build integration

// SPDX-License-Identifier: MIT

// Tier-4 integration tests for the declared lenny_ops_endpoints suite
// (TESTING.md §12.4). They drive the §25.10 configuration-drift surface
// against the real cmd/lenny-ops binary and a live Postgres, and the
// §25.9 audit-log query surface against the real cmd/lenny-gateway binary
// and a live Postgres — the two API surfaces the suite's "drift detection,
// audit query" coverage names. Both walk the surface above the
// httptest-with-stubs component tests, which never run the composition
// root against a real store.
package tier4_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	"github.com/lennylabs/lenny/tests/testinfra/opsprocess"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// TestOpsDriftEndpointsAgainstPostgresE2E boots cmd/lenny-ops against a
// real Postgres and Redis and walks the §25.10 configuration-drift
// surface: it seeds the bootstrap_seed_snapshot via POST
// /v1/admin/drift/snapshot/refresh, proves the snapshot persisted to the
// live store by querying Postgres directly, then exercises POST
// /v1/admin/drift/validate (the §25.10 match/diverged verdict against a
// caller-supplied desired state) and GET /v1/admin/drift (the drift
// report resolving its desired side from the stored snapshot).
//
// spec: §25.10 — "POST /v1/admin/drift/validate | Validate the desired-
// state snapshot against an externally-supplied desired state ... Returns
// differences as warnings without affecting any state" and its Snapshot
// Validation contract "Returns a structured diff between the two with
// classification (added/removed/modified). Reports snapshotValidationResult:
// "match" | "diverged"." GET /v1/admin/drift "compares: ... 2. Desired
// state — read from bootstrap_seed_snapshot table in Postgres."
// diagnosis: a failure means the cmd/lenny-ops composition root did not
// thread the live Postgres snapshot store through the §25.10 drift
// surface. Either snapshot/refresh did not persist the desired state to
// bootstrap_seed_snapshot, drift/validate did not compute the documented
// match/diverged verdict against the stored snapshot, or GET /v1/admin/drift
// did not resolve its desired side from the persisted snapshot — any of
// which shows the drift endpoints diverged from §25.10 when driven against
// a real store rather than an httptest stub.
func TestOpsDriftEndpointsAgainstPostgresE2E(t *testing.T) {
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
	client := http.DefaultClient
	ctx := context.Background()

	do := func(method, path string, body any) (int, map[string]any) {
		t.Helper()
		var reader io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			reader = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, base+path, reader)
		req.Header.Set("Content-Type", "application/json")
		// The dev / unauthenticated ops surface honours the X-Lenny-*
		// headers for identity and role; a drift agent presents
		// platform-admin.
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
		return resp.StatusCode, out
	}

	snapshotRows := func() int {
		t.Helper()
		var n int
		if err := pg.Pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM bootstrap_seed_snapshot WHERE id = 'live'`).Scan(&n); err != nil {
			t.Fatalf("db query bootstrap_seed_snapshot: %v", err)
		}
		return n
	}

	// The operator's desired-state document, resource-keyed the same way
	// the §25.10 running-state collector normalizes the gateway admin
	// LIST responses so the field-by-field diff compares like with like.
	desired := map[string]any{
		"runtimes": map[string]any{
			"echo": map[string]any{
				"image":  "lenny/echo@sha256:abc",
				"labels": map[string]any{"tier": "test"},
			},
		},
		"pools": map[string]any{
			"acme-default": map[string]any{
				"minWarm": float64(2),
				"runtime": "echo",
			},
		},
	}

	// ---- seed: POST /v1/admin/drift/snapshot/refresh persists the snapshot ----
	// §25.10 keeps refresh an explicit operator action: confirm:true is
	// required before the stored snapshot is replaced.
	code, refreshed := do(http.MethodPost, "/v1/admin/drift/snapshot/refresh", map[string]any{
		"desired": desired,
		"confirm": true,
	})
	if code != http.StatusOK {
		t.Fatalf("POST /v1/admin/drift/snapshot/refresh: status %d (%v)", code, refreshed)
	}
	if replaced, _ := refreshed["replaced"].(bool); !replaced {
		t.Errorf("snapshot refresh replaced = %v, want true", refreshed["replaced"])
	}
	if n := snapshotRows(); n != 1 {
		t.Fatalf("bootstrap_seed_snapshot 'live' row count = %d, want 1 (refresh did not persist to Postgres)", n)
	}

	// ---- validate (match): the stored snapshot matches the same desired ----
	code, match := do(http.MethodPost, "/v1/admin/drift/validate", map[string]any{
		"desired": desired,
	})
	if code != http.StatusOK {
		t.Fatalf("POST /v1/admin/drift/validate (match): status %d (%v)", code, match)
	}
	if got, _ := match["snapshotValidationResult"].(string); got != "match" {
		t.Errorf("validate against identical desired: snapshotValidationResult = %q, want match (%v)", got, match)
	}
	if diffs, _ := match["differenceCount"].(float64); diffs != 0 {
		t.Errorf("validate against identical desired: differenceCount = %v, want 0", diffs)
	}

	// ---- validate (diverged): a modified desired diverges from the snapshot ----
	divergedDesired := map[string]any{
		"runtimes": map[string]any{
			"echo": map[string]any{
				"image":  "lenny/echo@sha256:DIFFERENT", // §25.10 high-severity image change
				"labels": map[string]any{"tier": "test"},
			},
		},
		"pools": map[string]any{
			"acme-default": map[string]any{
				"minWarm": float64(5), // changed scaling parameter
				"runtime": "echo",
			},
		},
	}
	code, diverged := do(http.MethodPost, "/v1/admin/drift/validate", map[string]any{
		"desired": divergedDesired,
	})
	if code != http.StatusOK {
		t.Fatalf("POST /v1/admin/drift/validate (diverged): status %d (%v)", code, diverged)
	}
	if got, _ := diverged["snapshotValidationResult"].(string); got != "diverged" {
		t.Errorf("validate against modified desired: snapshotValidationResult = %q, want diverged (%v)", got, diverged)
	}
	if diffs, _ := diverged["differenceCount"].(float64); diffs < 1 {
		t.Errorf("validate against modified desired: differenceCount = %v, want >= 1", diffs)
	}
	if _, ok := diverged["differences"].([]any); !ok {
		t.Errorf("validate against modified desired: response has no differences array: %v", diverged)
	}

	// ---- report: GET /v1/admin/drift resolves the desired side from the snapshot ----
	// §25.10: the report compares the running state against the stored
	// bootstrap_seed_snapshot. This lenny-ops runs with no --gateway-url,
	// so the running-state collector degrades to the documented empty
	// running state; the desired side is still resolved from the persisted
	// snapshot, which is the store wiring this exercises.
	code, report := do(http.MethodGet, "/v1/admin/drift", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /v1/admin/drift: status %d (%v)", code, report)
	}
	if got, _ := report["desiredStateSource"].(string); got != "snapshot" {
		t.Errorf("GET /v1/admin/drift: desiredStateSource = %q, want snapshot (should read the stored snapshot)", got)
	}
	if got, _ := report["against"].(string); got != "live" {
		t.Errorf("GET /v1/admin/drift: against = %q, want live", got)
	}
	if _, ok := report["snapshot_written_at"].(string); !ok {
		t.Errorf("GET /v1/admin/drift: snapshot_written_at absent; the report did not surface the stored snapshot provenance: %v", report)
	}
	// The persisted desired declares resources the empty running state does
	// not, so each reads as removed drift: the report's driftCount is
	// non-zero, proving the diff ran against the resolved desired side.
	if got, _ := report["driftCount"].(float64); got < 1 {
		t.Errorf("GET /v1/admin/drift: driftCount = %v, want >= 1 (desired resources absent from running state read as drift)", got)
	}
}

// TestGatewayAuditQueryEndpointsE2E boots cmd/lenny-gateway and exercises
// the §25.9 audit-log query surface: after a session lifecycle writes
// audit rows, GET /v1/admin/audit-events returns the OCSF egress envelope
// and GET /v1/admin/audit-events/summary returns the aggregate counts
// grouped over the time window, rejecting an unsupported grouping.
//
// The gateway runs with the dev-mode audit store so the session-lifecycle
// events reliably accrue on the queried tenant's §11.7 chain; the §25.9
// list and summary handlers under test read the same StoreRouter audit
// path regardless of the backing store.
//
// spec: §25.9 — "GET /v1/admin/audit-events | Paginated query"; the
// envelope "carries ocsfVersion ("1.1.0") and chainIntegrityReport ... as
// top-level fields outside items[]"; "GET /v1/admin/audit-events/summary |
// Aggregate counts by type/actor/resource over a time window. Params:
// ?since=, ?until=, ?groupBy=eventType|actorId|resourceType".
// diagnosis: a failure means the real cmd/lenny-gateway binary did not
// serve the §25.9 audit query surface as specified. Either the
// audit-events list did not carry the OCSF egress envelope (ocsfVersion,
// chainIntegrityReport, items), the summary did not aggregate counts by
// the requested grouping, or the summary did not reject an unsupported
// groupBy value with the documented 400.
func TestGatewayAuditQueryEndpointsE2E(t *testing.T) {
	gateway.SkipUnlessAvailable(t)

	gw := gateway.StartWith(t, "--dev-mode")
	base := gw.BaseURL()
	client := http.DefaultClient

	do := func(method, path, roles string, body any) (int, map[string]any) {
		t.Helper()
		var reader io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			reader = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, base+path, reader)
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

	// ---- seed: bootstrap a tenant, runtime, and user, then run a
	//      session lifecycle so the §11.7 audit chain accrues rows under
	//      the acme tenant (session and message events are tenant-scoped,
	//      unlike platform-level admin mutations). ----
	code, boot := do(http.MethodPost, "/v1/admin/bootstrap", "platform-admin", map[string]any{
		"tenants": []map[string]any{{"id": "acme", "displayName": "Acme Corp"}},
		"runtimes": []map[string]any{{
			"name":   "echo",
			"image":  "lenny/echo@sha256:abc",
			"labels": map[string]string{"tier": "test"},
			// §5.1: declare injection support so the mid-session /messages
			// call is admitted and audited.
			"capabilities": map[string]any{
				"injection": map[string]any{"supported": true},
			},
		}},
		"users": []map[string]any{{
			"subject": "auth0|alice", "tenantId": "acme",
			"email": "alice@acme.com", "roles": []string{"tenant-admin"},
		}},
	})
	if code != http.StatusOK {
		t.Fatalf("POST /v1/admin/bootstrap: status %d (%v)", code, boot)
	}

	code, created := do(http.MethodPost, "/v1/sessions/start", "", map[string]any{
		"runtimeRef": "echo",
		"userId":     "alice@acme.com",
	})
	if code != http.StatusCreated {
		t.Fatalf("POST /v1/sessions/start: status %d (%v)", code, created)
	}
	sid, _ := created["id"].(string)
	if sid == "" {
		t.Fatal("session id missing")
	}
	code, msg := do(http.MethodPost, "/v1/sessions/"+sid+"/messages", "", map[string]any{
		"messages": []map[string]any{{"role": "user", "content": "hello audit"}},
	})
	if code != http.StatusOK {
		t.Fatalf("POST /v1/sessions/%s/messages: status %d (%v)", sid, code, msg)
	}
	code, _ = do(http.MethodPost, "/v1/sessions/"+sid+"/terminate", "", nil)
	if code != http.StatusOK {
		t.Fatalf("POST /v1/sessions/%s/terminate: status %d", sid, code)
	}

	// ---- list: GET /v1/admin/audit-events returns the OCSF egress envelope ----
	// The bootstrap mutation is audited under the actor's tenant (acme);
	// a platform-admin reads it by naming that tenant.
	code, list := do(http.MethodGet, "/v1/admin/audit-events?tenantId=acme", "platform-admin", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /v1/admin/audit-events: status %d (%v)", code, list)
	}
	if got, _ := list["ocsfVersion"].(string); got != "1.1.0" {
		t.Errorf("audit-events envelope ocsfVersion = %q, want 1.1.0", got)
	}
	items, ok := list["items"].([]any)
	if !ok || len(items) < 1 {
		t.Fatalf("audit-events envelope items = %v, want >= 1 OCSF record", list["items"])
	}
	report, _ := list["chainIntegrityReport"].(map[string]any)
	if report == nil {
		t.Fatalf("audit-events envelope carried no chainIntegrityReport: %v", list)
	}
	if broken, _ := report["broken"].(float64); broken != 0 {
		t.Errorf("audit chain integrity: %v broken rows, want 0", broken)
	}
	if verified, _ := report["verified"].(float64); verified < 1 {
		t.Errorf("audit chain integrity: %v verified rows, want >= 1", verified)
	}

	// ---- summary: GET /v1/admin/audit-events/summary aggregates by group ----
	code, summary := do(http.MethodGet,
		"/v1/admin/audit-events/summary?tenantId=acme&groupBy=eventType", "platform-admin", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /v1/admin/audit-events/summary: status %d (%v)", code, summary)
	}
	if got, _ := summary["groupBy"].(string); got != "eventType" {
		t.Errorf("summary groupBy = %q, want eventType", got)
	}
	if total, _ := summary["total"].(float64); total < 1 {
		t.Errorf("summary total = %v, want >= 1 (the bootstrap wrote audit rows)", total)
	}
	groups, ok := summary["groups"].([]any)
	if !ok || len(groups) < 1 {
		t.Fatalf("summary groups = %v, want >= 1 bucket", summary["groups"])
	}
	// §25.9: buckets are aggregate counts. Each carries a key and a
	// positive count, and buckets are returned in descending count order.
	prevCount := -1.0
	for i, g := range groups {
		bucket, _ := g.(map[string]any)
		if bucket == nil {
			t.Fatalf("summary group %d is not an object: %v", i, g)
		}
		if key, _ := bucket["key"].(string); key == "" {
			t.Errorf("summary group %d has an empty key: %v", i, bucket)
		}
		count, _ := bucket["count"].(float64)
		if count < 1 {
			t.Errorf("summary group %d count = %v, want >= 1", i, count)
		}
		if prevCount >= 0 && count > prevCount {
			t.Errorf("summary groups not in descending count order: group %d count %v > previous %v", i, count, prevCount)
		}
		prevCount = count
	}

	// ---- boundary: an unsupported groupBy is rejected ----
	// §25.9 fixes the grouping vocabulary at eventType|actorId|resourceType.
	code, badGroup := do(http.MethodGet,
		"/v1/admin/audit-events/summary?tenantId=acme&groupBy=bogus", "platform-admin", nil)
	if code != http.StatusBadRequest {
		t.Errorf("summary with unsupported groupBy: status %d, want 400 (%v)", code, badGroup)
	}
}

// TestGatewaySideOpsEndpointsE2E boots the real cmd/lenny-gateway binary
// against a live Postgres and Redis and walks the §25.3 gateway-side ops
// endpoints the declared lenny_ops_endpoints suite (TESTING.md §12.4)
// names — health, capacity recommendations, and version/config
// introspection — plus the §25.3 in-memory event buffer's cursor
// round-trip. These are the in-process endpoints the gateway serves from
// its own admin listener; the value over the httptest-with-stubs
// component tests is that the composition root threads the live backends
// through the health probes and surfaces the effective running config.
//
// spec: §25.3 — the Platform Health API ("GET /v1/admin/health |
// Aggregate health of all components"; "The health endpoint itself never
// returns 5xx"; "UNKNOWN_HEALTH_COMPONENT | ... 404"); Capacity
// Recommendations ("GET /v1/admin/recommendations | Prioritized
// recommendations. Optional ?category= filter"; "While every ring buffer
// is empty ... the response's degradation envelope reports "level":
// "degraded" with "thresholdSource": "compiled-in-defaults""; "UNKNOWN_
// RECOMMENDATION_CATEGORY | ... 400"); Version and Config Introspection
// ("GET /v1/admin/platform/version | Compiled-in version info
// (gateway.version, gitCommit, buildDate, goVersion)"; "GET
// /v1/admin/platform/config | Effective running configuration"); and the
// Gateway Event Buffer ("GET /v1/admin/events/buffer | Recent events from
// in-memory buffer. Params: ?since={monotonic_id} ...", "Each event is
// assigned a monotonic uint64 ID ... for cursor-based polling").
// diagnosis: a failure means the cmd/lenny-gateway composition root did
// not serve one of the §25.3 gateway-side ops endpoints as specified when
// driven against real backends. Either the health aggregate did not
// thread the live Postgres/Redis probes into per-component verdicts (or
// returned a forbidden 5xx / mis-coded an unknown component), the
// recommendations endpoint did not report the data-starved degraded
// envelope or its category-filter error codes, version/config did not
// surface the compiled-in metadata and effective backend wiring, or the
// event buffer did not round-trip a monotonic cursor over a real emitted
// event.
func TestGatewaySideOpsEndpointsE2E(t *testing.T) {
	gateway.SkipUnlessAvailable(t)

	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	rd := containers.StartRedis(t, containers.RedisOptions{})

	gw := gateway.StartWith(
		t, "--dev-mode",
		"--postgres-dsn="+pg.DSN,
		"--redis-url=redis://"+rd.Addr+"/0",
	)
	base := gw.BaseURL()
	client := http.DefaultClient

	// do issues an admin request under the given tenant/role dev headers
	// and returns the status plus the decoded top-level JSON object.
	do := func(method, path, tenant, roles string, body any) (int, map[string]any) {
		t.Helper()
		var reader io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			reader = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, base+path, reader)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Lenny-Tenant-ID", tenant)
		req.Header.Set("X-Lenny-User-ID", "alice@"+tenant+".example")
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

	// ---- Platform Health API ----
	// §25.3: the aggregate health synthesizes the live dependency probes.
	// With Postgres and Redis reachable, both components report healthy,
	// proving the composition root wired the real probes rather than a
	// static stub. The endpoint returns 200 regardless of verdict.
	t.Run("health", func(t *testing.T) {
		code, report := do(http.MethodGet, "/v1/admin/health", "platform", "platform-admin", nil)
		if code != http.StatusOK {
			t.Fatalf("GET /v1/admin/health: status %d, want 200 (never 5xx) (%v)", code, report)
		}
		status, _ := report["status"].(string)
		switch status {
		case "healthy", "degraded", "unhealthy":
		default:
			t.Errorf("aggregate status = %q, want one of healthy/degraded/unhealthy", status)
		}
		comps, _ := report["components"].([]any)
		if len(comps) == 0 {
			t.Fatalf("health report carried no components: %v", report)
		}
		byName := map[string]string{}
		for _, c := range comps {
			comp, _ := c.(map[string]any)
			name, _ := comp["name"].(string)
			st, _ := comp["status"].(string)
			byName[name] = st
		}
		// §25.3 Data Sources / Degradation: the live Postgres and Redis
		// probes are wired when the backends are configured, and both are
		// reachable here, so each reports healthy.
		if got, ok := byName["postgres"]; !ok {
			t.Errorf("health report has no postgres component (probe not wired): %v", byName)
		} else if got != "healthy" {
			t.Errorf("postgres component status = %q, want healthy (Postgres is reachable)", got)
		}
		if got, ok := byName["redis"]; !ok {
			t.Errorf("health report has no redis component (probe not wired): %v", byName)
		} else if got != "healthy" {
			t.Errorf("redis component status = %q, want healthy (Redis is reachable)", got)
		}

		// summary is the minimal synthetic-check surface: status + count.
		code, summary := do(http.MethodGet, "/v1/admin/health/summary", "platform", "platform-admin", nil)
		if code != http.StatusOK {
			t.Fatalf("GET /v1/admin/health/summary: status %d (%v)", code, summary)
		}
		if _, ok := summary["status"].(string); !ok {
			t.Errorf("health summary missing status: %v", summary)
		}
		if n, _ := summary["componentCount"].(float64); n < 1 {
			t.Errorf("health summary componentCount = %v, want >= 1", summary["componentCount"])
		}

		// component deep-dive resolves a registered component by name.
		code, comp := do(http.MethodGet, "/v1/admin/health/postgres", "platform", "platform-admin", nil)
		if code != http.StatusOK {
			t.Fatalf("GET /v1/admin/health/postgres: status %d (%v)", code, comp)
		}
		if name, _ := comp["name"].(string); name != "postgres" {
			t.Errorf("component deep-dive name = %q, want postgres", name)
		}

		// §25.3: UNKNOWN_HEALTH_COMPONENT (404) is the only error the
		// health surface returns; an unknown name is a 4xx, never a 5xx.
		code, unknown := do(http.MethodGet, "/v1/admin/health/no-such-component", "platform", "platform-admin", nil)
		if code != http.StatusNotFound {
			t.Fatalf("GET /v1/admin/health/{unknown}: status %d, want 404 (%v)", code, unknown)
		}
		errObj, _ := unknown["error"].(map[string]any)
		if errObj == nil || errObj["code"] != "UNKNOWN_HEALTH_COMPONENT" {
			t.Errorf("unknown component error code = %v, want UNKNOWN_HEALTH_COMPONENT", unknown["error"])
		}
	})

	// ---- Version and Config Introspection ----
	t.Run("version_and_config", func(t *testing.T) {
		code, ver := do(http.MethodGet, "/v1/admin/platform/version", "platform", "platform-admin", nil)
		if code != http.StatusOK {
			t.Fatalf("GET /v1/admin/platform/version: status %d (%v)", code, ver)
		}
		// §25.3: the version response is the gateway's own compiled-in
		// metadata — gatewayVersion, gitCommit, buildDate, goVersion.
		for _, k := range []string{"gatewayVersion", "gitCommit", "buildDate"} {
			if s, _ := ver[k].(string); s == "" {
				t.Errorf("platform/version %s is empty: %v", k, ver)
			}
		}
		if gv, _ := ver["goVersion"].(string); !strings.HasPrefix(gv, "go") {
			t.Errorf("platform/version goVersion = %q, want the runtime go version (go...)", gv)
		}

		code, cfg := do(http.MethodGet, "/v1/admin/platform/config", "platform", "platform-admin", nil)
		if code != http.StatusOK {
			t.Fatalf("GET /v1/admin/platform/config: status %d (%v)", code, cfg)
		}
		// §25.3: the effective merged running configuration. The
		// backend-wiring entries reflect the live posture booted here
		// (both Postgres and Redis configured), proving the endpoint
		// serves the effective config rather than a static default. No
		// value carries a raw DSN or password: the config surfaces only
		// booleans for the backends, never the connection secrets.
		entries, _ := cfg["config"].([]any)
		if len(entries) == 0 {
			t.Fatalf("platform/config carried no entries: %v", cfg)
		}
		effective := map[string]string{}
		for _, e := range entries {
			entry, _ := e.(map[string]any)
			k, _ := entry["key"].(string)
			v, _ := entry["value"].(string)
			effective[k] = v
			if strings.Contains(v, pg.DSN) {
				t.Errorf("platform/config entry %q leaks the raw Postgres DSN: %q", k, v)
			}
		}
		if effective["gateway.postgres"] != "true" {
			t.Errorf("platform/config gateway.postgres = %q, want true (Postgres is wired)", effective["gateway.postgres"])
		}
		if effective["gateway.redis"] != "true" {
			t.Errorf("platform/config gateway.redis = %q, want true (Redis is wired)", effective["gateway.redis"])
		}
	})

	// ---- Capacity Recommendations ----
	// §25.3: immediately after boot every ring buffer is empty, so no
	// per-category recommendation is generated and the response's
	// degradation envelope reports "degraded" with the compiled-in
	// threshold source. This is the data-starved signal an agent uses to
	// distinguish a starved window from a healthy platform with no issues.
	t.Run("recommendations", func(t *testing.T) {
		code, resp := do(http.MethodGet, "/v1/admin/recommendations", "platform", "platform-admin", nil)
		if code != http.StatusOK {
			t.Fatalf("GET /v1/admin/recommendations: status %d (%v)", code, resp)
		}
		recs, _ := resp["recommendations"].([]any)
		if len(recs) != 0 {
			t.Errorf("post-restart recommendations = %v, want empty (ring buffers start empty)", recs)
		}
		deg, _ := resp["degradation"].(map[string]any)
		if deg == nil {
			t.Fatalf("recommendations response carried no degradation envelope: %v", resp)
		}
		if lvl, _ := deg["level"].(string); lvl != "degraded" {
			t.Errorf("degradation level = %q, want degraded (no rule has data yet)", lvl)
		}
		if src, _ := deg["thresholdSource"].(string); src != "compiled-in-defaults" {
			t.Errorf("degradation thresholdSource = %q, want compiled-in-defaults", src)
		}
		if warns, _ := deg["warnings"].([]any); len(warns) == 0 {
			t.Errorf("data-starved degradation carried no warning: %v", deg)
		}

		// §25.3: an unrecognized ?category= is a client error.
		code, bad := do(http.MethodGet, "/v1/admin/recommendations?category=bogus", "platform", "platform-admin", nil)
		if code != http.StatusBadRequest {
			t.Fatalf("recommendations?category=bogus: status %d, want 400 (%v)", code, bad)
		}
		errObj, _ := bad["error"].(map[string]any)
		if errObj == nil || errObj["code"] != "UNKNOWN_RECOMMENDATION_CATEGORY" {
			t.Errorf("unknown category error code = %v, want UNKNOWN_RECOMMENDATION_CATEGORY", bad["error"])
		}

		// A recognized category narrows the result; it is accepted (200)
		// and, with empty windows, returns no entries.
		code, filtered := do(http.MethodGet, "/v1/admin/recommendations?category=warm_pool_sizing", "platform", "platform-admin", nil)
		if code != http.StatusOK {
			t.Fatalf("recommendations?category=warm_pool_sizing: status %d, want 200 (%v)", code, filtered)
		}
		if recs, _ := filtered["recommendations"].([]any); len(recs) != 0 {
			t.Errorf("filtered recommendations = %v, want empty (windows are starved)", recs)
		}
	})

	// ---- Gateway Event Buffer ----
	// §25.3: emitting a real operational event (opening a circuit breaker
	// records a circuit_breaker_opened event) lands it in the in-memory
	// buffer with a monotonic uint64 id. Polling the buffer surfaces the
	// event and a cursor; re-polling with that cursor returns no matching
	// events, proving the monotonic cursor round-trip an ops poller relies
	// on to iterate forward without replay.
	t.Run("event_buffer_cursor", func(t *testing.T) {
		const breaker = "rt-echo-buffer-e2e"
		code, opened := do(http.MethodPost, "/v1/admin/circuit-breakers/"+breaker+"/open", "platform", "platform-admin",
			map[string]any{
				"reason":     "buffer round-trip (e2e)",
				"limit_tier": "runtime",
				"scope":      map[string]any{"runtime": "echo"},
			})
		if code != http.StatusOK {
			t.Fatalf("POST /v1/admin/circuit-breakers/%s/open: status %d (%v)", breaker, code, opened)
		}

		// Read the buffer, narrowed to the emitted event type.
		code, page := do(http.MethodGet, "/v1/admin/events/buffer?eventType=circuit_breaker_opened", "platform", "platform-admin", nil)
		if code != http.StatusOK {
			t.Fatalf("GET /v1/admin/events/buffer: status %d (%v)", code, page)
		}
		evs, _ := page["events"].([]any)
		if len(evs) == 0 {
			t.Fatalf("event buffer returned no circuit_breaker_opened event after the breaker opened: %v", page)
		}
		last, _ := evs[len(evs)-1].(map[string]any)
		inner, _ := last["event"].(map[string]any)
		if typ, _ := inner["type"].(string); !strings.HasSuffix(typ, ".circuit_breaker_opened") {
			t.Errorf("buffered event type = %q, want a dev.lenny.circuit_breaker_opened record", typ)
		}
		pag, _ := page["pagination"].(map[string]any)
		if pag == nil {
			t.Fatalf("event buffer page carried no pagination envelope: %v", page)
		}
		if gap, _ := pag["gapDetected"].(bool); gap {
			t.Errorf("fresh buffer poll reported gapDetected; the cursor was never evicted: %v", pag)
		}
		cursor, _ := pag["cursor"].(float64)
		if cursor < 1 {
			t.Fatalf("buffer cursor = %v, want a monotonic id >= 1", pag["cursor"])
		}

		// §25.3: polling forward from the returned cursor yields no further
		// circuit_breaker_opened events — the cursor advanced past the last
		// one and the poll does not replay it. The cursor is stable at the
		// requested position when no new matching event has arrived.
		sinceParam := int64(cursor)
		code, next := do(http.MethodGet,
			"/v1/admin/events/buffer?eventType=circuit_breaker_opened&since="+strconv.FormatInt(sinceParam, 10), "platform", "platform-admin", nil)
		if code != http.StatusOK {
			t.Fatalf("GET /v1/admin/events/buffer?since=%d: status %d (%v)", sinceParam, code, next)
		}
		if evs, _ := next["events"].([]any); len(evs) != 0 {
			t.Errorf("re-poll from cursor %d returned %d events, want 0 (no replay past the cursor)", sinceParam, len(evs))
		}
		nextPag, _ := next["pagination"].(map[string]any)
		if gap, _ := nextPag["gapDetected"].(bool); gap {
			t.Errorf("re-poll from a live cursor reported gapDetected: %v", nextPag)
		}
		if c, _ := nextPag["cursor"].(float64); int64(c) != sinceParam {
			t.Errorf("re-poll cursor = %v, want it to hold at the requested %d when no new event arrived", nextPag["cursor"], sinceParam)
		}
	})
}
