// SPDX-License-Identifier: MIT

package subsystem

import (
	"context"
	"errors"
)

// ErrCircuitOpen is returned by Subsystem.Do when the breaker is
// open. The caller surfaces this as 503 SUBSYSTEM_UNAVAILABLE per
// §4.1 partial degradation contract.
var ErrCircuitOpen = errors.New("subsystem: circuit breaker open")

// Subsystem composes a Breaker and a Limiter so each §4.1 gateway
// subsystem boundary (Stream Proxy, Upload Handler, MCP Fabric, LLM
// Proxy) can gate its handler paths through a single Do call:
//
//  1. Check the breaker; if open, return ErrCircuitOpen immediately.
//  2. Acquire a slot from the limiter; if the context is cancelled
//     before a slot frees, return ErrLimiterStopped.
//  3. Invoke the caller-supplied work function.
//  4. Record the outcome (success / failure) against the breaker.
//
// The Name field is used to label the per-subsystem metrics
// (lenny_gateway_subsystem_circuit_state{subsystem=name},
// lenny_gateway_subsystem_queue_depth{subsystem=name}, etc.). It
// must match the subsystem label values the §16.5 alerts reference
// ("stream_proxy", "upload_handler", "mcp_fabric", "llm_proxy").
//
// A zero-value Subsystem (no breaker, no limiter) is callable: Do
// invokes the work function directly, threading the context.
//
// spec: §4.1 (Per-subsystem isolation guarantees)
type Subsystem struct {
	// Name is the subsystem identifier used as the `subsystem` label
	// on §16.1 per-subsystem metrics.
	Name string

	// Breaker is the per-subsystem circuit breaker. When nil, Do
	// does not gate admission on circuit state — useful for
	// subsystems that have only a concurrency limit.
	Breaker *Breaker

	// Limiter is the per-subsystem max-concurrent semaphore. When
	// nil, Do does not bound concurrency — useful for subsystems
	// that have only a breaker.
	Limiter *Limiter
}

// Do gates fn through the subsystem's breaker and limiter. The
// returned error is:
//
//   - ErrCircuitOpen when the breaker is open
//   - ErrLimiterStopped when ctx is cancelled before a slot is
//     acquired
//   - the error returned by fn otherwise
//
// A non-nil fn error counts as a failure against the breaker; a nil
// fn error counts as a success. Callers that classify some errors
// (e.g., a client 4xx) as non-breaker-triggering should call
// (*Breaker).RecordSuccess explicitly and bypass Do — or use the
// breaker / limiter directly.
func (s *Subsystem) Do(ctx context.Context, fn func(context.Context) error) error {
	if s.Breaker != nil && !s.Breaker.Allow() {
		return ErrCircuitOpen
	}
	var release func()
	if s.Limiter != nil {
		r, err := s.Limiter.Acquire(ctx)
		if err != nil {
			// The breaker admitted but the limiter could not. Roll
			// the breaker decision back so the rejection does not
			// burn a half-open probe slot.
			if s.Breaker != nil {
				s.Breaker.RecordSuccess()
			}
			return err
		}
		release = r
	}
	err := fn(ctx)
	if release != nil {
		release()
	}
	if s.Breaker != nil {
		if err != nil {
			s.Breaker.RecordFailure()
		} else {
			s.Breaker.RecordSuccess()
		}
	}
	return err
}

// State returns the breaker's current state, or StateClosed when no
// breaker is configured. Used by metric exporters that read the
// gauge value periodically.
func (s *Subsystem) State() State {
	if s.Breaker == nil {
		return StateClosed
	}
	return s.Breaker.State()
}

// QueueDepth returns the limiter's queued-callers count, or 0 when
// no limiter is configured. Used by metric exporters that publish
// the per-subsystem queue-depth gauge.
func (s *Subsystem) QueueDepth() int {
	if s.Limiter == nil {
		return 0
	}
	return s.Limiter.QueueDepth()
}

// InFlight returns the limiter's in-flight request count, or 0 when
// no limiter is configured. Used by metric exporters that publish
// per-subsystem capacity gauges (e.g., lenny_upload_handler_active_uploads).
func (s *Subsystem) InFlight() int {
	if s.Limiter == nil {
		return 0
	}
	return s.Limiter.InFlight()
}
