// SPDX-License-Identifier: MIT

package subsystem

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

// ErrLimiterStopped is returned by Limiter.Acquire when the supplied
// context is cancelled before a slot becomes available. Callers
// surface this as a 503 SERVICE_UNAVAILABLE on the §4.1 partial-
// degradation path so a saturated subsystem rejects new work rather
// than queueing it indefinitely.
var ErrLimiterStopped = errors.New("subsystem: limiter context cancelled before slot acquired")

// Limiter is the §4.1 per-subsystem max-concurrent semaphore.
//
// A subsystem bounds the number of goroutines it dedicates to a
// workload so a saturated upload queue cannot consume goroutines the
// stream proxy needs. The Limiter is a counting semaphore plus a
// queue-depth gauge: in-flight requests count against MaxConcurrent;
// requests that arrive when no slot is free count toward the queue-
// depth gauge until a slot frees.
//
// The Limiter is goroutine-safe.
//
// spec: §4.1 (per-subsystem goroutine pool / concurrency limits)
type Limiter struct {
	// MaxConcurrent is the maximum number of in-flight requests the
	// limiter admits at once. Zero is treated as unbounded
	// (admission always succeeds, queue depth stays at 0). Callers
	// should set a positive value to enforce a real bound.
	MaxConcurrent int

	once     sync.Once
	slots    chan struct{}
	inflight int64
	queued   int64
}

// init lazily initializes the slot channel on first use. The §4.1
// subsystem types are zero-value-usable so callers can declare them
// in struct literals without a constructor.
func (l *Limiter) init() {
	l.once.Do(func() {
		if l.MaxConcurrent > 0 {
			l.slots = make(chan struct{}, l.MaxConcurrent)
		}
	})
}

// Acquire blocks until a slot is available or ctx is cancelled. The
// queue-depth gauge is incremented while a caller is blocked and
// decremented once Acquire returns. A nil release function is
// returned on error; callers must always invoke the returned
// release function in a defer to free the slot, including on
// downstream errors.
func (l *Limiter) Acquire(ctx context.Context) (release func(), err error) {
	l.init()
	if l.MaxConcurrent <= 0 || l.slots == nil {
		// Unbounded: still track in-flight so InFlight() reports
		// truthfully, but never block.
		atomic.AddInt64(&l.inflight, 1)
		return func() {
			atomic.AddInt64(&l.inflight, -1)
		}, nil
	}
	// Fast path: try to grab a slot without queueing.
	select {
	case l.slots <- struct{}{}:
		atomic.AddInt64(&l.inflight, 1)
		return l.releaser(), nil
	default:
	}
	// Slow path: account a queued waiter and block until a slot is
	// available or ctx is cancelled.
	atomic.AddInt64(&l.queued, 1)
	defer atomic.AddInt64(&l.queued, -1)
	select {
	case l.slots <- struct{}{}:
		atomic.AddInt64(&l.inflight, 1)
		return l.releaser(), nil
	case <-ctx.Done():
		return nil, ErrLimiterStopped
	}
}

// TryAcquire attempts to take a slot without blocking. It returns
// (release, true) when a slot is admitted and (nil, false) when the
// limiter is saturated. Use it for callers that want to reject
// immediately rather than queue.
func (l *Limiter) TryAcquire() (release func(), ok bool) {
	l.init()
	if l.MaxConcurrent <= 0 || l.slots == nil {
		atomic.AddInt64(&l.inflight, 1)
		return func() {
			atomic.AddInt64(&l.inflight, -1)
		}, true
	}
	select {
	case l.slots <- struct{}{}:
		atomic.AddInt64(&l.inflight, 1)
		return l.releaser(), true
	default:
		return nil, false
	}
}

// InFlight reports the number of requests currently holding a slot.
// It is the source of the §16.1
// lenny_gateway_subsystem_queue_depth{subsystem=…} gauge's in-flight
// component; QueueDepth covers the queued (blocked) component.
func (l *Limiter) InFlight() int {
	return int(atomic.LoadInt64(&l.inflight))
}

// QueueDepth reports the number of callers currently blocked inside
// Acquire waiting for a slot. It is the source of the §16.1
// lenny_gateway_subsystem_queue_depth{subsystem=…} gauge — a
// non-zero, sustained value indicates the subsystem is saturated and
// is an HPA scale-out signal.
func (l *Limiter) QueueDepth() int {
	return int(atomic.LoadInt64(&l.queued))
}

// releaser returns a slot-release function. The caller must invoke
// it exactly once. The function is idempotent against double release
// (the second call is a no-op) so a defer-then-error path does not
// over-decrement.
func (l *Limiter) releaser() func() {
	released := atomic.Bool{}
	return func() {
		if !released.CompareAndSwap(false, true) {
			return
		}
		<-l.slots
		atomic.AddInt64(&l.inflight, -1)
	}
}
