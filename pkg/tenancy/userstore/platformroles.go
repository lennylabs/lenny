// SPDX-License-Identifier: MIT

package userstore

import (
	"context"
	"errors"

	"github.com/lennylabs/lenny/pkg/auth"
)

// PlatformRoles adapts a Store into the §10.2 platform-managed role
// resolver the bearer middleware consults (authmw.PlatformRoleResolver).
// Every process that authorizes on a principal's roles wires it over the
// same users table, so a tenant-admin's downgrade takes effect on the
// gateway and on lenny-ops at the same request rather than at token
// expiry.
//
// When a row carries a platform-managed assignment (RoleAssigned), even
// one whose Roles slice is empty, its Roles fully replace the OIDC claim,
// so a tenant-admin can downgrade a user with an over-broad OIDC claim by
// recording an explicit, possibly empty, assignment. A row with no
// assignment (the state DELETE /v1/admin/tenants/{id}/users/{userId}/role
// leaves) or a missing row leaves the token's roles claim authoritative.
// Any other store error is returned, and the middleware fails the request
// closed on it.
//
// spec: §10.2 (Authentication) "Role assignment"; §15.1 (the tenant user
// role endpoints); §25.4 (The lenny-ops Service: Authentication).
type PlatformRoles struct {
	// Store is the user registry. A nil Store resolves no assignment for
	// any principal, which is the dev path of a process with no
	// Postgres.
	Store Store
}

// ResolveRoles returns the platform-managed roles of (tenantID, subject)
// and whether an assignment exists for the principal.
func (r PlatformRoles) ResolveRoles(ctx context.Context, tenantID, subject string) ([]auth.Role, bool, error) {
	if r.Store == nil {
		return nil, false, nil
	}
	row, err := r.Store.Get(ctx, tenantID, subject)
	if errors.Is(err, ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return append([]auth.Role(nil), row.Roles...), row.RoleAssigned, nil
}
