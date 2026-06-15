// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/podclaim"
)

// fixedAttempt builds an acquire[T] that returns the supplied error on the
// first n calls and then succeeds with ok. callCount records how many times
// it ran so a test can assert the queue re-entered acquisition.
func fixedAttempt[T any](failFor int, failErr error, ok T, callCount *int32) acquire[T] {
	return func(ctx context.Context) (T, error) {
		n := atomic.AddInt32(callCount, 1)
		if int(n) <= failFor {
			var zero T
			return zero, failErr
		}
		return ok, nil
	}
}

// spec: §4.6.1 (Pool exhaustion behavior — reject keeps current behavior); §5.2
// (onPoolExhausted default reject).
// diagnosis: a reject pool that retried acquisition would change the documented
// immediate-failure behavior and could mask pool under-sizing from operators.
func TestRunWithQueueRejectReturnsImmediately(t *testing.T) {
	t.Parallel()
	q := newPodClaimQueue(time.Millisecond, time.Now)
	var calls int32
	_, err := runWithQueue(context.Background(), q, "pool-a", "reject", 30,
		fixedAttempt(10, podclaim.ErrNoIdlePod, "ok", &calls))
	if !errors.Is(err, podclaim.ErrNoIdlePod) {
		t.Fatalf("reject pool: want ErrNoIdlePod, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("reject pool must attempt exactly once, attempted %d", calls)
	}
}

// spec: §5.2 (empty onPoolExhausted defaults to reject).
// diagnosis: an empty disposition that queued would queue every pool by
// default, contradicting the spec default and surprising operators.
func TestRunWithQueueEmptyDispositionRejects(t *testing.T) {
	t.Parallel()
	q := newPodClaimQueue(time.Millisecond, time.Now)
	var calls int32
	_, err := runWithQueue(context.Background(), q, "pool-a", "", 30,
		fixedAttempt(10, podclaim.ErrNoIdlePod, "ok", &calls))
	if !errors.Is(err, podclaim.ErrNoIdlePod) {
		t.Fatalf("empty disposition: want ErrNoIdlePod, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("empty disposition must attempt exactly once, attempted %d", calls)
	}
}

// spec: §4.6.1 (queue re-enters acquisition as pods free); §5.2 (onPoolExhausted
// queue holds the request and re-attempts).
// diagnosis: a queue that did not re-attempt would reject a request that a pod
// freeing within the wait bound should have served, defeating the queue option.
func TestRunWithQueueQueueRetriesUntilSuccess(t *testing.T) {
	t.Parallel()
	q := newPodClaimQueue(time.Millisecond, time.Now)
	var calls int32
	// Fail the first two attempts (pool exhausted), succeed on the third.
	res, err := runWithQueue(context.Background(), q, "pool-a", "queue", 30,
		fixedAttempt(2, podclaim.ErrNoConcurrentSlot, "bound", &calls))
	if err != nil {
		t.Fatalf("queue retry: unexpected error %v", err)
	}
	if res != "bound" {
		t.Fatalf("queue retry: want result %q, got %q", "bound", res)
	}
	if calls < 3 {
		t.Fatalf("queue must re-enter acquisition until success, attempted %d", calls)
	}
}

// spec: §4.6.1 (queue-wait timeout returns WARM_POOL_EXHAUSTED); §5.2
// (maxQueueWaitSeconds bound); §15.1 (the sentinel maps to WARM_POOL_EXHAUSTED
// with Retry-After).
// diagnosis: a queue that never times out would hold a client indefinitely; the
// sentinel must surface so the handler returns WARM_POOL_EXHAUSTED.
func TestRunWithQueueTimeoutReturnsSentinel(t *testing.T) {
	t.Parallel()
	clk := &queueFakeClock{t: time.Unix(0, 0)}
	q := newPodClaimQueue(time.Millisecond, clk.now)
	var timeouts int32
	q.onTimeout = func(string) { atomic.AddInt32(&timeouts, 1) }

	var calls int32
	// Advance the clock past the 1s wait bound on every attempt so the queue
	// times out without sleeping in the test.
	attempt := func(ctx context.Context) (string, error) {
		atomic.AddInt32(&calls, 1)
		clk.advance(2 * time.Second)
		return "", podclaim.ErrNoIdlePod
	}
	_, err := runWithQueue(context.Background(), q, "pool-a", "queue", 1, attempt)
	if !errors.Is(err, podclaim.ErrNoIdlePod) {
		t.Fatalf("queue timeout: want ErrNoIdlePod sentinel, got %v", err)
	}
	if timeouts != 1 {
		t.Fatalf("queue timeout must increment lenny_pod_claim_timeout_total once, got %d", timeouts)
	}
}

// spec: §4.6.1 (a hard failure aborts the wait); §7.1 (a non-exhaustion error is
// not pool exhaustion).
// diagnosis: a queue that retried a non-exhaustion error (e.g. a credential or
// API failure) would mask the real cause and waste the wait budget.
func TestRunWithQueueNonExhaustionAbortsImmediately(t *testing.T) {
	t.Parallel()
	q := newPodClaimQueue(time.Millisecond, time.Now)
	hard := errors.New("api server unreachable")
	var calls int32
	_, err := runWithQueue(context.Background(), q, "pool-a", "queue", 30,
		fixedAttempt(10, hard, "ok", &calls))
	if !errors.Is(err, hard) {
		t.Fatalf("non-exhaustion error: want %v, got %v", hard, err)
	}
	if calls != 1 {
		t.Fatalf("a non-exhaustion error must not be retried, attempted %d", calls)
	}
}

// spec: §4.6.1 (a hard failure surfacing mid-wait aborts the wait).
// diagnosis: a queue that swallowed a hard failure that appeared after a few
// exhausted attempts would hang the request on a non-transient fault.
func TestRunWithQueueHardFailureMidWaitAborts(t *testing.T) {
	t.Parallel()
	q := newPodClaimQueue(time.Millisecond, time.Now)
	hard := errors.New("kms unavailable")
	var calls int32
	attempt := func(ctx context.Context) (string, error) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			return "", podclaim.ErrNoIdlePod
		}
		return "", hard
	}
	_, err := runWithQueue(context.Background(), q, "pool-a", "queue", 30, attempt)
	if !errors.Is(err, hard) {
		t.Fatalf("mid-wait hard failure: want %v, got %v", hard, err)
	}
}

// spec: §7.1 (a queued request holds no pod, slot, or claim; session_id only on
// success); §4.6.1 (context cancellation ends the wait).
// diagnosis: a queue that ignored context cancellation would leak a waiting
// goroutine and keep a FIFO slot occupied past the client's disconnect.
func TestRunWithQueueContextCancellationEndsWait(t *testing.T) {
	t.Parallel()
	q := newPodClaimQueue(50*time.Millisecond, time.Now)
	ctx, cancel := context.WithCancel(context.Background())
	attempt := func(ctx context.Context) (string, error) {
		return "", podclaim.ErrNoIdlePod
	}
	done := make(chan error, 1)
	go func() {
		_, err := runWithQueue(ctx, q, "pool-a", "queue", 30, attempt)
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled wait: want context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled wait did not return")
	}
}

// spec: §4.6.1 (the single per-pool FIFO admits the oldest waiter first).
// diagnosis: out-of-order admission would starve the earliest-arriving request,
// violating the documented FIFO ordering and the fairness operators expect.
//
// The admission order is asserted deterministically against the queue's
// internal enqueue/admit/dequeue primitives rather than racing goroutines:
// three tickets join in order, and each must be admitted (its buffered signal
// readable) only after the waiter ahead of it leaves.
func TestQueueFIFOAdmissionOrder(t *testing.T) {
	t.Parallel()
	q := newPodClaimQueue(time.Millisecond, time.Now)

	// Three waiters join pool-a in order.
	t1 := q.enqueue("pool-a")
	t2 := q.enqueue("pool-a")
	t3 := q.enqueue("pool-a")

	admitted := func(ch chan struct{}) bool {
		select {
		case <-ch:
			return true
		default:
			return false
		}
	}

	// Only the head (t1) is admitted at enqueue.
	if !admitted(t1) {
		t.Fatal("first waiter must be admitted immediately")
	}
	if admitted(t2) || admitted(t3) {
		t.Fatal("only the FIFO head may be admitted before the head leaves")
	}

	// t1 leaves: t2 (the next in line) is admitted, t3 stays.
	q.dequeue("pool-a", t1)
	if !admitted(t2) {
		t.Fatal("second waiter must be admitted after the head leaves")
	}
	if admitted(t3) {
		t.Fatal("third waiter must not jump ahead of the second")
	}

	// t2 leaves: t3 is admitted.
	q.dequeue("pool-a", t2)
	if !admitted(t3) {
		t.Fatal("third waiter must be admitted after the second leaves")
	}

	// t3 leaves: the FIFO is empty and drops the pool's depth gauge to zero.
	var lastDepth int = -1
	q.onDepth = func(_ string, depth int) { lastDepth = depth }
	q.dequeue("pool-a", t3)
	if lastDepth != 0 {
		t.Fatalf("empty FIFO must publish depth 0, got %d", lastDepth)
	}
}

// spec: §4.6.1 (yield rotates the head to the tail so the oldest waiter
// re-enters acquisition first on the next poll).
// diagnosis: a yield that re-admitted the same waiter ahead of an earlier
// arrival would starve the waiter behind it under sustained exhaustion.
func TestQueueYieldRotatesHeadToTail(t *testing.T) {
	t.Parallel()
	q := newPodClaimQueue(time.Millisecond, time.Now)
	t1 := q.enqueue("pool-a")
	t2 := q.enqueue("pool-a")

	drain := func(ch chan struct{}) {
		select {
		case <-ch:
		default:
		}
	}
	// t1 is the admitted head; consume its admission, then yield it.
	drain(t1)
	q.yield("pool-a", t1)
	// t2 (now the head) must be admitted; t1 rotated to the tail, unadmitted.
	select {
	case <-t2:
	default:
		t.Fatal("yield must admit the next waiter")
	}
	select {
	case <-t1:
		t.Fatal("yielded waiter must rejoin the tail unadmitted")
	default:
	}
}

// spec: §4.6.1 / §16.1 (the depth gauge and wait histogram measure the single
// queue).
// diagnosis: missing depth/wait emission blinds the PodClaimQueueSaturated alert
// to a saturated FIFO, so operators cannot detect pool under-sizing.
func TestRunWithQueueEmitsDepthAndWaitMetrics(t *testing.T) {
	t.Parallel()
	q := newPodClaimQueue(time.Millisecond, time.Now)
	var maxDepth int32
	var depthCalls, waitCalls int32
	q.onDepth = func(_ string, depth int) {
		atomic.AddInt32(&depthCalls, 1)
		for {
			cur := atomic.LoadInt32(&maxDepth)
			if int32(depth) <= cur || atomic.CompareAndSwapInt32(&maxDepth, cur, int32(depth)) {
				break
			}
		}
	}
	q.onWait = func(string, float64) { atomic.AddInt32(&waitCalls, 1) }

	var calls int32
	_, err := runWithQueue(context.Background(), q, "pool-a", "queue", 30,
		fixedAttempt(2, podclaim.ErrNoIdlePod, 0, &calls))
	if err != nil {
		t.Fatalf("queue retry: unexpected error %v", err)
	}
	if atomic.LoadInt32(&depthCalls) == 0 {
		t.Fatal("expected at least one depth gauge emission")
	}
	if atomic.LoadInt32(&maxDepth) < 1 {
		t.Fatalf("expected depth to reach at least 1 while waiting, got %d", maxDepth)
	}
	if atomic.LoadInt32(&waitCalls) != 1 {
		t.Fatalf("expected exactly one wait-histogram observation, got %d", waitCalls)
	}
}

// spec: §4.6.1 (nil queue is treated as reject — no queueing wiring).
// diagnosis: a nil queue must not panic; a deployment without the FIFO wired
// should behave as reject rather than crash the start path.
func TestRunWithQueueNilQueueRejects(t *testing.T) {
	t.Parallel()
	var calls int32
	_, err := runWithQueue[string](context.Background(), nil, "pool-a", "queue", 30,
		fixedAttempt(10, podclaim.ErrNoIdlePod, "ok", &calls))
	if !errors.Is(err, podclaim.ErrNoIdlePod) {
		t.Fatalf("nil queue: want ErrNoIdlePod, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("nil queue must attempt exactly once, attempted %d", calls)
	}
}

// spec: §4.6.1 (the single per-pool FIFO serializes acquisition); §5.2
// (atomic slot reservation — no over-acquisition); §7.1 (session_id only on
// success). Tier 7a concurrency: many waiters compete for a fixed pod supply.
// diagnosis: a race in the FIFO admission or yield path could hand the same
// freed pod to two waiters (over-acquisition) or deadlock a waiter; this test
// fails under -race if the queue's locking is wrong.
func TestRunWithQueueConcurrentWaitersNoOverAcquisition(t *testing.T) {
	t.Parallel()
	q := newPodClaimQueue(time.Millisecond, time.Now)

	const (
		waiters = 24
		pods    = 8
	)
	var mu sync.Mutex
	available := pods
	acquired := 0

	attempt := func(ctx context.Context) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		if available > 0 {
			available--
			acquired++
			return acquired, nil
		}
		return 0, podclaim.ErrNoIdlePod
	}

	var wg sync.WaitGroup
	var successes, timeouts int32
	for i := 0; i < waiters; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// A short wait bound so the surplus waiters time out cleanly once
			// the pod supply is gone.
			_, err := runWithQueue(context.Background(), q, "pool-a", "queue", 1, attempt)
			if err == nil {
				atomic.AddInt32(&successes, 1)
				return
			}
			if errors.Is(err, podclaim.ErrNoIdlePod) {
				atomic.AddInt32(&timeouts, 1)
				return
			}
			t.Errorf("unexpected error: %v", err)
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&successes); got != pods {
		t.Fatalf("acquired %d pods, want exactly %d (no over- or under-acquisition)", got, pods)
	}
	if got := atomic.LoadInt32(&timeouts); int(got) != waiters-pods {
		t.Fatalf("timed-out waiters = %d, want %d (waiters minus pods)", got, waiters-pods)
	}
	mu.Lock()
	defer mu.Unlock()
	if acquired != pods || available != 0 {
		t.Fatalf("pod accounting: acquired=%d available=%d, want acquired=%d available=0", acquired, available, pods)
	}
}

// queueFakeClock is a manually advanced clock for deterministic deadline
// tests. The queue reads it only from the single waiting goroutine, so no lock
// is needed.
type queueFakeClock struct {
	t time.Time
}

func (c *queueFakeClock) now() time.Time { return c.t }

func (c *queueFakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }
