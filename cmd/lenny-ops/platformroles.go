// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	userpg "github.com/lennylabs/lenny/pkg/gateway/environment/userstore/pgstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

// roleLookupLogInterval bounds how often a failed role lookup is logged,
// so a Postgres outage logs one record per interval rather than one per
// request.
const roleLookupLogInterval = 30 * time.Second

// opsPlatformRoles returns the §10.2 platform-managed role resolver over
// the users table lenny-ops shares with the gateway, or nil when no
// Postgres is wired. While the lookup succeeds, an assigned row replaces
// the token's roles claim as it does on the gateway, so a tenant-admin's
// downgrade reaches lenny-ops on the next request rather than at token
// expiry. Without Postgres there is no stored mapping, and the roles
// claim stays authoritative.
//
// A failed lookup leaves the roles claim in force rather than refusing
// the request. §25.4 revocation set rule 5 has lenny-ops accept a token
// while Postgres is unreachable, rather than apply the gateway replica's
// refusal to validate, so that the operability surface stays usable
// during a Postgres outage. A request the role lookup refused would undo
// that rule. §10.2 gives the mapping precedence when both sources are
// present, and during the outage only the claim is.
//
// spec: §10.2 (Authentication) "Role assignment"; §25.4 (The lenny-ops
// Service: Authentication, revocation set rule 5).
func opsPlatformRoles(pool *pgxpool.Pool) authmw.PlatformRoleResolver {
	if pool == nil {
		return nil
	}
	return &claimOnLookupError{next: userstore.PlatformRoles{Store: userpg.New(pool)}}
}

// claimOnLookupError resolves through next and reports no assignment when
// next fails, so the middleware keeps the token's roles claim. It logs a
// failure at most once per roleLookupLogInterval.
type claimOnLookupError struct {
	next authmw.PlatformRoleResolver
	// lastLog is the Unix nanosecond time of the last logged failure.
	lastLog atomic.Int64
	// now is the clock; nil uses time.Now.
	now func() time.Time
}

// ResolveRoles implements authmw.PlatformRoleResolver.
func (r *claimOnLookupError) ResolveRoles(ctx context.Context, tenantID, subject string) ([]auth.Role, bool, error) {
	roles, found, err := r.next.ResolveRoles(ctx, tenantID, subject)
	if err == nil {
		return roles, found, nil
	}
	r.logFailure(err)
	return nil, false, nil
}

// logFailure logs err unless a failure was logged within
// roleLookupLogInterval.
func (r *claimOnLookupError) logFailure(err error) {
	now := time.Now
	if r.now != nil {
		now = r.now
	}
	t := now().UnixNano()
	last := r.lastLog.Load()
	if last != 0 && t-last < int64(roleLookupLogInterval) {
		return
	}
	if r.lastLog.CompareAndSwap(last, t) {
		slog.Warn("lenny-ops: §10.2 platform role lookup failed; authorizing on the token's roles claim", "err", err)
	}
}
