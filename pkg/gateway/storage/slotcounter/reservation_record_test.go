// SPDX-License-Identifier: MIT

package slotcounter_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/alicebob/miniredis/v2"

	"github.com/lennylabs/lenny/pkg/gateway/storage/slotcounter"
)

// The reservation record (F-12.4.38): with a FallbackSource wired, every
// Reserve writes the session's record before the Redis script or the §12.4
// gate runs, a failed reservation deletes the record it inserted, and every
// Release deletes it. These tests pin the record on both paths so the Redis
// path and the gate count the same reservations.

// spec: §12.4 (Redis failure behavior), §5.2 (Slot assignment atomicity).
// diagnosis: the Redis fast path admitted a reservation without a durable
// record, so a gate started by a later Redis outage did not count it, or a
// refused reservation left a record that over-counts the pod.
func TestReserveRecordsTheReservationOnTheRedisPath_spec_12_4(t *testing.T) {
	for _, tc := range []struct {
		name       string
		bound      int32
		preHeld    int // members already in the set
		wantErr    error
		wantRecord bool
	}{
		{name: "admitted keeps the record", bound: 2, wantRecord: true},
		{name: "admitted at the last slot keeps the record", bound: 2, preHeld: 1, wantRecord: true},
		{name: "exhausted deletes the record", bound: 1, preHeld: 1, wantErr: slotcounter.ErrSlotsExhausted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fb := newFakeFallback()
			c, _, _ := newCounterNoSource(t, slotcounter.WithFallbackSource(fb))
			ctx := context.Background()
			for i := 0; i < tc.preHeld; i++ {
				id := fmt.Sprintf("held-%d", i)
				if _, _, err := c.Reserve(ctx, "pod-1", id, tc.bound); err != nil {
					t.Fatalf("pre-hold %s: %v", id, err)
				}
			}
			_, _, err := c.Reserve(ctx, "pod-1", "s", tc.bound)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Reserve = %v, want %v", err, tc.wantErr)
			}
			if got := fb.hasRecord("pod-1", "s"); got != tc.wantRecord {
				t.Errorf("record present = %v, want %v", got, tc.wantRecord)
			}
			if fb.callCount("pod-1") != 0 {
				t.Errorf("the gate ran %d times while Redis was reachable, want 0", fb.callCount("pod-1"))
			}
		})
	}
}

// spec: §12.4 (Redis failure behavior), §5.2 (Slot assignment atomicity).
// diagnosis: a record that existed before the reservation (a retried
// reservation of the same session) was deleted by the failure cleanup, so
// the session's earlier admission stopped counting.
func TestReserveKeepsAPreexistingRecordOnFailure_spec_12_4(t *testing.T) {
	fb := newFakeFallback()
	c, _, _ := newCounterNoSource(t, slotcounter.WithFallbackSource(fb))
	ctx := context.Background()
	if _, err := fb.RecordSlotReservation(ctx, "pod-1", "s"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Reserve(ctx, "pod-1", "other", 1); err != nil {
		t.Fatalf("Reserve other: %v", err)
	}
	if _, _, err := c.Reserve(ctx, "pod-1", "s", 1); !errors.Is(err, slotcounter.ErrSlotsExhausted) {
		t.Fatalf("Reserve s = %v, want ErrSlotsExhausted", err)
	}
	if !fb.hasRecord("pod-1", "s") {
		t.Error("the failure cleanup deleted a record this call did not insert")
	}
}

// spec: §12.4 (Redis failure behavior; fail closed when Postgres is
// unavailable).
// diagnosis: a reservation whose record could not be written was admitted,
// so a later outage's gate could not count it.
func TestReserveFailsClosedWhenTheRecordCannotBeWritten_spec_12_4(t *testing.T) {
	fb := newFakeFallback()
	fb.recordErr = errors.New("postgres unavailable")
	c, _, cl := newCounterNoSource(t, slotcounter.WithFallbackSource(fb))
	_, _, err := c.Reserve(context.Background(), "pod-1", "s", 4)
	if !errors.Is(err, slotcounter.ErrFailClosed) {
		t.Fatalf("Reserve = %v, want ErrFailClosed", err)
	}
	wantMembers(t, cl, "pod-1")
}

// spec: §12.4 (Redis failure behavior), §5.2 (Slot assignment atomicity).
// diagnosis: the §12.4 gate's admission or refusal left the record in the
// wrong state: a refusal must leave no record, an admission must keep one.
func TestFallbackReserveRecordsOnlyAdmissions_spec_12_4(t *testing.T) {
	fb := newFakeFallback()
	c := downedCounter(t, slotcounter.WithFallbackSource(fb))
	ctx := context.Background()
	if _, _, err := c.Reserve(ctx, "pod-1", "a", 1); err != nil {
		t.Fatalf("Reserve a: %v", err)
	}
	if !fb.hasRecord("pod-1", "a") {
		t.Error("the gate's admission of a left no record")
	}
	if _, _, err := c.Reserve(ctx, "pod-1", "b", 1); !errors.Is(err, slotcounter.ErrSlotsExhausted) {
		t.Fatalf("Reserve b = %v, want ErrSlotsExhausted", err)
	}
	if fb.hasRecord("pod-1", "b") {
		t.Error("the gate's refusal of b left a record")
	}
}

// spec: §12.4 (Redis failure behavior), §5.2 (Slot assignment atomicity).
// diagnosis: a release left the session's record, so the gate kept
// counting a slot the session no longer holds after its post-terminal
// window, or a release refused because Redis is down kept the record.
func TestReleaseDeletesTheRecord_spec_12_4(t *testing.T) {
	for _, leaked := range []bool{false, true} {
		t.Run(fmt.Sprintf("leaked %v redis up", leaked), func(t *testing.T) {
			fb := newFakeFallback()
			c, _, _ := newCounterNoSource(t, slotcounter.WithFallbackSource(fb))
			ctx := context.Background()
			if _, _, err := c.Reserve(ctx, "pod-1", "s", 2); err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := c.Release(ctx, "pod-1", "s", leaked); err != nil {
				t.Fatalf("Release: %v", err)
			}
			if fb.hasRecord("pod-1", "s") {
				t.Error("record present after the release")
			}
		})
	}
	t.Run("redis down", func(t *testing.T) {
		fb := newFakeFallback()
		c := downedCounter(t, slotcounter.WithFallbackSource(fb))
		ctx := context.Background()
		if _, _, err := c.Reserve(ctx, "pod-1", "s", 2); err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := c.Release(ctx, "pod-1", "s", false); err == nil {
			t.Fatal("Release with Redis down returned no error")
		}
		if fb.hasRecord("pod-1", "s") {
			t.Error("record present after a release Redis refused")
		}
	})
	t.Run("cancelled context still deletes", func(t *testing.T) {
		fb := newFakeFallback()
		c, _, _ := newCounterNoSource(t, slotcounter.WithFallbackSource(fb))
		if _, _, err := c.Reserve(context.Background(), "pod-1", "s", 2); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, _, _, _ = c.Release(ctx, "pod-1", "s", false)
		if fb.hasRecord("pod-1", "s") {
			t.Error("record present after a release on a cancelled context")
		}
	})
}

// spec: §12.4 (Redis failure behavior), §5.2 (Slot assignment atomicity).
// diagnosis: a reservation bound for the §12.4 gate carried a record
// written outside the gate's lock, so concurrent reservations still
// deciding counted each other and refused admissions the gate had room for;
// or a reservation the Redis script admitted while an outage was being
// recovered left no record.
func TestReservationRecordAroundAKnownOutage_spec_12_4(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)
	fb := newFakeFallback()
	c := slotcounter.New(fastClient(t, mr), slotcounter.WithFallbackSource(fb))

	// Redis becomes unreachable: the first reservation writes its record,
	// finds Redis down, deletes that record, and the gate records it again
	// under its lock.
	mr.Close()
	if _, _, err := c.Reserve(ctx, "pod-1", "a", 4); err != nil {
		t.Fatalf("Reserve a: %v", err)
	}
	if !fb.hasRecord("pod-1", "a") || fb.callCount("pod-1") != 1 {
		t.Fatalf("a: record %v, gate calls %d; want the gate's record", fb.hasRecord("pod-1", "a"), fb.callCount("pod-1"))
	}
	// The outage is known now: b writes no record before the gate.
	if _, _, err := c.Reserve(ctx, "pod-1", "b", 4); err != nil {
		t.Fatalf("Reserve b: %v", err)
	}
	if fb.recordCalls["b"] != 0 {
		t.Errorf("b wrote %d records outside the gate during a known outage, want 0", fb.recordCalls["b"])
	}
	if !fb.hasRecord("pod-1", "b") {
		t.Error("the gate's admission of b left no record")
	}
	// Redis returns. The outage is still known until the rebuild ends, so
	// d writes no record first and records itself after the script admits.
	if err := mr.Restart(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Reserve(ctx, "pod-1", "d", 4); err != nil {
		t.Fatalf("Reserve d: %v", err)
	}
	if fb.callCount("pod-1") != 2 {
		t.Errorf("gate calls = %d, want 2: d ran on the Redis script", fb.callCount("pod-1"))
	}
	if !fb.hasRecord("pod-1", "d") {
		t.Error("d, admitted by the script during the recovery, has no record")
	}
}

// spec: §12.4 (Redis failure behavior).
// diagnosis: a Counter with no FallbackSource tried to write a record.
func TestReserveWithoutFallbackWritesNoRecord_spec_12_4(t *testing.T) {
	c, _, cl := newCounterNoSource(t)
	if _, _, err := c.Reserve(context.Background(), "pod-1", "s", 2); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	wantMembers(t, cl, "pod-1", "s")
	if _, _, _, err := c.Release(context.Background(), "pod-1", "s", false); err != nil {
		t.Fatalf("Release: %v", err)
	}
}
