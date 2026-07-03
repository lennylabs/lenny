//go:build contract

// SPDX-License-Identifier: MIT

// Tier-3 contract test for the §4.4 line 232 / §25.9 admin
// audit-egress envelope. The list and single-event endpoints under
// /v1/admin/audit-events MUST translate each canonical row through the
// §11.7 OCSF translator and return the records inside an envelope
// carrying `ocsfVersion` and `translatorVersion`. This test exercises
// the production handlers end-to-end (admin router → audit chain →
// OCSF translator → JSON encoder) so a schema regression in any layer
// surfaces as a contract violation, not as a downstream-only failure.
//
// spec: §4.4 line 232 — "the audit-egress path includes an OCSF
// translator ... the translator version and OCSF wire version are
// surfaced on every response envelope."
// spec: §25.9 — "all endpoints in this subsection return audit records
// as OCSF v1.1.0 JSON objects per the Wire Format in §11.7".
package ocsf_audit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/audit/ocsf"
	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

// adminPrincipal returns a request carrying a platform-admin
// principal so the audit-query handlers' authz check passes.
func adminPrincipal(req *http.Request) *http.Request {
	return req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
		TenantID: "platform",
		Subject:  "platform-admin@acme.com",
		Roles:    []pkgauth.Role{pkgauth.RolePlatformAdmin},
	}))
}

// newRouter constructs the same admin router shape used by the
// gateway main, with the in-memory ChainSet backing the audit chain.
func newRouter(t *testing.T) *admin.Router {
	t.Helper()
	chains := audit.NewChainSet()
	store := tenantstore.NewMemory()
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	return admin.NewRouter(store, admin.Options{
		Clock: clock,
		Audit: admin.NewChainAuditSink(chains, clock),
	}).WithAuditChains(chains)
}

// TestAdminAuditEventsListReturnsOCSFEnvelope is the §4.4 line 232
// contract on the list endpoint: the response is the OCSF envelope
// (ocsfVersion, translatorVersion, items[]) with each item satisfying
// the §11.7 OCSF v1.1.0 structural contract.
//
// spec: §4.4 line 232, §11.7.
// diagnosis: a failure means the admin audit-events list endpoint does
// not return a well-formed OCSF envelope, so SIEM consumers would
// receive a non-conformant audit payload.
func TestAdminAuditEventsListReturnsOCSFEnvelope(t *testing.T) {
	router := newRouter(t)
	// Generate one row by creating a tenant.
	body, _ := json.Marshal(admin.TenantPayload{ID: "acme"})
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, adminPrincipal(httptest.NewRequest(
		http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body),
	)))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create tenant: %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, adminPrincipal(httptest.NewRequest(
		http.MethodGet, "/v1/admin/audit-events?tenantId=platform", nil,
	)))
	if rr.Code != http.StatusOK {
		t.Fatalf("list: %d body=%s", rr.Code, rr.Body.String())
	}

	var env admin.AuditEventEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.OCSFVersion != ocsf.Version {
		t.Errorf("ocsfVersion = %q, want %q", env.OCSFVersion, ocsf.Version)
	}
	if env.TranslatorVersion != ocsf.TranslatorVersion {
		t.Errorf("translatorVersion = %q, want %q",
			env.TranslatorVersion, ocsf.TranslatorVersion)
	}
	if env.TenantID != "platform" {
		t.Errorf("tenantId = %q, want platform", env.TenantID)
	}
	if len(env.Items) != 1 {
		t.Fatalf("items: got %d, want 1", len(env.Items))
	}

	// The OCSF record must satisfy the §11.7 structural keys.
	var rec map[string]any
	if err := json.Unmarshal(env.Items[0], &rec); err != nil {
		t.Fatalf("decode OCSF record: %v", err)
	}
	for _, key := range []string{"class_uid", "category_uid", "activity_id", "type_uid", "time", "severity_id", "metadata", "unmapped"} {
		if _, ok := rec[key]; !ok {
			t.Errorf("OCSF record missing required key %q", key)
		}
	}
	// metadata.version pins the OCSF v1.1.0 wire format.
	meta := rec["metadata"].(map[string]any)
	if meta["version"] != ocsf.Version {
		t.Errorf("metadata.version = %v, want %q", meta["version"], ocsf.Version)
	}
	// unmapped.lenny_chain carries the hash-chain fields so the
	// external auditor can recompute the chain from the OCSF wire form
	// alone (§11.7 reversibility contract).
	unmapped := rec["unmapped"].(map[string]any)
	chain := unmapped["lenny_chain"].(map[string]any)
	if _, ok := chain["prev_hash"]; !ok {
		t.Errorf("unmapped.lenny_chain.prev_hash missing")
	}
	if _, ok := chain["integrity"]; !ok {
		t.Errorf("unmapped.lenny_chain.integrity missing")
	}
}

// TestAdminAuditEventsGetReturnsOCSFEnvelope is the same contract on
// the single-row endpoint /v1/admin/audit-events/{seq}.
//
// spec: §4.4 line 232, §11.7.
// diagnosis: a failure means the single-row admin audit-events endpoint
// does not return a well-formed OCSF envelope, diverging from the list
// endpoint's contract.
func TestAdminAuditEventsGetReturnsOCSFEnvelope(t *testing.T) {
	router := newRouter(t)
	body, _ := json.Marshal(admin.TenantPayload{ID: "acme"})
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, adminPrincipal(httptest.NewRequest(
		http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body),
	)))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create tenant: %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, adminPrincipal(httptest.NewRequest(
		http.MethodGet, "/v1/admin/audit-events/1?tenantId=platform", nil,
	)))
	if rr.Code != http.StatusOK {
		t.Fatalf("get: %d body=%s", rr.Code, rr.Body.String())
	}
	var env admin.AuditEventEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.OCSFVersion != ocsf.Version || env.TranslatorVersion != ocsf.TranslatorVersion {
		t.Errorf("missing version fields: %+v", env)
	}
	if len(env.Items) != 1 {
		t.Fatalf("items: got %d, want 1", len(env.Items))
	}
}

// TestAdminAuditEventsListIsOCSFNotCanonicalTuple is the negative
// version of the prior tests: the list response MUST NOT carry the
// legacy `auditEvents` field (the pre-translation canonical Postgres
// tuple). A regression that re-introduces it would fail this assertion.
//
// spec: §4.4 line 232, §11.7.
// diagnosis: a failure means the list response re-introduces the legacy
// auditEvents canonical-tuple field, leaking the pre-translation
// Postgres tuple alongside the OCSF envelope.
func TestAdminAuditEventsListIsOCSFNotCanonicalTuple(t *testing.T) {
	router := newRouter(t)
	body, _ := json.Marshal(admin.TenantPayload{ID: "acme"})
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, adminPrincipal(httptest.NewRequest(
		http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body),
	)))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create tenant: %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, adminPrincipal(httptest.NewRequest(
		http.MethodGet, "/v1/admin/audit-events?tenantId=platform", nil,
	)))
	if rr.Code != http.StatusOK {
		t.Fatalf("list: %d", rr.Code)
	}
	var raw map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	if _, regressed := raw["auditEvents"]; regressed {
		t.Errorf("response carries the legacy `auditEvents` field — this is the §4.4 line 232 OCSF-translation gap")
	}
}

// craftedGapAuditLog is a minimal AuditLog whose Rows returns a fixed
// set of rows with a sequence-number gap, so the audit-query handler
// computes an auditMetadata.suspectedGaps window on the wire without a
// live datastore. It is read-only for this contract; Append is unused.
type craftedGapAuditLog struct{ rows []audit.Row }

func (c *craftedGapAuditLog) Append(context.Context, string, string, json.RawMessage, time.Time) (audit.Row, error) {
	return audit.Row{}, nil
}

func (c *craftedGapAuditLog) Rows(context.Context, string) ([]audit.Row, error) {
	return c.rows, nil
}

func (c *craftedGapAuditLog) Verify(context.Context, string) (audit.VerifyResult, error) {
	return audit.VerifyResult{Integrity: audit.ChainGapSuspected}, nil
}

// craftGapRow builds one sealed audit row with the given seq, timestamp,
// and prev_hash, mirroring the production seal (§11.7 item 3 hash input).
func craftGapRow(seq uint64, ts time.Time, prevHash string) audit.Row {
	r := audit.Row{
		ID:                 "platform:" + strconv.FormatUint(seq, 10),
		Seq:                seq,
		TenantID:           "platform",
		EventType:          "admin.tenant.created",
		EventSchemaVersion: audit.DefaultEventSchemaVersion,
		Payload:            json.RawMessage(`{}`),
		Timestamp:          ts.UTC(),
		PrevHash:           prevHash,
	}
	r.Hash = audit.ComputeHash(r)
	return r
}

// gapRouter builds an admin router backed by a crafted AuditLog whose
// rows carry a sequence-number gap.
func gapRouter(t *testing.T, rows []audit.Row) *admin.Router {
	t.Helper()
	store := tenantstore.NewMemory()
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	return admin.NewRouter(store, admin.Options{Clock: clock}).
		WithAuditLog(&craftedGapAuditLog{rows: rows})
}

// TestAdminAuditEventsGapWindowReasonNextvalRollback pins the §25.9 line
// 3669 wire contract: a gap_suspected window whose prev_hash links across
// the gap is reported in auditMetadata.suspectedGaps with
// reason "nextval_rollback", the value the reconciled §25.9 and the
// audit-chain-gap runbook direct operators to look for in the API
// response. Against the pre-fix handler (which emitted "sequence_gap" for
// every window) this contract assertion fails.
//
// spec: §25.9 line 3668-3669 (nextval-rollback gap reason), §11.7
// (prev_hash tamper authority).
// diagnosis: a failure means the audit-events API does not emit the
// nextval_rollback gap reason the spec and runbook promise, so an
// operator following the runbook to distinguish a benign rollback gap
// from an outage gap never finds the reason value and cannot resolve the
// gap's source from the API response alone.
func TestAdminAuditEventsGapWindowReasonNextvalRollback(t *testing.T) {
	ts1 := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	ts2 := ts1.Add(time.Hour)
	row1 := craftGapRow(1, ts1, audit.GenesisPrevHash)
	// A sequence jump (1 → 5) whose prev_hash still links across the gap
	// is the benign nextval-rollback signal.
	row5 := craftGapRow(5, ts2, audit.LinkHash(row1))
	router := gapRouter(t, []audit.Row{row1, row5})

	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, adminPrincipal(httptest.NewRequest(
		http.MethodGet, "/v1/admin/audit-events?tenantId=platform", nil,
	)))
	if rr.Code != http.StatusOK {
		t.Fatalf("list: %d body=%s", rr.Code, rr.Body.String())
	}
	var env admin.AuditEventEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.AuditMetadata == nil || len(env.AuditMetadata.SuspectedGaps) != 1 {
		t.Fatalf("expected one suspected gap window, got %+v", env.AuditMetadata)
	}
	if got := env.AuditMetadata.SuspectedGaps[0].Reason; got != "nextval_rollback" {
		t.Errorf("suspectedGaps[0].reason = %q, want %q (the §25.9 line 3669 wire value)", got, "nextval_rollback")
	}
}

// TestAdminAuditEventsGapWindowNonLinkingNotOutage pins the §25.9 line
// 3668-3669 wire contract for a non-linking sequence gap: a gap whose
// prev_hash does not link across it is the tamper case, not a Postgres
// outage, so the handler must not emit an outage window for it. §25.9 line
// 3669 reserves the reason "postgres_unreachable" for a window computed
// from ops_postgres_outage_log, a subsystem the gateway does not operate,
// so the handler never emits that reason and lists no benign window for a
// non-linking gap. The tamper is carried by the per-row chainIntegrity
// verdict instead. Against the pre-fix handler (which stamped a
// non-linking gap "postgres_unreachable", mislabeling a tamper as an
// outage) this contract assertion fails.
//
// spec: §25.9 line 3668 (a non-linking prev_hash gap is tampering, not an
// outage), §25.9 line 3669 (postgres_unreachable is outage-log-covered).
// F-11.2.10.
// diagnosis: a failure means the audit-events API attributes a non-linking
// (tamper) sequence gap to a Postgres outage window on the wire, so an
// operator following the runbook classifies a committed-row tamper as a
// benign outage and takes no remediation.
func TestAdminAuditEventsGapWindowNonLinkingNotOutage(t *testing.T) {
	ts1 := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	ts2 := ts1.Add(time.Hour)
	row1 := craftGapRow(1, ts1, audit.GenesisPrevHash)
	// A sequence jump whose prev_hash does not link is the tamper case.
	row5 := craftGapRow(5, ts2,
		"deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	router := gapRouter(t, []audit.Row{row1, row5})

	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, adminPrincipal(httptest.NewRequest(
		http.MethodGet, "/v1/admin/audit-events?tenantId=platform", nil,
	)))
	if rr.Code != http.StatusOK {
		t.Fatalf("list: %d body=%s", rr.Code, rr.Body.String())
	}
	var env admin.AuditEventEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.AuditMetadata != nil {
		for _, g := range env.AuditMetadata.SuspectedGaps {
			t.Errorf("non-linking gap listed as suspectedGaps window %+v; want none (postgres_unreachable is reserved for an ops_postgres_outage_log window this handler does not compute)", g)
		}
	}
}

// Reference the context import so the file compiles even when the
// suite is reorganised; the explicit use keeps the import live.
var _ = context.Background
