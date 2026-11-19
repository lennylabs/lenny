// SPDX-License-Identifier: MIT

package seamrow_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools/seamrow"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/session/state"
)

// TestRecordAndRowCarryTheRecordsFields checks that a stored row converts
// to the session record and back with every field the record carries, and
// that the record's pool falls back to the row's pool name.
//
// spec: GWSEAM-LookupSession
func TestRecordAndRowCarryTheRecordsFields(t *testing.T) {
	at := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	row := sessionstore.Session{
		ID: "s1", TenantID: "acme", UserID: "alice", State: session.StateRunning,
		ParentSessionID: "p", RootSessionID: "r", RuntimeRef: "rt", PoolRef: "pool-a",
		DelegationDepth: 2, PodAssignment: "pod-1", LastSeq: 9,
		CreatedAt: at, UpdatedAt: at.Add(time.Second), TerminatedAt: at.Add(2 * time.Second),
	}
	rec := seamrow.Record(row)
	want := seam.Session{
		ID: "s1", TenantID: "acme", UserID: "alice", State: state.State("running"),
		ParentSessionID: "p", RootSessionID: "r", RuntimeRef: "rt", Pool: "pool-a",
		DelegationDepth: 2, PodAssignment: "pod-1", LastSeq: 9,
		CreatedAt: at, UpdatedAt: at.Add(time.Second), TerminatedAt: at.Add(2 * time.Second),
	}
	if stored, ok := rec.Stored.(sessionstore.Session); !ok || stored.ID != row.ID {
		t.Fatalf("record carries %+v, want the row", rec.Stored)
	}
	bare := rec
	bare.Stored = nil
	if bare != want {
		t.Fatalf("record = %+v, want %+v", bare, want)
	}
	if got := seamrow.Row(rec); got.ID != row.ID || got.PodAssignment != row.PodAssignment {
		t.Fatalf("row of a carrying record = %+v, want the carried row", got)
	}
	back := seamrow.Row(bare)
	if back.ID != row.ID || back.PoolRef != "pool-a" || back.DelegationDepth != 2 || back.LastSeq != 9 ||
		back.PodAssignment != "pod-1" || !back.TerminatedAt.Equal(row.TerminatedAt) || back.State != row.State {
		t.Errorf("row = %+v, want the record's fields of %+v", back, row)
	}
	row.PoolRef, row.Pool = "", "legacy-pool"
	if got := seamrow.Record(row).Pool; got != "legacy-pool" {
		t.Errorf("pool fallback = %q, want legacy-pool", got)
	}
}

// recordingTerminals records the terminal pipeline calls.
type recordingTerminals struct {
	began []string
	from  state.State
	rec   seam.Session
}

func (r *recordingTerminals) BeginTerminal(id string) func() {
	r.began = append(r.began, id)
	return func() {}
}

func (r *recordingTerminals) OnSessionTerminal(_ context.Context, from state.State, s seam.Session) {
	r.from, r.rec = from, s
}

func (r *recordingTerminals) AfterTerminalWrite(_ context.Context, _ state.State, s seam.Session, _ func(context.Context, seam.Session)) seam.Session {
	return s
}

// TestTerminalHookHandsTheCoreTheRecordOfTheWrittenRow checks that a sweep's
// terminal hook opens the core's claim and runs its pipeline on the record
// of the row the sweep wrote, from the state it left.
//
// spec: GWSEAM-BeginTerminal, GWSEAM-OnSessionTerminal
func TestTerminalHookHandsTheCoreTheRecordOfTheWrittenRow(t *testing.T) {
	core := &recordingTerminals{}
	hook := seamrow.TerminalHook(core)
	hook.BeginTerminal("s1")()
	hook.OnSessionTerminal(context.Background(), session.StateRunning, sessionstore.Session{ID: "s1", TenantID: "acme", State: session.StateExpired})
	if len(core.began) != 1 || core.began[0] != "s1" {
		t.Errorf("claims = %v", core.began)
	}
	if core.from != "running" || core.rec.ID != "s1" || core.rec.State != "expired" {
		t.Errorf("pipeline ran from %q on %+v", core.from, core.rec)
	}
}
