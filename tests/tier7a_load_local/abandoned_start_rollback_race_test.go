// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local ordering coverage for the failure rollback of a start
// whose slot a compensating Shutdown reclaimed while Runtime.Start ran.
//
// StartSession holds no per-slot guard between its claim and Runtime.Start.
// The gateway's context can expire inside that window, and its compensating
// Shutdown then reclaims the attempt's entry. A retry of the same session on
// the same pod creates a fresh entry under the same slot identifier. When the
// abandoned attempt's Runtime.Start then fails, its rollback must leave the
// successor's entry, token and staged tree in place and open no reclaim hold
// on the identifier, because the §4.7.1 stamp-once rule gives that entry to
// the successor.
//
// spec: §4.7.1 (role and gateway RPC contract), §5.2 (slot-identifier reclaim
// hold), §7.1 (normal flow).
package tier7a_load_local_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// failingStartRuntime is a hookRuntime whose first Start for failSession
// parks until unpark closes and then fails, standing in for a Runtime.Start
// that fails on the abandoned attempt's cancelled inbound context.
type failingStartRuntime struct {
	*hookRuntime
	failSession    string
	parked, unpark chan struct{}
	once           sync.Once
}

func newFailingStartRuntime(sessionID string) *failingStartRuntime {
	return &failingStartRuntime{
		hookRuntime: &hookRuntime{},
		failSession: sessionID,
		parked:      make(chan struct{}),
		unpark:      make(chan struct{}),
	}
}

func (r *failingStartRuntime) Start(ctx context.Context, sessionID string) error {
	failed := false
	if sessionID == r.failSession {
		r.once.Do(func() {
			close(r.parked)
			<-r.unpark
			failed = true
		})
	}
	if failed {
		return errors.New("runtime start failed on a cancelled context")
	}
	return r.hookRuntime.Start(ctx, sessionID)
}

// spec: §4.7.1 (role and gateway RPC contract); §5.2 (slot-identifier reclaim hold); §7.1 (normal flow)
//
// diagnosis: the failure rollback of a StartSession whose slot was reclaimed
// while Runtime.Start ran released the slot by session identifier alone and
// removed the entry a later attempt at the same session had created. The
// successor then loses its staged uploads and credential directory, and when
// the removal runs unguarded the identifier stays held for the life of the
// pod, so every later bind of that session there is refused.
func TestAnAbandonedStartsLateFailureLeavesTheSuccessorsEntry_spec_4_7_1(t *testing.T) {
	for i := range bindRaceIterations {
		abandonedStartFailureIteration(t, i)
	}
}

func abandonedStartFailureIteration(t *testing.T, i int) {
	t.Helper()
	rt := newFailingStartRuntime("alice")
	s := reclaimPod(t, rt)
	if err := assignCreds(s, "alice", "attempt-a"); err != nil {
		t.Fatalf("iteration %d: assign alice for attempt a: %v", i, err)
	}
	startDone := make(chan error, 1)
	go func() { startDone <- startSession(s, "alice") }()
	<-rt.parked

	resp, err := fencedReclaim(s, "alice", "attempt-a")
	if err != nil || resp.GetSlotReclaim() != adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_RECLAIMED {
		t.Fatalf("iteration %d: compensating Shutdown = (%v, %v), want RECLAIMED", i, resp.GetSlotReclaim(), err)
	}
	if err := singleFramePrepare(s, prepareFrameFor("alice", "attempt-b", false)); err != nil {
		t.Fatalf("iteration %d: stage attempt b's upload: %v", i, err)
	}
	if err := assignCreds(s, "alice", "attempt-b"); err != nil {
		t.Fatalf("iteration %d: assign alice for attempt b: %v", i, err)
	}

	close(rt.unpark)
	if err := <-startDone; status.Code(err) != codes.Internal {
		t.Fatalf("iteration %d: abandoned StartSession = %v, want the runtime-start failure", i, err)
	}
	if !slotTreeExists(s, "alice") {
		t.Errorf("iteration %d: the abandoned rollback removed attempt b's staged tree", i)
	}
	// A request naming attempt b is admitted only while attempt b's entry
	// stands and no reclaim hold is open: an absent entry answers the
	// removal as ABSENT, and a held identifier refuses the bind.
	if err := assignCreds(s, "alice", "attempt-b"); err != nil {
		t.Errorf("iteration %d: attempt b's repeat bind = %v, want admitted", i, err)
	}
	resp, err = fencedReclaim(s, "alice", "attempt-b")
	if err != nil || resp.GetSlotReclaim() != adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_RECLAIMED {
		t.Errorf("iteration %d: Shutdown naming attempt b = (%v, %v), want RECLAIMED of the surviving entry", i, resp.GetSlotReclaim(), err)
	}
}
