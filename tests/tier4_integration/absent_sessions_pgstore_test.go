// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test for the Postgres session store's
// ListAbsentSessions, the cross-tenant read the per-replica budget state
// sweep resolves tracked session ids through, run against the production
// migrations.
package tier4_integration_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	sessionpg "github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
)

// spec: 11.2 (budget enforcement), 10.1.1 (replica-local state), 12.8 (Compliance Interfaces)
// diagnosis: the budget state sweep's Postgres read reports the wrong
// sessions as absent. It must return exactly the identifiers with no row,
// across tenants (an erased row, a never-created UUID, and a string that is
// not a UUID), and never a live or terminal row in any tenant, or a sweep
// either forgets a live session's budget state or leaks an erased one's.
func TestListAbsentSessionsOnPostgres_spec_11_2(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pg := sharedDatabase(t)
	seedTenant(t, pg, "acme")
	seedTenant(t, pg, "globex")
	store := sessionpg.New(pg.Pool)

	ids := seedSlotRows(t, ctx, store, []slotRow{
		{"acme", session.StateRunning, "", ""},   // 0: live
		{"globex", session.StateRunning, "", ""}, // 1: live, other tenant
		{"acme", session.StateRunning, "", ""},   // 2: terminal
		{"globex", session.StateRunning, "", ""}, // 3: erased, other tenant
	})
	if _, err := store.Update(ctx, "acme", ids[2], func(r *sessionstore.Session) error {
		from := r.State
		r.State = session.StateCompleted
		sessionstore.MarkTerminated(r, from, time.Now().UTC())
		return nil
	}); err != nil {
		t.Fatalf("terminal write: %v", err)
	}
	if err := store.Delete(ctx, "globex", ids[3]); err != nil {
		t.Fatalf("delete: %v", err)
	}
	missing := newSessionUUID(t)

	query := append(append([]string(nil), ids...), strings.ToUpper(ids[1]), missing, "not-a-uuid")
	got, err := store.ListAbsentSessions(ctx, query)
	if err != nil {
		t.Fatalf("ListAbsentSessions: %v", err)
	}
	if want := sortedJoin([]string{ids[3], missing, "not-a-uuid"}); sortedJoin(got) != want {
		t.Fatalf("ListAbsentSessions = %s, want %s (the erased row, the missing UUID, and the non-UUID)", sortedJoin(got), want)
	}
	if got, err := store.ListAbsentSessions(ctx, []string{"not-a-uuid"}); err != nil || len(got) != 1 {
		t.Fatalf("ListAbsentSessions(non-UUID only) = (%v, %v), want the non-UUID with no query", got, err)
	}
	if got, err := store.ListAbsentSessions(ctx, nil); err != nil || len(got) != 0 {
		t.Fatalf("ListAbsentSessions(nil) = (%v, %v), want none", got, err)
	}
}
