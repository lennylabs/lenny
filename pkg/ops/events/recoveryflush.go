// SPDX-License-Identifier: MIT

package events

import (
	"context"
	"fmt"

	gwevents "github.com/lennylabs/lenny/pkg/events"
)

// FlushBufferedToRedis re-emits this replica's locally-buffered
// lenny-ops-originated events to the recovered Redis ops:events:stream,
// deduplicated by eventKey. It is the §25.5 best-effort recovery flush,
// invoked by the replica-level source-health edge detector on a Redis
// down-to-up edge: during the outage lenny-ops keeps emitting its own events
// into the local ring (the Redis XADD failed), so on recovery those events
// must reach the shared stream, or a consumer that connects only after Redis
// is back never observes them.
//
// The flush reads the eventKeys already retained in the recovered stream and
// re-emits only the local-ring events whose eventKey is absent, so an event
// that did reach Redis before the outage is not duplicated on the stream and
// an already-flushed event on a repeated edge is skipped. A per-event re-emit
// failure is logged by the caller and does not stop the flush; the returned
// count is the number re-emitted and the returned error is the last re-emit
// failure (nil when every re-emit succeeded). A nil re-emit path or a
// nil Redis source makes the flush a no-op (a no-Redis deployment has nothing
// to flush to). spec: §25.5 (best-effort recovery flush, eventKey dedup).
func (s *Service) FlushBufferedToRedis(ctx context.Context) (int, error) {
	if s.redisReEmit == nil || s.redis == nil {
		return 0, nil
	}

	present, err := s.redis.retainedEventKeys(ctx)
	if err != nil {
		return 0, fmt.Errorf("read retained eventKeys: %w", err)
	}

	// Snapshot the whole local ring: it holds only this replica's
	// lenny-ops-originated events, and the eventKey filter below drops the
	// ones already on the stream, so re-emitting from the full ring covers
	// the outage window without tracking a separate watermark.
	buffered := s.buffer.Query(0, gwevents.EventFilter{}, 0).Events

	flushed := 0
	seen := make(map[string]struct{}, len(buffered))
	var lastErr error
	for _, ev := range buffered {
		key := ev.Event.ID
		if key == "" {
			continue
		}
		if _, ok := present[key]; ok {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if err := s.redisReEmit(ctx, ev.Event); err != nil {
			// Best-effort: record the failure and keep flushing the rest so
			// one unreachable write does not abandon the remaining events.
			lastErr = fmt.Errorf("re-emit %s: %w", key, err)
			continue
		}
		flushed++
	}
	return flushed, lastErr
}

// retainedEventKeys returns the set of CloudEvents ids currently retained in
// the Redis ops:events:stream, bounded by the retained window. The §25.5
// recovery flush consults it to skip re-emitting an event already on the
// stream, so consumer-side eventKey deduplication is not the sole guard
// against a duplicate stream entry. spec: §25.5 (best-effort recovery flush,
// eventKey dedup).
func (rs *redisSource) retainedEventKeys(ctx context.Context) (map[string]struct{}, error) {
	msgs, err := rs.client.XRangeN(ctx, rs.stream, "-", "+", maxWindow).Result()
	if err != nil {
		return nil, fmt.Errorf("xrange scan %s: %w", rs.stream, err)
	}
	keys := make(map[string]struct{}, len(msgs))
	for _, m := range msgs {
		ev, ok := decodeRedisEntry(m)
		if !ok {
			continue
		}
		if ev.Event.ID != "" {
			keys[ev.Event.ID] = struct{}{}
		}
	}
	return keys, nil
}
