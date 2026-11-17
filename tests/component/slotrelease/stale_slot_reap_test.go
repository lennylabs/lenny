// SPDX-License-Identifier: MIT

//go:build component

package slotrelease_test

import (
	"context"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
)

// reapWait is the limiter wait the reaper hands ReapStaleSlots, admitting
// every call.
func reapWait(context.Context) error { return nil }

// spec: 5.2 (Slot assignment atomicity)
// diagnosis: the reaper cannot reach a pod from its Sandbox, so stale members are never released.
func TestReapStaleSlotsResolvesThePodFromItsSandbox_spec_5_2(t *testing.T) {
	ctx := context.Background()

	t.Run("a stale member is torn down and removed", func(t *testing.T) {
		f := newReleaseFixture(t, &slotRuntime{})
		f.bindSlot(t, "sess-stale")
		f.bindSlot(t, "sess-live")
		released, err := f.binder.ReapStaleSlots(ctx, releasePod, []string{"sess-stale"}, reapWait)
		if err != nil || released != 1 {
			t.Fatalf("ReapStaleSlots = %d, %v, want 1 released", released, err)
		}
		if got := f.activeSlots(t); got != 1 {
			t.Errorf("pod occupancy = %d, want 1 (the live session's slot)", got)
		}
		if calls := f.shutdowns.list(); len(calls) != 1 || calls[0] != "sess-stale:" {
			t.Errorf("Shutdown calls = %v, want one for sess-stale", calls)
		}
		if !f.claimExists(t) {
			t.Error("the reaper disposed of the per-pod claim")
		}
	})

	t.Run("an unclean teardown keeps the member", func(t *testing.T) {
		f := newReleaseFixture(t, &slotRuntime{closeErr: context.DeadlineExceeded})
		f.bindSlot(t, "sess-stale")
		f.bindSlot(t, "sess-live")
		released, err := f.binder.ReapStaleSlots(ctx, releasePod, []string{"sess-stale"}, reapWait)
		if err != nil || released != 0 {
			t.Fatalf("ReapStaleSlots = %d, %v, want 0 released", released, err)
		}
		if got := f.activeSlots(t); got != 2 {
			t.Errorf("pod occupancy = %d, want 2 (the unclean slot stays counted)", got)
		}
	})

	t.Run("a missing Sandbox keeps every member", func(t *testing.T) {
		f := newReleaseFixture(t, &slotRuntime{})
		f.bindSlot(t, "sess-stale")
		f.bindSlot(t, "sess-live")
		sb := &lennyv1.Sandbox{}
		if err := f.kube.Get(ctx, client.ObjectKey{Namespace: releaseNS, Name: releasePod}, sb); err != nil {
			t.Fatalf("read the Sandbox: %v", err)
		}
		if err := f.kube.Delete(ctx, sb); err != nil {
			t.Fatalf("delete the Sandbox: %v", err)
		}
		released, err := f.binder.ReapStaleSlots(ctx, releasePod, []string{"sess-stale"}, reapWait)
		if err == nil || released != 0 {
			t.Fatalf("ReapStaleSlots = %d, %v, want an error and nothing released", released, err)
		}
		if got := f.activeSlots(t); got != 2 {
			t.Errorf("pod occupancy = %d, want 2", got)
		}
		if calls := f.shutdowns.list(); len(calls) != 0 {
			t.Errorf("Shutdown calls = %v, want none", calls)
		}
	})
}
