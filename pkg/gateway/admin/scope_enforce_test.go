// SPDX-License-Identifier: MIT

package admin_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/common/scopes"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/openapi"
)

// scopeReq attaches a platform-admin Principal carrying the parsed
// space-separated scope claim, the way the §10.2 auth middleware attaches
// it after validating a Bearer JWT. An empty claim yields an absent scope
// set (the dev-header / no-narrowing case, §25.1 line 90).
func scopeReq(t *testing.T, req *http.Request, claim string) *http.Request {
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

// TestRouteScopesResolvesCanonicalScope asserts the §15.1 route-to-scope
// registry the admin scope gate consumes resolves the canonical
// `tools:<domain>:<action>` scope for representative routes, including
// one of the admin domains the S1/S2 taxonomy expansion added
// (`legal_hold`). A drift between the served `x-lenny-scope` and the
// canonical taxonomy would make the gate inert or mis-deny.
//
// spec: §15.1 (x-lenny-scope per operation, line 920), §25.1 (scope
// enforcement point 1, line 94).
func TestRouteScopesResolvesCanonicalScope(t *testing.T) {
	rs := openapi.NewRouteScopes()
	cases := []struct {
		name, method, path, want string
	}{
		// A static platform-CRUD route.
		{"tenants-write", http.MethodPost, "/v1/admin/tenants", "tools:tenant:write"},
		{"tenants-read", http.MethodGet, "/v1/admin/tenants", "tools:tenant:read"},
		// A newly-added admin domain from the taxonomy expansion.
		{"legal-hold-write", http.MethodPost, "/v1/admin/legal-hold", "tools:legal_hold:write"},
		// A templated route resolves through the same {param} engine the
		// live mux routes on.
		{"audit-event-read", http.MethodGet, "/v1/admin/audit-events/1", "tools:audit:read"},
		// The fine-grained destructive audit route the central registry
		// now covers (so its per-handler HasScope check could be dropped).
		{"audit-partition-drop", http.MethodPost, "/v1/admin/audit-partitions/acme/drop", "tools:audit:partition_drop"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := rs.RequiredScope(tc.method, tc.path)
			if !ok {
				t.Fatalf("RequiredScope(%s, %s): no scope resolved, want %q", tc.method, tc.path, tc.want)
			}
			if got != tc.want {
				t.Errorf("RequiredScope(%s, %s) = %q, want %q", tc.method, tc.path, got, tc.want)
			}
			// Every resolved scope must parse through the canonical
			// matcher, so the gate's Matches call is not silently inert.
			if _, err := scopes.ParseScope(got); err != nil {
				t.Errorf("resolved scope %q does not parse through scopes.ParseScope: %v", got, err)
			}
		})
	}
}

// TestAdminScopeGateBlocksBeforeHandler asserts the central §25.1 scope
// gate in Router.Handler rejects a scope-narrowed token with
// SCOPE_FORBIDDEN before the destructive handler runs, admits a
// matching-scope token, and defers an absent claim to the role ceiling.
// This pins the ADM-1 fix at the admin Router seam (the unit-tier
// counterpart of the tier-9 security regression).
//
// spec: §15.1 (scope enforcement before routing, line 914,920;
// SCOPE_FORBIDDEN, line 1030), §25.1 (line 94).
func TestAdminScopeGateBlocksBeforeHandler(t *testing.T) {
	// fakeDropper.ForceDrop sets gotTenant when the handler runs, so a
	// blank gotTenant after a request means the gate rejected before the
	// handler. The router is the one audit_partition_drop_test.go wires.
	d := &fakeDropper{}
	router := newForceDropRouter(d)
	const path = "/v1/admin/audit-partitions/acme/drop?force=true&tenantId=platform"
	const body = `{"acknowledgeDataLoss":true,"partition":"acme"}`

	// Narrowed token: tools:audit:read does not grant the route's
	// tools:audit:partition_drop, so the gate denies before the handler.
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, scopeReq(t,
		httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)), "tools:audit:read"))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("narrowed token: status %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if env.Error.Code != "SCOPE_FORBIDDEN" {
		t.Fatalf("error code = %q, want SCOPE_FORBIDDEN; body=%s", env.Error.Code, rr.Body.String())
	}
	if env.Error.Details["requiredScope"] != "tools:audit:partition_drop" {
		t.Errorf("requiredScope = %v, want tools:audit:partition_drop", env.Error.Details["requiredScope"])
	}
	if env.Error.Details["activeScope"] != "tools:audit:read" {
		t.Errorf("activeScope = %v, want tools:audit:read", env.Error.Details["activeScope"])
	}
	if d.gotTenant != "" {
		t.Error("destructive handler ran despite a scope-narrowed token (ADM-1 fail-open)")
	}

	// Matching scope passes the gate and reaches the handler.
	d.gotTenant = ""
	rr = httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, scopeReq(t,
		httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)), "tools:audit:partition_drop"))
	if rr.Code == http.StatusForbidden {
		t.Fatalf("matching-scope token rejected: body=%s", rr.Body.String())
	}
	if d.gotTenant == "" {
		t.Errorf("matching-scope token did not reach the handler (status %d)", rr.Code)
	}

	// Absent claim defers to the role ceiling.
	d.gotTenant = ""
	rr = httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, scopeReq(t,
		httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)), ""))
	if rr.Code == http.StatusForbidden {
		t.Fatalf("absent-scope token rejected (should defer to role): body=%s", rr.Body.String())
	}
	if d.gotTenant == "" {
		t.Errorf("absent-scope token did not reach the handler (status %d)", rr.Code)
	}
}
