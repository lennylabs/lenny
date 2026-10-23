// SPDX-License-Identifier: MIT

package lifecyclefence

import (
	"context"
	"fmt"
	"sort"
	"testing"
)

// userSnapshot is what one user holds in the stores a World models outside
// the sessions an operation creates: the user's TokenStore rows, the leases
// the user's sessions hold, and the residue of the user's seeded replay
// source.
type userSnapshot struct {
	tokens int
	leases []string
	source residue
}

// snapshotUser reads what the user holds now.
func (w *World) snapshotUser(ctx context.Context, user string) userSnapshot {
	w.mu.Lock()
	src := w.sources[user]
	w.mu.Unlock()
	leases := w.Leases.Held(w.Tenant, user)
	sort.Strings(leases)
	return userSnapshot{
		tokens: w.Tokens.CountUser(w.Tenant, user),
		leases: leases,
		source: w.residueOf(ctx, src, map[string]bool{}),
	}
}

// snapshotUsers records every user's snapshot as the bystander baseline.
func (w *World) snapshotUsers(ctx context.Context) {
	base := make(map[string]userSnapshot, len(Users))
	for _, u := range Users {
		base[u] = w.snapshotUser(ctx, u)
	}
	w.mu.Lock()
	w.userBaseline = base
	w.mu.Unlock()
}

// markTouched records that an operation scoped to the user began: an
// erasure Start or a full_revoke request. A touched user is no bystander.
func (w *World) markTouched(user string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.touched == nil {
		w.touched = map[string]bool{}
	}
	w.touched[user] = true
}

// checkUserResidue runs the user half of the residue invariant while the
// tenant's deletion has not left Phase 4, which checkResidue covers for the
// whole tenant.
//
//   - A user whose erasure wrote its completion receipt holds no TokenStore
//     row, and the seeded replay source and every seeded live session hold
//     no row, object, manifest, transcript entry, or reservation. Lease
//     records are left out: the user path's credential_pool entry is a no-op
//     and the terminal pipeline returns a drained session's leases, which
//     the harness terminator does not model, so the Phase 4 credential_pool
//     entry deletes the records. The kept user row and the gdpr receipts are
//     allowed, and the creates are judged by checkOutcome.
//   - A bystander, a user no erasure or full_revoke targeted, still holds
//     every TokenStore row and lease and the whole seeded source it held
//     when the World was built, until a deletion step of the tenant runs.
//
// spec: §12.8 (Compliance Interfaces: DeleteByUser dependency-ordered deletion sequence), §11.4 (User Invalidation)
func (w *World) checkUserResidue(t testing.TB, ctx context.Context, erasedAt map[string]int64) {
	t.Helper()
	if w.Phase4Done() {
		return
	}
	for _, u := range Users {
		if _, erased := erasedAt[u]; erased {
			if msg := w.erasedUserResidue(ctx, u); msg != "" {
				t.Errorf("user %s of %s after its erasure completed: %s", u, w.Tenant, msg)
			}
		}
	}
	if len(w.DeletionSteps()) > 0 {
		return
	}
	w.mu.Lock()
	base, touched := w.userBaseline, w.touched
	w.mu.Unlock()
	for _, u := range Users {
		if b, ok := base[u]; ok && !touched[u] {
			if msg := bystanderChange(b, w.snapshotUser(ctx, u)); msg != "" {
				t.Errorf("bystander %s of %s changed: %s", u, w.Tenant, msg)
			}
		}
	}
}

// erasedUserResidue returns what an erased user still holds, or "".
func (w *World) erasedUserResidue(ctx context.Context, user string) string {
	if n := w.Tokens.CountUser(w.Tenant, user); n > 0 {
		return fmt.Sprintf("holds %d TokenStore rows", n)
	}
	w.mu.Lock()
	ids := append([]string{w.sources[user]}, w.seededLive[user]...)
	w.mu.Unlock()
	reserved, err := w.reservations(ctx)
	if err != nil {
		return err.Error()
	}
	for _, id := range ids {
		r := w.residueOf(ctx, id, reserved)
		if r.readErr != nil {
			return r.readErr.Error()
		}
		if !r.empty() {
			return fmt.Sprintf("seeded session %s holds %+v", id, r)
		}
	}
	return ""
}

// bystanderChange describes how a bystander's holdings changed from the
// baseline, or returns "". A lease or session the bystander gained after the
// baseline is not a change; one it lost is.
func bystanderChange(base, now userSnapshot) string {
	if now.tokens != base.tokens {
		return fmt.Sprintf("TokenStore rows %d, want %d", now.tokens, base.tokens)
	}
	held := make(map[string]bool, len(now.leases))
	for _, id := range now.leases {
		held[id] = true
	}
	for _, id := range base.leases {
		if !held[id] {
			return fmt.Sprintf("lease %s is no longer held", id)
		}
	}
	if now.source != base.source {
		return fmt.Sprintf("seeded source %+v, want %+v", now.source, base.source)
	}
	return ""
}
