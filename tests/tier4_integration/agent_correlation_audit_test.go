// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §25.1 agent correlation headers
// (X-Lenny-Operation-ID, X-Lenny-Agent-Name) survive the full
// header-to-persisted-audit-event journey. The journey crosses the
// correlation middleware (pkg/gateway/middleware/correlation), the
// admin mutation handler (pkg/gateway/externalapi/admin), the §11.7
// Postgres-backed audit hash chain (pkg/gateway/audit/auditstore), and
// the §11.7 OCSF translator (pkg/audit/ocsf) at the audit-events read
// boundary. Unit tests pin only the first hop (the correlation
// builder/middleware); this test asserts the last hop — that both
// header values land on the persisted audit row and on its OCSF
// projection — so a regression that drops either header anywhere
// between the inbound request and the durable/egress form is caught.
package tier4_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/common/seqname"
	"github.com/lennylabs/lenny/pkg/gateway/audit/auditstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	correlationmw "github.com/lennylabs/lenny/pkg/gateway/middleware/correlation"
	corr "github.com/lennylabs/lenny/pkg/observability/correlation"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// spec: 25.1 (agent identity and correlation), 11.7 (audit chain / OCSF)
// diagnosis: a failure means an agent's X-Lenny-Operation-ID and/or
// X-Lenny-Agent-Name header did not survive the journey from the
// inbound admin request to the persisted audit row and its OCSF
// projection. §25.1 requires that "all audit events produced during the
// request include this ID" and that both headers are "propagated to
// audit events". A break in the correlation middleware, the admin
// handler's emit path, the Postgres audit store, or the OCSF translator
// would drop the correlation and defeat post-incident analysis of
// multi-step remediations.
func TestAgentCorrelationHeadersPropagateToPersistedAuditAndOCSF(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// spec: §25.1 — the correlation headers are caller-generated. A
	// UUID operation id ties the multi-step remediation together; the
	// agent name is the human-readable agent instance identifier.
	const (
		operationID = "550e8400-e29b-41d4-a716-446655440000"
		agentName   = "prod-watchdog-us-east-1"
		// Platform-admin mutations land on the "platform" audit chain.
		chainTenant = "platform"
		// The tenant the mutating request creates.
		newTenant = "acme"
	)

	// Live stack: a real Postgres container with the production
	// migrations. The admin mutation's audit event is committed to the
	// platform tenant's §11.7 hash chain, so that chain's genesis row
	// and audit sequence must exist before the first Append.
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	if _, err := pg.Pool.Exec(ctx,
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x00')`, chainTenant); err != nil {
		t.Fatalf("seed platform tenant: %v", err)
	}
	if _, err := pg.Pool.Exec(ctx,
		"CREATE SEQUENCE IF NOT EXISTS "+seqname.AuditSequenceName(chainTenant)+
			" START WITH 1 INCREMENT BY 1 NO CYCLE"); err != nil {
		t.Fatalf("provision platform audit sequence: %v", err)
	}

	clock := func() time.Time { return time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC) }

	// The admin router writes audit events to the Postgres-backed audit
	// chain (the same durable trail the gateway wires in production) and
	// reads them back through the §11.7 OCSF translator at
	// GET /v1/admin/audit-events.
	store := auditstore.New(pg.Router(t))
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: clock,
		Audit: admin.NewAuditLogSink(store, clock),
	}).WithAuditLog(store)

	// The correlation middleware is the production entry point that reads
	// the X-Lenny-Operation-ID / X-Lenny-Agent-Name headers off the
	// inbound request and attaches them to the request context the admin
	// handler's emit path reads. Wrapping the real router closes the loop
	// from header to handler.
	handler := correlationmw.Wrap(router.Handler(), correlationmw.Options{})

	// 1. An agent issues a mutating admin request carrying both headers.
	//    caller_type "agent" mirrors the §25.1 agent service-account JWT.
	body, _ := json.Marshal(admin.TenantPayload{ID: newTenant})
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body))
	req.Header.Set(corr.HeaderOperationID, operationID)
	req.Header.Set(corr.HeaderAgentName, agentName)
	req = req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
		TenantID:   chainTenant,
		Subject:    "sa-prod-watchdog-01",
		CallerType: "agent",
		Roles:      []pkgauth.Role{pkgauth.RolePlatformAdmin},
	}))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create tenant: status %d body=%s", rr.Code, rr.Body.String())
	}

	// 2. The persisted audit row carries both correlation fields. §25.1:
	//    the headers are "propagated to audit events"; the operation id is
	//    included on "all audit events produced during the request".
	rows, err := store.Rows(ctx, chainTenant)
	if err != nil {
		t.Fatalf("read persisted audit rows: %v", err)
	}
	created := findEventRow(t, rows, "admin.tenant.created")
	var payload map[string]any
	if err := json.Unmarshal(created.Payload, &payload); err != nil {
		t.Fatalf("decode persisted payload: %v", err)
	}
	if payload["operation_id"] != operationID {
		t.Errorf("persisted row operation_id = %v, want %q", payload["operation_id"], operationID)
	}
	if payload["agent_name"] != agentName {
		t.Errorf("persisted row agent_name = %v, want %q", payload["agent_name"], agentName)
	}

	// 3. The OCSF projection carries both fields at the audit-egress
	//    boundary. §25.1 requires propagation "to audit events"; the OCSF
	//    v1.1.0 record is the audit event's egress wire form. §11.7 maps
	//    operation_id → metadata.correlation_uid and routes agent_name
	//    (an unmapped payload key) verbatim into unmapped.lenny.
	getReq := httptest.NewRequest(http.MethodGet,
		"/v1/admin/audit-events?tenantId="+chainTenant, nil)
	getReq = getReq.WithContext(authmw.WithPrincipal(getReq.Context(), authmw.Principal{
		TenantID: chainTenant,
		Subject:  "sa-prod-watchdog-01",
		Roles:    []pkgauth.Role{pkgauth.RolePlatformAdmin},
	}))
	getRR := httptest.NewRecorder()
	handler.ServeHTTP(getRR, getReq)
	if getRR.Code != http.StatusOK {
		t.Fatalf("list audit events: status %d body=%s", getRR.Code, getRR.Body.String())
	}
	var env admin.AuditEventEnvelope
	if err := json.Unmarshal(getRR.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode OCSF envelope: %v", err)
	}
	rec := findOCSFRecord(t, env.Items, "admin.tenant.created")

	meta, _ := rec["metadata"].(map[string]any)
	if meta == nil {
		t.Fatalf("OCSF record has no metadata block: %v", rec)
	}
	if meta["correlation_uid"] != operationID {
		t.Errorf("OCSF metadata.correlation_uid = %v, want %q (operation_id projection)",
			meta["correlation_uid"], operationID)
	}
	unmapped, _ := rec["unmapped"].(map[string]any)
	lenny, _ := unmapped["lenny"].(map[string]any)
	if lenny == nil {
		t.Fatalf("OCSF record has no unmapped.lenny block: %v", rec)
	}
	if lenny["agent_name"] != agentName {
		t.Errorf("OCSF unmapped.lenny.agent_name = %v, want %q", lenny["agent_name"], agentName)
	}
}

// findEventRow returns the single persisted row with the given event
// type, failing the test when it is absent.
func findEventRow(t *testing.T, rows []audit.Row, eventType string) audit.Row {
	t.Helper()
	for _, r := range rows {
		if r.EventType == eventType {
			return r
		}
	}
	t.Fatalf("no persisted audit row with event_type %q in %d rows", eventType, len(rows))
	return audit.Row{}
}

// findOCSFRecord decodes each OCSF envelope item and returns the record
// whose unmapped.lenny surfaces the given event type via its Lenny
// event-type field. It matches on the target_resource the create emits
// so the correct row is selected even if the chain grows.
func findOCSFRecord(t *testing.T, items []json.RawMessage, eventType string) map[string]any {
	t.Helper()
	for _, it := range items {
		var rec map[string]any
		if err := json.Unmarshal(it, &rec); err != nil {
			t.Fatalf("decode OCSF item: %v", err)
		}
		unmapped, _ := rec["unmapped"].(map[string]any)
		lenny, _ := unmapped["lenny"].(map[string]any)
		if lenny != nil && lenny["target_resource"] == "acme" {
			return rec
		}
	}
	t.Fatalf("no OCSF record for event %q in %d items", eventType, len(items))
	return nil
}
