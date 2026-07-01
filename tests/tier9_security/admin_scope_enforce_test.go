// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security tests for the §25.1 enforcement-point-1 admin-API
// scope gate (proposal 0019 finding ADM-1 / 25.1-H1). The §15.1 admin
// middleware maps every endpoint to a canonical `tools:<domain>:<action>`
// scope via its `x-lenny-scope` OpenAPI extension and rejects a caller
// whose `scope` claim does not grant that scope with `403
// SCOPE_FORBIDDEN` before routing to any handler.
//
// The gate is exercised in-process rather than against the live Kind
// cluster: the e2e gateway runs in dev mode (global.devMode: true),
// whose dev-header auth path carries no `scope` claim, so a request
// always defers to the role ceiling and the scope-narrowing boundary is
// never reachable end-to-end without an OIDC provider in the cluster.
// Driving the genuine admin Router with an injected Principal that
// carries a parsed narrowed scope set tests the same authorization code
// path a Bearer-JWT caller exercises. The Principal here is the one the
// §10.2 auth middleware attaches after it validates the JWT and parses
// the RFC 9068 scope claim.
//
// spec: §15.1 (scope enforcement before routing, line 914,920;
// SCOPE_FORBIDDEN, line 1030), §25.1 (middleware checks scopes before
// routing, line 94).

package tier9_security_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/common/scopes"
	"github.com/lennylabs/lenny/pkg/gateway/audit/auditretention"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

// scopeFakeDropper is the §16.4 force-drop seam. It records whether the
// destructive handler ran so a test can assert the scope gate rejected
// the request before the partition was touched.
type scopeFakeDropper struct {
	called bool
}

func (d *scopeFakeDropper) ForceDrop(_ context.Context, tenantID, sub string, _ time.Time) (auditretention.ForceDropResult, error) {
	d.called = true
	return auditretention.ForceDropResult{Partition: tenantID, RequesterSub: sub, AcknowledgedDataLoss: true}, nil
}

// scopeAuditLog adapts an in-memory audit.ChainSet to the admin.AuditLog
// the audit-recovery routes require, so the force-drop and single-event
// routes register without a Postgres store. It is the same in-memory
// chain the admin package's own tests use.
type scopeAuditLog struct{ chains *audit.ChainSet }

func (a scopeAuditLog) Append(_ context.Context, tenantID, eventType string, payload json.RawMessage, at time.Time) (audit.Row, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	return a.chains.Append(tenantID, eventType, payload, at), nil
}

func (a scopeAuditLog) Rows(_ context.Context, tenantID string) ([]audit.Row, error) {
	c := a.chains.Chain(tenantID)
	if c == nil {
		return nil, nil
	}
	return c.Rows(), nil
}

func (a scopeAuditLog) Verify(_ context.Context, tenantID string) (audit.VerifyResult, error) {
	return audit.VerifyResult{Integrity: audit.ChainVerified}, nil
}

func newScopeDropRouter(d admin.AuditPartitionDropper) *admin.Router {
	return admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithAuditLog(scopeAuditLog{chains: audit.NewChainSet()}).WithAuditPruner(d)
}

func newScopeAuditEventRouter() *admin.Router {
	return admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithAuditLog(scopeAuditLog{chains: audit.NewChainSet()})
}

// scopePrincipalReq attaches a §10.2 platform-admin Principal carrying
// the given space-separated scope claim, mirroring what the auth
// middleware attaches after validating a Bearer JWT and parsing the RFC
// 9068 `scope` claim. An empty claim yields an absent scope set (the
// dev-header / no-narrowing case).
func scopePrincipalReq(t *testing.T, req *http.Request, claim string) *http.Request {
	t.Helper()
	set, err := scopes.Parse(claim)
	if err != nil {
		t.Fatalf("parse scope claim %q: %v", claim, err)
	}
	ctx := authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject:  "alice@acme.com",
		TenantID: "platform",
		Roles:    []pkgauth.Role{pkgauth.RolePlatformAdmin},
		Scopes:   set,
	})
	return req.WithContext(ctx)
}

// scopeErrorCode extracts the {error:{code}} from an admin error body.
func scopeErrorCode(t *testing.T, body string) (string, map[string]any) {
	t.Helper()
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("decode error body %q: %v", body, err)
	}
	return env.Error.Code, env.Error.Details
}

// spec: 15.1, 25.1
// diagnosis: the §25.1 admin-API scope gate did not reject a
// scope-narrowed token before routing. A platform-admin token whose
// `scope` claim excludes the destructive endpoint's `x-lenny-scope` must
// receive 403 SCOPE_FORBIDDEN (with details.requiredScope /
// details.activeScope) before the handler runs; a token carrying the
// matching scope, or no scope claim at all, must pass the gate. A
// failure here means a scope-narrowed token is honored at its full role
// ceiling — the ADM-1 fail-open this fix closes.
func TestAdminScopeGateRejectsNarrowedToken_spec_15_1_25_1(t *testing.T) {
	dropper := &scopeFakeDropper{}
	router := newScopeDropRouter(dropper)
	const path = "/v1/admin/audit-partitions/acme/drop?force=true&tenantId=platform"
	const body = `{"acknowledgeDataLoss":true,"partition":"acme"}`

	// 1. A platform-admin token narrowed to tools:audit:read (which the
	//    role would otherwise permit) is denied on the destructive drop,
	//    whose route scope is tools:audit:partition_drop.
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, scopePrincipalReq(t,
		httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)), "tools:audit:read"))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("narrowed token: status %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
	code, details := scopeErrorCode(t, rr.Body.String())
	if code != "SCOPE_FORBIDDEN" {
		t.Fatalf("narrowed token: error code %q, want SCOPE_FORBIDDEN; body=%s", code, rr.Body.String())
	}
	if got := details["requiredScope"]; got != "tools:audit:partition_drop" {
		t.Errorf("details.requiredScope = %v, want tools:audit:partition_drop", got)
	}
	if got := details["activeScope"]; got != "tools:audit:read" {
		t.Errorf("details.activeScope = %v, want tools:audit:read", got)
	}
	if dropper.called {
		t.Error("destructive force-drop handler ran despite a scope-narrowed token (ADM-1 fail-open)")
	}

	// 2. The matching scope passes the gate. The handler then runs (and
	//    returns a non-403 — here a 200 from the fake dropper).
	dropper.called = false
	rr = httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, scopePrincipalReq(t,
		httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)), "tools:audit:partition_drop"))
	if rr.Code == http.StatusForbidden {
		t.Fatalf("matching-scope token was rejected by the scope gate: body=%s", rr.Body.String())
	}
	if !dropper.called {
		t.Errorf("matching-scope token did not reach the handler (status %d, body=%s)", rr.Code, rr.Body.String())
	}

	// 3. An absent scope claim defers to the role ceiling (§25.1 line
	//    90): the platform-admin role admits the call, the gate does not
	//    narrow, and the handler runs.
	dropper.called = false
	rr = httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, scopePrincipalReq(t,
		httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)), ""))
	if rr.Code == http.StatusForbidden {
		t.Fatalf("absent-scope token was rejected by the scope gate (should defer to role): body=%s", rr.Body.String())
	}
	if !dropper.called {
		t.Errorf("absent-scope token did not reach the handler (status %d, body=%s)", rr.Code, rr.Body.String())
	}
}

// spec: 15.1, 25.1
// diagnosis: the central §25.1 scope gate regressed the
// query-parameter-conditional raw-canonical scope on GET
// /v1/admin/audit-events/{seq}. The route's coarse route-level scope is
// tools:audit:read, so a tools:audit:read token must read the ordinary
// OCSF event (the central gate admits, the handler serves it) and be
// denied SCOPE_FORBIDDEN only on ?format=raw-canonical (the
// handler-level conditional check). A failure means the central registry
// either denied the plain read (over-restrictive) or admitted the
// raw-canonical read (under-restrictive).
func TestAdminScopeGateRawCanonicalConditional_spec_25_9_3653(t *testing.T) {
	router := newScopeAuditEventRouter()
	// The single-event route returns 404 on an unknown seq once the gate
	// admits; that is a non-403 outcome, which is what this test asserts
	// for the plain OCSF read by a tools:audit:read token.
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, scopePrincipalReq(t,
		httptest.NewRequest(http.MethodGet, "/v1/admin/audit-events/1?tenantId=platform", nil),
		"tools:audit:read"))
	if rr.Code == http.StatusForbidden {
		t.Fatalf("tools:audit:read was denied the ordinary OCSF read (central gate over-restrictive): body=%s", rr.Body.String())
	}

	// The same token on ?format=raw-canonical is denied SCOPE_FORBIDDEN
	// by the handler-level conditional check.
	rr = httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, scopePrincipalReq(t,
		httptest.NewRequest(http.MethodGet, "/v1/admin/audit-events/1?tenantId=platform&format=raw-canonical", nil),
		"tools:audit:read"))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("raw-canonical with tools:audit:read: status %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
	code, details := scopeErrorCode(t, rr.Body.String())
	if code != "SCOPE_FORBIDDEN" {
		t.Errorf("raw-canonical denial: error code %q, want SCOPE_FORBIDDEN; body=%s", code, rr.Body.String())
	}
	if got := details["requiredScope"]; got != "tools:audit:raw_canonical_read" {
		t.Errorf("details.requiredScope = %v, want tools:audit:raw_canonical_read", got)
	}
}
