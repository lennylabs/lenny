// SPDX-License-Identifier: MIT

package slotcounter_test

import (
	"context"
	"sort"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/storage/slotcounter"
)

// spec: §5.2 (Slot assignment atomicity)
//
// The stale-slot reaper removes a plain member only while another member
// remains and the rehydrated flag is present. It never empties the set,
// never rehydrates, and refuses an empty or a leaked: identifier before any
// script runs.
func TestReapRemovesOnlyUnderItsRemovalConditions_spec_5_2(t *testing.T) {
	const pod = "sbx-reap"
	ctx := context.Background()

	t.Run("a present member while another remains", func(t *testing.T) {
		c, mr, _ := newCounterNoSource(t)
		mustReserve(t, c, pod, "s1", 4)
		mustReserve(t, c, pod, "s2", 4)
		removed, err := c.Reap(ctx, pod, "s1")
		if err != nil || !removed {
			t.Fatalf("Reap = %v, %v, want removed", removed, err)
		}
		if got, _ := mr.Members("lenny:pod:{" + pod + "}:active_slots"); len(got) != 1 || got[0] != "s2" {
			t.Errorf("members = %v, want [s2]", got)
		}
	})

	t.Run("the pod's last member is kept", func(t *testing.T) {
		c, mr, _ := newCounterNoSource(t)
		mustReserve(t, c, pod, "s1", 4)
		removed, err := c.Reap(ctx, pod, "s1")
		if err != nil || removed {
			t.Fatalf("Reap = %v, %v, want kept", removed, err)
		}
		if ok, _ := mr.SIsMember("lenny:pod:{"+pod+"}:active_slots", "s1"); !ok {
			t.Error("the last member was removed")
		}
	})

	t.Run("an absent rehydrated flag changes nothing and seeds nothing", func(t *testing.T) {
		src := newFakeSource()
		src.set("s1", pod, false)
		src.set("s2", pod, false)
		c, mr, _ := newCounterNoSource(t, slotcounter.WithSlotSource(src))
		mustReserve(t, c, pod, "s1", 4)
		mustReserve(t, c, pod, "s2", 4)
		mr.Del("lenny:pod:{" + pod + "}:rehydrated")
		seeds := src.callCount(pod)
		removed, err := c.Reap(ctx, pod, "s1")
		if err != nil || removed {
			t.Fatalf("Reap = %v, %v, want kept", removed, err)
		}
		if src.callCount(pod) != seeds {
			t.Error("Reap rehydrated the pod's set")
		}
		if mr.Exists("lenny:pod:{" + pod + "}:rehydrated") {
			t.Error("Reap set the rehydrated flag")
		}
		if ok, _ := mr.SIsMember("lenny:pod:{"+pod+"}:active_slots", "s1"); !ok {
			t.Error("the member was removed with the flag absent")
		}
	})

	t.Run("a non-member changes nothing", func(t *testing.T) {
		c, mr, _ := newCounterNoSource(t)
		mustReserve(t, c, pod, "s1", 4)
		mustReserve(t, c, pod, "s2", 4)
		removed, err := c.Reap(ctx, pod, "s9")
		if err != nil || removed {
			t.Fatalf("Reap = %v, %v, want nothing removed", removed, err)
		}
		if got, _ := mr.Members("lenny:pod:{" + pod + "}:active_slots"); len(got) != 2 {
			t.Errorf("members = %v, want two", got)
		}
	})

	for _, id := range []string{"", slotcounter.LeakedMember("s1")} {
		t.Run("refuses "+id, func(t *testing.T) {
			c, mr, cl := newCounterNoSource(t)
			mustReserve(t, c, pod, "s1", 4)
			mustReserve(t, c, pod, "s2", 4)
			if _, _, _, err := c.Release(ctx, pod, "s1", true); err != nil {
				t.Fatal(err)
			}
			rec := &keyRecorder{}
			cl.AddHook(rec)
			if _, err := c.Reap(ctx, pod, id); err == nil {
				t.Fatalf("Reap(%q) returned no error", id)
			}
			if len(rec.cmds) != 0 {
				t.Errorf("Reap(%q) sent %v, want no command", id, rec.cmds)
			}
			if ok, _ := mr.SIsMember("lenny:pod:{"+pod+"}:active_slots", slotcounter.LeakedMember("s1")); !ok {
				t.Error("the leaked member was removed")
			}
		})
	}
}

// spec: §5.2 (Slot assignment atomicity)
//
// Members returns every member of the pod's set, leaked: members included,
// so the reaper sees the whole set and filters it itself.
func TestMembersReturnsEveryMember_spec_5_2(t *testing.T) {
	const pod = "sbx-members"
	ctx := context.Background()
	c, _, _ := newCounterNoSource(t)
	mustReserve(t, c, pod, "s1", 4)
	mustReserve(t, c, pod, "s2", 4)
	if _, _, _, err := c.Release(ctx, pod, "s2", true); err != nil {
		t.Fatal(err)
	}
	got, err := c.Members(ctx, pod)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	want := []string{"leaked:s2", "s1"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("Members = %v, want %v", got, want)
	}
	if empty, err := c.Members(ctx, "sbx-none"); err != nil || len(empty) != 0 {
		t.Errorf("Members of an unknown pod = %v, %v, want empty", empty, err)
	}
}
