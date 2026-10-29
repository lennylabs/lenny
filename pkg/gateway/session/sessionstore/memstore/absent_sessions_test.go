// SPDX-License-Identifier: MIT

package memstore_test

import (
	"context"
	"sort"
	"testing"

	"github.com/google/uuid"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// spec: §11.2, §10.1.1
//
// ListAbsentSessions returns exactly the identifiers with no row, across
// tenants: a deleted row and a never-created UUID are absent, a live or a
// terminal row in either tenant is present, and a string that is not a UUID
// is absent because it can name no row in the Postgres store this mirrors.
func TestListAbsentSessionsReturnsMissingRows_spec_11_2(t *testing.T) {
	ctx := context.Background()
	s := memstore.New()
	ids := make([]string, 4)
	tenants := []string{"acme", "globex", "acme", "globex"}
	for i := range ids {
		ids[i] = uuid.NewString()
		if err := s.Create(ctx, sessionstore.Session{ID: ids[i], TenantID: tenants[i], State: session.StateRunning}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Update(ctx, "globex", ids[1], func(r *sessionstore.Session) error {
		r.State = session.StateCompleted
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "globex", ids[3]); err != nil {
		t.Fatal(err)
	}
	missing := uuid.NewString()

	got, err := s.ListAbsentSessions(ctx, append(append([]string(nil), ids...), missing, "not-a-uuid"))
	if err != nil {
		t.Fatalf("ListAbsentSessions: %v", err)
	}
	want := []string{ids[3], missing, "not-a-uuid"}
	sort.Strings(got)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("ListAbsentSessions = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ListAbsentSessions = %v, want %v", got, want)
		}
	}
	if got, err := s.ListAbsentSessions(ctx, nil); err != nil || len(got) != 0 {
		t.Fatalf("ListAbsentSessions(nil) = (%v, %v), want none", got, err)
	}
}
