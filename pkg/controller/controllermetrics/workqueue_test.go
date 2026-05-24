// SPDX-License-Identifier: MIT

package controllermetrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func req(name string) reconcile.Request {
	return reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: name}}
}

// spec: §4.6.1 line 425 — at max depth, new reconciliation events are
// dropped and lenny_controller_queue_overflow_total is incremented.
func TestBoundedQueueShedsNewEventsAtMaxDepth(t *testing.T) {
	const ctrl = "shed-test"
	rl := workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]()
	q := NewQueueFactory(2)(ctrl, rl)
	defer q.ShutDown()

	q.Add(req("a"))
	q.Add(req("b"))
	// The queue is now at max depth; the third new event is shed.
	q.Add(req("c"))

	if got := q.Len(); got != 2 {
		t.Fatalf("queue length = %d, want 2 (third Add should be shed)", got)
	}
	if got := testutil.ToFloat64(queueOverflow.WithLabelValues(ctrl)); got != 1 {
		t.Fatalf("overflow counter = %v, want 1", got)
	}
	if got := testutil.ToFloat64(workqueueDepth.WithLabelValues(ctrl, ctrl)); got != 2 {
		t.Fatalf("depth gauge = %v, want 2", got)
	}
}

// A non-positive max depth disables shedding: every new event is enqueued.
func TestBoundedQueueUnboundedWhenMaxDepthNonPositive(t *testing.T) {
	const ctrl = "unbounded-test"
	rl := workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]()
	q := NewQueueFactory(0)(ctrl, rl)
	defer q.ShutDown()

	for _, n := range []string{"a", "b", "c", "d"} {
		q.Add(req(n))
	}
	if got := q.Len(); got != 4 {
		t.Fatalf("queue length = %d, want 4 (no shedding)", got)
	}
}

// Get and Done re-observe the queue depth so the gauge tracks draining.
func TestBoundedQueueDepthTracksGetAndDone(t *testing.T) {
	const ctrl = "drain-test"
	rl := workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]()
	q := NewQueueFactory(10)(ctrl, rl)
	defer q.ShutDown()

	q.Add(req("a"))
	q.Add(req("b"))
	if got := testutil.ToFloat64(workqueueDepth.WithLabelValues(ctrl, ctrl)); got != 2 {
		t.Fatalf("depth after adds = %v, want 2", got)
	}
	item, shutdown := q.Get()
	if shutdown {
		t.Fatal("queue shut down unexpectedly")
	}
	if got := testutil.ToFloat64(workqueueDepth.WithLabelValues(ctrl, ctrl)); got != 1 {
		t.Fatalf("depth after Get = %v, want 1", got)
	}
	q.Done(item)
	if got := testutil.ToFloat64(workqueueDepth.WithLabelValues(ctrl, ctrl)); got != 1 {
		t.Fatalf("depth after Done = %v, want 1 (one item still queued)", got)
	}
}

// NewQueueFactory publishes the configured max depth so the §16.5
// ControllerWorkQueueDepthHigh alert's scalar() threshold resolves.
func TestNewQueueFactorySetsMaxDepthGauge(t *testing.T) {
	NewQueueFactory(2000)
	if got := testutil.ToFloat64(workqueueMaxDepth.WithLabelValues()); got != 2000 {
		t.Fatalf("max-depth gauge = %v, want 2000", got)
	}
}
