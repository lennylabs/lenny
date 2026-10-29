//go:build contract

// SPDX-License-Identifier: MIT

// Tier-3 contract test for the §15.1 extension-denial clear on the admin
// REST wire:
//
//	DELETE /v1/admin/trees/{rootSessionId}/subtrees/{sessionId}/extension-denial
//
// The route resolves the root row from the session store before the clear
// and clears the denial under the row's tenant. The test drives the
// production admin router over the production store-backed budget source,
// a memstore session store, and a MemoryDenialStore, and asserts the status
// codes, the error envelopes, and the denial state the clear leaves behind.
//
// spec: §15.1 (REST API), §8.6 (Extension state), §24.11
package admin_extension_denial_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

const (
	root = "11111111-1111-1111-1111-111111111111"
	sub  = "22222222-2222-2222-2222-222222222222"
	path = "/v1/admin/trees/" + root + "/subtrees/" + sub + "/extension-denial"
)

// failingSessions is a session store whose reads fail.
type failingSessions struct{ sessionstore.Store }

func (failingSessions) Get(context.Context, string, string) (sessionstore.Session, error) {
	return sessionstore.Session{}, errors.New("session store down")
}

func (failingSessions) GetByID(context.Context, string) (sessionstore.Session, error) {
	return sessionstore.Session{}, errors.New("session store down")
}

// fixture wires the router over a store-backed source whose acme tree is
// denied.
type fixture struct {
	router  *admin.Router
	denials *leasecontrol.MemoryDenialStore
}

func newFixture(t *testing.T, sessions sessionstore.Store) fixture {
	t.Helper()
	denials := leasecontrol.NewMemoryDenialStore(nil)
	if err := denials.Deny(context.Background(), "acme", root, time.Hour); err != nil {
		t.Fatalf("seed denial: %v", err)
	}
	src, err := leasecontrol.NewStoreBudgetSource(sessions, denials, leasecontrol.ExtensionConfig{})
	if err != nil {
		t.Fatalf("NewStoreBudgetSource: %v", err)
	}
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithLeaseDenials(src).WithSessions(sessions)
	return fixture{router: router, denials: denials}
}

func acmeSessions(t *testing.T) sessionstore.Store {
	t.Helper()
	s := memstore.New()
	if err := s.Create(context.Background(), sessionstore.Session{ID: root, TenantID: "acme", UserID: "alice@acme.com"}); err != nil {
		t.Fatalf("seed root row: %v", err)
	}
	return s
}

func asPrincipal(req *http.Request, tenant string, role pkgauth.Role) *http.Request {
	return req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject: "admin@" + tenant, TenantID: tenant, Roles: []pkgauth.Role{role},
	}))
}

func (f fixture) serve(req *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	f.router.Handler().ServeHTTP(rr, req)
	return rr
}

func (f fixture) denied(t *testing.T) bool {
	t.Helper()
	d, _, err := f.denials.Denied(context.Background(), "acme", root)
	if err != nil {
		t.Fatalf("Denied: %v", err)
	}
	return d
}

func envelopeCode(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error envelope %q: %v", rr.Body.String(), err)
	}
	return body.Code
}

// spec: §15.1 (REST API), §8.6 (Extension state), §24.11
// diagnosis: the clear route did not clear the denial under the root row's
// tenant. A platform-admin (tenant platform) clear of an acme tree must
// answer 204 with no body and leave the acme row undenied; a clear under
// the principal's tenant answers 204 and leaves the user's denial in force.
func TestClearExtensionDenialPlatformAdminClearsRowTenant(t *testing.T) {
	f := newFixture(t, acmeSessions(t))
	rr := f.serve(asPrincipal(httptest.NewRequest(http.MethodDelete, path, nil), "platform", pkgauth.RolePlatformAdmin))
	if rr.Code != http.StatusNoContent || rr.Body.Len() != 0 {
		t.Fatalf("status %d body %q, want 204 with no body", rr.Code, rr.Body.String())
	}
	if f.denied(t) {
		t.Fatal("acme denial still in force after a platform-admin clear")
	}
}

// spec: §15.1 (REST API), §10.2
// diagnosis: the clear route leaked across tenants or rejected the owner. A
// globex tenant-admin must get the RESOURCE_NOT_FOUND envelope for an acme
// tree and leave its denial in force, and the acme tenant-admin must clear
// it.
func TestClearExtensionDenialTenantAdminConfined(t *testing.T) {
	f := newFixture(t, acmeSessions(t))
	rr := f.serve(asPrincipal(httptest.NewRequest(http.MethodDelete, path, nil), "globex", pkgauth.RoleTenantAdmin))
	if rr.Code != http.StatusNotFound || envelopeCode(t, rr) != "RESOURCE_NOT_FOUND" {
		t.Fatalf("foreign tenant-admin: status %d body %s, want 404 RESOURCE_NOT_FOUND", rr.Code, rr.Body.String())
	}
	if !f.denied(t) {
		t.Fatal("a cross-tenant request cleared the acme denial")
	}
	rr = f.serve(asPrincipal(httptest.NewRequest(http.MethodDelete, path, nil), "acme", pkgauth.RoleTenantAdmin))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("owning tenant-admin: status %d body %s, want 204", rr.Code, rr.Body.String())
	}
	if f.denied(t) {
		t.Fatal("acme denial still in force after the owner's clear")
	}
}

// spec: §15.1 (REST API)
// diagnosis: the clear route answered an unresolvable root with the wrong
// envelope or ran the clear anyway. An unknown root is RESOURCE_NOT_FOUND,
// a session-store failure is INTERNAL_ERROR, and neither clears the denial.
func TestClearExtensionDenialRootReadErrors(t *testing.T) {
	f := newFixture(t, acmeSessions(t))
	rr := f.serve(asPrincipal(httptest.NewRequest(http.MethodDelete,
		"/v1/admin/trees/33333333-3333-3333-3333-333333333333/subtrees/"+sub+"/extension-denial", nil),
		"platform", pkgauth.RolePlatformAdmin))
	if rr.Code != http.StatusNotFound || envelopeCode(t, rr) != "RESOURCE_NOT_FOUND" {
		t.Fatalf("unknown root: status %d body %s, want 404 RESOURCE_NOT_FOUND", rr.Code, rr.Body.String())
	}

	f = newFixture(t, failingSessions{Store: memstore.New()})
	rr = f.serve(asPrincipal(httptest.NewRequest(http.MethodDelete, path, nil), "acme", pkgauth.RoleTenantAdmin))
	if rr.Code != http.StatusInternalServerError || envelopeCode(t, rr) != "INTERNAL_ERROR" {
		t.Fatalf("store error: status %d body %s, want 500 INTERNAL_ERROR", rr.Code, rr.Body.String())
	}
	if !f.denied(t) {
		t.Fatal("a failed root read still cleared the denial")
	}
}
