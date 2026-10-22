// SPDX-License-Identifier: MIT

package lifecyclefence

import (
	"context"
	"sync"

	"github.com/google/uuid"

	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
)

// lease is one §4.9 credential lease a session holds.
type lease struct {
	tenant, user, session string
}

// Leases is the World's credential lease pool: the leases its sessions hold,
// keyed by lease ID. Returning a lease removes its record, as returning a
// lease to its pool revokes it and deletes its row, so a renewal of a
// returned lease fails and nothing recreates it. It satisfies
// admin.UserLeaseRevoker, which is the lease step that §11.4 full_revoke and
// §12.8 Phase 3 share.
type Leases struct {
	mu   sync.Mutex
	byID map[string]lease
	// silentFaults is the number of upcoming lease steps that return
	// nothing, as a lease-store error the lease step cannot see does.
	silentFaults int
	// silentFired counts the lease steps a silent fault swallowed.
	silentFired int
	renewals    int
}

var _ admin.UserLeaseRevoker = (*Leases)(nil)

// NewLeases returns an empty pool.
func NewLeases() *Leases { return &Leases{byID: map[string]lease{}} }

// Grant records a lease held by the session and returns its ID.
func (l *Leases) Grant(tenantID, userID, sessionID string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	id := uuid.NewString()
	l.byID[id] = lease{tenant: tenantID, user: userID, session: sessionID}
	return id
}

// Renew extends a lease and reports whether it is still held. A returned
// lease cannot be renewed back into existence.
func (l *Leases) Renew(id string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.byID[id]; !ok {
		return false
	}
	l.renewals++
	return true
}

// RenewRandom renews one random held lease, as a concurrent proxy request
// does, and reports whether one was renewed.
func (l *Leases) RenewRandom(pick func(n int) int) bool {
	l.mu.Lock()
	ids := make([]string, 0, len(l.byID))
	for id := range l.byID {
		ids = append(ids, id)
	}
	l.mu.Unlock()
	if len(ids) == 0 {
		return false
	}
	return l.Renew(ids[pick(len(ids))])
}

// FailSilently makes the next n lease steps return nothing and report no
// error. The production lease store's listing reports no error, so a
// lease-store failure inside the lease step is silent.
func (l *Leases) FailSilently(n int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.silentFaults = n
}

// SilentFired returns how many lease steps a silent fault swallowed.
func (l *Leases) SilentFired() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.silentFired
}

// RevokeUserLeases returns every lease held by the named sessions of the
// tenant and returns the count enumerated, unless a silent fault swallows
// the step.
//
// spec: §11.4 step 1 (User Invalidation), §12.8 (Tenant deletion lifecycle Phase 3)
func (l *Leases) RevokeUserLeases(tenantID, _ string, sessionIDs []string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.silentFaults > 0 {
		l.silentFaults--
		l.silentFired++
		return 0
	}
	in := make(map[string]bool, len(sessionIDs))
	for _, id := range sessionIDs {
		in[id] = true
	}
	n := 0
	for id, ls := range l.byID {
		if ls.tenant == tenantID && in[ls.session] {
			delete(l.byID, id)
			n++
		}
	}
	return n
}

// DeleteByTenant deletes every lease record of the tenant, as the Phase 4
// credential_pool entry does.
func (l *Leases) DeleteByTenant(_ context.Context, tenantID string) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for id, ls := range l.byID {
		if ls.tenant == tenantID {
			delete(l.byID, id)
			n++
		}
	}
	return n, nil
}

// Held returns the IDs of the leases the tenant still holds, restricted to
// user when user is non-empty.
func (l *Leases) Held(tenantID, user string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for id, ls := range l.byID {
		if ls.tenant == tenantID && (user == "" || ls.user == user) {
			out = append(out, id)
		}
	}
	return out
}

// HeldBySessions returns, per held lease of the tenant (of user when user
// is non-empty), the session that holds it.
func (l *Leases) HeldBySessions(tenantID, user string) map[string]string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := map[string]string{}
	for id, ls := range l.byID {
		if ls.tenant == tenantID && (user == "" || ls.user == user) {
			out[id] = ls.session
		}
	}
	return out
}

// Renewals returns the number of renewals that found their lease held.
func (l *Leases) Renewals() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.renewals
}

// Tokens is the World's §12.8 TokenStore: the connector refresh tokens and
// registered credentials of each user, counted per tenant and user.
type Tokens struct {
	mu   sync.Mutex
	rows map[[2]string]int
}

// NewTokens returns an empty TokenStore.
func NewTokens() *Tokens { return &Tokens{rows: map[[2]string]int{}} }

// Seed stores n TokenStore rows for the user.
func (k *Tokens) Seed(tenantID, user string, n int) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.rows[[2]string{tenantID, user}] += n
}

// DeleteByTenant deletes every TokenStore row of the tenant.
func (k *Tokens) DeleteByTenant(_ context.Context, tenantID string) (int, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	n := 0
	for key, c := range k.rows {
		if key[0] == tenantID {
			n += c
			delete(k.rows, key)
		}
	}
	return n, nil
}

// Count returns the number of TokenStore rows the tenant holds.
func (k *Tokens) Count(tenantID string) int {
	k.mu.Lock()
	defer k.mu.Unlock()
	n := 0
	for key, c := range k.rows {
		if key[0] == tenantID {
			n += c
		}
	}
	return n
}
