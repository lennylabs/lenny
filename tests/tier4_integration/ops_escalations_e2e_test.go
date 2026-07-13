//go:build integration

// SPDX-License-Identifier: MIT

// Tier-4 integration test: the real cmd/lenny-ops binary running against
// a Postgres and a Redis container. It walks the §25.4 escalation surface
// end to end — creating escalations, listing them through the §25.2
// canonical pagination envelope, filtering by status and severity, and
// updating an escalation's lifecycle status — and proves the composition
// root threads the live Postgres Tier-1 store through the store-backed
// escalation handlers rather than the in-memory fake the tier3 contract
// suite pins against. Each assertion is cross-checked against the
// ops_escalations table so a passing test means the record actually
// round-tripped through Postgres.
package tier4_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/opsprocess"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// TestOpsServiceEscalationStoreBackedE2E boots cmd/lenny-ops against real
// Postgres and Redis and walks the §25.4 escalation surface, asserting that
// escalations created through POST /v1/admin/escalations land durably in the
// Tier-1 Postgres store, that GET /v1/admin/escalations returns the §25.2
// canonical pagination envelope populated from that store, that the ?status=
// and ?severity= filters and ?limit= cap resolve against the live store, and
// that a PUT status update round-trips through Postgres and is reflected on a
// subsequent filtered list.
//
// spec: §25.2 Pagination — "All list endpoints use the following canonical
// parameters and response fields ... Response envelope for paginated
// responses: { "items": [ ... ], "pagination": { ... "limit": 100 ... } }";
// §25.4 Escalation Storage Tiers (Create Path) — "Tier 1 | Postgres | Insert
// into ops_escalations table. Full durability. | 201 Created |
// "durable-postgres"" and its "X-Lenny-Persistence response header matching
// the persistence field"; §25.4 Storage Tiers (Query Path) — "Postgres
// available: full query with pagination, filtering, and status updates."
// diagnosis: a failure means the cmd/lenny-ops composition root did not
// thread the live Postgres Tier-1 store through the §25.4 escalation
// endpoints. Either POST did not persist a durable-postgres record to
// ops_escalations, the list did not return the §25.2 items/pagination
// envelope from the store, the status/severity filters or the limit cap were
// not honored against the real store, or a PUT status update did not
// round-trip through Postgres — any of which shows the escalation endpoints
// diverged from §25.2/§25.4 when driven against a real store rather than the
// tier3 in-memory fake.
func TestOpsServiceEscalationStoreBackedE2E(t *testing.T) {
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

	// do issues an admin request under the platform-admin dev headers the
	// unauthenticated ops surface honours and returns the status, the
	// response headers, and the decoded top-level JSON object.
	do := func(method, path string, body any) (int, http.Header, map[string]any) {
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

	// escalationRow reads the persisted status of a single escalation
	// straight from the Tier-1 store, so the assertions confirm the record
	// round-tripped through Postgres rather than an in-memory buffer.
	escalationRow := func(id string) (string, bool) {
		t.Helper()
		var status string
		err := pg.Pool.QueryRow(ctx,
			`SELECT status FROM ops_escalations WHERE id = $1`, id).Scan(&status)
		if err != nil {
			return "", false
		}
		return status, true
	}

	// ---- create: three escalations land durably in Postgres ----
	// §25.4 Tier 1: with Postgres reachable, each create is a 201 with
	// persistence "durable-postgres" and the matching X-Lenny-Persistence
	// header.
	type seed struct {
		severity string
		summary  string
	}
	seeds := []seed{
		{"critical", "warm pool exhausted; scale-up failed"},
		{"warning", "connector token nearing expiry"},
		{"info", "routine capacity review requested"},
	}
	ids := make([]string, 0, len(seeds))
	for _, s := range seeds {
		code, hdr, esc := do(http.MethodPost, "/v1/admin/escalations", map[string]any{
			"severity": s.severity,
			"summary":  s.summary,
		})
		if code != http.StatusCreated {
			t.Fatalf("POST /v1/admin/escalations (%s): status %d, want 201 (%v)", s.severity, code, esc)
		}
		id, _ := esc["id"].(string)
		if id == "" {
			t.Fatalf("created escalation (%s) has no id: %v", s.severity, esc)
		}
		if got, _ := esc["persistence"].(string); got != "durable-postgres" {
			t.Errorf("escalation (%s) persistence = %q, want durable-postgres (Tier 1 with Postgres reachable)", s.severity, got)
		}
		if got := hdr.Get("X-Lenny-Persistence"); got != "durable-postgres" {
			t.Errorf("escalation (%s) X-Lenny-Persistence header = %q, want durable-postgres", s.severity, got)
		}
		if got, _ := esc["status"].(string); got != "open" {
			t.Errorf("newly created escalation (%s) status = %q, want open", s.severity, got)
		}
		if status, ok := escalationRow(id); !ok {
			t.Errorf("escalation %s (%s) not found in ops_escalations (create did not persist to Postgres)", id, s.severity)
		} else if status != "open" {
			t.Errorf("persisted escalation %s status = %q, want open", id, status)
		}
		ids = append(ids, id)
	}

	// ---- list: GET returns the §25.2 canonical pagination envelope ----
	// The list is served from the live Postgres store: every created id
	// appears, the items carry the durable-postgres persistence attribute,
	// and the pagination envelope echoes the default limit.
	code, _, list := do(http.MethodGet, "/v1/admin/escalations", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /v1/admin/escalations: status %d (%v)", code, list)
	}
	items, ok := list["items"].([]any)
	if !ok {
		t.Fatalf("list response has no items array: %v", list)
	}
	seen := map[string]map[string]any{}
	for _, it := range items {
		rec, _ := it.(map[string]any)
		if rec == nil {
			continue
		}
		if id, _ := rec["id"].(string); id != "" {
			seen[id] = rec
		}
	}
	for _, id := range ids {
		rec, present := seen[id]
		if !present {
			t.Errorf("escalation %s absent from the store-backed list: %v", id, list["items"])
			continue
		}
		if got, _ := rec["persistence"].(string); got != "durable-postgres" {
			t.Errorf("listed escalation %s persistence = %q, want durable-postgres", id, got)
		}
	}
	pag, ok := list["pagination"].(map[string]any)
	if !ok {
		t.Fatalf("list response carried no §25.2 pagination envelope: %v", list)
	}
	// §25.2: the pagination envelope echoes the effective page limit (the
	// default 100 when no ?limit= is supplied).
	if got, _ := pag["limit"].(float64); got != 100 {
		t.Errorf("pagination.limit = %v, want 100 (the default page size)", pag["limit"])
	}

	// ---- filter by severity: ?severity=critical narrows to the one ----
	// §25.4 Query Path: the Postgres store applies the filter. Only the
	// critical escalation matches, proving the filter runs against the
	// live store rather than being ignored.
	code, _, crit := do(http.MethodGet, "/v1/admin/escalations?severity=critical", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /v1/admin/escalations?severity=critical: status %d (%v)", code, crit)
	}
	critItems, _ := crit["items"].([]any)
	if len(critItems) != 1 {
		t.Fatalf("?severity=critical returned %d items, want exactly 1 (%v)", len(critItems), crit["items"])
	}
	if rec, _ := critItems[0].(map[string]any); rec != nil {
		if got, _ := rec["severity"].(string); got != "critical" {
			t.Errorf("?severity=critical returned a %q escalation", got)
		}
		if got, _ := rec["id"].(string); got != ids[0] {
			t.Errorf("?severity=critical returned id %q, want the critical escalation %q", got, ids[0])
		}
	}

	// ---- limit cap: ?limit=1 caps the page at one item ----
	// §25.2 / §25.4: the store honours the canonical limit parameter, so a
	// list of three escalations narrowed to limit=1 returns a single item
	// and the envelope echoes the requested limit.
	code, _, capped := do(http.MethodGet, "/v1/admin/escalations?limit=1", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /v1/admin/escalations?limit=1: status %d (%v)", code, capped)
	}
	cappedItems, _ := capped["items"].([]any)
	if len(cappedItems) != 1 {
		t.Errorf("?limit=1 returned %d items, want exactly 1 (store did not honour the limit cap): %v", len(cappedItems), capped["items"])
	}
	cappedPag, _ := capped["pagination"].(map[string]any)
	if cappedPag == nil {
		t.Fatalf("?limit=1 response carried no pagination envelope: %v", capped)
	}
	if got, _ := cappedPag["limit"].(float64); got != 1 {
		t.Errorf("?limit=1 pagination.limit = %v, want 1 (the requested page size)", cappedPag["limit"])
	}

	// ---- status update round-trip: resolve one, then filter it out ----
	// §25.4 Query Path: "status updates" are served from Postgres. PUT the
	// warning escalation to resolved, confirm the row status changed in the
	// store, then confirm ?status=open no longer returns it while the other
	// two open escalations remain.
	resolvedID := ids[1]
	code, _, updated := do(http.MethodPut, "/v1/admin/escalations/"+resolvedID, map[string]any{
		"status": "resolved",
	})
	if code != http.StatusOK {
		t.Fatalf("PUT /v1/admin/escalations/%s: status %d (%v)", resolvedID, code, updated)
	}
	if got, _ := updated["status"].(string); got != "resolved" {
		t.Errorf("PUT status update returned status = %q, want resolved", got)
	}
	if status, ok := escalationRow(resolvedID); !ok {
		t.Errorf("escalation %s vanished from ops_escalations after update", resolvedID)
	} else if status != "resolved" {
		t.Errorf("persisted escalation %s status = %q after PUT, want resolved (update did not round-trip through Postgres)", resolvedID, status)
	}

	code, _, open := do(http.MethodGet, "/v1/admin/escalations?status=open", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /v1/admin/escalations?status=open: status %d (%v)", code, open)
	}
	openItems, _ := open["items"].([]any)
	openIDs := map[string]bool{}
	for _, it := range openItems {
		rec, _ := it.(map[string]any)
		if rec == nil {
			continue
		}
		if got, _ := rec["status"].(string); got != "open" {
			t.Errorf("?status=open returned a %q escalation: %v", got, rec)
		}
		if id, _ := rec["id"].(string); id != "" {
			openIDs[id] = true
		}
	}
	if openIDs[resolvedID] {
		t.Errorf("?status=open still returned the resolved escalation %s (store-backed status filter did not exclude it)", resolvedID)
	}
	if !openIDs[ids[0]] || !openIDs[ids[2]] {
		t.Errorf("?status=open dropped a still-open escalation: got %v, want both %s and %s", openIDs, ids[0], ids[2])
	}
}
