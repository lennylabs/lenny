// SPDX-License-Identifier: MIT

package controllermetrics

import (
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// QueueFactory matches the signature of controller.Options.NewQueue for
// the default reconcile.Request queue. cmd wires the result into each
// controller's controller.Options so every reconciliation work queue is
// the §4.6.1 bounded, depth-instrumented queue.
type QueueFactory func(controllerName string, rateLimiter workqueue.TypedRateLimiter[reconcile.Request]) workqueue.TypedRateLimitingInterface[reconcile.Request]

// NewQueueFactory returns a QueueFactory whose queues are bounded at
// maxDepth. A non-positive maxDepth disables work-shedding (an unbounded
// queue) while still emitting the depth gauge. The configured max-depth
// gauge is set so the §16.5 ControllerWorkQueueDepthHigh alert threshold
// resolves.
//
// spec: §4.6.1 line 425 — "The work queue max depth is configurable
// (default: 500); if the queue exceeds this depth, new reconciliation
// events are dropped and a lenny_controller_queue_overflow_total metric
// is incremented."
func NewQueueFactory(maxDepth int) QueueFactory {
	if maxDepth > 0 {
		workqueueMaxDepth.WithLabelValues().Set(float64(maxDepth))
	}
	return func(name string, rl workqueue.TypedRateLimiter[reconcile.Request]) workqueue.TypedRateLimitingInterface[reconcile.Request] {
		inner := workqueue.NewTypedRateLimitingQueueWithConfig(rl,
			workqueue.TypedRateLimitingQueueConfig[reconcile.Request]{Name: name})
		q := &boundedQueue{
			TypedRateLimitingInterface: inner,
			controller:                 name,
			queue:                      name,
			maxDepth:                   maxDepth,
		}
		q.observeDepth()
		return q
	}
}

// boundedQueue wraps a controller-runtime reconcile-request work queue
// with the §4.6.1 max-depth work-shedding and depth instrumentation. New
// events arriving on Add are dropped once the queue is at its max depth;
// requeues (AddAfter / AddRateLimited) are never shed, so a reconcile's
// retry is never lost to back-pressure meant for fresh events.
type boundedQueue struct {
	workqueue.TypedRateLimitingInterface[reconcile.Request]
	controller string
	queue      string
	maxDepth   int
}

// Add enqueues a new reconciliation event unless the queue is already at
// its configured max depth, in which case the event is dropped and the
// overflow counter is incremented. This is the path informer events take;
// requeue paths bypass the bound (see AddAfter / AddRateLimited).
func (q *boundedQueue) Add(item reconcile.Request) {
	if q.maxDepth > 0 && q.TypedRateLimitingInterface.Len() >= q.maxDepth {
		queueOverflow.WithLabelValues(q.controller).Inc()
		return
	}
	q.TypedRateLimitingInterface.Add(item)
	q.observeDepth()
}

// AddAfter and AddRateLimited are inherited from the embedded queue
// unchanged: a delayed or rate-limited requeue is a controller-driven
// retry, not a fresh event, so it bypasses the max-depth bound. Shedding
// a requeue would silently drop in-flight work.

// Get pops the next item and re-observes depth.
func (q *boundedQueue) Get() (reconcile.Request, bool) {
	item, shutdown := q.TypedRateLimitingInterface.Get()
	q.observeDepth()
	return item, shutdown
}

// Done marks an item processed and re-observes depth.
func (q *boundedQueue) Done(item reconcile.Request) {
	q.TypedRateLimitingInterface.Done(item)
	q.observeDepth()
}

// observeDepth publishes the inner queue's current length to the depth
// gauge.
func (q *boundedQueue) observeDepth() {
	workqueueDepth.WithLabelValues(q.controller, q.queue).Set(float64(q.TypedRateLimitingInterface.Len()))
}
