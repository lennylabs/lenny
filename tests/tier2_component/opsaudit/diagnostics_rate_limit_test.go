//go:build component

// SPDX-License-Identifier: MIT

// Component test for the §25.9 diagnostics-audit rate limiting wired over
// a real §11.7 durable audit chain and a real Prometheus counter. The
// unit coverage in pkg/ops/opsserver/diagnostics_audit_test.go asserts the
// per-service-account cap drops the excess event and fires a fake
// RateLimited callback; it never proves, against the real audit store the
// coalesced events commit to, that a dropped distinct event is absent from
// the durable chain while the surviving events land, nor that the
// operator-facing lenny_audit_rate_limited_total counter (the observable
// signal §25.9 line 3704 promises operators) actually increments. This
// test wires the diagnostics-audit Emit and RateLimited exactly as
// cmd/lenny-ops buildDiagnosticsAudit does — Emit through the same
// opsaudit.Recorder over a real auditstore.Store routed through the §12.6
// StoreRouter, RateLimited into a real prometheus.CounterVec — caps the
// per-service-account rate, drives the §25.6 diagnostic endpoints over
// HTTP past the cap, and asserts the coalesced survivors read back from
// the durable chain with their invocationCount while the dropped event is
// both absent from the chain and counted on lenny_audit_rate_limited_total.
package opsaudit_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/gateway/audit/auditstore"
	"github.com/lennylabs/lenny/pkg/ops/auditrate"
	"github.com/lennylabs/lenny/pkg/ops/diagnostics"
	"github.com/lennylabs/lenny/pkg/ops/opsaudit"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
	"github.com/lennylabs/lenny/pkg/storerouter"
)

// spec: §25.9 line 3703 ("repeated diagnostic calls for the same
// {resourceType, resourceId} within a 60s window emit only one audit event
// with an incremented invocationCount field, instead of one per call");
// §25.9 line 3704 ("ops.audit.diagnosticsRatePerMinute (default 60) caps
// the number of distinct diagnostic audit events per minute per service
// account. Excess is dropped silently with an lenny_audit_rate_limited_total
// counter increment (so operators can detect)"); §11.7 line 435
// (ops_event.* commit to the platform-tenant hash chain).
//
// diagnosis: a failure means the §25.9 rate-limit seam is not correctly
// integrated with the durable audit chain and the operator metric. Either
// a rate-limited-dropped diagnostic access still committed to the §11.7
// chain (the drop was not silent), a surviving coalesced access failed to
// commit with its invocationCount, or lenny_audit_rate_limited_total did
// not increment for the dropped event so operators cannot detect that
// diagnostics are being shed.
func TestDiagnosticsAuditRateLimit_DropsAreCountedNotChained_spec_25_9_3704(t *testing.T) {
	t.Parallel()
	pg := startPG(t)
	ctx := context.Background()

	if _, err := pg.Pool.Exec(ctx,
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x00')`, opsaudit.PlatformTenantID); err != nil {
		t.Fatalf("seed platform tenant: %v", err)
	}

	// Build the router + audit store + recorder exactly as cmd/lenny-ops
	// does, so a surviving diagnostic access commits to the real §11.7 chain.
	router, err := storerouter.NewSingleShardRouter(storerouter.Config{Postgres: pg.Pool})
	if err != nil {
		t.Fatalf("store router: %v", err)
	}
	store := auditstore.New(router)
	recorder := opsaudit.New(store)
	if !recorder.Durable() {
		t.Fatal("recorder not durable with a wired store")
	}

	// A real Prometheus counter with the §25.9 line 3729 label set, so the
	// RateLimited seam increments an actual counter the operator /metrics
	// exposition would surface, not a test double.
	rateLimited := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "lenny_audit_rate_limited_total",
		Help: "§25.9 diagnostic audit events dropped by rate limiting.",
	}, []string{"event_type", "service_account"})

	// A fixed clock keeps every diagnostic call inside one 60s coalescing
	// window and one rate-limit minute, so the coalescing and the cap are
	// both deterministic.
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)

	// RatePerMinute 2 admits the first two distinct diagnostic events per
	// service account and drops the third. The Emit / RateLimited closures
	// mirror cmd/lenny-ops buildDiagnosticsAudit.
	cfg := &opsserver.DiagnosticsAuditConfig{
		RatePerMinute: 2,
		Now:           func() time.Time { return now },
		Emit: func(ev auditrate.Event) {
			recorder.Record(ev.EventType, map[string]any{
				"resourceType":    ev.ResourceType,
				"resourceId":      ev.ResourceID,
				"invocationCount": ev.InvocationCount,
				"serviceAccount":  ev.ServiceAccount,
				"operationId":     ev.OperationID,
			}, ev.FirstAt)
		},
		RateLimited: func(eventType, serviceAccount string) {
			rateLimited.WithLabelValues(eventType, serviceAccount).Inc()
		},
	}
	srv := opsserver.New(opsserver.Options{
		Diagnostics:      diagnostics.NewService(diagAllFound{}),
		DiagnosticsAudit: cfg,
	})

	get := func(path string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: status %d, want 200; body %s", path, rec.Code, rec.Body.String())
		}
	}

	// First distinct event: session sess-1, called three times within the
	// window so its coalesced audit event carries invocationCount 3 and
	// counts once against the per-service-account cap.
	get("/v1/admin/diagnostics/sessions/sess-1")
	get("/v1/admin/diagnostics/sessions/sess-1")
	get("/v1/admin/diagnostics/sessions/sess-1")
	// Second distinct event: pool p-1, invocationCount 1. This reaches the
	// cap of 2 distinct events for the anonymous service account.
	get("/v1/admin/diagnostics/pools/p-1")
	// Third distinct event: credential-pool c-1. It exceeds the cap and is
	// dropped silently; it must never open a coalescing window, so it never
	// reaches the durable chain, and lenny_audit_rate_limited_total ticks.
	get("/v1/admin/diagnostics/credential-pools/c-1")

	// The dropped event increments the operator-facing counter under its
	// §16.7 event type and the resolved service account (anonymous, as the
	// httptest requests carry no principal or agent-name correlation).
	if got := testutil.ToFloat64(
		rateLimited.WithLabelValues("diagnostics.credential_pool_diagnosed", "anonymous"),
	); got != 1 {
		t.Errorf("lenny_audit_rate_limited_total{credential_pool_diagnosed,anonymous} = %v, want 1", got)
	}
	// No other label combination was dropped, so the counter has exactly one
	// series.
	if got := testutil.CollectAndCount(rateLimited); got != 1 {
		t.Errorf("lenny_audit_rate_limited_total series = %d, want 1", got)
	}

	// Drain the open coalescing windows: each surviving distinct event emits
	// its accumulated audit event through the recorder to the durable chain.
	// The dropped credential-pool event opened no window, so it flushes
	// nothing.
	srv.FlushDiagnosticsAudit()
	if recorder.FailedAppends() != 0 {
		t.Fatalf("FailedAppends = %d, want 0", recorder.FailedAppends())
	}

	rows, err := store.Rows(ctx, opsaudit.PlatformTenantID)
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	// Exactly the two admitted events land durably; the rate-limited third
	// is absent from the chain.
	wantCounts := map[string]float64{
		"diagnostics.session_diagnosed": 3,
		"diagnostics.pool_diagnosed":    1,
	}
	if len(rows) != len(wantCounts) {
		t.Fatalf("durable diagnostics audit rows = %d, want %d", len(rows), len(wantCounts))
	}
	seen := map[string]bool{}
	for _, row := range rows {
		if row.EventType == "diagnostics.credential_pool_diagnosed" {
			t.Errorf("rate-limited-dropped event landed in the durable chain: %s", row.EventType)
			continue
		}
		want, ok := wantCounts[row.EventType]
		if !ok {
			t.Errorf("unexpected durable event_type %q", row.EventType)
			continue
		}
		seen[row.EventType] = true
		var payload map[string]any
		if err := json.Unmarshal(row.Payload, &payload); err != nil {
			t.Fatalf("row %s payload not JSON: %v", row.EventType, err)
		}
		if got, _ := payload["invocationCount"].(float64); got != want {
			t.Errorf("%s invocationCount = %v, want %v", row.EventType, got, want)
		}
	}
	for evType := range wantCounts {
		if !seen[evType] {
			t.Errorf("durable chain missing %s", evType)
		}
	}

	// The §11.7 hash links hold across the committed diagnostic events.
	res, err := store.Verify(ctx, opsaudit.PlatformTenantID)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if res.Integrity != audit.ChainVerified {
		t.Errorf("platform audit chain Integrity = %v, want ChainVerified", res.Integrity)
	}
}
