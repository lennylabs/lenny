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
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/audit/ocsf"
	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
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

// Reference the context import so the file compiles even when the
// suite is reorganised; the explicit use keeps the import live.
var _ = context.Background
