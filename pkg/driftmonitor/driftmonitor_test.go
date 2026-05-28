// SPDX-License-Identifier: MIT

package driftmonitor

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeEmitter struct {
	mu     sync.Mutex
	values []float64
}

func (f *fakeEmitter) SetTimeDrift(seconds float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.values = append(f.values, seconds)
}

func (f *fakeEmitter) last() float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.values) == 0 {
		return 0
	}
	return f.values[len(f.values)-1]
}

// TestSamplePublishesGauge confirms Sample reads the source and writes
// the seconds value to the emitter. spec: §13.3 line 595.
func TestSamplePublishesGauge(t *testing.T) {
	e := &fakeEmitter{}
	m := New(func() time.Duration { return 750 * time.Millisecond }, e)
	m.Sample()
	if got := e.last(); got != 0.75 {
		t.Fatalf("emitter got %v, want 0.75", got)
	}
	if m.Offset() != 750*time.Millisecond {
		t.Fatalf("Offset = %v, want 750ms", m.Offset())
	}
}

// TestNilMonitorIsSafe ensures a nil Monitor's methods are no-ops so
// callers can wire a nil monitor without crashing.
func TestNilMonitorIsSafe(t *testing.T) {
	var m *Monitor
	m.Sample()
	if m.Degraded() {
		t.Fatal("nil monitor must not report degraded")
	}
	if m.Offset() != 0 {
		t.Fatal("nil monitor Offset must be 0")
	}
}

// TestNilSourceReportsZero confirms a Monitor without an OffsetFunc
// reports zero drift and publishes 0 to the gauge.
func TestNilSourceReportsZero(t *testing.T) {
	e := &fakeEmitter{}
	m := New(nil, e)
	m.Sample()
	if got := e.last(); got != 0 {
		t.Fatalf("emitter got %v, want 0", got)
	}
	if m.Degraded() {
		t.Fatal("zero drift must not be degraded")
	}
}

// TestDegradedThresholds walks the §13.3 line 595 5s ceiling: below,
// at, and above on both sides.
func TestDegradedThresholds(t *testing.T) {
	cases := []struct {
		name     string
		offset   time.Duration
		degraded bool
	}{
		{"zero", 0, false},
		{"warning-positive", 800 * time.Millisecond, false},
		{"critical-positive", 3 * time.Second, false},
		{"below-threshold-positive", 4*time.Second + 999*time.Millisecond, false},
		{"at-threshold-positive", 5 * time.Second, true},
		{"above-threshold-positive", 6 * time.Second, true},
		{"at-threshold-negative", -5 * time.Second, true},
		{"above-threshold-negative", -7 * time.Second, true},
		{"below-threshold-negative", -4 * time.Second, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := New(func() time.Duration { return tc.offset }, nil)
			m.Sample()
			if got := m.Degraded(); got != tc.degraded {
				t.Fatalf("Degraded(%v) = %v, want %v", tc.offset, got, tc.degraded)
			}
		})
	}
}

// TestStartFirstSampleImmediate confirms Start triggers a Sample on
// entry so the gauge populates without waiting one tick.
func TestStartFirstSampleImmediate(t *testing.T) {
	e := &fakeEmitter{}
	var calls atomic.Int32
	source := func() time.Duration {
		calls.Add(1)
		return 2 * time.Second
	}
	m := New(source, e)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		m.Start(ctx, time.Hour) // long interval so only the immediate sample fires
		close(done)
	}()
	// Give the goroutine a moment to run the immediate sample.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if calls.Load() >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	if calls.Load() < 1 {
		t.Fatal("Start did not perform the immediate Sample")
	}
	if e.last() != 2.0 {
		t.Fatalf("gauge = %v, want 2.0", e.last())
	}
}

// TestStartZeroIntervalIsNoOp ensures Start with a non-positive
// interval returns without starting a ticker.
func TestStartZeroIntervalIsNoOp(t *testing.T) {
	m := New(nil, nil)
	m.Start(context.Background(), 0)
	m.Start(context.Background(), -1*time.Second)
}
