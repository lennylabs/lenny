// SPDX-License-Identifier: MIT

package memstore

import (
	"context"
	"fmt"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// spec: §7.2 (Message delivery routing — seven paths, path 6)
// ListPodlessSuspended returns only the tenant's suspended rows with no pod,
// after the given id, at most the limit, in id order.
func TestListPodlessSuspendedPagesByID_spec_7_2(t *testing.T) {
	ctx := context.Background()
	s := New()
	mk := func(id, tenant string, st session.State, pod string) {
		t.Helper()
		if err := s.Create(ctx, sessionstore.Session{ID: id, TenantID: tenant, State: st, PodAssignment: pod}); err != nil {
			t.Fatalf("Create %s: %v", id, err)
		}
	}
	for i := 5; i >= 1; i-- {
		mk(fmt.Sprintf("00000000-0000-4000-8000-00000000000%d", i), "acme", session.StateSuspended, "")
	}
	mk("00000000-0000-4000-8000-000000000006", "acme", session.StateSuspended, "pod-1")
	mk("00000000-0000-4000-8000-000000000007", "acme", session.StateRunning, "")
	mk("00000000-0000-4000-8000-000000000008", "globex", session.StateSuspended, "")

	page, err := s.ListPodlessSuspended(ctx, "acme", "", 2)
	if err != nil {
		t.Fatalf("ListPodlessSuspended: %v", err)
	}
	if len(page) != 2 || page[0].ID != "00000000-0000-4000-8000-000000000001" || page[1].ID != "00000000-0000-4000-8000-000000000002" {
		t.Fatalf("first page = %v, want ids 1 and 2", ids(page))
	}
	rest, err := s.ListPodlessSuspended(ctx, "acme", page[1].ID, 10)
	if err != nil {
		t.Fatalf("ListPodlessSuspended: %v", err)
	}
	if got := ids(rest); len(got) != 3 || got[0] != "00000000-0000-4000-8000-000000000003" || got[2] != "00000000-0000-4000-8000-000000000005" {
		t.Fatalf("second page = %v, want ids 3, 4, 5", got)
	}
	last, _ := s.ListPodlessSuspended(ctx, "acme", rest[2].ID, 10)
	if len(last) != 0 {
		t.Fatalf("page after the last = %v, want none", ids(last))
	}
}

func ids(rows []sessionstore.Session) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out
}
