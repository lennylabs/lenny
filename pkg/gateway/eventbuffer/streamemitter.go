// SPDX-License-Identifier: MIT

package eventbuffer

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/events"
)

// DefaultStreamKey is the §25.5 platform-scoped Redis-stream key the
// gateway, the controllers, and lenny-ops all write operational events
// to.
const DefaultStreamKey = "ops:events:stream"

// DefaultStreamMaxLen is the §25.5 Tier 1 default cap on the
// MAXLEN-approximated stream. Operators raise it per the §25.5 Tier
// presets via StreamEmitterOptions.MaxLen; the Tier 1 default keeps the
// memory budget under ~5 MB.
const DefaultStreamMaxLen = 10000

// StreamRedis is the subset of redis.UniversalClient the StreamEmitter
// uses. Tests substitute a fake; production passes a real client.
type StreamRedis interface {
	XAdd(ctx context.Context, args *redis.XAddArgs) *redis.StringCmd
}

// StreamEmitter is the §25.5 cross-process EventEmitter. It writes
// every event to the §25.5 Redis stream `ops:events:stream` (XADD with
// `MAXLEN ~ {ops.events.streamMaxLen}`) and tees a copy into the local
// in-process EventBuffer so the §25.3 GET /v1/admin/events/buffer query
// continues to serve the gateway's most-recent events even when the
// caller has not consumed the stream yet.
//
// Failure mode: when the Redis write errors the StreamEmitter still
// records the event in the local buffer and returns the local id with
// a non-nil error so the caller may log. §25.5 has lenny-ops fall back
// to the gateway buffer when Redis is unreachable; this preserves the
// gateway side of that fall-back without dropping events.
type StreamEmitter struct {
	client    StreamRedis
	buffer    *EventBuffer
	streamKey string
	maxLen    int64
	source    string
	now       func() time.Time
	keyer     *events.Keyer
	metrics   *events.Metrics
}

// Compile-time guard that *StreamEmitter satisfies events.EventEmitter.
var _ events.EventEmitter = (*StreamEmitter)(nil)

// StreamEmitterOptions configures NewStreamEmitter.
type StreamEmitterOptions struct {
	// Client is the Redis client the emitter calls XADD on. Required.
	Client StreamRedis
	// Buffer is the local in-process ring buffer the emitter tees a
	// copy of each event into. Required: §25.5 fall-back is the
	// gateway's in-memory buffer, so the local buffer write is the
	// guaranteed-durable path.
	Buffer *EventBuffer
	// StreamKey overrides DefaultStreamKey. A non-empty value lets a
	// deployment shard the stream by tenant or rename it.
	StreamKey string
	// MaxLen overrides DefaultStreamMaxLen. Non-positive uses the
	// Tier 1 default. Operators raise this per §25.5 tier presets via
	// the Helm value ops.events.streamMaxLen.
	MaxLen int64
	// Source is the §25.5 CloudEvents `source` prefix this emitter
	// stamps on events that do not already carry a Source — e.g.,
	// "//lenny.dev/gateway" for the gateway, "//lenny.dev/controller"
	// for the controller binary. Empty leaves Source unset.
	Source string
	// ReplicaID is the per-replica identifier baked into the stable
	// eventKey on events that do not already carry an ID. Empty falls
	// back to "gateway".
	ReplicaID string
	// Now overrides time.Now; tests use it to anchor timestamps.
	Now func() time.Time
	// NonceCheckpoint, when non-nil, enables the §25.3 on-disk nonce
	// checkpoint so the eventKey nonce survives a restart. spec: §25.3
	// line 748.
	NonceCheckpoint *events.NonceCheckpoint
	// Metrics, when non-nil, receives the §25.3 emission counters
	// (lenny_ops_events_emitted_total / _emit_failed_total). spec: §25.3
	// lines 705-710.
	Metrics *events.Metrics
	// OnError receives non-fatal emit-path errors (a failed nonce
	// checkpoint persist). The Redis-write error is returned from Emit
	// instead.
	OnError func(error)
}

// NewStreamEmitter returns a StreamEmitter that writes to opts.Client.
// A nil Client or Buffer panics — callers select StreamEmitter only
// when both are available, and a partial wiring would silently drop
// events.
func NewStreamEmitter(opts StreamEmitterOptions) *StreamEmitter {
	if opts.Client == nil {
		panic("eventbuffer.NewStreamEmitter: Client is required")
	}
	if opts.Buffer == nil {
		panic("eventbuffer.NewStreamEmitter: Buffer is required")
	}
	key := opts.StreamKey
	if key == "" {
		key = DefaultStreamKey
	}
	maxLen := opts.MaxLen
	if maxLen <= 0 {
		maxLen = DefaultStreamMaxLen
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	cp, start := events.ResolveCheckpoint(opts.NonceCheckpoint, opts.OnError)
	return &StreamEmitter{
		client:    opts.Client,
		buffer:    opts.Buffer,
		streamKey: key,
		maxLen:    maxLen,
		source:    opts.Source,
		now:       now,
		keyer:     events.NewKeyer(opts.ReplicaID, cp, start, opts.OnError),
		metrics:   opts.Metrics,
	}
}

// Emit stamps the §25.3 envelope, appends to the local buffer, then
// publishes the event to the §25.5 Redis stream. The buffer write always
// happens first so the event survives a Redis failure (§25.5 has
// lenny-ops fall back to the gateway buffer); a Redis-write failure
// returns a non-nil error the caller may log. The §25.3 EventEmitter
// contract is error-only; the local-buffer cursor is read back via
// Buffer().Query.
func (e *StreamEmitter) Emit(ctx context.Context, event events.OperationalEvent) error {
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
	if event.Source == "" && e.source != "" {
		event.Source = e.source
	}
	e.buffer.Append(event)
	e.metrics.IncEmitted(event.Type)
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("eventbuffer: marshal event: %w", err)
	}
	args := &redis.XAddArgs{
		Stream: e.streamKey,
		MaxLen: e.maxLen,
		Approx: true,
		Values: map[string]any{"event": payload},
	}
	if _, err := e.client.XAdd(ctx, args).Result(); err != nil {
		// spec: §25.3 line 703 — a Redis-unreachable emit still lands in
		// the local buffer (already appended above); count the failed
		// remote write and return the error for the caller to log.
		e.metrics.IncEmitFailed(event.Type)
		log.Printf("eventbuffer: XADD %s failed: %v (event %s)", e.streamKey, err, event.ID)
		return fmt.Errorf("eventbuffer: xadd %s: %w", e.streamKey, err)
	}
	return nil
}

// Buffer returns the local ring buffer this emitter tees events into.
// The gateway's GET /v1/admin/events/buffer query reads it; the §25.5
// fall-back path serves the same buffer when Redis is unreachable.
func (e *StreamEmitter) Buffer() *EventBuffer { return e.buffer }

// StreamKey reports the §25.5 Redis-stream key this emitter writes to.
// Exposed for observability and test assertions.
func (e *StreamEmitter) StreamKey() string { return e.streamKey }
