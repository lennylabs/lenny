// SPDX-License-Identifier: MIT

package events

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"
)

// CloudEventsSpecVersion is the §25.3 / §12.6 CloudEvents envelope
// version every operational event carries.
const CloudEventsSpecVersion = "1.0.2"

// EventEmitter is the §4.0 operational-event sink every subsystem
// depends on. Subsystems take an EventEmitter and call Emit at the
// documented §16.6 state-change points. The local *Emitter satisfies
// it; the §25.5 Redis-stream emitter satisfies it; tests substitute
// fakes through the same interface. ctx threads cancellation through
// the emit path so a slow Redis write does not pin a shutdown.
type EventEmitter interface {
	// Emit records an operational event and returns the assigned
	// monotonic buffer id. Emitters that wrap a remote stream return
	// the local-side id and a non-nil error when the remote write
	// failed; the caller may log without falling back. The local-only
	// emitter always succeeds (err is nil).
	Emit(ctx context.Context, event OperationalEvent) (uint64, error)
}

// Emitter records §25.3 operational events into the in-process
// EventBuffer. The §25.5 Redis-stream destination is provided by
// StreamEmitter, which composes this Emitter with an XADD writer.
type Emitter struct {
	buffer    *EventBuffer
	replicaID string
	now       func() time.Time
	nonce     atomic.Uint64
}

// Compile-time guard that *Emitter satisfies EventEmitter.
var _ EventEmitter = (*Emitter)(nil)

// NewEmitter returns an Emitter that records events into buffer.
// replicaID is the per-replica identifier baked into each event's
// stable eventKey; an empty replicaID falls back to "gateway".
func NewEmitter(buffer *EventBuffer, replicaID string) *Emitter {
	if replicaID == "" {
		replicaID = "gateway"
	}
	return &Emitter{buffer: buffer, replicaID: replicaID, now: time.Now}
}

// Emit stamps an operational event with the §25.3 envelope — the
// CloudEvents spec version, a timestamp, and the stable eventKey — and
// records it in the buffer, returning the assigned buffer id. A
// caller-set ID, Time, or SpecVersion is preserved. The local-only
// emitter never returns an error; ctx is honored for cancellation but
// the write itself is in-process and non-blocking.
func (e *Emitter) Emit(ctx context.Context, event OperationalEvent) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if event.SpecVersion == "" {
		event.SpecVersion = CloudEventsSpecVersion
	}
	if event.Time.IsZero() {
		event.Time = e.now().UTC()
	}
	if event.ID == "" {
		event.ID = e.eventKey(event.Time)
	}
	return e.buffer.Append(event), nil
}

// eventKey composes the §25.3 stable event identifier
// {replicaID}:{emittedAt}:{nonce}. The nonce is a per-replica
// monotonically increasing counter that increments for every emitted
// event regardless of the emitting subsystem; combined with the unique
// replicaID it makes the key globally unique. v1 keeps the nonce
// in-process — the §25.3 disk checkpoint that survives a restart is a
// refinement.
func (e *Emitter) eventKey(at time.Time) string {
	return fmt.Sprintf("%s:%d:%d", e.replicaID, at.UnixNano(), e.nonce.Add(1))
}

// Buffer returns the underlying event buffer so the query side
// (GET /v1/admin/events/buffer) can read what the Emitter records.
func (e *Emitter) Buffer() *EventBuffer { return e.buffer }
