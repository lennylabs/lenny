// SPDX-License-Identifier: MIT

package main

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	userpg "github.com/lennylabs/lenny/pkg/gateway/environment/userstore/pgstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

// opsPlatformRoles returns the §10.2 platform-managed role resolver over
// the users table lenny-ops shares with the gateway, or nil when no
// Postgres is wired. The §25.4 bearer middleware is the gateway admin
// API's, so lenny-ops applies the stored role assignment the gateway
// applies: an assigned row replaces the token's roles claim, and a lookup
// error fails the request closed with the gateway's 500
// platform_role_lookup_failed response rather than falling back to the
// claim. Without Postgres there is no stored mapping to consult, and the
// roles claim stays authoritative.
//
// spec: §10.2 (Authentication) "Role assignment"; §25.4 (The lenny-ops
// Service: Authentication).
func opsPlatformRoles(pool *pgxpool.Pool) authmw.PlatformRoleResolver {
	if pool == nil {
		return nil
	}
	return userstore.PlatformRoles{Store: userpg.New(pool)}
}
