// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
)

// TestNewPodClaimQueueDefaults pins the §4.6.1 queue construction defaults: a
// non-positive poll selects DefaultQueuePollInterval and a nil clock defaults
// to wall time, so a caller that omits either still gets a usable queue.
//
// spec: 4.6.1 (Pool exhaustion behavior — bounded FIFO poll cadence)
func TestNewPodClaimQueueDefaults(t *testing.T) {
	t.Parallel()
	q := newPodClaimQueue(0, nil)
	if q.poll != DefaultQueuePollInterval {
		t.Fatalf("poll = %v, want DefaultQueuePollInterval %v", q.poll, DefaultQueuePollInterval)
	}
	if q.now == nil {
		t.Fatal("now must default to a wall clock, got nil")
	}
	// The defaulted clock must be callable and return a non-zero time.
	if q.now().IsZero() {
		t.Fatal("defaulted clock returned the zero time")
	}
}

// TestWaitInQueueZeroWaitBoundUsesDefault pins the §5.2 default
// maxQueueWaitSeconds: a zero wait bound selects DefaultMaxQueueWaitSeconds so a
// queue pool that omits the bound still waits the documented default before
// timing out, rather than timing out immediately on a zero deadline.
//
// spec: 5.2 (maxQueueWaitSeconds default), 4.6.1 (bounded queue wait)
func TestWaitInQueueZeroWaitBoundUsesDefault(t *testing.T) {
	t.Parallel()
	clk := &queueFakeClock{t: time.Unix(0, 0)}
	q := newPodClaimQueue(time.Millisecond, clk.now)
	// Succeed on the first in-queue attempt so the wait-bound branch is the only
	// thing under test; the request must not time out at a zero deadline.
	var calls int
	attempt := func(_ context.Context) (string, error) {
		calls++
		return "ok", nil
	}
	res, err := waitInQueue(context.Background(), q, "pool-a", 0, podclaim.ErrNoIdlePod, attempt)
	if err != nil {
		t.Fatalf("waitInQueue with a zero wait bound = %v, want success on the default deadline", err)
	}
	if res != "ok" {
		t.Fatalf("result = %q, want ok", res)
	}
}

// TestQueueGuardsOnMissingFIFO pins the FIFO bookkeeping guards: yield and
// dequeue on a pool with no FIFO are no-ops, admitHead on an empty FIFO is a
// no-op, and removeTicket returns the slice unchanged when the target ticket is
// absent, so a stale ticket or a double dequeue cannot panic or corrupt the
// queue.
//
// spec: 4.6.1 (FIFO admission bookkeeping)
func TestQueueGuardsOnMissingFIFO(t *testing.T) {
	t.Parallel()
	q := newPodClaimQueue(time.Millisecond, time.Now)
	ghost := make(chan struct{}, 1)

	// yield and dequeue on a pool with no FIFO must not panic.
	q.yield("absent-pool", ghost)
	q.dequeue("absent-pool", ghost)

	// admitHead on an empty FIFO is a no-op.
	q.admitHead(&poolFIFO{})

	// removeTicket returns the slice unchanged when the target is absent.
	others := []chan struct{}{make(chan struct{}), make(chan struct{})}
	got := removeTicket(others, ghost)
	if len(got) != len(others) {
		t.Fatalf("removeTicket of an absent target changed the slice length: %d, want %d", len(got), len(others))
	}
}
