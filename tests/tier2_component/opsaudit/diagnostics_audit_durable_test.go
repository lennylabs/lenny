//go:build component

// SPDX-License-Identifier: MIT

// Component test for the §25.6 diagnostics-audit path all the way to the
// durable §11.7 hash chain. The unit coverage in
// pkg/ops/opsserver/diagnostics_audit_test.go asserts the coalesced
// diagnostic audit events reach an in-memory captureSink; it never proves
// they land in the hash-chained platform audit store or that they read
// back. This test wires the diagnostics-audit Emit through the same
// opsaudit.Recorder over a real auditstore.Store (routed through the §12.6
// StoreRouter) that cmd/lenny-ops builds, drives the four §25.6 diagnostic
// endpoints over HTTP, flushes the coalescing windows, and reads the four
// diagnostics.* events back from the durable chain with their coalesced
// invocationCount, then verifies the chain integrity.
package opsaudit_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/gateway/audit/auditstore"
	"github.com/lennylabs/lenny/pkg/ops/auditrate"
	"github.com/lennylabs/lenny/pkg/ops/diagnostics"
	"github.com/lennylabs/lenny/pkg/ops/opsaudit"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
	"github.com/lennylabs/lenny/pkg/storerouter"
)

// diagAllFound is a diagnostics DataSource that reports every resource as
// present with full fidelity, so each of the four §25.6 endpoints serves a
// successful diagnosis and records its audit event.
type diagAllFound struct{}

func (diagAllFound) Session(context.Context, string) (diagnostics.SessionRecord, error) {
	return diagnostics.SessionRecord{SessionID: "sess-1", State: "failed", Found: true}, nil
}

func (diagAllFound) Pool(context.Context, string) (diagnostics.PoolRecord, error) {
	return diagnostics.PoolRecord{Name: "p-1", Found: true}, nil
}

func (diagAllFound) CredentialPool(context.Context, string) (diagnostics.CredentialPoolRecord, error) {
	return diagnostics.CredentialPoolRecord{Name: "c-1", Found: true}, nil
}

func (diagAllFound) Connectivity(context.Context) ([]diagnostics.ConnectivityDependency, error) {
	return []diagnostics.ConnectivityDependency{{Name: "postgres", Reachable: true}}, nil
}

// spec: §25.6 line 2945 ("`diagnostics.session_diagnosed`,
// `diagnostics.pool_diagnosed`, `diagnostics.connectivity_checked`,
// `diagnostics.credential_pool_diagnosed`.") — the four diagnostic audit
// events; §25.9 lines 3699-3700 (coalescing into a single event carrying
// the invocationCount); §11.7 line 435 (ops_event.* commit to the
// platform-tenant hash chain).
//
// diagnosis: a failure means a §25.6 diagnostic access is not durably
// committed to the §11.7 platform audit chain, so the audit trail the
// §25.9 query API cross-references would be missing diagnostic events even
// though the in-memory emission fires. Either the coalesced event never
// reached the recorder, the recorder did not append it under the platform
// tenant, the durable payload dropped the coalesced invocationCount, or
// the chain did not verify.
func TestDiagnosticsAuditReachesDurableChain_spec_25_6_2945(t *testing.T) {
	t.Parallel()
	pg := startPG(t)
	ctx := context.Background()

	if _, err := pg.Pool.Exec(ctx,
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x00')`, opsaudit.PlatformTenantID); err != nil {
		t.Fatalf("seed platform tenant: %v", err)
	}

	// Build the router + audit store + recorder exactly as cmd/lenny-ops
	// does, so the diagnostics-audit Emit commits to the real §11.7 chain.
	router, err := storerouter.NewSingleShardRouter(storerouter.Config{Postgres: pg.Pool})
	if err != nil {
		t.Fatalf("store router: %v", err)
	}
	store := auditstore.New(router)
	recorder := opsaudit.New(store)
	if !recorder.Durable() {
		t.Fatal("recorder not durable with a wired store")
	}

	// A fixed clock keeps every diagnostic call inside one 60s coalescing
	// window, so repeated calls for a resource coalesce deterministically.
	now := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	// The diagnostics-audit Emit mirrors cmd/lenny-ops buildDiagnosticsAudit:
	// the coalesced event's fields (including the invocationCount) are the
	// durable audit-row payload, committed through the platform recorder.
	cfg := &opsserver.DiagnosticsAuditConfig{
		RatePerMinute: auditrate.DefaultRatePerMinute,
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
		// The session/pool/credential-pool endpoints serve 200 for a found
		// resource; connectivity serves 200 for a completed probe.
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: status %d, want 200; body %s", path, rec.Code, rec.Body.String())
		}
	}

	// Drive each of the four §25.6 diagnostic endpoints. The session
	// endpoint is called three times inside the window so its coalesced
	// event carries invocationCount 3; the other three carry 1.
	get("/v1/admin/diagnostics/sessions/sess-1")
	get("/v1/admin/diagnostics/sessions/sess-1")
	get("/v1/admin/diagnostics/sessions/sess-1")
	get("/v1/admin/diagnostics/pools/p-1")
	get("/v1/admin/diagnostics/credential-pools/c-1")
	get("/v1/admin/diagnostics/connectivity")

	// Nothing is committed until a window closes: the events are still
	// coalescing in memory.
	if rows, err := store.Rows(ctx, opsaudit.PlatformTenantID); err != nil {
		t.Fatalf("Rows (pre-flush): %v", err)
	} else if len(rows) != 0 {
		t.Fatalf("durable rows before flush = %d, want 0 (windows still open)", len(rows))
	}

	// Drain every open coalescing window; each emits its accumulated event
	// through the recorder to the durable chain.
	srv.FlushDiagnosticsAudit()
	if recorder.FailedAppends() != 0 {
		t.Fatalf("FailedAppends = %d, want 0", recorder.FailedAppends())
	}

	rows, err := store.Rows(ctx, opsaudit.PlatformTenantID)
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("durable diagnostics audit rows = %d, want 4", len(rows))
	}

	// Each of the four §25.6 event types must be query-recoverable from the
	// durable chain, carrying the documented coalesced invocationCount.
	wantCounts := map[string]float64{
		"diagnostics.session_diagnosed":         3,
		"diagnostics.pool_diagnosed":            1,
		"diagnostics.credential_pool_diagnosed": 1,
		"diagnostics.connectivity_checked":      1,
	}
	seen := map[string]bool{}
	for _, row := range rows {
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
