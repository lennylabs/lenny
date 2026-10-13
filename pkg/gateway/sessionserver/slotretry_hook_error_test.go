// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/runtime/slothealth"
)

// spec: §7.3 (Retry and Resume), §5.2 (Concurrent-workspace slot retry policy)
// diagnosis: the §5.2 slot retry policy retried or released a BindSlot
// failure that carries no *SlotBindError. The binder returns a post-claim hook
// error that way after it has already released the reservation itself, so a
// second BindSlot call would claim a second slot for an attempt whose
// `resuming` write failed, and a release here would decrement the pod's
// occupancy a second time.
func TestSlotRetryStopsOnAHookErrorAndReleasesNothing_spec_7_3(t *testing.T) {
	hookErr := errors.New("post-claim hook: the resuming write found the row ended")
	binder := &fakeSlotBinder{errs: []error{hookErr}}
	_, err := applySlotRetryPolicy(context.Background(), binder, slothealth.New(), nil, nil, nil, req("pool-x", 4))
	if err != hookErr {
		t.Fatalf("applySlotRetryPolicy err = %v, want the hook error returned as is", err)
	}
	if binder.bindCall != 1 {
		t.Errorf("BindSlot calls = %d, want 1 (a hook error is not retried)", binder.bindCall)
	}
	if len(binder.released) != 0 {
		t.Errorf("released = %v, want none (the binder released the reservation before returning)", binder.released)
	}
	if len(binder.drained) != 0 {
		t.Errorf("drained = %v, want none (a hook error is not a slot failure)", binder.drained)
	}
}
