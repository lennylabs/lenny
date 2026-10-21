// SPDX-License-Identifier: MIT

package podsession

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
)

// scriptedReleaser returns the scripted errors in order, then nil, and
// records each call.
type scriptedReleaser struct {
	errs  []error
	calls int
}

func (r *scriptedReleaser) ReleaseSlot(ctx context.Context, _, _ string, _, _ bool) (bool, error) {
	r.calls++
	if ctx.Err() != nil {
		return false, fmt.Errorf("%w: %w", podclaim.ErrSlotCounterRelease, ctx.Err())
	}
	if len(r.errs) == 0 {
		return true, nil
	}
	err := r.errs[0]
	r.errs = r.errs[1:]
	return false, err
}

// counterErr is a slot-set release failure as SlotClaimer.ReleaseSlot
// wraps it.
func counterErr() error {
	return fmt.Errorf("%w: redis unreachable", podclaim.ErrSlotCounterRelease)
}

// TestReleaseSlotRetryingRetriesCounterFailuresOnly_spec_5_2 pins the
// bounded detached retry of the keyed slot release: a counter failure is
// repeated until it succeeds, the repeats stop when the detached bound ends,
// and a disposition failure after the member was removed is returned at once
// with no second ReleaseSlot call.
//
// spec: §5.2 (Slot assignment atomicity), §6.2 (leaked slot semantics)
func TestReleaseSlotRetryingRetriesCounterFailuresOnly_spec_5_2(t *testing.T) {
	var pauses []time.Duration
	b := &Binder{slotReleasePause: func(ctx context.Context, d time.Duration) error {
		pauses = append(pauses, d)
		return ctx.Err()
	}}

	t.Run("counter failures then success", func(t *testing.T) {
		pauses = nil
		r := &scriptedReleaser{errs: []error{counterErr(), counterErr(), counterErr()}}
		recycled, err := b.releaseSlotRetrying(context.Background(), r, "pod", "s1", true, false)
		if err != nil || !recycled || r.calls != 4 {
			t.Fatalf("= (%v, %v) after %d calls, want (true, nil) after 4", recycled, err, r.calls)
		}
		if fmt.Sprint(pauses) != "[100ms 200ms 400ms]" {
			t.Fatalf("pauses = %v, want doubling from 100ms", pauses)
		}
	})

	t.Run("disposition failure is not repeated", func(t *testing.T) {
		disp := errors.New("delete claim: apiserver unreachable")
		r := &scriptedReleaser{errs: []error{disp}}
		_, err := b.releaseSlotRetrying(context.Background(), r, "pod", "s1", false, false)
		if !errors.Is(err, disp) || r.calls != 1 {
			t.Fatalf("= %v after %d calls, want the disposition error after exactly 1 call", err, r.calls)
		}
	})

	t.Run("stops at the detached bound", func(t *testing.T) {
		stop := &Binder{slotReleasePause: func(context.Context, time.Duration) error {
			return context.DeadlineExceeded
		}}
		r := &scriptedReleaser{errs: []error{counterErr(), counterErr(), counterErr()}}
		_, err := stop.releaseSlotRetrying(context.Background(), r, "pod", "s1", false, false)
		if !errors.Is(err, podclaim.ErrSlotCounterRelease) || r.calls != 1 {
			t.Fatalf("= %v after %d calls, want the counter error after 1 call", err, r.calls)
		}
	})

	t.Run("runs detached from a cancelled caller", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		r := &scriptedReleaser{}
		if _, err := b.releaseSlotRetrying(ctx, r, "pod", "s1", false, false); err != nil || r.calls != 1 {
			t.Fatalf("= %v after %d calls, want success on the detached context", err, r.calls)
		}
	})

	t.Run("at least two attempts within the real bound", func(t *testing.T) {
		real := &Binder{}
		r := &scriptedReleaser{errs: []error{counterErr()}}
		if _, err := real.releaseSlotRetrying(context.Background(), r, "pod", "s1", false, false); err != nil || r.calls != 2 {
			t.Fatalf("= %v after %d calls, want success on the second attempt", err, r.calls)
		}
	})
}

// TestRetryPauseIsBounded pins the pause schedule's cap.
//
// spec: §5.2 (Slot assignment atomicity)
func TestRetryPauseIsBounded(t *testing.T) {
	if got := retryPause(10); got != slotReleaseMaxPause {
		t.Fatalf("retryPause(10) = %v, want %v", got, slotReleaseMaxPause)
	}
	if retryPause(0)*4 >= slotCleanupFloor {
		t.Fatal("the first pauses must leave room for several attempts inside the detached bound")
	}
}
