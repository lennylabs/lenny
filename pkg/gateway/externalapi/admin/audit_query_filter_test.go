// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
)

// spec: §25.9 Audit Log Query API — query filters (line 3659), time
// window + AUDIT_QUERY_TOO_BROAD (lines 3707-3708), per-row
// chainIntegrity + auditMetadata (lines 3653, 3670-3679), error codes
// (lines 3732-3735), audit.query_executed /
// audit.chain_integrity_broken_detected emission (line 3750), and the
// summary endpoint (line 3661).

var auditTestClock = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// craftedAuditLog is an admin.AuditLog that returns a fixed row set, so
// the §25.9 query filters, integrity verdicts, and degradation paths are
// exercisable without Postgres. A non-nil err makes every read fail so
// the AUDIT_STORE_UNAVAILABLE mapping can be asserted.
type craftedAuditLog struct {
	rows []audit.Row
	err  error
}

func (c *craftedAuditLog) Append(context.Context, string, string, json.RawMessage, time.Time) (audit.Row, error) {
	return audit.Row{}, nil
}

func (c *craftedAuditLog) Rows(context.Context, string) ([]audit.Row, error) {
	if c.err != nil {
		return nil, c.err
	}
	return c.rows, nil
}

func (c *craftedAuditLog) Verify(context.Context, string) (audit.VerifyResult, error) {
	if c.err != nil {
		return audit.VerifyResult{}, c.err
	}
	return audit.VerifyResult{Integrity: audit.ChainVerified}, nil
}

// recordingSink captures the AuditEvents the §25.9 query path emits.
type recordingSink struct{ events []admin.AuditEvent }

func (s *recordingSink) EmitAdminEvent(_ context.Context, e admin.AuditEvent) {
	s.events = append(s.events, e)
}

func (s *recordingSink) has(eventType string) bool {
	for _, e := range s.events {
		if e.Type == eventType {
			return true
		}
	}
	return false
}

func newCraftedRouter(backend admin.AuditLog, sink admin.AuditSink) *admin.Router {
	return admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return auditTestClock },
		Audit: sink,
	}).WithAuditLog(backend)
}

// craftRow builds a sealed audit row with a valid content hash so the
// §11.7 verifier treats it as verified (the gap test relies on a valid
// content hash so the row is classified gap_suspected rather than
// broken).
func craftRow(seq uint64, tenant, eventType, payload string, ts time.Time, prevHash string) audit.Row {
	r := audit.Row{
		ID:                 tenant + ":" + strconv.FormatUint(seq, 10),
		Seq:                seq,
		TenantID:           tenant,
		EventType:          eventType,
		EventSchemaVersion: audit.DefaultEventSchemaVersion,
		Payload:            json.RawMessage(payload),
		Timestamp:          ts.UTC(),
		PrevHash:           prevHash,
	}
	r.Hash = audit.ComputeHash(r)
	return r
}

// craftChain builds a contiguous, valid chain over the given (eventType,
// payload, ts) tuples starting at seq 1.
func craftChain(tenant string, specs ...[3]any) []audit.Row {
	var rows []audit.Row
	prev := audit.GenesisPrevHash
	for i, s := range specs {
		row := craftRow(uint64(i+1), tenant, s[0].(string), s[1].(string), s[2].(time.Time), prev)
		rows = append(rows, row)
		prev = audit.LinkHash(row)
	}
	return rows
}

func listAudit(t *testing.T, router *admin.Router, query string) (*httptest.ResponseRecorder, admin.AuditEventEnvelope) {
	t.Helper()
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(
		httptest.NewRequest(http.MethodGet, "/v1/admin/audit-events?tenantId=platform&"+query, nil),
	))
	var env admin.AuditEventEnvelope
	if rr.Code == http.StatusOK {
		if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode envelope: %v (body=%s)", err, rr.Body.String())
		}
	}
	return rr, env
}

func TestListAuditEventsFilterByEventType_spec_25_9_3659(t *testing.T) {
	ts := auditTestClock.Add(-time.Hour)
	backend := &craftedAuditLog{rows: craftChain(
		"platform",
		[3]any{"admin.tenant.created", `{}`, ts},
		[3]any{"delegation.cycle_warning", `{}`, ts},
	)}
	router := newCraftedRouter(backend, &recordingSink{})

	rr, env := listAudit(t, router, "eventType=delegation.cycle_warning&since="+ts.Add(-time.Hour).Format(time.RFC3339))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	if len(env.Items) != 1 {
		t.Fatalf("eventType filter returned %d items, want 1", len(env.Items))
	}
}

// spec: §12.8 line 986 — the DSAR template's category-(b) invocation
// passes a comma-separated eventType list
// (admin.impersonation_started,admin.impersonation_ended). A row matches
// when its event type is any list member.
func TestListAuditEventsFilterByEventTypeList_spec_12_8_986(t *testing.T) {
	ts := auditTestClock.Add(-time.Hour)
	backend := &craftedAuditLog{rows: craftChain(
		"platform",
		[3]any{"admin.impersonation_started", `{}`, ts},
		[3]any{"admin.impersonation_ended", `{}`, ts},
		[3]any{"admin.tenant.created", `{}`, ts},
	)}
	router := newCraftedRouter(backend, &recordingSink{})
	since := "since=" + ts.Add(-time.Hour).Format(time.RFC3339)

	rr, env := listAudit(t, router,
		"eventType=admin.impersonation_started,admin.impersonation_ended&"+since)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	if len(env.Items) != 2 {
		t.Fatalf("eventType list returned %d items, want 2 (both impersonation events)", len(env.Items))
	}
	// A list with surrounding spaces is trimmed (the %20 decodes to a
	// space before TrimSpace); the named rows match.
	_, env = listAudit(t, router, "eventType=admin.impersonation_started,%20admin.tenant.created&"+since)
	if len(env.Items) != 2 {
		t.Fatalf("trimmed eventType list returned %d items, want 2", len(env.Items))
	}
}

func TestListAuditEventsFilterByActorAndResource_spec_25_9_3659(t *testing.T) {
	ts := auditTestClock.Add(-time.Hour)
	backend := &craftedAuditLog{rows: craftChain(
		"platform",
		[3]any{"admin.tenant.created", `{"actor_subject":"alice@acme.com","target_resource":"tenant/acme"}`, ts},
		[3]any{"admin.tenant.created", `{"actor_subject":"bob@acme.com","target_resource":"pool/warm"}`, ts},
	)}
	router := newCraftedRouter(backend, &recordingSink{})
	since := "since=" + ts.Add(-time.Hour).Format(time.RFC3339)

	// actorId narrows to alice's row.
	_, env := listAudit(t, router, "actorId=alice@acme.com&"+since)
	if len(env.Items) != 1 {
		t.Fatalf("actorId filter returned %d items, want 1", len(env.Items))
	}
	// resourceType + resourceId from the "type/id" target_resource.
	_, env = listAudit(t, router, "resourceType=pool&resourceId=warm&"+since)
	if len(env.Items) != 1 {
		t.Fatalf("resource filter returned %d items, want 1", len(env.Items))
	}
	// AND semantics: a non-matching actor with a matching resource yields none.
	_, env = listAudit(t, router, "actorId=alice@acme.com&resourceType=pool&"+since)
	if len(env.Items) != 0 {
		t.Fatalf("AND filter returned %d items, want 0", len(env.Items))
	}
}

func TestListAuditEventsTimeWindowDefault_spec_25_9_3708(t *testing.T) {
	old := auditTestClock.Add(-48 * time.Hour) // outside the default 24h window
	backend := &craftedAuditLog{rows: craftChain(
		"platform",
		[3]any{"admin.tenant.created", `{}`, old},
	)}
	router := newCraftedRouter(backend, &recordingSink{})

	// Default window (last 24h) excludes the 48h-old row.
	_, env := listAudit(t, router, "")
	if len(env.Items) != 0 {
		t.Fatalf("default window returned %d items, want 0 (row is 48h old)", len(env.Items))
	}
	// An explicit since reaching back includes it.
	_, env = listAudit(t, router, "since="+old.Add(-time.Hour).Format(time.RFC3339))
	if len(env.Items) != 1 {
		t.Fatalf("widened window returned %d items, want 1", len(env.Items))
	}
}

func TestListAuditEventsTooBroad_spec_25_9_3707(t *testing.T) {
	router := newCraftedRouter(&craftedAuditLog{}, &recordingSink{})
	since := auditTestClock.Add(-100 * 24 * time.Hour).Format(time.RFC3339)

	// > 90 days without a narrowing filter → 400 AUDIT_QUERY_TOO_BROAD.
	rr, _ := listAudit(t, router, "since="+since)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("too-broad: status %d, want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "AUDIT_QUERY_TOO_BROAD") {
		t.Errorf("missing AUDIT_QUERY_TOO_BROAD: %s", rr.Body.String())
	}
	// The same span WITH a narrowing filter is permitted.
	rr, _ = listAudit(t, router, "since="+since+"&eventType=admin.tenant.created")
	if rr.Code != http.StatusOK {
		t.Fatalf("narrowed broad query: status %d, want 200: %s", rr.Code, rr.Body.String())
	}
}

func TestListAuditEventsSeverityFilter_spec_25_9_3659(t *testing.T) {
	ts := auditTestClock.Add(-time.Hour)
	backend := &craftedAuditLog{rows: craftChain(
		"platform",
		[3]any{"admin.tenant.created", `{}`, ts},     // OCSF severity Informational
		[3]any{"delegation.cycle_warning", `{}`, ts}, // OCSF severity Medium (Warning)
	)}
	router := newCraftedRouter(backend, &recordingSink{})
	since := "since=" + ts.Add(-time.Hour).Format(time.RFC3339)

	_, env := listAudit(t, router, "severity=medium&"+since)
	if len(env.Items) != 1 {
		t.Fatalf("severity=medium returned %d items, want 1", len(env.Items))
	}
	// Numeric severity_id form is accepted too (1 = Informational).
	_, env = listAudit(t, router, "severity=1&"+since)
	if len(env.Items) != 1 {
		t.Fatalf("severity=1 returned %d items, want 1", len(env.Items))
	}
}

func TestListAuditEventsCursorPagination_spec_25_9_3659(t *testing.T) {
	ts := auditTestClock.Add(-time.Hour)
	backend := &craftedAuditLog{rows: craftChain(
		"platform",
		[3]any{"admin.tenant.created", `{}`, ts},
		[3]any{"admin.tenant.created", `{}`, ts},
		[3]any{"admin.tenant.created", `{}`, ts},
	)}
	router := newCraftedRouter(backend, &recordingSink{})
	since := "since=" + ts.Add(-time.Hour).Format(time.RFC3339)

	rr, env := listAudit(t, router, "limit=2&"+since)
	if rr.Code != http.StatusOK || len(env.Items) != 2 {
		t.Fatalf("page 1: status %d items %d, want 200/2", rr.Code, len(env.Items))
	}
	if env.NextCursor == "" {
		t.Fatalf("page 1 should carry a nextCursor")
	}
	_, env2 := listAudit(t, router, "limit=2&cursor="+env.NextCursor+"&"+since)
	if len(env2.Items) != 1 {
		t.Fatalf("page 2 returned %d items, want 1", len(env2.Items))
	}
	if env2.NextCursor != "" {
		t.Errorf("page 2 should be the last page (no nextCursor), got %q", env2.NextCursor)
	}

	// A malformed cursor is rejected.
	rr, _ = listAudit(t, router, "cursor=not-base64-!!!&"+since)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("malformed cursor: status %d, want 400", rr.Code)
	}
}

func TestListAuditEventsChainIntegrityReport_spec_25_9_3653(t *testing.T) {
	router, _ := newAuditQueryRouter(t)
	body, _ := json.Marshal(admin.TenantPayload{ID: "acme"})
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(
		httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", strings.NewReader(string(body))),
	))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create tenant: %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(
		httptest.NewRequest(http.MethodGet, "/v1/admin/audit-events?tenantId=platform", nil),
	))
	if rr.Code != http.StatusOK {
		t.Fatalf("list: %d", rr.Code)
	}
	var env admin.AuditEventEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.ChainIntegrityReport == nil {
		t.Fatalf("envelope missing chainIntegrityReport")
	}
	if env.ChainIntegrityReport.Verified != len(env.Items) || env.ChainIntegrityReport.Broken != 0 {
		t.Errorf("report = %+v, want verified=%d broken=0", env.ChainIntegrityReport, len(env.Items))
	}
	// Each OCSF record carries its per-row integrity in unmapped.lenny_chain.
	var rec map[string]any
	if err := json.Unmarshal(env.Items[0], &rec); err != nil {
		t.Fatalf("decode item: %v", err)
	}
	unmapped, _ := rec["unmapped"].(map[string]any)
	chain, _ := unmapped["lenny_chain"].(map[string]any)
	if chain["integrity"] != string(audit.ChainVerified) {
		t.Errorf("per-row integrity = %v, want verified", chain["integrity"])
	}
}

// TestListAuditEventsGapWindow_spec_25_9_3669 pins the §25.9 line 3669
// benign nextval-rollback gap window: a sequence-number jump whose
// prev_hash links across the gap is reported with reason
// "nextval_rollback", the value the reconciled §25.9 and the
// audit-chain-gap runbook direct operators to look for. Against the
// pre-fix gapWindows (which hard-coded reason "sequence_gap" for every
// window) this assertion fails.
func TestListAuditEventsGapWindow_spec_25_9_3669(t *testing.T) {
	ts1 := auditTestClock.Add(-2 * time.Hour)
	ts2 := auditTestClock.Add(-time.Hour)
	row1 := craftRow(1, "platform", "admin.tenant.created", `{}`, ts1, audit.GenesisPrevHash)
	// A sequence jump (1 → 5) whose prev_hash still links across the gap
	// is the benign §25.9 nextval-rollback signal.
	row5 := craftRow(5, "platform", "admin.tenant.created", `{}`, ts2, audit.LinkHash(row1))
	backend := &craftedAuditLog{rows: []audit.Row{row1, row5}}
	router := newCraftedRouter(backend, &recordingSink{})

	_, env := listAudit(t, router, "since="+ts1.Add(-time.Hour).Format(time.RFC3339))
	if env.AuditMetadata == nil || len(env.AuditMetadata.SuspectedGaps) != 1 {
		t.Fatalf("expected one suspected gap window, got %+v", env.AuditMetadata)
	}
	if got := env.AuditMetadata.SuspectedGaps[0].Reason; got != "nextval_rollback" {
		t.Errorf("gap window reason = %q, want %q", got, "nextval_rollback")
	}
	if env.ChainIntegrityReport.GapSuspected != 1 {
		t.Errorf("report gap_suspected = %d, want 1", env.ChainIntegrityReport.GapSuspected)
	}
}

// TestListAuditEventsGapWindowNonLinking_spec_25_9_3668 pins that a
// sequence-number gap whose prev_hash does NOT link across it is not
// listed as a suspected-gap window at all, and is never labeled the
// outage reason "postgres_unreachable". §25.9 line 3669 reserves
// "postgres_unreachable" for a gap an ops_postgres_outage_log window
// covers; this handler does not operate that subsystem and cannot compute
// an outage window, so it never emits that reason. §25.9 line 3668 makes
// a non-linking-prev_hash gap the tamper case rather than an outage, so it
// is excluded from the benign nextval-rollback window list entirely.
// Against the pre-fix gapWindows this non-linking gap was emitted as an
// outage window with reason "postgres_unreachable", mislabeling a tamper
// as a Postgres outage — the §25.9 line 3668 condition the spec says is
// tampering, not an outage — so this assertion fails against the pre-fix
// code.
//
// spec: §25.9 line 3668 (a non-linking prev_hash gap is tampering, not an
// outage), §25.9 line 3669 (postgres_unreachable is outage-log-covered).
// F-11.2.10.
func TestListAuditEventsGapWindowNonLinking_spec_25_9_3668(t *testing.T) {
	ts1 := auditTestClock.Add(-2 * time.Hour)
	ts2 := auditTestClock.Add(-time.Hour)
	row1 := craftRow(1, "platform", "admin.tenant.created", `{}`, ts1, audit.GenesisPrevHash)
	// A sequence jump whose prev_hash does not link (a garbage prev_hash)
	// is the tamper-or-removal case, not a benign rollback.
	row5 := craftRow(5, "platform", "admin.tenant.created", `{}`, ts2,
		"deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	backend := &craftedAuditLog{rows: []audit.Row{row1, row5}}
	router := newCraftedRouter(backend, &recordingSink{})

	_, env := listAudit(t, router, "since="+ts1.Add(-time.Hour).Format(time.RFC3339))
	// The non-linking gap is not an outage window: it must not appear in
	// the suspected-gap list at all, and never under "postgres_unreachable".
	if env.AuditMetadata != nil {
		for _, g := range env.AuditMetadata.SuspectedGaps {
			t.Errorf("non-linking gap listed as suspected window %+v; want none (postgres_unreachable is reserved for an ops_postgres_outage_log window this handler does not compute)", g)
		}
	}
}

func TestListAuditEventsInvalidPublishState_spec_25_9_3659(t *testing.T) {
	router := newCraftedRouter(&craftedAuditLog{}, &recordingSink{})
	rr, _ := listAudit(t, router, "eventbus_publish_state=bogus")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid publish state: status %d, want 400", rr.Code)
	}
}

func TestGetAuditEventNotFoundCode_spec_25_9_3732(t *testing.T) {
	router := newCraftedRouter(&craftedAuditLog{}, &recordingSink{})
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(
		httptest.NewRequest(http.MethodGet, "/v1/admin/audit-events/99?tenantId=platform", nil),
	))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "AUDIT_EVENT_NOT_FOUND") {
		t.Errorf("missing AUDIT_EVENT_NOT_FOUND: %s", rr.Body.String())
	}
}

func TestAuditStoreUnavailable_spec_25_9_3714(t *testing.T) {
	backend := &craftedAuditLog{err: errors.New("postgres unreachable")}
	router := newCraftedRouter(backend, &recordingSink{})
	for _, path := range []string{
		"/v1/admin/audit-events?tenantId=platform",
		"/v1/admin/audit-events/1?tenantId=platform",
		"/v1/admin/audit-events/summary?tenantId=platform",
	} {
		rr := httptest.NewRecorder()
		router.Handler().ServeHTTP(rr, withAdminPrincipal(httptest.NewRequest(http.MethodGet, path, nil)))
		if rr.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: status %d, want 503", path, rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "AUDIT_STORE_UNAVAILABLE") {
			t.Errorf("%s: missing AUDIT_STORE_UNAVAILABLE: %s", path, rr.Body.String())
		}
	}
}

func TestAuditQueryEmitsQueryExecuted_spec_25_9_3750(t *testing.T) {
	sink := &recordingSink{}
	ts := auditTestClock.Add(-time.Hour)
	backend := &craftedAuditLog{rows: craftChain(
		"platform",
		[3]any{"admin.tenant.created", `{}`, ts},
	)}
	router := newCraftedRouter(backend, sink)
	listAudit(t, router, "since="+ts.Add(-time.Hour).Format(time.RFC3339))
	if !sink.has("audit.query_executed") {
		t.Errorf("audit.query_executed not emitted; events=%+v", sink.events)
	}
}

func TestAuditQueryEmitsChainIntegrityBroken_spec_25_9_3750(t *testing.T) {
	sink := &recordingSink{}
	ts := auditTestClock.Add(-time.Hour)
	// A row whose stored hash does not match its content is broken.
	row := craftRow(1, "platform", "admin.tenant.created", `{}`, ts, audit.GenesisPrevHash)
	row.Hash = "deadbeef"
	backend := &craftedAuditLog{rows: []audit.Row{row}}
	router := newCraftedRouter(backend, sink)
	_, env := listAudit(t, router, "since="+ts.Add(-time.Hour).Format(time.RFC3339))
	if env.ChainIntegrityReport.Broken != 1 {
		t.Fatalf("report broken = %d, want 1", env.ChainIntegrityReport.Broken)
	}
	if !sink.has("audit.chain_integrity_broken_detected") {
		t.Errorf("broken-chain event not emitted; events=%+v", sink.events)
	}
}

func TestAuditSummaryGroupByEventType_spec_25_9_3661(t *testing.T) {
	ts := auditTestClock.Add(-time.Hour)
	backend := &craftedAuditLog{rows: craftChain(
		"platform",
		[3]any{"admin.tenant.created", `{}`, ts},
		[3]any{"admin.tenant.created", `{}`, ts},
		[3]any{"delegation.cycle_warning", `{}`, ts},
	)}
	router := newCraftedRouter(backend, &recordingSink{})

	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(httptest.NewRequest(http.MethodGet,
		"/v1/admin/audit-events/summary?tenantId=platform&groupBy=eventType&since="+ts.Add(-time.Hour).Format(time.RFC3339), nil)))
	if rr.Code != http.StatusOK {
		t.Fatalf("summary: status %d: %s", rr.Code, rr.Body.String())
	}
	var resp admin.AuditSummaryResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if resp.Total != 3 || len(resp.Groups) != 2 {
		t.Fatalf("summary total=%d groups=%d, want 3/2", resp.Total, len(resp.Groups))
	}
	// Highest count first.
	if resp.Groups[0].Key != "admin.tenant.created" || resp.Groups[0].Count != 2 {
		t.Errorf("top group = %+v, want admin.tenant.created/2", resp.Groups[0])
	}
}

// TestAuditQueryExecutedRowReQueriesCleanly confirms the emitted
// audit.query_executed row is itself OCSF-translatable (it matches the
// `audit.` prefix mapping), so a second list call does not 500 when it
// encounters the row the first call wrote.
//
// spec: §25.9 line 3750; §4.4 line 232 (every audit row is OCSF-egress
// translatable).
func TestAuditQueryExecutedRowReQueriesCleanly(t *testing.T) {
	router, _ := newAuditQueryRouter(t)
	body, _ := json.Marshal(admin.TenantPayload{ID: "acme"})
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(
		httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", strings.NewReader(string(body))),
	))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create tenant: %d", rr.Code)
	}

	// First list writes an audit.query_executed row onto the platform chain.
	_, env1 := listAudit(t, router, "")
	first := len(env1.Items)

	// Second list must translate that row without erroring, and the row
	// must now be visible.
	rr2, env2 := listAudit(t, router, "")
	if rr2.Code != http.StatusOK {
		t.Fatalf("second list: status %d: %s", rr2.Code, rr2.Body.String())
	}
	if len(env2.Items) <= first {
		t.Errorf("second list items=%d, want > %d (query_executed row should appear)", len(env2.Items), first)
	}
}

func TestAuditSummaryInvalidGroupBy_spec_25_9_3661(t *testing.T) {
	router := newCraftedRouter(&craftedAuditLog{}, &recordingSink{})
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(httptest.NewRequest(http.MethodGet,
		"/v1/admin/audit-events/summary?tenantId=platform&groupBy=bogus", nil)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid groupBy: status %d, want 400", rr.Code)
	}
}
