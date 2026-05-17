// SPDX-License-Identifier: MIT

package opsevents

import (
	"fmt"
	"sync/atomic"
	"time"
)

// CloudEventsSpecVersion is the §25.3 / §12.6 CloudEvents envelope
// version every operational event carries.
const CloudEventsSpecVersion = "1.0.2"

// Emitter records §25.3 operational events. v1 writes each event to the
// in-process EventBuffer; the §25.3 Redis-stream destination is added
// alongside the §10.1 Redis EventBus. Emit fills the CloudEvents
// envelope and assigns the stable eventKey before buffering.
type Emitter struct {
	buffer    *EventBuffer
	replicaID string
	now       func() time.Time
	nonce     atomic.Uint64
}

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
// caller-set ID, Time, or SpecVersion is preserved.
func (e *Emitter) Emit(event OperationalEvent) uint64 {
	if event.SpecVersion == "" {
		event.SpecVersion = CloudEventsSpecVersion
	}
	if event.Time.IsZero() {
		event.Time = e.now().UTC()
	}
	if event.ID == "" {
		event.ID = e.eventKey(event.Time)
	}
	return e.buffer.Append(event)
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
