//go:build component

// SPDX-License-Identifier: MIT

// Component test for the §25.9 Audit Log Query API driven through the
// production admin router (pkg/gateway/externalapi/admin) wired to the
// Postgres-backed auditstore.Store over a real Postgres container. The
// in-memory audit.ChainSet-backed tests in pkg/gateway/externalapi/admin
// cover the wire contract; this test pins the query filters, cursor
// pagination, default time window, chainIntegrityReport tally, and the
// summary aggregation against actual Postgres rows read through the
// §12.3 per-tenant RLS path.
package auditstore_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/audit/auditstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/storage/eventbus"
)

// adminQueryClock is the fixed gateway clock for the §25.9 query router.
// The §25.9 default look-back window is anchored on it, so the seeded
// rows are placed relative to it.
var adminQueryClock = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

// newAdminAuditRouter builds the production admin router over the
// Postgres-backed auditstore.Store so the §25.9 query endpoints read the
// durable trail rather than an in-memory chain.
func newAdminAuditRouter(store *auditstore.Store) *admin.Router {
	return admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return adminQueryClock },
	}).WithAuditLog(store)
}

// asPlatformAdmin attaches a §10.2 platform-admin principal so the §25.9
// audit-query role gate admits the request and a ?tenantId= selects the
// tenant chain to read.
func asPlatformAdmin(req *http.Request) *http.Request {
	ctx := authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject:  "admin@acme.com",
		TenantID: "platform",
		Roles:    []pkgauth.Role{pkgauth.RolePlatformAdmin},
	})
	return req.WithContext(ctx)
}

// appendAuditRow seals one row into the tenant's Postgres chain with a
// tailored payload and timestamp so the query-filter and time-window
// assertions have concrete rows to match.
func appendAuditRow(t *testing.T, ctx context.Context, store *auditstore.Store, tenant, eventType, actor, target string, at time.Time) {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{
		"actor_subject":   actor,
		"target_resource": target,
	})
	if _, err := store.Append(ctx, tenant, eventType, json.RawMessage(payload), at); err != nil {
		t.Fatalf("append %s: %v", eventType, err)
	}
}

// getAuditEnvelope drives one GET against the admin router and decodes
// the §25.9 audit-event envelope, failing on a non-200 status.
func getAuditEnvelope(t *testing.T, router *admin.Router, url string) admin.AuditEventEnvelope {
	t.Helper()
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, asPlatformAdmin(httptest.NewRequest(http.MethodGet, url, nil)))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET %s: status %d, body=%s", url, rr.Code, rr.Body.String())
	}
	var env admin.AuditEventEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope for %s: %v", url, err)
	}
	return env
}

// spec: §25.9 lines 3653-3710 — "The response envelope carries
// `ocsfVersion` (\"1.1.0\") and `chainIntegrityReport` ({verified,
// broken, gap_suspected, rechained_post_outage, redacted_gdpr}) as
// top-level fields outside `items[]`." The list endpoint params are
// "?since=, ?until=, ?eventType=, ?actorId=, ?resourceType=, ...
// ?limit= (default 100, max 1000), ?cursor=" and "Combining filters is
// AND." Queries "without `since` or `until` default to the last 24
// hours (not \"all time\")."
//
// diagnosis: a failure means the §25.9 query endpoints, when wired to
// the Postgres-backed auditstore.Store instead of the in-memory chain,
// mis-apply a filter, mis-page the cursor, ignore the default time
// window, or miscount chainIntegrityReport against the rows Postgres
// actually returned through the §12.3 RLS read path.
func TestAdminAuditQueryOverPostgres(t *testing.T) {
	t.Parallel()
	pg := startPG(t)
	store := auditstore.New(pg.Router(t))
	router := newAdminAuditRouter(store)
	ctx := context.Background()

	seedTenant(t, ctx, pg, "acme")
	// Rows 1-3 fall inside the 24h default window; row 4 falls outside it.
	// Append order fixes the sequence order (seq 1..4).
	appendAuditRow(t, ctx, store, "acme", "admin.tenant.created", "alice@acme.com", "tenant/acme", adminQueryClock.Add(-1*time.Hour))
	appendAuditRow(t, ctx, store, "acme", "credential.leased", "bob@acme.com", "credential/c1", adminQueryClock.Add(-2*time.Hour))
	appendAuditRow(t, ctx, store, "acme", "credential.leased", "alice@acme.com", "credential/c2", adminQueryClock.Add(-3*time.Hour))
	appendAuditRow(t, ctx, store, "acme", "token.exchanged", "alice@acme.com", "token/t1", adminQueryClock.Add(-48*time.Hour))

	// A second tenant with its own row: the §12.3 RLS read path must keep
	// it out of the acme query results.
	seedTenant(t, ctx, pg, "globex")
	appendAuditRow(t, ctx, store, "globex", "credential.leased", "carol@globex.com", "credential/g1", adminQueryClock.Add(-1*time.Hour))

	// A `since` that admits every acme row (all rows fall within the last
	// two days of the clock) while keeping the span under the §25.9 line
	// 3707 90-day cap so an unfiltered query is not rejected as too broad.
	sinceAll := "&since=2026-05-01T00:00:00Z"

	t.Run("default window excludes rows older than 24h and tallies verified", func(t *testing.T) {
		env := getAuditEnvelope(t, router, "/v1/admin/audit-events?tenantId=acme")
		if env.TenantID != "acme" {
			t.Errorf("tenantId = %q, want acme", env.TenantID)
		}
		if env.OCSFVersion == "" || env.TranslatorVersion == "" {
			t.Errorf("envelope missing OCSF version fields: ocsf=%q translator=%q", env.OCSFVersion, env.TranslatorVersion)
		}
		// Rows 1-3 are inside the last 24h; row 4 (-48h) is excluded.
		if len(env.Items) != 3 {
			t.Fatalf("default-window items = %d, want 3 (the -48h row is outside the window)", len(env.Items))
		}
		if env.ChainIntegrityReport == nil {
			t.Fatal("chainIntegrityReport missing from envelope")
		}
		if env.ChainIntegrityReport.Verified != 3 || env.ChainIntegrityReport.Broken != 0 {
			t.Errorf("chainIntegrityReport = %+v, want 3 verified / 0 broken over the Postgres chain", *env.ChainIntegrityReport)
		}
		// Each item is an OCSF record carrying the lenny_chain extension.
		var rec map[string]any
		if err := json.Unmarshal(env.Items[0], &rec); err != nil {
			t.Fatalf("items[0] is not JSON: %v", err)
		}
		unmapped, _ := rec["unmapped"].(map[string]any)
		if chain, _ := unmapped["lenny_chain"].(map[string]any); chain == nil {
			t.Errorf("items[0].unmapped.lenny_chain missing")
		}
	})

	t.Run("far-past since admits every row (time window is the only exclusion)", func(t *testing.T) {
		env := getAuditEnvelope(t, router, "/v1/admin/audit-events?tenantId=acme"+sinceAll)
		if len(env.Items) != 4 {
			t.Fatalf("full-window items = %d, want 4", len(env.Items))
		}
		if env.ChainIntegrityReport.Verified != 4 {
			t.Errorf("chainIntegrityReport.verified = %d, want 4", env.ChainIntegrityReport.Verified)
		}
	})

	t.Run("eventType filter", func(t *testing.T) {
		env := getAuditEnvelope(t, router, "/v1/admin/audit-events?tenantId=acme"+sinceAll+"&eventType=credential.leased")
		if len(env.Items) != 2 {
			t.Fatalf("eventType=credential.leased items = %d, want 2", len(env.Items))
		}
	})

	t.Run("actorId AND resourceType filter", func(t *testing.T) {
		// alice touched a tenant (row 1), a credential (row 3), and a token
		// (row 4). AND with resourceType=credential leaves only row 3.
		env := getAuditEnvelope(t, router, "/v1/admin/audit-events?tenantId=acme"+sinceAll+"&actorId=alice@acme.com&resourceType=credential")
		if len(env.Items) != 1 {
			t.Fatalf("actorId+resourceType AND items = %d, want 1 (only the credential row alice touched)", len(env.Items))
		}
	})

	t.Run("cursor pagination walks every row exactly once", func(t *testing.T) {
		seen := map[string]bool{}
		url := "/v1/admin/audit-events?tenantId=acme" + sinceAll + "&limit=2"
		pages := 0
		for {
			env := getAuditEnvelope(t, router, url)
			pages++
			if pages > 10 {
				t.Fatal("pagination did not terminate")
			}
			if len(env.Items) > 2 {
				t.Fatalf("page %d returned %d items, want <= limit 2", pages, len(env.Items))
			}
			for _, it := range env.Items {
				var rec map[string]any
				_ = json.Unmarshal(it, &rec)
				meta, _ := rec["metadata"].(map[string]any)
				uid, _ := meta["uid"].(string)
				if uid == "" {
					t.Fatalf("page %d item missing metadata.uid", pages)
				}
				if seen[uid] {
					t.Errorf("uid %q returned on more than one page", uid)
				}
				seen[uid] = true
			}
			if env.NextCursor == "" {
				break
			}
			url = "/v1/admin/audit-events?tenantId=acme" + sinceAll + "&limit=2&cursor=" + env.NextCursor
		}
		if len(seen) != 4 {
			t.Fatalf("pagination surfaced %d distinct rows across %d pages, want 4", len(seen), pages)
		}
		if pages < 2 {
			t.Fatalf("expected the 4-row result to span more than one page at limit 2, got %d page(s)", pages)
		}
	})

	t.Run("RLS keeps another tenant's rows out of the acme query", func(t *testing.T) {
		env := getAuditEnvelope(t, router, "/v1/admin/audit-events?tenantId=acme"+sinceAll)
		// The globex row (carol) must never appear in acme's result. Every
		// acme actor is alice or bob.
		for i, it := range env.Items {
			var rec map[string]any
			_ = json.Unmarshal(it, &rec)
			actor, _ := rec["actor"].(map[string]any)
			if actor != nil {
				user, _ := actor["user"].(map[string]any)
				if uid, _ := user["uid"].(string); uid == "carol@globex.com" {
					t.Errorf("items[%d] leaked the globex actor across the tenant boundary", i)
				}
			}
		}
		globex := getAuditEnvelope(t, router, "/v1/admin/audit-events?tenantId=globex"+sinceAll)
		if len(globex.Items) != 1 {
			t.Fatalf("globex query items = %d, want 1 (its own row only)", len(globex.Items))
		}
	})

	t.Run("single-event get returns the OCSF envelope; missing returns 404", func(t *testing.T) {
		env := getAuditEnvelope(t, router, "/v1/admin/audit-events/2?tenantId=acme")
		if len(env.Items) != 1 {
			t.Fatalf("get seq 2 items = %d, want 1", len(env.Items))
		}
		if env.OCSFVersion == "" {
			t.Errorf("get seq 2 envelope missing ocsfVersion")
		}
		rr := httptest.NewRecorder()
		router.Handler().ServeHTTP(rr, asPlatformAdmin(
			httptest.NewRequest(http.MethodGet, "/v1/admin/audit-events/99?tenantId=acme", nil),
		))
		if rr.Code != http.StatusNotFound {
			t.Errorf("get missing seq: status %d, want 404", rr.Code)
		}
	})
}

// spec: §25.9 line 3659 — the list endpoint accepts
// "?eventbus_publish_state= (one of pending | retry_pending | published
// | failed ...)" and "Combining filters is AND." §25.9 line 3663 —
// "Operators reconciling after an EventBus outage typically query
// ?eventbus_publish_state=failed&since=<outage_start>." The publish
// state lives on the §12.3.7 audit_log.eventbus_publish_state column,
// which is not part of the §11.7 hash input, and is set on real rows via
// the Postgres-backed store's SetPublishState.
//
// diagnosis: a failure means the §25.9 ?eventbus_publish_state=failed
// filter, resolved against the real Postgres audit_log column rather than
// a fake, returns rows whose column is not 'failed' (or drops rows whose
// column is 'failed'), so an operator reconciling after an EventBus
// outage sees the wrong set of un-published events.
func TestAdminAuditQueryEventbusPublishStateFilterOverPostgres(t *testing.T) {
	t.Parallel()
	pg := startPG(t)
	store := auditstore.New(pg.Router(t))
	router := newAdminAuditRouter(store)
	ctx := context.Background()

	seedTenant(t, ctx, pg, "reconcile")

	// Six rows across the eventbus_publish_state enum. The two 'failed'
	// rows are the ones an operator reconciling after an outage must see;
	// every other state must be excluded from the ?eventbus_publish_state=
	// failed page. Newly appended rows default to 'pending'; SetPublishState
	// transitions the column to the target state under the per-tenant lock.
	type seeded struct {
		row   audit.Row
		state eventbus.PublishState
	}
	appendState := func(eventType, actor string, at time.Time, state eventbus.PublishState) seeded {
		t.Helper()
		payload, _ := json.Marshal(map[string]string{
			"actor_subject":   actor,
			"target_resource": "credential/" + actor,
		})
		row, err := store.Append(ctx, "reconcile", eventType, json.RawMessage(payload), at)
		if err != nil {
			t.Fatalf("append %s: %v", eventType, err)
		}
		if err := store.SetPublishState(ctx, "reconcile", row.Seq, state, 0); err != nil {
			t.Fatalf("set publish state %s on seq %d: %v", state, row.Seq, err)
		}
		return seeded{row: row, state: state}
	}

	all := []seeded{
		appendState("credential.leased", "f1@acme.com", adminQueryClock.Add(-1*time.Hour), eventbus.PublishFailed),
		appendState("credential.leased", "p1@acme.com", adminQueryClock.Add(-2*time.Hour), eventbus.PublishPublished),
		appendState("credential.leased", "f2@acme.com", adminQueryClock.Add(-3*time.Hour), eventbus.PublishFailed),
		appendState("credential.leased", "pend@acme.com", adminQueryClock.Add(-4*time.Hour), eventbus.PublishPending),
		appendState("credential.leased", "retry@acme.com", adminQueryClock.Add(-5*time.Hour), eventbus.PublishRetryPending),
		appendState("credential.leased", "p2@acme.com", adminQueryClock.Add(-6*time.Hour), eventbus.PublishPublished),
	}

	// The exact set of row UUIDs whose column is 'failed'. metadata.uid on
	// each OCSF record is the row UUID, so the returned page is compared to
	// this set for identity, not merely count.
	wantFailed := map[string]bool{}
	for _, s := range all {
		if s.state == eventbus.PublishFailed {
			wantFailed[s.row.ID] = true
		}
	}
	if len(wantFailed) != 2 {
		t.Fatalf("test setup: expected 2 failed rows, got %d", len(wantFailed))
	}

	// spec: §25.9 line 3663 — the reconciliation query form. `since` opens
	// the window wide enough to admit every seeded row so the only
	// exclusion under test is the eventbus_publish_state=failed predicate.
	env := getAuditEnvelope(t, router,
		"/v1/admin/audit-events?tenantId=reconcile&since=2026-05-01T00:00:00Z&eventbus_publish_state=failed")

	if len(env.Items) != 2 {
		t.Fatalf("?eventbus_publish_state=failed items = %d, want 2 (only the failed rows)", len(env.Items))
	}
	gotFailed := map[string]bool{}
	for i, it := range env.Items {
		var rec map[string]any
		if err := json.Unmarshal(it, &rec); err != nil {
			t.Fatalf("items[%d] not JSON: %v", i, err)
		}
		meta, _ := rec["metadata"].(map[string]any)
		uid, _ := meta["uid"].(string)
		if !wantFailed[uid] {
			t.Errorf("items[%d] uid %q is not one of the two failed rows", i, uid)
		}
		gotFailed[uid] = true
	}
	for uid := range wantFailed {
		if !gotFailed[uid] {
			t.Errorf("failed row %q missing from the ?eventbus_publish_state=failed page", uid)
		}
	}

	// A control query with no publish-state filter admits every row, so the
	// filter above is genuinely excluding the four non-failed rows rather
	// than the window doing it.
	unfiltered := getAuditEnvelope(t, router,
		"/v1/admin/audit-events?tenantId=reconcile&since=2026-05-01T00:00:00Z")
	if len(unfiltered.Items) != len(all) {
		t.Fatalf("unfiltered items = %d, want %d (every seeded row)", len(unfiltered.Items), len(all))
	}

	// AND semantics: ?eventbus_publish_state=published narrows to exactly
	// the two published rows, confirming the filter keys off the column
	// value rather than defaulting to failed.
	published := getAuditEnvelope(t, router,
		"/v1/admin/audit-events?tenantId=reconcile&since=2026-05-01T00:00:00Z&eventbus_publish_state=published")
	if len(published.Items) != 2 {
		t.Fatalf("?eventbus_publish_state=published items = %d, want 2", len(published.Items))
	}
}

// spec: §25.9 line 3661 — "GET /v1/admin/audit-events/summary Aggregate
// counts by type/actor/resource over a time window. Params: ?since=,
// ?until=, ?groupBy=eventType|actorId|resourceType".
//
// diagnosis: a failure means the §25.9 summary endpoint miscounts the
// per-group tallies or the total when aggregating over the rows Postgres
// returned, so an operator investigating an incident reads the wrong
// event distribution.
func TestAdminAuditSummaryOverPostgres(t *testing.T) {
	t.Parallel()
	pg := startPG(t)
	store := auditstore.New(pg.Router(t))
	router := newAdminAuditRouter(store)
	ctx := context.Background()

	seedTenant(t, ctx, pg, "summ")
	appendAuditRow(t, ctx, store, "summ", "admin.tenant.created", "alice@acme.com", "tenant/acme", adminQueryClock.Add(-1*time.Hour))
	appendAuditRow(t, ctx, store, "summ", "credential.leased", "bob@acme.com", "credential/c1", adminQueryClock.Add(-2*time.Hour))
	appendAuditRow(t, ctx, store, "summ", "credential.leased", "alice@acme.com", "credential/c2", adminQueryClock.Add(-3*time.Hour))
	appendAuditRow(t, ctx, store, "summ", "token.exchanged", "alice@acme.com", "token/t1", adminQueryClock.Add(-4*time.Hour))

	getSummary := func(t *testing.T, groupBy string) admin.AuditSummaryResponse {
		t.Helper()
		url := "/v1/admin/audit-events/summary?tenantId=summ&since=2026-01-01T00:00:00Z&groupBy=" + groupBy
		rr := httptest.NewRecorder()
		router.Handler().ServeHTTP(rr, asPlatformAdmin(httptest.NewRequest(http.MethodGet, url, nil)))
		if rr.Code != http.StatusOK {
			t.Fatalf("summary groupBy=%s: status %d, body=%s", groupBy, rr.Code, rr.Body.String())
		}
		var resp admin.AuditSummaryResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode summary: %v", err)
		}
		return resp
	}

	t.Run("groupBy eventType", func(t *testing.T) {
		resp := getSummary(t, "eventType")
		if resp.Total != 4 {
			t.Errorf("total = %d, want 4", resp.Total)
		}
		counts := map[string]int{}
		for _, g := range resp.Groups {
			counts[g.Key] = g.Count
		}
		if counts["credential.leased"] != 2 || counts["admin.tenant.created"] != 1 || counts["token.exchanged"] != 1 {
			t.Errorf("eventType groups = %+v, want credential.leased:2, admin.tenant.created:1, token.exchanged:1", counts)
		}
		// Groups are returned in descending count order.
		if len(resp.Groups) > 0 && resp.Groups[0].Key != "credential.leased" {
			t.Errorf("groups[0] = %q (count %d), want the highest-count credential.leased", resp.Groups[0].Key, resp.Groups[0].Count)
		}
	})

	t.Run("groupBy actorId", func(t *testing.T) {
		resp := getSummary(t, "actorId")
		counts := map[string]int{}
		for _, g := range resp.Groups {
			counts[g.Key] = g.Count
		}
		if counts["alice@acme.com"] != 3 || counts["bob@acme.com"] != 1 {
			t.Errorf("actorId groups = %+v, want alice:3, bob:1", counts)
		}
	})
}
