// SPDX-License-Identifier: MIT

package me_test

import (
	"reflect"
	"testing"

	"github.com/lennylabs/lenny/pkg/auth"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/ops/me"
)

// spec: §25.4 — Me copies the principal's identity into the response
// shape, including a defensive copy of the Roles slice.
func TestService_Me(t *testing.T) {
	svc := me.NewService(nil)
	p := authmw.Principal{
		Subject:    "alice",
		TenantID:   "acme",
		SessionID:  "sess_42",
		CallerType: "user",
		Typ:        "Bearer",
		Roles:      []auth.Role{auth.RolePlatformAdmin},
	}
	id := svc.Me(p)
	if id.Subject != "alice" || id.TenantID != "acme" || id.SessionID != "sess_42" {
		t.Errorf("Me: %+v", id)
	}
	if !reflect.DeepEqual(id.Roles, []auth.Role{auth.RolePlatformAdmin}) {
		t.Errorf("Roles: %+v", id.Roles)
	}
	// Defensive copy: mutating the returned slice must not touch the
	// caller's principal.
	id.Roles[0] = "spoofed"
	if p.Roles[0] != auth.RolePlatformAdmin {
		t.Error("Me must defensively copy Roles")
	}
}

// spec: §25.4 — Me on a zero principal returns an Identity with empty
// fields and an empty (non-nil) Roles slice when no roles are set.
func TestService_Me_ZeroPrincipal(t *testing.T) {
	svc := me.NewService(nil)
	got := svc.Me(authmw.Principal{})
	if got.Subject != "" || got.TenantID != "" || len(got.Roles) != 0 {
		t.Errorf("zero principal: %+v", got)
	}
}

// spec: §25.4 — Authorized filters by MinRole; entries whose MinRole
// the principal does not satisfy are dropped.
func TestService_Authorized_RoleFilter(t *testing.T) {
	catalog := []me.AuthorizedTool{
		{Tool: "admin.bootstrap", Scope: "admin.bootstrap.write", Category: "platform", MinRole: auth.RolePlatformAdmin},
		{Tool: "admin.create_user", Scope: "admin.users.write", Category: "user", MinRole: auth.RoleTenantAdmin},
		{Tool: "admin.create_tenant", Scope: "admin.tenants.write", Category: "tenant", MinRole: auth.RolePlatformAdmin},
	}
	svc := me.NewService(catalog)
	tenantAdmin := authmw.Principal{Roles: []auth.Role{auth.RoleTenantAdmin}}
	got := svc.Authorized(tenantAdmin)
	if len(got) != 1 || got[0].Tool != "admin.create_user" {
		t.Fatalf("tenant-admin authorized: %+v", got)
	}
}

// spec: §25.4 — Authorized returns tools sorted alphabetically by
// Tool name so the response is deterministic.
func TestService_Authorized_SortedAlphabetically(t *testing.T) {
	catalog := []me.AuthorizedTool{
		{Tool: "admin.zebra", MinRole: auth.RolePlatformAdmin},
		{Tool: "admin.apple", MinRole: auth.RolePlatformAdmin},
		{Tool: "admin.mango", MinRole: auth.RolePlatformAdmin},
	}
	svc := me.NewService(catalog)
	p := authmw.Principal{Roles: []auth.Role{auth.RolePlatformAdmin}}
	got := svc.Authorized(p)
	if len(got) != 3 || got[0].Tool != "admin.apple" || got[1].Tool != "admin.mango" || got[2].Tool != "admin.zebra" {
		t.Errorf("not sorted: %+v", got)
	}
}

// spec: §25.4 — Authorized on a zero principal returns no tools.
func TestService_Authorized_ZeroPrincipal(t *testing.T) {
	svc := me.NewService([]me.AuthorizedTool{{Tool: "admin.x", MinRole: auth.RolePlatformAdmin}})
	if got := svc.Authorized(authmw.Principal{}); len(got) != 0 {
		t.Errorf("Authorized(zero): %+v", got)
	}
}
