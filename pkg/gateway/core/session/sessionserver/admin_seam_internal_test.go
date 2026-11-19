// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/session/state"
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
	if got := revokedRow(seam.Session{ID: "sess-1", TenantID: "acme", Stored: &own}); got.FailureReason != "carried" {
		t.Errorf("own row: FailureReason = %q, want the carried row", got.FailureReason)
	}
	other := sessionstore.Session{ID: "sess-1", TenantID: "globex", FailureReason: "carried"}
	got := revokedRow(seam.Session{ID: "sess-1", TenantID: "acme", UserID: "alice", Stored: &other})
	if got.FailureReason != "" || got.TenantID != "acme" || got.UserID != "alice" {
		t.Errorf("other tenant's row: got %+v, want the record's own fields", got)
	}
}

// TestAdminSeamForceTerminateCarriesTheWrittenRow checks that the core's
// GWSEAM-ForceTerminate moves the session to failed, returns a record that
// carries the written row with the state the session left, runs the
// terminal pipeline on that row, and answers a session missing from the
// tenant as ErrSessionNotFound.
//
// spec: GWSEAM-ForceTerminate
func TestAdminSeamForceTerminateCarriesTheWrittenRow(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	if err := store.Create(ctx, sessionstore.Session{ID: "sess-1", TenantID: "acme", UserID: "alice", State: session.StateRunning}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	var piped []sessionstore.Session
	f := ForceTerminator{Store: store, OnSessionTerminal: func(_ context.Context, _ session.State, row sessionstore.Session) {
		piped = append(piped, row)
	}}
	rec, prev, transitioned, err := forceTerminateRecord(ctx, f, "acme", "sess-1")
	if err != nil || !transitioned || prev != state.State(session.StateRunning) {
		t.Fatalf("ForceTerminate = %v, %v, %v", prev, transitioned, err)
	}
	if rec.Stored == nil || rec.ID != "sess-1" || rec.TenantID != "acme" || rec.Stored.State != session.StateFailed {
		t.Errorf("record = %+v, want sess-1 of acme carrying its failed row", rec)
	}
	if len(piped) != 1 || piped[0].State != session.StateFailed {
		t.Errorf("pipeline rows = %+v, want the failed row once", piped)
	}
	if _, _, _, err := forceTerminateRecord(ctx, f, "globex", "sess-1"); !errors.Is(err, seam.ErrSessionNotFound) {
		t.Errorf("other tenant err = %v, want ErrSessionNotFound", err)
	}
}
