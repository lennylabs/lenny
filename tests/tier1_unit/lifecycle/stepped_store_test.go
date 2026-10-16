// SPDX-License-Identifier: MIT

package lifecycle_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
)

// TestSteppedClockStoreStampsFromTheSteppedClock_spec_6_2 pins the harness
// decorator that lets a Postgres-backed scenario run under the stepped
// clock without a clock seam in the production store: a row created and
// updated through it reads, on every read path and inside a mutator, with
// the updated_at of the stepped clock and the active age accrued against
// it, although the store underneath stamps from the wall clock.
//
// spec: §6.2 (maxSessionAge timer behavior across states), §7.3 (Retry and
// Resume)
func TestSteppedClockStoreStampsFromTheSteppedClock_spec_6_2(t *testing.T) {
	ctx := context.Background()
	stepped := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Microsecond)
	now := func() time.Time { return stepped }
	st := lifecycleharness.NewSteppedClockStore(memstore.New(), now)
	if err := st.Create(ctx, sessionstore.Session{ID: "sess-a", TenantID: "acme", State: session.StateRunning}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	created, err := st.Get(ctx, "acme", "sess-a")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !created.CreatedAt.Equal(stepped) || !created.UpdatedAt.Equal(stepped) {
		t.Fatalf("created row stamps = %v / %v, want the stepped clock %v", created.CreatedAt, created.UpdatedAt, stepped)
	}

	stepped = stepped.Add(90 * time.Second)
	var seen time.Time
	updated, err := st.Update(ctx, "acme", "sess-a", func(r *sessionstore.Session) error {
		seen = r.UpdatedAt
		r.State = session.StateSuspended
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !seen.Equal(created.UpdatedAt) {
		t.Fatalf("mutator saw updated_at %v, want the stepped stamp %v", seen, created.UpdatedAt)
	}
	if !updated.UpdatedAt.Equal(stepped) {
		t.Fatalf("Update returned updated_at %v, want %v", updated.UpdatedAt, stepped)
	}
	if got := updated.AccumulatedSessionAgeSeconds; got != 90 {
		t.Fatalf("active age after 90 stepped seconds running = %v s, want 90", got)
	}
	reads := map[string]func() (sessionstore.Session, error){
		"Get":     func() (sessionstore.Session, error) { return st.Get(ctx, "acme", "sess-a") },
		"GetByID": func() (sessionstore.Session, error) { return st.GetByID(ctx, "sess-a") },
		"List": func() (sessionstore.Session, error) {
			rows, err := st.List(ctx, "acme", sessionstore.ListFilter{})
			if err != nil || len(rows) != 1 {
				t.Fatalf("List = %d rows, %v; want one", len(rows), err)
			}
			return rows[0], nil
		},
	}
	for name, read := range reads {
		row, err := read()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !row.UpdatedAt.Equal(stepped) || row.AccumulatedSessionAgeSeconds != 90 {
			t.Fatalf("%s = updated_at %v, age %v; want %v and 90", name, row.UpdatedAt, row.AccumulatedSessionAgeSeconds, stepped)
		}
	}

	// A suspended row accrues no active age across a further step.
	stepped = stepped.Add(time.Hour)
	again, err := st.Update(ctx, "acme", "sess-a", func(*sessionstore.Session) error { return nil })
	if err != nil {
		t.Fatalf("second Update: %v", err)
	}
	if again.AccumulatedSessionAgeSeconds != 90 || !again.UpdatedAt.Equal(stepped) {
		t.Fatalf("suspended row after an hour = age %v, updated_at %v; want 90 and %v", again.AccumulatedSessionAgeSeconds, again.UpdatedAt, stepped)
	}
}
