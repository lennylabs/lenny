// SPDX-License-Identifier: MIT

package events

import "context"

// CloudEventsSpecVersion is the §25.3 / §12.6 CloudEvents envelope
// version every operational event carries.
const CloudEventsSpecVersion = "1.0.2"

// EventEmitter is the §4.0 / §25.3 operational-event sink every
// subsystem depends on. Subsystems take an EventEmitter and call Emit at
// the documented §16.6 state-change points. The local *eventbuffer.Emitter
// satisfies it; the §25.5 Redis-stream emitter satisfies it; tests
// substitute fakes through the same interface. ctx threads cancellation
// through the emit path so a slow Redis write does not pin a shutdown.
// spec: §25.3 lines 660-663 — `Emit(ctx context.Context, event
// OperationalEvent) error`.
type EventEmitter interface {
	// Emit records an operational event. An emitter that wraps a remote
	// stream returns a non-nil error when the remote write failed; the
	// in-process buffer write always succeeds first so the event is
	// never lost, and the caller may log the error. The local-only
	// emitter always returns nil.
	Emit(ctx context.Context, event OperationalEvent) error
}
