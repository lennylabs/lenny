// SPDX-License-Identifier: MIT

package runtime

import (
	"context"
	"errors"
	"sync/atomic"
	"time"
)

// Config configures a Stub's response behaviour.
type Config struct {
	// ResponseLatency is the artificial delay before each tool-call
	// response. Zero means respond immediately.
	ResponseLatency time.Duration

	// ErrorRate is the fraction of tool calls that return an error.
	// Must be in [0, 1]. Zero means never error.
	ErrorRate float64

	// MaxConcurrent caps the number of simultaneously-in-flight
	// tool calls the stub accepts. Calls past the cap return
	// ErrAtCapacity. Zero means unlimited.
	MaxConcurrent int
}

// ErrAtCapacity is returned when a tool call would exceed
// Config.MaxConcurrent.
var ErrAtCapacity = errors.New("runtime stub: at capacity")

// Stub is the configurable in-process adapter. Each Stub instance
// stands in for one adapter container; scenarios that need a pool
// instantiate multiple Stubs.
type Stub struct {
	config Config

	// inFlight tracks the live tool-call count for cap enforcement.
	// Accessed exclusively through atomic operations.
	inFlight int32

	// totalCalls counts every call accepted (excluding ErrAtCapacity
	// rejections). Exposed to scenarios so they can assert load.
	totalCalls atomic.Int64

	// totalErrors counts every error response returned.
	totalErrors atomic.Int64

	// rngState is the per-stub deterministic counter the error
	// injector uses. Each call increments it; the injector emits an
	// error when (counter * Config.ErrorRate * scale) crosses an
	// integer boundary. Deterministic so tests under -race do not
	// see flaky error rates.
	rngState atomic.Uint64
}

// New returns a Stub with the supplied configuration.
func New(c Config) *Stub {
	return &Stub{config: c}
}

// Call simulates a tool-call against the adapter. It blocks for
// ResponseLatency and returns an injected error per ErrorRate, or
// ErrAtCapacity if MaxConcurrent would be exceeded.
func (s *Stub) Call(ctx context.Context) error {
	if !s.acquire() {
		return ErrAtCapacity
	}
	defer s.release()
	s.totalCalls.Add(1)

	if s.config.ResponseLatency > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.config.ResponseLatency):
		}
	}

	if s.shouldError() {
		s.totalErrors.Add(1)
		return errors.New("runtime stub: injected error")
	}
	return nil
}

// acquire atomically reserves an in-flight slot. The MaxConcurrent
// gate uses a CAS loop so the path is race-free even when the
// release path is racing the same counter.
func (s *Stub) acquire() bool {
	cap := int32(s.config.MaxConcurrent)
	if cap <= 0 {
		atomic.AddInt32(&s.inFlight, 1)
		return true
	}
	for {
		cur := atomic.LoadInt32(&s.inFlight)
		if cur >= cap {
			return false
		}
		if atomic.CompareAndSwapInt32(&s.inFlight, cur, cur+1) {
			return true
		}
	}
}

func (s *Stub) release() {
	for {
		cur := atomic.LoadInt32(&s.inFlight)
		if cur <= 0 {
			return
		}
		if atomic.CompareAndSwapInt32(&s.inFlight, cur, cur-1) {
			return
		}
	}
}

// shouldError returns true with probability Config.ErrorRate using
// a deterministic counter so behaviour is reproducible under -race.
func (s *Stub) shouldError() bool {
	if s.config.ErrorRate <= 0 {
		return false
	}
	if s.config.ErrorRate >= 1 {
		return true
	}
	// Scale ErrorRate to a u64 step; emit error when the cumulative
	// count crosses the boundary.
	step := uint64(s.config.ErrorRate * float64(1<<32))
	state := s.rngState.Add(step)
	return (state >> 32) != ((state - step) >> 32)
}

// TotalCalls returns the count of accepted calls.
func (s *Stub) TotalCalls() int64 { return s.totalCalls.Load() }

// TotalErrors returns the count of injected error responses.
func (s *Stub) TotalErrors() int64 { return s.totalErrors.Load() }

// InFlight returns the current in-flight tool-call count.
func (s *Stub) InFlight() int { return int(atomic.LoadInt32(&s.inFlight)) }
