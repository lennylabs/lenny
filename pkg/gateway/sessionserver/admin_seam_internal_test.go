// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

type fakeBindingReleases struct{ got []string }

func (f *fakeBindingReleases) TerminateUserSessions(_ context.Context, tenantID, userID string, ids []string) seam.UserTermination {
	f.got = append(f.got, tenantID+"/"+userID)
	return seam.UserTermination{PodsTerminated: len(ids)}
}

// TestAdminSeamReleasesUserBindingsOnlyWhenWired checks that the core's
// side of GWSEAM-TerminateUserSessions releases through the binding
// releases it was given and releases nothing without them.
//
// spec: GWSEAM-TerminateUserSessions
func TestAdminSeamReleasesUserBindingsOnlyWhenWired(t *testing.T) {
	pods := &fakeBindingReleases{}
	res := (&Server{}).AdminSeam(pods).TerminateUserSessions(context.Background(), "acme", "alice", []string{"s1", "s2"})
	if res.PodsTerminated != 2 || len(pods.got) != 1 || pods.got[0] != "acme/alice" {
		t.Errorf("TerminateUserSessions = %+v via %v, want 2 released for acme/alice", res, pods.got)
	}
	if res := (&Server{}).AdminSeam(nil).TerminateUserSessions(context.Background(), "acme", "alice", []string{"s1"}); res.PodsTerminated != 0 || len(res.FailedSessions) != 0 {
		t.Errorf("unwired TerminateUserSessions = %+v, want nothing released", res)
	}
}

// TestRevokedRowUsesOnlyTheRowOfTheNamedSession checks that the revocation
// operations run on the row the record carries only when it is the row of
// the session and tenant the record names, and otherwise on the row the
// record's fields describe.
//
// spec: GWSEAM-AfterRevokeWrite, GWSEAM-ReclaimRevokedPreRunning
func TestRevokedRowUsesOnlyTheRowOfTheNamedSession(t *testing.T) {
	own := sessionstore.Session{ID: "sess-1", TenantID: "acme", FailureReason: "carried"}
	if got := revokedRow(seam.Session{ID: "sess-1", TenantID: "acme", Stored: own}); got.FailureReason != "carried" {
		t.Errorf("own row: FailureReason = %q, want the carried row", got.FailureReason)
	}
	other := sessionstore.Session{ID: "sess-1", TenantID: "globex", FailureReason: "carried"}
	got := revokedRow(seam.Session{ID: "sess-1", TenantID: "acme", UserID: "alice", Stored: other})
	if got.FailureReason != "" || got.TenantID != "acme" || got.UserID != "alice" {
		t.Errorf("other tenant's row: got %+v, want the record's own fields", got)
	}
}
