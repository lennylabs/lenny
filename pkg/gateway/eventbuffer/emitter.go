// SPDX-License-Identifier: MIT

package eventbuffer

import (
	"context"
	"time"

	"github.com/lennylabs/lenny/pkg/events"
)

// emitterConfig is the resolved set of EmitterOption choices.
type emitterConfig struct {
	now        func() time.Time
	checkpoint *events.NonceCheckpoint
	metrics    *events.Metrics
	onError    func(error)
}

// EmitterOption configures NewEmitter.
type EmitterOption func(*emitterConfig)

// WithClock overrides time.Now; tests use it to anchor timestamps.
func WithClock(now func() time.Time) EmitterOption {
	return func(c *emitterConfig) {
		if now != nil {
			c.now = now
		}
	}
}

// WithNonceCheckpoint enables the §25.3 on-disk nonce checkpoint so the
// eventKey nonce survives a restart. spec: §25.3 line 748.
func WithNonceCheckpoint(spec events.NonceCheckpoint) EmitterOption {
	return func(c *emitterConfig) { c.checkpoint = &spec }
}

// WithMetrics wires the §25.3 event-emission metrics. spec: §25.3
// lines 705-710.
func WithMetrics(m *events.Metrics) EmitterOption {
	return func(c *emitterConfig) { c.metrics = m }
}

// WithEmitErrorLogger registers a callback for non-fatal emit-path errors
// (currently a failed nonce-checkpoint persist). The local-only emitter
// never returns these through Emit, so the callback is the only place
// they surface.
func WithEmitErrorLogger(fn func(error)) EmitterOption {
	return func(c *emitterConfig) { c.onError = fn }
}

// Emitter records §25.3 operational events into the in-process
// EventBuffer. The §25.5 Redis-stream destination is provided by
// StreamEmitter, which composes this Emitter with an XADD writer.
type Emitter struct {
	buffer  *EventBuffer
	keyer   *events.Keyer
	now     func() time.Time
	metrics *events.Metrics
}

// Compile-time guard that *Emitter satisfies events.EventEmitter.
var _ events.EventEmitter = (*Emitter)(nil)

// NewEmitter returns an Emitter that records events into buffer.
// replicaID is the per-replica identifier baked into each event's
// stable eventKey; an empty replicaID falls back to "gateway".
func NewEmitter(buffer *EventBuffer, replicaID string, opts ...EmitterOption) *Emitter {
	cfg := emitterConfig{now: time.Now}
	for _, o := range opts {
		o(&cfg)
	}
	cp, start := events.ResolveCheckpoint(cfg.checkpoint, cfg.onError)
	return &Emitter{
		buffer:  buffer,
		keyer:   events.NewKeyer(replicaID, cp, start, cfg.onError),
		now:     cfg.now,
		metrics: cfg.metrics,
	}
}

// Emit stamps an operational event with the §25.3 envelope — the
// CloudEvents spec version, a timestamp, and the stable eventKey — and
// records it in the buffer. A caller-set ID, Time, or SpecVersion is
// preserved. The local-only emitter never returns an error; ctx is
// honored for cancellation but the write itself is in-process and
// non-blocking. The assigned buffer id is read back via the buffer
// (Buffer().Query); the §25.3 EventEmitter contract is error-only.
func (e *Emitter) Emit(ctx context.Context, event events.OperationalEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if event.SpecVersion == "" {
		event.SpecVersion = events.CloudEventsSpecVersion
	}
	if event.Time.IsZero() {
		event.Time = e.now().UTC()
	}
	if event.ID == "" {
		event.ID = e.keyer.EventKey(event.Time)
	}
	e.buffer.Append(event)
	e.metrics.IncEmitted(event.Type)
	return nil
}

// Buffer returns the underlying event buffer so the query side
// (GET /v1/admin/events/buffer) can read what the Emitter records.
func (e *Emitter) Buffer() *EventBuffer { return e.buffer }
