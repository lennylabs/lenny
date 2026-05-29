// SPDX-License-Identifier: MIT

package pgwritemetrics_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/pgwritemetrics"
)

// spec: §12.3 lines 115-125 sustained Postgres write-IOPS sampler. F-12.3.7.

// fakeEmitter records the last published gauge value.
type fakeEmitter struct {
	value float64
	sets  int
}

func (e *fakeEmitter) SetPostgresWriteIops(v float64) {
	e.value = v
	e.sets++
}

// stepClock returns successive times advanced by a fixed step per read,
// so the sampler sees a deterministic elapsed interval.
type stepClock struct {
	now  time.Time
	step time.Duration
}

func (c *stepClock) next() time.Time {
	t := c.now
	c.now = c.now.Add(c.step)
	return t
}

func TestSampleFirstReadEstablishesBaselineWithoutPublishing(t *testing.T) {
	emit := &fakeEmitter{}
	clk := &stepClock{now: time.Unix(1000, 0), step: time.Second}
	s := pgwritemetrics.New(func(context.Context) (uint64, error) { return 100, nil }, emit, clk.next)

	iops, ok, err := s.Sample(context.Background())
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	if ok {
		t.Error("first sample must not publish a rate (no prior point)")
	}
	if iops != 0 {
		t.Errorf("first sample iops = %v, want 0", iops)
	}
	if emit.sets != 0 {
		t.Errorf("emitter set %d times on first sample, want 0", emit.sets)
	}
}

func TestSampleComputesPerSecondRate(t *testing.T) {
	emit := &fakeEmitter{}
	// 10-second interval; counter advances 100 → 1100 → a 1000-write
	// delta over 10s = 100 IOPS.
	clk := &stepClock{now: time.Unix(0, 0), step: 10 * time.Second}
	counts := []uint64{100, 1100}
	i := 0
	read := func(context.Context) (uint64, error) {
		v := counts[i]
		i++
		return v, nil
	}
	s := pgwritemetrics.New(read, emit, clk.next)

	if _, _, err := s.Sample(context.Background()); err != nil { // baseline
		t.Fatalf("baseline Sample: %v", err)
	}
	iops, ok, err := s.Sample(context.Background())
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	if !ok {
		t.Fatal("second sample should publish a rate")
	}
	if iops != 100 {
		t.Errorf("iops = %v, want 100", iops)
	}
	if emit.value != 100 {
		t.Errorf("emitter value = %v, want 100", emit.value)
	}
}

func TestSampleCounterResetReBaselinesWithoutSpuriousRate(t *testing.T) {
	emit := &fakeEmitter{}
	clk := &stepClock{now: time.Unix(0, 0), step: time.Second}
	// 500 → 5 models a pg_stat_reset()/restart: cur < prev.
	counts := []uint64{500, 5, 105}
	i := 0
	read := func(context.Context) (uint64, error) { v := counts[i]; i++; return v, nil }
	s := pgwritemetrics.New(read, emit, clk.next)

	s.Sample(context.Background()) // baseline 500
	iops, ok, err := s.Sample(context.Background())
	if err != nil {
		t.Fatalf("Sample after reset: %v", err)
	}
	if ok || iops != 0 {
		t.Errorf("counter reset must not publish a rate; got iops=%v ok=%v", iops, ok)
	}
	if emit.sets != 0 {
		t.Errorf("emitter set %d times across a reset, want 0", emit.sets)
	}
	// After re-baselining at 5, the next read (105) over 1s = 100 IOPS.
	iops, ok, err = s.Sample(context.Background())
	if err != nil {
		t.Fatalf("Sample after re-baseline: %v", err)
	}
	if !ok || iops != 100 {
		t.Errorf("post-reset rate = %v ok=%v, want 100 true", iops, ok)
	}
}

func TestSampleNonPositiveIntervalSkipped(t *testing.T) {
	emit := &fakeEmitter{}
	// A clock that never advances yields dt = 0.
	fixed := time.Unix(42, 0)
	counts := []uint64{10, 20}
	i := 0
	read := func(context.Context) (uint64, error) { v := counts[i]; i++; return v, nil }
	s := pgwritemetrics.New(read, emit, func() time.Time { return fixed })

	s.Sample(context.Background()) // baseline
	iops, ok, err := s.Sample(context.Background())
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	if ok || iops != 0 {
		t.Errorf("zero elapsed interval must be skipped; got iops=%v ok=%v", iops, ok)
	}
}

func TestSampleReadErrorPropagatedAndNoPublish(t *testing.T) {
	emit := &fakeEmitter{}
	wantErr := errors.New("pg_stat_database unreachable")
	s := pgwritemetrics.New(func(context.Context) (uint64, error) { return 0, wantErr }, emit, time.Now)

	iops, ok, err := s.Sample(context.Background())
	if !errors.Is(err, wantErr) {
		t.Errorf("Sample err = %v, want %v", err, wantErr)
	}
	if ok || iops != 0 {
		t.Errorf("a read error must not publish; got iops=%v ok=%v", iops, ok)
	}
	if emit.sets != 0 {
		t.Errorf("emitter set %d times on read error, want 0", emit.sets)
	}
}

func TestSampleToleratesNilEmitter(t *testing.T) {
	clk := &stepClock{now: time.Unix(0, 0), step: time.Second}
	counts := []uint64{0, 50}
	i := 0
	read := func(context.Context) (uint64, error) { v := counts[i]; i++; return v, nil }
	s := pgwritemetrics.New(read, nil, clk.next)

	s.Sample(context.Background())
	iops, ok, err := s.Sample(context.Background())
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	if !ok || iops != 50 {
		t.Errorf("rate with nil emitter = %v ok=%v, want 50 true", iops, ok)
	}
}
