// SPDX-License-Identifier: MIT

package podsession_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/storage/slotcounter"
)

// emptySlotSource is a SlotSource whose pods hold no slot.
type emptySlotSource struct{}

func (emptySlotSource) GetActiveSlotsByPod(context.Context, string) ([]string, error) {
	return nil, nil
}

func (emptySlotSource) ListPodsWithActiveSlots(context.Context) ([]string, error) {
	return nil, nil
}

// spec: 5.2 (Post-recovery rehydration atomicity)
// diagnosis: a slot release that found the pod's rehydrated flag absent
// rehydrated the set without recording the lenny_slot_rehydration_total
// event, or recorded it under a pool label other than the pod's. A release
// through ReleaseSlotReservation and one through Binder.ReleaseSlot each
// record the event once with the pod and its warm-pool label.
func TestSlotReleaseRecordsRehydrationOnce_spec_5_2(t *testing.T) {
	ns := testNamespace(t)
	c := k8sClient(t, idleSandbox(ns, "sbx-1", "10.244.1.7"), idleSandbox(ns, "sbx-2", "10.244.1.8"))
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	b := newBinder(ns, c, nil)
	b.SlotCounter = slotcounter.New(rc, slotcounter.WithSlotSource(emptySlotSource{}))
	var mu sync.Mutex
	var events []string
	b.Rehydration = func(pod, pool string) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, pod+"/"+pool)
	}
	ctx := context.Background()
	if err := b.ReleaseSlotReservation(ctx, "sbx-1", "s1", false); err != nil {
		t.Fatalf("ReleaseSlotReservation: %v", err)
	}
	if err := b.ReleaseSlot(ctx, &podsession.BindResult{SandboxName: "sbx-2", SessionID: "s2", SlotID: "s2"}); err != nil {
		t.Fatalf("ReleaseSlot: %v", err)
	}
	// A second release on a rehydrated pod records nothing.
	if err := b.ReleaseSlotReservation(ctx, "sbx-1", "s1", false); err != nil {
		t.Fatalf("repeat ReleaseSlotReservation: %v", err)
	}
	want := fmt.Sprintf("[sbx-1/%s sbx-2/%s]", testPool, testPool)
	if got := fmt.Sprint(events); got != want {
		t.Fatalf("rehydration events = %s, want %s", got, want)
	}
}
