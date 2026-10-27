// SPDX-License-Identifier: MIT

package userstore_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
)

// failingStore is a Store whose Get fails with err, standing in for a
// Postgres outage.
type failingStore struct {
	userstore.Store
	err error
}

func (f failingStore) Get(context.Context, string, string) (userstore.User, error) {
	return userstore.User{}, f.err
}

// TestPlatformRoles_ResolvesStoredAssignment_spec_10_2 pins the §10.2
// precedence of the platform-managed mapping over the token's roles
// claim, row by row: an assigned row replaces the claim (an empty
// assignment included), and an unassigned row, a missing row, a row in
// another tenant, and a nil store leave the claim authoritative. A store
// error is returned so the middleware fails closed.
//
// spec: 10.2 (Authentication) "Role assignment", 15.1 (REST API) tenant user role endpoints
func TestPlatformRoles_ResolvesStoredAssignment_spec_10_2(t *testing.T) {
	ctx := context.Background()
	s := userstore.NewMemory()
	seed := []userstore.User{
		{TenantID: "acme", Subject: "alice@acme.com", Roles: []auth.Role{auth.RoleUser}, RoleAssigned: true},
		{TenantID: "acme", Subject: "bob@acme.com", Roles: nil, RoleAssigned: true},
		{TenantID: "acme", Subject: "carol@acme.com", Roles: []auth.Role{auth.RoleTenantAdmin}, RoleAssigned: false},
		{TenantID: "globex", Subject: "dave@acme.com", Roles: []auth.Role{auth.RoleUser}, RoleAssigned: true},
	}
	for _, u := range seed {
		if err := s.Create(ctx, u); err != nil {
			t.Fatalf("Create %s: %v", u.Subject, err)
		}
	}
	outage := errors.New("postgres: connection refused")
	cases := []struct {
		name      string
		resolver  userstore.PlatformRoles
		tenant    string
		subject   string
		wantRoles []auth.Role
		wantFound bool
		wantErr   error
	}{
		{name: "assigned downgrade replaces the claim", resolver: userstore.PlatformRoles{Store: s}, tenant: "acme", subject: "alice@acme.com", wantRoles: []auth.Role{auth.RoleUser}, wantFound: true},
		{name: "empty assignment replaces the claim", resolver: userstore.PlatformRoles{Store: s}, tenant: "acme", subject: "bob@acme.com", wantRoles: nil, wantFound: true},
		{name: "unassigned row keeps the claim", resolver: userstore.PlatformRoles{Store: s}, tenant: "acme", subject: "carol@acme.com", wantRoles: []auth.Role{auth.RoleTenantAdmin}, wantFound: false},
		{name: "missing row keeps the claim", resolver: userstore.PlatformRoles{Store: s}, tenant: "acme", subject: "erin@acme.com"},
		{name: "another tenant's row is not visible", resolver: userstore.PlatformRoles{Store: s}, tenant: "acme", subject: "dave@acme.com"},
		{name: "nil store resolves nothing", resolver: userstore.PlatformRoles{}, tenant: "acme", subject: "alice@acme.com"},
		{name: "store error is returned", resolver: userstore.PlatformRoles{Store: failingStore{err: outage}}, tenant: "acme", subject: "alice@acme.com", wantErr: outage},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			roles, found, err := tc.resolver.ResolveRoles(ctx, tc.tenant, tc.subject)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if found != tc.wantFound {
				t.Errorf("found = %v, want %v", found, tc.wantFound)
			}
			if tc.wantFound && !slices.Equal(roles, tc.wantRoles) {
				t.Errorf("roles = %v, want %v", roles, tc.wantRoles)
			}
		})
	}
}

// TestPlatformRoles_ReturnsACopy_spec_10_2 pins that the resolver hands
// the middleware its own slice, so a caller that edits the principal's
// roles cannot change the stored row.
//
// spec: 10.2 (Authentication) "Role assignment"
func TestPlatformRoles_ReturnsACopy_spec_10_2(t *testing.T) {
	ctx := context.Background()
	s := userstore.NewMemory()
	if err := s.Create(ctx, userstore.User{TenantID: "acme", Subject: "alice@acme.com", Roles: []auth.Role{auth.RoleUser}, RoleAssigned: true}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	roles, _, err := userstore.PlatformRoles{Store: s}.ResolveRoles(ctx, "acme", "alice@acme.com")
	if err != nil {
		t.Fatalf("ResolveRoles: %v", err)
	}
	roles[0] = auth.RolePlatformAdmin
	again, _, _ := userstore.PlatformRoles{Store: s}.ResolveRoles(ctx, "acme", "alice@acme.com")
	if again[0] != auth.RoleUser {
		t.Fatalf("stored role changed to %v through the returned slice", again[0])
	}
}
