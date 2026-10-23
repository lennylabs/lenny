// SPDX-License-Identifier: MIT

package lifecyclefence

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// Residue is what a tenant still holds in the stores a World models: its
// session rows, reservations, user rows, transcript entries, blob objects,
// checkpoint manifests, TokenStore rows, and credential leases, and whether
// the tenant registry admits its bearers.
type Residue struct {
	Sessions, Reservations, Users, TranscriptEntries int
	Objects, Manifests, Tokens, Leases               int
	Registered                                       bool
}

// Erased reports whether the tenant holds nothing and is not registered,
// which is what a deletion that has left Phase 4 leaves behind.
func (r Residue) Erased() bool { return r == Residue{} }

// Residue reads what the World's tenant still holds.
func (w *World) Residue(ctx context.Context) (Residue, error) {
	rows, err := w.S.Sessions.List(ctx, w.Tenant, sessionstore.ListFilter{Primary: true})
	if err != nil {
		return Residue{}, fmt.Errorf("list sessions of %s: %w", w.Tenant, err)
	}
	r := Residue{
		Sessions:  len(rows),
		Objects:   w.Objects.TenantCount(w.Tenant),
		Manifests: w.Manifests.TenantCount(w.Tenant),
		Tokens:    w.Tokens.Count(w.Tenant),
		Leases:    len(w.Leases.Held(w.Tenant, "")),
	}
	if r.Reservations, r.Users, err = w.userResidue(ctx); err != nil {
		return Residue{}, err
	}
	if r.TranscriptEntries, err = w.transcriptResidue(ctx, rows); err != nil {
		return Residue{}, err
	}
	if r.Registered, err = w.registered(ctx); err != nil {
		return Residue{}, err
	}
	return r, nil
}

// userResidue counts the reservations and the user rows of the World's
// users.
func (w *World) userResidue(ctx context.Context) (reservations, users int, err error) {
	for _, u := range Users {
		ids, err := w.S.Sessions.ReservedSessionIDs(ctx, w.Tenant, u)
		if err != nil {
			return 0, 0, fmt.Errorf("list reservations of %s: %w", u, err)
		}
		reservations += len(ids)
		_, err = w.S.Users.Get(ctx, w.Tenant, u)
		switch {
		case err == nil:
			users++
		case !errors.Is(err, userstore.ErrNotFound):
			return 0, 0, fmt.Errorf("read user %s: %w", u, err)
		}
	}
	return reservations, users, nil
}

// transcriptResidue sums the transcript entries under every session ID the
// World knows of and every listed row.
func (w *World) transcriptResidue(ctx context.Context, rows []sessionstore.Session) (int, error) {
	ids := w.knownSessionIDs()
	for _, r := range rows {
		ids[r.ID] = true
	}
	total := 0
	for id := range ids {
		n, err := w.S.transcriptLen(ctx, w.Tenant, id)
		if err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}

// knownSessionIDs returns every session ID the World created or seeded.
func (w *World) knownSessionIDs() map[string]bool {
	ids := map[string]bool{}
	for _, o := range w.Outcomes() {
		ids[o.ID] = true
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, id := range w.sources {
		ids[id] = true
	}
	for _, live := range w.seededLive {
		for _, id := range live {
			ids[id] = true
		}
	}
	for id := range w.parentCommits {
		ids[id] = true
	}
	return ids
}

// registered reports whether the tenant registry admits the tenant: the
// registered reading of its row, and false for a missing row.
//
// spec: §10.2 (Authentication: `tenant_id` extraction), §12.8 (Tenant deletion lifecycle)
func (w *World) registered(ctx context.Context) (bool, error) {
	t, err := w.S.Tenants.Get(ctx, w.Tenant)
	if errors.Is(err, tenantstore.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read tenant %s: %w", w.Tenant, err)
	}
	return t.Registered(), nil
}

// checkResidue runs the residue invariant: once the deletion has left Phase
// 4, the tenant still holds nothing, and a twin holds exactly what it held
// when the pair was built.
func (w *World) checkResidue(t testing.TB, ctx context.Context) {
	t.Helper()
	if w.Phase4Done() {
		r, err := w.Residue(ctx)
		if err != nil {
			t.Error(err)
		} else if !r.Erased() {
			t.Errorf("tenant %s still holds %+v after its deletion left Phase 4", w.Tenant, r)
		}
	}
	if w.twin == nil {
		return
	}
	got, err := w.twin.Residue(ctx)
	if err != nil {
		t.Error(err)
		return
	}
	if got != w.twinBaseline {
		t.Errorf("twin tenant %s changed from %+v to %+v: an operation on %s crossed tenants", w.twin.Tenant, w.twinBaseline, got, w.Tenant)
	}
}
