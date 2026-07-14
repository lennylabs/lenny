// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
)

// fakeScatterReader is an in-test §25.9 cross-tenant scatter-gather
// reader. It returns canned rows / missing-shard list / error and counts
// invocations so a test can assert the cache short-circuit.
type fakeScatterReader struct {
	rows    []audit.Row
	missing []string
	err     error
	calls   int
}

func (f *fakeScatterReader) ScatterGatherRows(_ context.Context) ([]audit.Row, []string, error) {
	f.calls++
	return f.rows, f.missing, f.err
}

// twoTenantRows builds a valid pair of per-tenant §11.7 chains (acme has
// two rows, globex one), already ordered by (tenant_id, sequence_number).
func twoTenantRows() []audit.Row {
	cs := audit.NewChainSet()
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var rows []audit.Row
	rows = append(rows, cs.Append("acme", "session.created", json.RawMessage(`{"actor_id":"alice"}`), ts))
	rows = append(rows, cs.Append("acme", "session.completed", json.RawMessage(`{"actor_id":"alice"}`), ts))
	rows = append(rows, cs.Append("globex", "session.created", json.RawMessage(`{"actor_id":"bob"}`), ts))
	return rows
}

func crossTenantRouter(t *testing.T, reader *fakeScatterReader, cacheEnabled bool) *admin.Router {
	t.Helper()
	router, _ := newAuditQueryRouter(t)
	cache := admin.NewMemScatterGatherCache(func() time.Time {
		return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	})
	return router.WithAuditScatter(reader).WithScatterGatherCache(cache, cacheEnabled)
}

// capturingAuditSink records every admin audit event the Router emits so a
// test can inspect the §25.9 audit.query_executed receipt payload directly,
// before OCSF translation.
type capturingAuditSink struct {
	events []admin.AuditEvent
}

func (c *capturingAuditSink) EmitAdminEvent(_ context.Context, e admin.AuditEvent) {
	c.events = append(c.events, e)
}

// queryExecutedReceipts returns the recorded audit.query_executed events in
// emission order.
func (c *capturingAuditSink) queryExecutedReceipts() []admin.AuditEvent {
	var out []admin.AuditEvent
	for _, e := range c.events {
		if e.Type == "audit.query_executed" {
			out = append(out, e)
		}
	}
	return out
}

// crossTenantRouterWithSink builds the cross-tenant router with a capturing
// audit sink so the emitted audit.query_executed receipt is inspectable.
func crossTenantRouterWithSink(t *testing.T, reader *fakeScatterReader, cacheEnabled bool, sink admin.AuditSink) *admin.Router {
	t.Helper()
	store := tenantstore.NewMemory()
	chains := audit.NewChainSet()
	router := admin.NewRouter(store, admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Audit: sink,
	}).WithAuditChains(chains)
	cache := admin.NewMemScatterGatherCache(func() time.Time {
		return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	})
	return router.WithAuditScatter(reader).WithScatterGatherCache(cache, cacheEnabled)
}

func getCrossTenant(t *testing.T, router *admin.Router, query string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(
		httptest.NewRequest(http.MethodGet, "/v1/admin/audit-events"+query, nil),
	))
	return rr
}

// TestListAuditEventsCrossTenantMergesShards covers §25.9 line 3668: a
// platform-admin query with no tenantId reads every tenant's chain via
// the scatter-gather reader, returns the merged rows, and verifies each
// per-tenant chain.
func TestListAuditEventsCrossTenantMergesShards_spec_25_9_3668(t *testing.T) {
	reader := &fakeScatterReader{rows: twoTenantRows()}
	router := crossTenantRouter(t, reader, true)

	rr := getCrossTenant(t, router, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if reader.calls != 1 {
		t.Fatalf("scatter reader calls = %d, want 1", reader.calls)
	}
	var env admin.AuditEventEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.TenantID != "" {
		t.Errorf("tenantId = %q, want empty (cross-tenant)", env.TenantID)
	}
	if len(env.Items) != 3 {
		t.Fatalf("items = %d, want 3", len(env.Items))
	}
	// Every per-tenant chain is intact, so the verdict tally is all
	// verified with no broken segment.
	if env.ChainIntegrityReport == nil || env.ChainIntegrityReport.Verified != 3 {
		t.Errorf("chainIntegrityReport = %+v, want 3 verified", env.ChainIntegrityReport)
	}
	if env.ChainIntegrityReport.Broken != 0 {
		t.Errorf("broken = %d, want 0", env.ChainIntegrityReport.Broken)
	}
}

// TestListAuditEventsCrossTenantCachesResults covers §25.9 line 3709: a
// repeated identical cross-tenant query is served from the cache without
// re-reading the shards.
func TestListAuditEventsCrossTenantCachesResults_spec_25_9_3709(t *testing.T) {
	reader := &fakeScatterReader{rows: twoTenantRows()}
	router := crossTenantRouter(t, reader, true)

	first := getCrossTenant(t, router, "")
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d", first.Code)
	}
	second := getCrossTenant(t, router, "")
	if second.Code != http.StatusOK {
		t.Fatalf("second status = %d", second.Code)
	}
	if reader.calls != 1 {
		t.Fatalf("scatter reader calls = %d, want 1 (second served from cache)", reader.calls)
	}
	if first.Body.String() != second.Body.String() {
		t.Errorf("cached body differs from fresh body")
	}
}

// TestCrossTenantQueryAuditReceiptRecordsCacheAndShards covers §25.9 line
// 3754: the audit.query_executed receipt "includes ... cache hit/miss,
// shards touched". A first cross-tenant query runs the scatter fan-out and
// records cacheHit=false; the repeated identical query served from the
// 5-minute result cache records cacheHit=true. Both record the shard
// fan-out width (1 for the v1 single-shard AllAuditShards).
//
// spec: §25.9 line 3754 (audit.query_executed includes cache hit/miss and
// shards touched)
// diagnosis: the audit-of-audit receipt misreports cross-tenant query cost.
// If cacheHit is hardcoded, an operator auditing query cost cannot
// distinguish a cached (cheap) query from a fresh scatter fan-out
// (expensive), and shardsTouched no longer reflects the real read fan-out.
func TestCrossTenantQueryAuditReceiptRecordsCacheAndShards_spec_25_9_3754(t *testing.T) {
	reader := &fakeScatterReader{rows: twoTenantRows()}
	sink := &capturingAuditSink{}
	router := crossTenantRouterWithSink(t, reader, true, sink)

	// First query: cache miss, real scatter fan-out.
	if rr := getCrossTenant(t, router, ""); rr.Code != http.StatusOK {
		t.Fatalf("first status = %d, body=%s", rr.Code, rr.Body.String())
	}
	// Second identical query: served from the 5-minute result cache.
	if rr := getCrossTenant(t, router, ""); rr.Code != http.StatusOK {
		t.Fatalf("second status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if reader.calls != 1 {
		t.Fatalf("scatter reader calls = %d, want 1 (second served from cache)", reader.calls)
	}

	receipts := sink.queryExecutedReceipts()
	if len(receipts) != 2 {
		t.Fatalf("audit.query_executed receipts = %d, want 2", len(receipts))
	}
	if got := receipts[0].Detail["cacheHit"]; got != false {
		t.Errorf("first receipt cacheHit = %v, want false (scatter fan-out ran)", got)
	}
	if got := receipts[1].Detail["cacheHit"]; got != true {
		t.Errorf("second receipt cacheHit = %v, want true (served from cache)", got)
	}
	for i, rec := range receipts {
		if got := rec.Detail["crossTenant"]; got != true {
			t.Errorf("receipt[%d] crossTenant = %v, want true", i, got)
		}
		if got := rec.Detail["shardsTouched"]; got != 1 {
			t.Errorf("receipt[%d] shardsTouched = %v, want 1", i, got)
		}
	}
}

// TestListAuditEventsCrossTenantFreshBypassesCache covers §25.9 line
// 3709: ?fresh=true re-reads the shards even when a cached entry exists.
func TestListAuditEventsCrossTenantFreshBypassesCache_spec_25_9_3709(t *testing.T) {
	reader := &fakeScatterReader{rows: twoTenantRows()}
	router := crossTenantRouter(t, reader, true)

	if rr := getCrossTenant(t, router, ""); rr.Code != http.StatusOK {
		t.Fatalf("warm status = %d", rr.Code)
	}
	if rr := getCrossTenant(t, router, "?fresh=true"); rr.Code != http.StatusOK {
		t.Fatalf("fresh status = %d", rr.Code)
	}
	if reader.calls != 2 {
		t.Fatalf("scatter reader calls = %d, want 2 (fresh bypassed cache)", reader.calls)
	}
}

// TestListAuditEventsCrossTenantCacheOptOut covers §25.9 line 3709
// ops.audit.scatterGatherCacheEnabled=false: every cross-tenant query
// reads the shards fresh.
func TestListAuditEventsCrossTenantCacheOptOut_spec_25_9_3709(t *testing.T) {
	reader := &fakeScatterReader{rows: twoTenantRows()}
	router := crossTenantRouter(t, reader, false)

	_ = getCrossTenant(t, router, "")
	_ = getCrossTenant(t, router, "")
	if reader.calls != 2 {
		t.Fatalf("scatter reader calls = %d, want 2 (cache disabled)", reader.calls)
	}
}

// TestListAuditEventsCrossTenantPartialShard covers §25.9 "Degradation":
// a partial-shard outage returns 207 AUDIT_PARTIAL_RESULTS with the
// degradation envelope listing the missing shards, and is not cached.
func TestListAuditEventsCrossTenantPartialShard_spec_25_9_partial(t *testing.T) {
	reader := &fakeScatterReader{rows: twoTenantRows(), missing: []string{"shard-2"}}
	router := crossTenantRouter(t, reader, true)

	rr := getCrossTenant(t, router, "")
	if rr.Code != http.StatusMultiStatus {
		t.Fatalf("status = %d, want 207", rr.Code)
	}
	var env admin.AuditEventEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Degradation == nil {
		t.Fatalf("degradation envelope missing on partial result")
	}
	if len(env.Degradation.MissingShards) != 1 || env.Degradation.MissingShards[0] != "shard-2" {
		t.Errorf("missingShards = %v, want [shard-2]", env.Degradation.MissingShards)
	}
	// A degraded 207 must not be cached: the next call re-reads.
	_ = getCrossTenant(t, router, "")
	if reader.calls != 2 {
		t.Fatalf("scatter reader calls = %d, want 2 (207 not cached)", reader.calls)
	}
}

// TestListAuditEventsCrossTenantTotalOutage covers §25.9 "Degradation":
// when every shard is unreachable the read returns 503
// AUDIT_STORE_UNAVAILABLE.
func TestListAuditEventsCrossTenantTotalOutage_spec_25_9_503(t *testing.T) {
	reader := &fakeScatterReader{err: context.DeadlineExceeded}
	router := crossTenantRouter(t, reader, true)

	rr := getCrossTenant(t, router, "")
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body.Error.Code != "AUDIT_STORE_UNAVAILABLE" {
		t.Errorf("code = %v, want AUDIT_STORE_UNAVAILABLE", body.Error.Code)
	}
}

// TestListAuditEventsExplicitTenantStaysSingleTenant confirms an explicit
// ?tenantId= keeps a platform-admin on the single-tenant read path (the
// scatter reader is never consulted), preserving the §25.9 per-tenant
// AuditShard route.
func TestListAuditEventsExplicitTenantStaysSingleTenant_spec_25_9_3668(t *testing.T) {
	reader := &fakeScatterReader{rows: twoTenantRows()}
	router := crossTenantRouter(t, reader, true)

	rr := getCrossTenant(t, router, "?tenantId=acme")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if reader.calls != 0 {
		t.Fatalf("scatter reader calls = %d, want 0 (explicit tenant is single-tenant)", reader.calls)
	}
}

// TestListAuditEventsCrossTenantLimitPaginates covers §25.9 pagination on
// the cross-tenant page: a limit below the merged row count yields a
// nextCursor and the second page returns the remainder.
func TestListAuditEventsCrossTenantLimitPaginates_spec_25_9_3659(t *testing.T) {
	reader := &fakeScatterReader{rows: twoTenantRows()}
	router := crossTenantRouter(t, reader, false)

	rr := getCrossTenant(t, router, "?limit=2")
	if rr.Code != http.StatusOK {
		t.Fatalf("page1 status = %d", rr.Code)
	}
	var page1 admin.AuditEventEnvelope
	_ = json.Unmarshal(rr.Body.Bytes(), &page1)
	if len(page1.Items) != 2 {
		t.Fatalf("page1 items = %d, want 2", len(page1.Items))
	}
	if page1.NextCursor == "" {
		t.Fatalf("page1 nextCursor empty, want a continuation token")
	}

	rr = getCrossTenant(t, router, "?limit=2&cursor="+page1.NextCursor)
	if rr.Code != http.StatusOK {
		t.Fatalf("page2 status = %d", rr.Code)
	}
	var page2 admin.AuditEventEnvelope
	_ = json.Unmarshal(rr.Body.Bytes(), &page2)
	if len(page2.Items) != 1 {
		t.Fatalf("page2 items = %d, want 1 (remainder)", len(page2.Items))
	}
	if page2.NextCursor != "" {
		t.Errorf("page2 nextCursor = %q, want empty (last page)", page2.NextCursor)
	}
}

// TestMemScatterGatherCacheTTL covers the in-memory cache contract: a
// stored entry is returned until its TTL elapses, then evicted.
func TestMemScatterGatherCacheTTL(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	cache := admin.NewMemScatterGatherCache(clock)
	cache.Set("k", []byte("v"), 5*time.Minute)

	if v, ok := cache.Get("k"); !ok || string(v) != "v" {
		t.Fatalf("Get within TTL = %q,%v, want v,true", v, ok)
	}
	now = now.Add(6 * time.Minute)
	if _, ok := cache.Get("k"); ok {
		t.Errorf("Get after TTL = true, want false (expired)")
	}
}
