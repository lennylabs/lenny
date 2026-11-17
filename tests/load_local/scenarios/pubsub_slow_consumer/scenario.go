// SPDX-License-Identifier: MIT

//go:build load_local

// Package pubsub_slow_consumer asserts that one slow subscriber does
// not stall fast subscribers. The §4.8 invariant: per-subscriber
// queues are independent; backpressure on one is invisible to others.
//
// TESTING.md §12.7.a component-isolated benches.
package pubsub_slow_consumer

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "pubsub_slow_consumer"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

// broker uses per-subscriber bounded channels and a non-blocking
// send so a slow subscriber drops events instead of stalling
// publish.
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

func (b *broker) publish(v int64) int {
	b.mu.RLock()
	subs := append([]chan int64{}, b.subs...)
	b.mu.RUnlock()
	drops := 0
	for _, ch := range subs {
		select {
		case ch <- v:
		default:
			drops++
		}
	}
	return drops
}

type Scenario struct {
	counters *scenkit.Counters
	br       *broker
	fastRecv atomic.Int64
	slowRecv atomic.Int64

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

	fast := s.br.subscribe(1024)
	slow := s.br.subscribe(8) // small buffer makes the slow path drop fast.

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-s.stop:
				return
			case <-fast:
				s.fastRecv.Add(1)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-s.stop:
				return
			case <-slow:
				time.Sleep(2 * time.Millisecond) // deliberately slow
				s.slowRecv.Add(1)
			}
		}
	}()
	go func() { wg.Wait(); close(s.done) }()
	return nil
}

func (s *Scenario) Teardown(ctx context.Context) error {
	close(s.stop)
	<-s.done
	return nil
}

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	drops := s.br.publish(int64(iter))
	s.counters.Inc("published")
	s.counters.Add("drops", int64(drops))
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	time.Sleep(100 * time.Millisecond)
	s.counters.EmitTo(r)
	r.AddCustom("fast_recv", float64(s.fastRecv.Load()))
	r.AddCustom("slow_recv", float64(s.slowRecv.Load()))
	if s.counters.Get("published") == 0 {
		return fmt.Errorf("scenario published nothing")
	}
	// The §4.8 invariant: fast subscriber received nearly everything,
	// slow one received much less but did not stall the publisher.
	if s.fastRecv.Load() < s.slowRecv.Load() {
		return fmt.Errorf("§4.8 violated: slow subscriber outpaced fast (slow=%d fast=%d)", s.slowRecv.Load(), s.fastRecv.Load())
	}
	if s.counters.Get("drops") == 0 {
		return fmt.Errorf("scenario did not exercise the backpressure path; raise the rate or shrink the slow buffer")
	}
	return nil
}
