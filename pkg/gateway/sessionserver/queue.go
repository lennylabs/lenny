// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/podclaim"
)

// DefaultMaxQueueWaitSeconds is the §5.2 / §4.6.1 fallback queue-wait bound
// applied when a `sessionPolicy.onPoolExhausted: queue` pool sets no explicit
// maxQueueWaitSeconds. It matches the spec default of 30 seconds.
// spec: §5.2 (maxQueueWaitSeconds default 30), §4.6.1 (Pool exhaustion behavior).
const DefaultMaxQueueWaitSeconds = 30

// DefaultQueuePollInterval is the default cadence at which a queued claim
// request re-enters acquisition while it waits for a pod to free. It is
// operator-tunable through Options.QueuePollInterval; the §4.6.1 spec fixes
// only the wait bound, not the poll cadence, so the cadence carries an
// overridable default rather than a hard-coded constant.
// spec: §4.6.1 (re-entering acquisition as pods free).
const DefaultQueuePollInterval = 250 * time.Millisecond

// podClaimQueue is the §4.6.1 per-pool claim FIFO parameterized by
// `sessionPolicy.onPoolExhausted`. The single queue serves both the session-
// claim and concurrent-slot acquisition paths: when an acquisition attempt
// exhausts the claim-path timeout and the Postgres fallback (surfacing one of
// the §5.2 exhaustion sentinels), a `queue` pool holds the request in this
// FIFO for up to its maxQueueWaitSeconds and re-enters acquisition as pods
// free. A `reject` pool returns the sentinel immediately, preserving the
// current behavior. A queued request holds no pod, slot, or claim, so the
// §7.1 atomicity contract is unaffected: the caller publishes the binding only
// after a successful acquisition return.
//
// The queue is scoped to session mode; service-mode messages are routed to
// ready replicas rather than claimed, so they never reach this path.
//
// spec: §4.6.1 (Pool exhaustion behavior, single per-pool claim queue), §5.2
// (onPoolExhausted, maxQueueWaitSeconds), §7.1 (session_id only on success).
type podClaimQueue struct {
	// poll is the cadence at which a queued waiter re-attempts acquisition.
	poll time.Duration
	// now supplies the wall clock for the wait-deadline and wait-duration
	// measurement; injected so a unit test can drive it deterministically.
	now func() time.Time

	// mu guards the per-pool FIFO state.
	mu sync.Mutex
	// fifos holds one ordered admission queue per pool. The queue admits one
	// waiter at a time so the oldest request re-enters acquisition first
	// (FIFO), matching the §4.6.1 "remains in the same per-pool FIFO" order.
	fifos map[string]*poolFIFO

	// onDepth, when set, publishes the §16.1 lenny_pod_claim_queue_depth gauge
	// for a pool as its queued count changes. Nil drops the emission.
	onDepth func(pool string, depth int)
	// onWait, when set, observes the §16.1 lenny_pod_claim_queue_wait_seconds
	// histogram for a pool when a queued request leaves the FIFO (whether it
	// acquired a pod or timed out). Nil drops the emission.
	onWait func(pool string, seconds float64)
	// onTimeout, when set, increments the §16.1 lenny_pod_claim_timeout_total
	// counter for a pool when a queued request exhausts its wait bound. Nil
	// drops the emission.
	onTimeout func(pool string)
}

// poolFIFO is one pool's ordered admission queue. depth is the number of
// requests currently waiting or being served. head is the channel the
// currently-admitted waiter closes when it leaves so the next waiter is
// admitted; tickets is the ordered list of per-waiter admission channels.
type poolFIFO struct {
	depth   int
	tickets []chan struct{}
}

// newPodClaimQueue builds a claim queue with the given poll cadence and clock.
// A zero poll selects DefaultQueuePollInterval; a nil clock uses time.Now.
func newPodClaimQueue(poll time.Duration, now func() time.Time) *podClaimQueue {
	if poll <= 0 {
		poll = DefaultQueuePollInterval
	}
	if now == nil {
		now = time.Now
	}
	return &podClaimQueue{
		poll:  poll,
		now:   now,
		fifos: make(map[string]*poolFIFO),
	}
}

// isExhaustion reports whether err is one of the §5.2 pool-exhaustion
// sentinels that mean both the claim-path timeout and the Postgres fallback
// found no pod or slot: ErrNoIdlePod (no pods at all), ErrNoConcurrentSlot
// (pods exist but every slot is full), or ErrTenantMismatch (the only pods
// with capacity are pinned to another tenant). These are the signals a
// `queue` pool waits on; every other error is a hard failure that aborts the
// wait. spec: §5.2 line 519, §4.6.1 (after both paths are exhausted).
func isExhaustion(err error) bool {
	return errors.Is(err, podclaim.ErrNoIdlePod) ||
		errors.Is(err, podclaim.ErrNoConcurrentSlot) ||
		errors.Is(err, podclaim.ErrTenantMismatch)
}

// acquire is the §4.6.1 acquisition attempt the queue retries: it returns a
// result on success, or an error. An exhaustion sentinel (isExhaustion) is the
// retry signal; any other error aborts.
type acquire[T any] func(ctx context.Context) (T, error)

// runWithQueue runs attempt once and, on a `queue` pool, holds the request in
// the per-pool FIFO re-entering acquisition until it succeeds, the wait bound
// elapses, or the context is cancelled. On a `reject` pool (or empty, the
// default) it returns the first attempt's result unchanged, preserving the
// current immediate-failure behavior. The queue activates only when the first
// attempt returns an exhaustion sentinel; any other error returns immediately.
//
// maxQueueWaitSeconds is the §5.2 wait bound; zero selects
// DefaultMaxQueueWaitSeconds. A timeout returns the last exhaustion sentinel
// so the caller maps it to WARM_POOL_EXHAUSTED with a Retry-After header
// exactly as the reject path does, and increments the §16.1 timeout counter.
//
// A queued request holds no pod, slot, or claim between attempts: attempt
// either acquires atomically and returns, or returns an exhaustion sentinel
// having released anything it briefly touched. The §7.1 atomicity contract is
// therefore preserved across the wait.
//
// spec: §4.6.1 (Pool exhaustion behavior), §5.2 (onPoolExhausted,
// maxQueueWaitSeconds), §7.1 (session_id only on success), §3.1 (queue
// composes after the claim-path timeout and Postgres fallback).
func runWithQueue[T any](ctx context.Context, q *podClaimQueue, pool, onPoolExhausted string, maxQueueWaitSeconds int, attempt acquire[T]) (T, error) {
	var zero T
	result, err := attempt(ctx)
	if err == nil {
		return result, nil
	}
	// Only the bounded queue extends the wait, and only on an exhaustion
	// sentinel. Every other disposition and every non-exhaustion error returns
	// the first attempt's outcome unchanged (the reject default).
	if q == nil || onPoolExhausted != "queue" || !isExhaustion(err) {
		return zero, err
	}
	return waitInQueue(ctx, q, pool, maxQueueWaitSeconds, err, attempt)
}

// waitInQueue holds the request in pool's FIFO and re-enters acquisition until
// it succeeds, the wait bound elapses, or the context ends. firstErr is the
// exhaustion sentinel the initial attempt returned, used as the timeout return
// value when the wait bound elapses before a pod frees. It is a free function
// rather than a method because the generic type parameter must live on the
// function (Go methods cannot add type parameters).
func waitInQueue[T any](
	ctx context.Context, q *podClaimQueue, pool string, maxQueueWaitSeconds int, firstErr error, attempt acquire[T],
) (T, error) {
	var zero T

	waitBound := time.Duration(maxQueueWaitSeconds) * time.Second
	if waitBound <= 0 {
		waitBound = DefaultMaxQueueWaitSeconds * time.Second
	}

	start := q.now()
	ticket := q.enqueue(pool)
	// The wait-duration histogram is observed for every queued request when it
	// leaves the FIFO, whether it acquired a pod or timed out, so the series
	// reflects the full queue residency. The dequeue runs on every return.
	defer func() {
		q.dequeue(pool, ticket)
		if q.onWait != nil {
			q.onWait(pool, q.now().Sub(start).Seconds())
		}
	}()

	// Bound the whole wait with a deadline so a cancelled or detached client
	// does not occupy a FIFO slot past the budget.
	deadline := start.Add(waitBound)

	ticker := time.NewTicker(q.poll)
	defer ticker.Stop()

	for {
		// Admission: only the FIFO head re-enters acquisition, so the oldest
		// waiter gets the next freed pod first. A non-head waiter blocks on its
		// ticket until the waiter ahead of it leaves.
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-ticket:
			// Admitted as the FIFO head; fall through to attempt acquisition.
		}

		if q.now().After(deadline) {
			// The wait bound elapsed while this request was behind others in the
			// FIFO. Return the exhaustion sentinel so the handler surfaces
			// WARM_POOL_EXHAUSTED with a Retry-After header, and count the timeout.
			if q.onTimeout != nil {
				q.onTimeout(pool)
			}
			return zero, firstErr
		}

		result, err := attempt(ctx)
		if err == nil {
			return result, nil
		}
		if !isExhaustion(err) {
			// A hard failure (not pool exhaustion) aborts the wait immediately;
			// retrying would not change the outcome.
			return zero, err
		}
		firstErr = err

		// Still exhausted: yield the FIFO head to the next waiter and wait one
		// poll interval (or until the deadline / context end) before re-queuing
		// at the head for another attempt. Re-entering acquisition here is the
		// §4.6.1 "re-enters acquisition as pods free" mechanism.
		q.yield(pool, ticket)

		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-ticker.C:
			if q.now().After(deadline) {
				if q.onTimeout != nil {
					q.onTimeout(pool)
				}
				return zero, firstErr
			}
		}
	}
}

// enqueue appends a fresh admission ticket to pool's FIFO and returns it. The
// head ticket is signalled immediately so the first waiter re-enters
// acquisition without delay; subsequent tickets are signalled as the waiters
// ahead of them leave (dequeue) or yield. The depth gauge is published.
func (q *podClaimQueue) enqueue(pool string) chan struct{} {
	q.mu.Lock()
	defer q.mu.Unlock()

	f := q.fifos[pool]
	if f == nil {
		f = &poolFIFO{}
		q.fifos[pool] = f
	}
	ticket := make(chan struct{}, 1)
	f.tickets = append(f.tickets, ticket)
	f.depth++
	if len(f.tickets) == 1 {
		// First in line: admit immediately.
		ticket <- struct{}{}
	}
	q.publishDepth(pool, f.depth)
	return ticket
}

// yield releases the FIFO head (this ticket) so the next waiter is admitted
// for its attempt, then re-appends this ticket to the tail so the request
// rejoins the FIFO in order for its next poll. The depth is unchanged: the
// request is still waiting.
func (q *podClaimQueue) yield(pool string, ticket chan struct{}) {
	q.mu.Lock()
	defer q.mu.Unlock()

	f := q.fifos[pool]
	if f == nil {
		return
	}
	// Remove this ticket from wherever it sits (the head) and re-append it.
	f.tickets = removeTicket(f.tickets, ticket)
	// Drain any pending admission signal so the re-appended ticket starts
	// unadmitted and must wait its turn again.
	select {
	case <-ticket:
	default:
	}
	f.tickets = append(f.tickets, ticket)
	q.admitHead(f)
}

// dequeue removes this ticket from pool's FIFO permanently (the request is
// leaving) and admits the new head. The depth gauge is republished.
func (q *podClaimQueue) dequeue(pool string, ticket chan struct{}) {
	q.mu.Lock()
	defer q.mu.Unlock()

	f := q.fifos[pool]
	if f == nil {
		return
	}
	before := len(f.tickets)
	f.tickets = removeTicket(f.tickets, ticket)
	if len(f.tickets) < before {
		f.depth--
	}
	if len(f.tickets) == 0 {
		delete(q.fifos, pool)
		q.publishDepth(pool, 0)
		return
	}
	q.admitHead(f)
	q.publishDepth(pool, f.depth)
}

// admitHead signals the current FIFO head's ticket so it re-enters
// acquisition. It is idempotent: the buffered channel holds at most one
// pending admission, so a double signal is dropped.
func (q *podClaimQueue) admitHead(f *poolFIFO) {
	if len(f.tickets) == 0 {
		return
	}
	head := f.tickets[0]
	select {
	case head <- struct{}{}:
	default:
		// Already admitted; nothing to do.
	}
}

// publishDepth emits the §16.1 lenny_pod_claim_queue_depth gauge for pool.
func (q *podClaimQueue) publishDepth(pool string, depth int) {
	if q.onDepth != nil {
		q.onDepth(pool, depth)
	}
}

// removeTicket returns tickets with the first occurrence of target removed,
// preserving order. It allocates a new backing slice only when target is
// found, keeping the common-case re-append cheap.
func removeTicket(tickets []chan struct{}, target chan struct{}) []chan struct{} {
	for i, t := range tickets {
		if t == target {
			return append(tickets[:i:i], tickets[i+1:]...)
		}
	}
	return tickets
}
