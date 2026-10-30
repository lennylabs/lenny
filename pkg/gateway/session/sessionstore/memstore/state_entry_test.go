// SPDX-License-Identifier: MIT

package memstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// stateEntryCreate is one way to insert a row: through Create, or through
// CreateIfGeneration against a source row seeded at generation 1.
type stateEntryCreate struct {
	name   string
	insert func(ctx context.Context, st *memstore.Store, row sessionstore.Session) error
}

func stateEntryCreates() []stateEntryCreate {
	return []stateEntryCreate{
		{name: "Create", insert: func(ctx context.Context, st *memstore.Store, row sessionstore.Session) error {
			return st.Create(ctx, row)
		}},
		{name: "CreateIfGeneration", insert: func(ctx context.Context, st *memstore.Store, row sessionstore.Session) error {
			if err := st.Create(ctx, sessionstore.Session{ID: "sess_src", TenantID: "acme", State: session.StateRunning}); err != nil {
				return err
			}
			return st.CreateIfGeneration(ctx, row, "acme", "sess_src", 1)
		}},
	}
}

// spec: §6.2 (Pod State Machine)
//
// The store owns state_entered_at: an insert sets it to the creation
// instant whatever the caller passed, through Create and through
// CreateIfGeneration, with and without a caller-supplied CreatedAt.
func TestCreateStampsStateEntryAtCreation_spec_6_2(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	forged := start.Add(-72 * time.Hour)
	for _, c := range stateEntryCreates() {
		for _, callerCreated := range []bool{false, true} {
			name := c.name + "/store-stamped created_at"
			if callerCreated {
				name = c.name + "/caller created_at"
			}
			t.Run(name, func(t *testing.T) {
				ctx := context.Background()
				clock := newSteppedClock(start)
				st := memstore.NewWithClock(clock.read)
				row := sessionstore.Session{
					ID: "sess_new", TenantID: "acme", State: session.StateCreated,
					StateEnteredAt: forged,
				}
				if callerCreated {
					row.CreatedAt = start.Add(-time.Minute)
				}
				if err := c.insert(ctx, st, row); err != nil {
					t.Fatalf("insert: %v", err)
				}
				got, err := st.Get(ctx, "acme", "sess_new")
				if err != nil {
					t.Fatalf("Get: %v", err)
				}
				if !got.StateEnteredAt.Equal(got.CreatedAt) {
					t.Fatalf("StateEnteredAt = %v, want CreatedAt %v (caller passed %v)", got.StateEnteredAt, got.CreatedAt, forged)
				}
			})
		}
	}
}

// spec: §6.2 (Pod State Machine)
//
// Update moves state_entered_at to the write's UpdatedAt only when the
// write changes the state. A same-state write leaves it where it was, and a
// value the mutator writes is discarded on both kinds of write. A store that
// let a same-state write move the instant would extend every per-state
// deadline on each heartbeat or activity write.
func TestUpdateStampsStateEntryOnlyOnStateChange_spec_6_2(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name        string
		next        session.State
		forge       bool
		wantChanged bool
	}{
		{name: "same-state write", next: session.StateRunning, wantChanged: false},
		{name: "same-state write with a forged value", next: session.StateRunning, forge: true, wantChanged: false},
		{name: "state-changing write", next: session.StateSuspended, wantChanged: true},
		{name: "state-changing write with a forged value", next: session.StateSuspended, forge: true, wantChanged: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			clock := newSteppedClock(start)
			st := memstore.NewWithClock(clock.read)
			if err := st.Create(ctx, sessionstore.Session{ID: "sess_se", TenantID: "acme", State: session.StateRunning}); err != nil {
				t.Fatalf("Create: %v", err)
			}
			clock.advance(5 * time.Minute)
			next, forge := c.next, c.forge
			got, err := st.Update(ctx, "acme", "sess_se", func(r *sessionstore.Session) error {
				r.State = next
				if forge {
					r.StateEnteredAt = start.Add(time.Hour)
				}
				return nil
			})
			if err != nil {
				t.Fatalf("Update: %v", err)
			}
			want := start
			if c.wantChanged {
				want = got.UpdatedAt
			}
			if !got.StateEnteredAt.Equal(want) {
				t.Fatalf("returned StateEnteredAt = %v, want %v", got.StateEnteredAt, want)
			}
			stored, err := st.Get(ctx, "acme", "sess_se")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if !stored.StateEnteredAt.Equal(want) {
				t.Fatalf("stored StateEnteredAt = %v, want %v", stored.StateEnteredAt, want)
			}
		})
	}
}

// spec: §6.2 (Pod State Machine)
//
// Across a history of writes, state_entered_at always names the UpdatedAt of
// the most recent write that changed the state: a row that leaves a state
// and re-enters it carries the re-entry instant, and the same-state writes
// between them move nothing.
func TestStateEntryTracksLastStateChangeAcrossHistory_spec_6_2(t *testing.T) {
	ctx := context.Background()
	clock := newSteppedClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	st := memstore.NewWithClock(clock.read)
	if err := st.Create(ctx, sessionstore.Session{ID: "sess_hist", TenantID: "acme", State: session.StateRunning}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	created, _ := st.Get(ctx, "acme", "sess_hist")
	wantEntered := created.CreatedAt
	history := []session.State{
		session.StateRunning, session.StateRunning, session.StateSuspended,
		session.StateSuspended, session.StateRunning, session.StateRunning,
		session.StateResumePending, session.StateResuming, session.StateRunning,
	}
	prev := session.StateRunning
	for i, next := range history {
		clock.advance(time.Duration(i+1) * time.Second)
		state := next
		row, err := st.Update(ctx, "acme", "sess_hist", func(r *sessionstore.Session) error {
			r.State = state
			return nil
		})
		if err != nil {
			t.Fatalf("step %d Update: %v", i, err)
		}
		if next != prev {
			wantEntered = row.UpdatedAt
		}
		if !row.StateEnteredAt.Equal(wantEntered) {
			t.Fatalf("step %d (%s -> %s): StateEnteredAt = %v, want %v", i, prev, next, row.StateEnteredAt, wantEntered)
		}
		prev = next
	}
}
