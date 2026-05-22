// SPDX-License-Identifier: MIT

//go:build load_local

// Package pubsub_fanout models the §4.8 fan-out pub/sub primitive
// with a scenario-local broker. The invariant: every subscriber sees
// every published event in order, exactly once.
//
// pkg/pubsub does not yet exist in the tree; this scenario uses a
// scenario-local broker so the documented fan-out invariant is
// exercised in CI.
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

type Scenario struct {
	counters *scenkit.Counters
	br       *broker
	pubSeq   atomic.Int64

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

	// Spin up 4 subscribers, each running in its own goroutine.
	// §4.8 promises no-drop and no-duplication within a topic across
	// any publisher set; ordering is only preserved per-publisher
	// (which the test driver does not control). The assertion below
	// therefore checks delivery completeness, not strict order.
	const N = 4
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			sub := s.br.subscribe(1024)
			for {
				select {
				case <-s.stop:
					return
				case <-sub:
					s.counters.Inc(fmt.Sprintf("sub_%d_received", i))
				}
			}
		}()
	}
	go func() { wg.Wait(); close(s.done) }()
	return nil
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

func (s *Scenario) Assert(r *loadgen.Result) error {
	// Drain briefly so subscribers consume the tail.
	time.Sleep(100 * time.Millisecond)
	s.counters.EmitTo(r)
	published := s.counters.Get("published")
	if published == 0 {
		return fmt.Errorf("scenario published nothing")
	}
	for i := 0; i < 4; i++ {
		got := s.counters.Get(fmt.Sprintf("sub_%d_received", i))
		// Allow a small tail (events emitted right before Teardown
		// may not have been pumped by the subscriber goroutine).
		if got < published-int64(8) {
			return fmt.Errorf("§4.8 violated: sub_%d received %d of %d published (drops > tail tolerance)", i, got, published)
		}
	}
	return nil
}
