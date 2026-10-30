// SPDX-License-Identifier: MIT

package memstore_test

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// spec: §5.2 (Slot assignment atomicity), §7.2 (Interactive Session Model)
//
// ListStaleTerminalSessions returns exactly the identifiers whose rows are
// terminal with a terminated_at before the cutoff, across tenants, and
// excludes non-terminal rows, terminal rows with no terminated_at, missing
// identifiers, and strings that are not UUIDs. A relabel to failed keeps the
// first terminated_at, so the relabelled row stays eligible.
func TestListStaleTerminalSessionsSelectsEndedRows_spec_5_2(t *testing.T) {
	ctx := context.Background()
	s := memstore.New()
	cutoff := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	ids := make([]string, 6)
	tenants := []string{"acme", "globex", "acme", "acme", "acme", "acme"}
	for i := range ids {
		ids[i] = uuid.NewString()
		if err := s.Create(ctx, sessionstore.Session{ID: ids[i], TenantID: tenants[i], State: session.StateRunning}); err != nil {
			t.Fatal(err)
		}
	}
	end := func(i int, st session.State, at time.Time) {
		t.Helper()
		if _, err := s.Update(ctx, tenants[i], ids[i], func(r *sessionstore.Session) error {
			from := r.State
			r.State = st
			sessionstore.MarkTerminated(r, from, at)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	end(0, session.StateCompleted, cutoff.Add(-time.Minute))
	end(1, session.StateCancelled, cutoff.Add(-time.Second))
	end(2, session.StateExpired, cutoff)
	if _, err := s.Update(ctx, "acme", ids[4], func(r *sessionstore.Session) error {
		r.State = session.StateCompleted
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	end(5, session.StateCompleted, cutoff.Add(-time.Minute))
	end(5, session.StateFailed, cutoff.Add(time.Hour))

	query := append(append([]string(nil), ids...), uuid.NewString(), "leaked:"+ids[0], "not-a-uuid", "")
	got, err := s.ListStaleTerminalSessions(ctx, query, cutoff)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	want := []string{ids[0], ids[1], ids[5]}
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("ListStaleTerminalSessions = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ListStaleTerminalSessions = %v, want %v", got, want)
		}
	}
}
