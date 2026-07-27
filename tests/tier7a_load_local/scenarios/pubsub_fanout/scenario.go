// SPDX-License-Identifier: MIT

//go:build load_local

// Package pubsub_fanout models the §12.6 EventBus fan-out primitive
// with a scenario-local broker: a topic carries every event to every
// connected subscriber, each subscriber holds its own queue, and
// delivery is at-most-once so no subscriber sees an event twice.
//
// The scenario drives a scenario-local broker rather than the
// Redis-backed §12.6 RedisEventBus so the fan-out invariant is
// exercised in-process at tier-7a rates with the race detector on.
//
// The two things the scenario controls make the assertion
// deterministic rather than timing-dependent:
//
//  1. Every subscriber is connected before Setup returns, so no event
//     is published while a subscriber is absent. Absence is the drop
//     the §12.6 delivery contract names, and it is not the behavior
//     this scenario exercises.
//  2. Teardown drains each subscriber queue to empty before the
//     consumer goroutine exits. loadgen.Run joins every publisher
//     before it calls Teardown, so an empty queue after the stop
//     signal means the fan-out is complete rather than merely quiet.
//
// With both in place the delivered set equals the published set on
// every host, so the assertion carries no throughput-scaled tolerance
// and no drain sleep.
//
// TESTING.md §12.7.a regression scenarios.
package pubsub_fanout

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "pubsub_fanout"

// subscribers is the fan-out width and subscriberQueue the per-subscriber
// queue depth. The queue is bounded so publish exerts backpressure
// instead of growing without limit.
const (
	subscribers     = 4
	subscriberQueue = 1024
)

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

// broker is an in-memory pub/sub broker. Subscribers each get their
// own buffered channel; publish fans out the event to every
// subscriber.
type broker struct {
	mu   sync.RWMutex
	subs []chan int64
}

func (b *broker) subscribe(buffer int) <-chan int64 {
	ch := make(chan int64, buffer)
	b.mu.Lock()
	b.subs = append(b.subs, ch)
	b.mu.Unlock()
	return ch
}

func (b *broker) publish(v int64) {
	b.mu.RLock()
	subs := append([]chan int64{}, b.subs...)
	b.mu.RUnlock()
	for _, ch := range subs {
		ch <- v // intentionally blocking to enforce no-drop
	}
}

// subStats is one subscriber's delivery accounting. The consumer
// goroutine keeps its tallies on the stack and stores them here once,
// on exit, so the hot receive loop takes no lock and Assert reads a
// settled value.
type subStats struct {
	delivered atomic.Int64
	duplicate atomic.Int64
	maxSeq    atomic.Int64
}

type Scenario struct {
	counters *scenkit.Counters
	br       *broker
	pubSeq   atomic.Int64
	stats    [subscribers]subStats

	stop chan struct{}
	done chan struct{}
}

func (s *Scenario) Name() string { return name }
func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 8, Duration: 1 * time.Second}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.br = &broker{}
	s.stop = make(chan struct{})
	s.done = make(chan struct{})
	// Reset the tallies so a second Setup/Run/Teardown cycle on the
	// same instance (the capacity-ramp path) compares one run's
	// deliveries against that same run's publishes.
	s.counters = scenkit.NewCounters()
	s.pubSeq.Store(0)
	for i := range s.stats {
		s.stats[i].delivered.Store(0)
		s.stats[i].duplicate.Store(0)
		s.stats[i].maxSeq.Store(0)
	}

	var wg sync.WaitGroup
	for i := 0; i < subscribers; i++ {
		// Subscribe on this goroutine, before Setup returns, so every
		// subscriber is connected ahead of the first publish. Calling
		// subscribe inside the consumer goroutine would let the driver
		// publish into a topic a subscriber has not yet joined, which
		// costs that subscriber the head of the run for reasons that
		// have nothing to do with fan-out.
		sub := s.br.subscribe(subscriberQueue)
		wg.Add(1)
		go func(idx int, ch <-chan int64) {
			defer wg.Done()
			s.consume(idx, ch)
		}(i, sub)
	}
	go func() { wg.Wait(); close(s.done) }()
	return nil
}

// consume receives from one subscriber queue until the queue is empty
// and the stop signal has fired. Queued events always win over stop:
// a plain select over both would pick uniformly at random whenever
// both are ready and discard whatever was still queued, which is the
// shutdown race that made this scenario's delivery count depend on how
// fast the host ran.
//
// spec: §12.6 (EventBus fan-out and at-most-once delivery).
func (s *Scenario) consume(idx int, ch <-chan int64) {
	// seen is indexed by sequence number minus one; the consumer owns
	// it outright, so the exactly-once check costs no synchronization.
	seen := make([]bool, 0, subscriberQueue)
	var delivered, duplicate, maxSeq int64

	record := func(v int64) {
		delivered++
		if v > maxSeq {
			maxSeq = v
		}
		if v < 1 {
			return
		}
		for int64(len(seen)) < v {
			seen = append(seen, false)
		}
		if seen[v-1] {
			duplicate++
			return
		}
		seen[v-1] = true
	}

	publish := func() {
		s.stats[idx].delivered.Store(delivered)
		s.stats[idx].duplicate.Store(duplicate)
		s.stats[idx].maxSeq.Store(maxSeq)
		s.counters.Add(fmt.Sprintf("sub_%d_received", idx), delivered)
	}

	for {
		// Prefer a queued event over the stop signal.
		select {
		case v := <-ch:
			record(v)
			continue
		default:
		}
		select {
		case v := <-ch:
			record(v)
		case <-s.stop:
			// Publishers are joined by now, so what remains in the
			// queue is the whole outstanding tail.
			for {
				select {
				case v := <-ch:
					record(v)
				default:
					publish()
					return
				}
			}
		}
	}
}

func (s *Scenario) Teardown(ctx context.Context) error {
	close(s.stop)
	<-s.done
	return nil
}

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	v := s.pubSeq.Add(1)
	s.br.publish(v)
	s.counters.Inc("published")
	return nil
}

// Assert checks the §12.6 fan-out contract on settled counts. The
// driver calls Teardown before Assert, and Teardown returns only once
// every subscriber has drained its queue, so no sleep or tolerance
// stands between the run and the assertion.
func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	published := s.counters.Get("published")
	if published == 0 {
		return fmt.Errorf("scenario published nothing")
	}
	if seq := s.pubSeq.Load(); seq != published {
		return fmt.Errorf("scenario accounting broken: %d sequence numbers issued for %d completed publishes", seq, published)
	}
	for i := range s.stats {
		st := &s.stats[i]
		r.AddCustom(fmt.Sprintf("sub_%d_duplicate", i), float64(st.duplicate.Load()))
		if dup := st.duplicate.Load(); dup != 0 {
			return fmt.Errorf("§12.6 at-most-once violated: sub_%d received %d duplicate deliveries out of %d published", i, dup, published)
		}
		if hi := st.maxSeq.Load(); hi > published {
			return fmt.Errorf("§12.6 violated: sub_%d received sequence %d beyond the %d events published", i, hi, published)
		}
		if got := st.delivered.Load(); got != published {
			return fmt.Errorf("§12.6 fan-out violated: sub_%d received %d of %d published; a subscriber connected for the whole publish window must receive every event on the topic", i, got, published)
		}
	}
	return nil
}
