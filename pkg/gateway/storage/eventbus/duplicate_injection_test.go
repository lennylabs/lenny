// SPDX-License-Identifier: MIT

package eventbus

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// sendCounter records every channel send the bus performs so the
// duplicate-injection factor can be observed directly.
type sendCounter struct {
	mu    sync.Mutex
	n     int
	fail  bool
	calls []string
}

func (c *sendCounter) send(_ context.Context, channel string, _ []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
	c.calls = append(c.calls, channel)
	if c.fail {
		return errors.New("backend down")
	}
	return nil
}

func (c *sendCounter) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// spec: §12.6 line 699 — "Configure the staging EventBus to publish every
// event twice (eventBus.duplicateInjectionFactor: 2)."
// diagnosis: a factor of N must re-send the byte-identical envelope N
// times total so the dedup integration test can assert no doubled side
// effects; the default factor 1 must send exactly once.
func TestDuplicateInjectionFactor_spec_12_6_699(t *testing.T) {
	cases := []struct {
		name      string
		factor    int
		wantSends int
	}{
		{"default-factor-1-sends-once", 1, 1},
		{"factor-2-sends-twice", 2, 2},
		{"factor-3-sends-thrice", 3, 3},
		{"factor-0-clamped-to-1", 0, 1},
		{"negative-factor-clamped-to-1", -5, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			counter := &sendCounter{}
			bus := NewRedisEventBus(nil, nil, WithDuplicateInjectionFactor(tc.factor))
			bus.send = counter.send
			if err := bus.Publish(context.Background(), "acme", TopicSessionLifecycle,
				mustEvent(t, "acme", "x")); err != nil {
				t.Fatalf("Publish: %v", err)
			}
			if got := counter.count(); got != tc.wantSends {
				t.Errorf("send count = %d, want %d (factor %d)", got, tc.wantSends, tc.factor)
			}
		})
	}
}

// spec: §12.6 line 683/699 — a failed first send is buffered exactly once
// for opportunistic replay; the duplicate-injection copies must not run
// (and must not double-buffer) when the primary send fails.
func TestDuplicateInjectionSkippedOnPrimaryFailure_spec_12_6_699(t *testing.T) {
	counter := &sendCounter{fail: true}
	bus := NewRedisEventBus(nil, NewCountingBusMetrics(), WithDuplicateInjectionFactor(3))
	bus.send = counter.send
	err := bus.Publish(context.Background(), "acme", TopicSessionLifecycle, mustEvent(t, "acme", "x"))
	if err == nil {
		t.Fatal("Publish over a failing backend returned nil error")
	}
	// Only the single primary send is attempted; the (factor-1) duplicate
	// copies are skipped because the success branch never runs.
	if got := counter.count(); got != 1 {
		t.Errorf("send count = %d, want 1 (duplicates must not run on primary failure)", got)
	}
	if got := bus.ReplayBufferLen(); got != 1 {
		t.Errorf("replay buffer len = %d, want 1 (no double-buffering)", got)
	}
}
