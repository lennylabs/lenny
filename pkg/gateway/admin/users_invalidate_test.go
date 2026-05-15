// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/userstore"
)

// spec: §11.4 three-tier user invalidation.

func seedUser(t *testing.T, store userstore.Store, tenant, subject string) {
	t.Helper()
	if err := store.Create(context.Background(), userstore.User{
		Subject:  subject,
		TenantID: tenant,
		Roles:    []pkgauth.Role{pkgauth.RoleUser},
	}); err != nil {
		t.Fatalf("seed user %q: %v", subject, err)
	}
}

func invalidateUser(t *testing.T, h http.Handler, subject string, body admin.InvalidateUserRequest, as func(*http.Request) *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := as(httptest.NewRequest(http.MethodPost,
		"/v1/admin/users/"+subject+"/invalidate", bytes.NewReader(b)))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestInvalidateUserSoftDisable(t *testing.T) {
	router, store, audit := newUserAdmin(t)
	seedUser(t, store, "acme", "alice@acme.com")

	rr := invalidateUser(t, router.Handler(), "alice@acme.com",
		admin.InvalidateUserRequest{TenantID: "acme", Mode: admin.InvalidateSoftDisable, Reason: "ticket-42"},
		withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("soft_disable: status %d, body %s", rr.Code, rr.Body.String())
	}

	got, err := store.Get(context.Background(), "acme", "alice@acme.com")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.Disabled {
		t.Error("soft_disable must set the disabled flag")
	}
	if !got.DeletedAt.IsZero() {
		t.Error("soft_disable must not raise the deleted_at tombstone")
	}
	if snap := audit.snapshot(); len(snap) != 1 || snap[0].Type != "admin.user.invalidated" {
		t.Errorf("audit: %+v", snap)
	}
}

func TestInvalidateUserHardDisable(t *testing.T) {
	router, store, _ := newUserAdmin(t)
	seedUser(t, store, "acme", "bob@acme.com")

	rr := invalidateUser(t, router.Handler(), "bob@acme.com",
		admin.InvalidateUserRequest{TenantID: "acme", Mode: admin.InvalidateHardDisable},
		withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("hard_disable: status %d, body %s", rr.Code, rr.Body.String())
	}

	got, err := store.Get(context.Background(), "acme", "bob@acme.com")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.Disabled || got.DeletedAt.IsZero() {
		t.Errorf("hard_disable must set disabled and the tombstone: %+v", got)
	}
}

func TestInvalidateUserFullRevoke(t *testing.T) {
	router, store, _ := newUserAdmin(t)
	seedUser(t, store, "acme", "carol@acme.com")

	rr := invalidateUser(t, router.Handler(), "carol@acme.com",
		admin.InvalidateUserRequest{TenantID: "acme", Mode: admin.InvalidateFullRevoke},
		withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("full_revoke: status %d, body %s", rr.Code, rr.Body.String())
	}

	got, err := store.Get(context.Background(), "acme", "carol@acme.com")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.Disabled || got.DeletedAt.IsZero() {
		t.Errorf("full_revoke must set disabled and the tombstone: %+v", got)
	}
}

func TestInvalidateUserRejectsUnknownMode(t *testing.T) {
	router, store, _ := newUserAdmin(t)
	seedUser(t, store, "acme", "dave@acme.com")

	rr := invalidateUser(t, router.Handler(), "dave@acme.com",
		admin.InvalidateUserRequest{TenantID: "acme", Mode: "banhammer"},
		withAdminPrincipal)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("unknown mode: status %d, want 400", rr.Code)
	}
	got, _ := store.Get(context.Background(), "acme", "dave@acme.com")
	if got.Disabled {
		t.Error("an unknown mode must not mutate the user")
	}
}

func TestInvalidateUserNotFound(t *testing.T) {
	router, _, _ := newUserAdmin(t)
	rr := invalidateUser(t, router.Handler(), "ghost@acme.com",
		admin.InvalidateUserRequest{TenantID: "acme", Mode: admin.InvalidateSoftDisable},
		withAdminPrincipal)
	if rr.Code != http.StatusNotFound {
		t.Errorf("unknown user: status %d, want 404", rr.Code)
	}
}

func TestInvalidateUserTenantAdminScoped(t *testing.T) {
	router, store, _ := newUserAdmin(t)
	seedUser(t, store, "acme", "erin@acme.com")

	// A tenant-admin omits tenantId; the handler derives it from the
	// principal's tenant.
	rr := invalidateUser(t, router.Handler(), "erin@acme.com",
		admin.InvalidateUserRequest{Mode: admin.InvalidateSoftDisable},
		func(req *http.Request) *http.Request { return withTenantAdminFor(req, "acme") })
	if rr.Code != http.StatusOK {
		t.Fatalf("tenant-admin invalidate: status %d, body %s", rr.Code, rr.Body.String())
	}
	got, _ := store.Get(context.Background(), "acme", "erin@acme.com")
	if !got.Disabled {
		t.Error("a tenant-admin must be able to invalidate a user in its own tenant")
	}
}
