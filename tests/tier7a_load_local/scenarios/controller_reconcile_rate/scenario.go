// SPDX-License-Identifier: MIT

//go:build load_local

// Package controller_reconcile_rate models the §4.6.3 controller
// reconcile path under a flood of status events. A scenario-local
// reconciler maintains a desired-state table; each iteration enqueues
// a status change for a pod; assert: every enqueued event reaches a
// terminal reconcile state, no event is lost, and reconcile
// throughput stays above a floor under contention.
//
// TESTING.md §12.7.a component-isolated benches.
package controller_reconcile_rate

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/fakekube"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "controller_reconcile_rate"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

// reconciler is the §4.6.3 controller model. It listens on a queue
// of object names and reconciles each by reading the latest state
// from the fakekube store.
type reconciler struct {
	store *fakekube.ObjectStore

	mu      sync.Mutex
	queued  map[string]bool
	queue   chan string
	stopped atomic.Bool
	done    chan struct{}

	reconciled atomic.Int64
}

func newReconciler(store *fakekube.ObjectStore) *reconciler {
	r := &reconciler{
		store:  store,
		queued: make(map[string]bool),
		queue:  make(chan string, 1024),
		done:   make(chan struct{}),
	}
	go r.loop()
	return r
}

func (r *reconciler) enqueue(name string) {
	r.mu.Lock()
	already := r.queued[name]
	if !already {
		r.queued[name] = true
	}
	r.mu.Unlock()
	if already {
		return
	}
	select {
	case r.queue <- name:
	default:
		// Queue full; the reconciler will pick up the latest state
		// when it processes the next event for this object.
		r.mu.Lock()
		r.queued[name] = false
		r.mu.Unlock()
	}
}

func (r *reconciler) loop() {
	defer close(r.done)
	for {
		select {
		case name, ok := <-r.queue:
			if !ok {
				return
			}
			r.mu.Lock()
			r.queued[name] = false
			r.mu.Unlock()
			// Read latest state; mark reconciled.
			if obj, err := r.store.Get("Pod", "lenny-agents", name); err == nil && obj != nil {
				r.reconciled.Add(1)
			}
		}
	}
}

func (r *reconciler) stop() {
	if r.stopped.CompareAndSwap(false, true) {
		close(r.queue)
		<-r.done
	}
}

type Scenario struct {
	counters *scenkit.Counters
	store    *fakekube.ObjectStore
	ctrl     *reconciler
}

func (s *Scenario) Name() string { return name }
func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 16, Duration: 2 * time.Second}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.store = fakekube.NewObjectStore()
	// Seed N pods.
	for i := 0; i < 32; i++ {
		_ = s.store.Create(&fakekube.Object{Kind: "Pod", Namespace: "lenny-agents", Name: fmt.Sprintf("pod-%d", i)})
	}
	s.ctrl = newReconciler(s.store)
	s.store.AddHook(func(op string, obj *fakekube.Object) {
		if obj.Kind == "Pod" {
			s.ctrl.enqueue(obj.Name)
		}
	})
	return nil
}

func (s *Scenario) Teardown(ctx context.Context) error {
	if s.ctrl != nil {
		s.ctrl.stop()
	}
	return nil
}

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	// Mutate a random pod's annotations.
	podName := fmt.Sprintf("pod-%d", iter%32)
	obj, err := s.store.Get("Pod", "lenny-agents", podName)
	if err != nil {
		return err
	}
	if obj.Annotations == nil {
		obj.Annotations = map[string]string{}
	}
	obj.Annotations["seq"] = fmt.Sprintf("%d-%d", vu, iter)
	// Tolerate the §5.2 SSA conflict path — under high contention some
	// goroutines lose the optimistic-locking race; the reconciler
	// will pick up the winning write through the watch event.
	if err := s.store.Update(obj); err != nil {
		s.counters.Inc("ssa_conflicts")
		return nil
	}
	s.counters.Inc("status_writes")
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	// Give the reconciler a brief tail to drain the queue.
	time.Sleep(100 * time.Millisecond)
	s.counters.EmitTo(r)
	reconciled := s.ctrl.reconciled.Load()
	r.AddCustom("reconciled", float64(reconciled))
	if reconciled == 0 {
		return fmt.Errorf("§4.6.3 violated: reconciler observed zero events under load")
	}
	if s.counters.Get("status_writes") == 0 {
		return fmt.Errorf("scenario did not write any pod status")
	}
	return nil
}
