// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// spec: §25.9 metrics table — lenny_audit_query_duration_seconds,
// lenny_audit_chain_verification_broken_total,
// lenny_audit_chain_rechained_post_outage_total,
// lenny_audit_scatter_gather_shards_queried. F-25.9.13.

// fakeAuditMetrics records the §25.9 audit-query observability calls so a
// test can assert the query path emits them with the spec labels.
type fakeAuditMetrics struct {
	durations []durationCall
	shards    []int
	broken    int
	rechained int
}

type durationCall struct {
	endpoint string
	shards   int
}

func (f *fakeAuditMetrics) ObserveAuditQueryDuration(endpoint string, shards int, _ float64) {
	f.durations = append(f.durations, durationCall{endpoint: endpoint, shards: shards})
}
func (f *fakeAuditMetrics) IncAuditChainVerificationBroken()      { f.broken++ }
func (f *fakeAuditMetrics) IncAuditChainRechainedPostOutage()     { f.rechained++ }
func (f *fakeAuditMetrics) ObserveAuditScatterGatherShards(s int) { f.shards = append(f.shards, s) }

func (f *fakeAuditMetrics) endpointCount(endpoint string) int {
	n := 0
	for _, d := range f.durations {
		if d.endpoint == endpoint {
			n++
		}
	}
	return n
}

func newMeteredAuditRouter(t *testing.T, rec admin.AuditQueryMetrics) *admin.Router {
	t.Helper()
	chains := audit.NewChainSet()
	store := tenantstore.NewMemory()
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	return admin.NewRouter(store, admin.Options{
		Clock: clock,
		Audit: admin.NewChainAuditSink(chains, clock),
	}).WithAuditChains(chains).WithAuditMetrics(rec)
}

// TestAuditQueryMetricsEmittedPerEndpoint confirms the list and summary
// endpoints each record a duration + scatter-gather observation labeled
// with the §25.9 single-shard fan-out width. (§25.9 carries chain
// integrity in the list envelope, so there is no separate verify
// endpoint — F-25.9.10.)
func TestAuditQueryMetricsEmittedPerEndpoint_spec_25_9_metrics(t *testing.T) {
	rec := &fakeAuditMetrics{}
	router := newMeteredAuditRouter(t, rec)
	// Generate an audit row so the chain is non-empty.
	body, _ := json.Marshal(admin.TenantPayload{ID: "acme"})
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(
		httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body))))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create tenant: %d", rr.Code)
	}

	for _, path := range []string{
		"/v1/admin/audit-events?tenantId=platform",
		"/v1/admin/audit-events/summary?tenantId=platform",
	} {
		rr = httptest.NewRecorder()
		router.Handler().ServeHTTP(rr, withAdminPrincipal(
			httptest.NewRequest(http.MethodGet, path, nil)))
		if rr.Code != http.StatusOK {
			t.Fatalf("GET %s: status %d, body=%s", path, rr.Code, rr.Body.String())
		}
	}

	for _, ep := range []string{"list", "summary"} {
		if rec.endpointCount(ep) != 1 {
			t.Errorf("endpoint %q recorded %d durations, want 1 (all: %+v)", ep, rec.endpointCount(ep), rec.durations)
		}
	}
	for _, d := range rec.durations {
		if d.shards != 1 {
			t.Errorf("endpoint %q recorded shards=%d, want 1 (single-shard v1)", d.endpoint, d.shards)
		}
	}
	if len(rec.shards) != 2 {
		t.Errorf("scatter-gather observations = %d, want 2", len(rec.shards))
	}
}

// TestAuditListMetricsRecordsBroken confirms the list endpoint increments
// the broken-segment counter when a returned row's chainIntegrity verdict
// is broken (the §25.9 line 3653 chainIntegrityReport tally). F-25.9.10,
// F-25.9.13.
func TestAuditListMetricsRecordsBroken_spec_25_9_metrics(t *testing.T) {
	rec := &fakeAuditMetrics{}
	fake := &brokenChainLog{}
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithAuditLog(fake).WithAuditMetrics(rec)
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(
		httptest.NewRequest(http.MethodGet, "/v1/admin/audit-events?tenantId=platform", nil)))
	if rr.Code != http.StatusOK {
		t.Fatalf("list: status %d, body=%s", rr.Code, rr.Body.String())
	}
	if rec.broken != 1 {
		t.Errorf("broken counter = %d, want 1", rec.broken)
	}
}

// brokenChainLog is an admin.AuditLog returning a single row whose stored
// hash does not match its content, so the list path's per-row
// chainIntegrity verdict is broken and the §25.9 broken counter fires.
type brokenChainLog struct{}

func (brokenChainLog) Append(context.Context, string, string, json.RawMessage, time.Time) (audit.Row, error) {
	return audit.Row{}, nil
}
func (brokenChainLog) Rows(context.Context, string) ([]audit.Row, error) {
	// Timestamp inside the default 24h window; Hash deliberately wrong so
	// VerifyRows classifies the row as broken.
	return []audit.Row{{
		Seq:       1,
		TenantID:  "platform",
		EventType: "admin.tenant.created",
		Payload:   json.RawMessage(`{}`),
		Timestamp: time.Date(2025, 12, 31, 23, 0, 0, 0, time.UTC),
		PrevHash:  audit.GenesisPrevHash,
		Hash:      "deadbeef",
	}}, nil
}
func (brokenChainLog) Verify(context.Context, string) (audit.VerifyResult, error) {
	return audit.VerifyResult{Integrity: audit.ChainBroken, BreakSeq: 1}, nil
}
