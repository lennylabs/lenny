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
// The flush is bounded by the outage window: MarkRedisOutage records the local
// ring position at the down edge, and only the events buffered after it are
// re-emitted. The window is the boundary that keeps the flush from re-emitting
// events that already reached Redis long before the outage: the shared stream
// is trimmed at its MAXLEN by every producer's traffic while the local ring
// holds this replica's own events for far longer, so an event's absence from
// the stream's retained window says nothing about whether it was ever XADDed.
// Re-emitting on that signal alone would put an already-delivered event back at
// the head of the stream, where a consumer resuming by stream position receives
// it a second time. The retained-eventKey check stays as a secondary guard so a
// repeated edge does not flush one window twice.
//
// A per-event re-emit failure is logged by the caller and does not stop the
// flush; the returned count is the number re-emitted and the returned error is
// the last re-emit failure (nil when every re-emit succeeded). A nil re-emit
// path, a nil Redis source, or a recovery with no observed outage makes the
// flush a no-op. spec: §25.5 (best-effort recovery flush, eventKey dedup).
func (s *Service) FlushBufferedToRedis(ctx context.Context) (int, error) {
	if s.redisReEmit == nil || s.redis == nil {
		return 0, nil
	}
	since, outage := s.takeOutageWindow()
	if !outage {
		// No down edge was observed, so this replica buffered nothing that
		// failed to reach Redis and has nothing to re-emit.
		return 0, nil
	}

	present, err := s.redis.retainedEventKeys(ctx)
	if err != nil {
		return 0, fmt.Errorf("read retained eventKeys: %w", err)
	}

	buffered := s.buffer.Query(since, gwevents.EventFilter{}, DefaultBufferCapacity).Events

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

// MarkRedisOutage opens the §25.5 recovery-flush outage window by recording the
// local ring position at the moment the replica-level source-health probe
// observes Redis going down. Every event this replica buffers from here until
// the flush is one whose XADD to the shared stream failed, so the window is
// exactly the set the flush must re-emit. A second call while an outage is
// already open keeps the original position, so a flapping probe does not narrow
// the window past events it has yet to flush. spec: §25.5 (best-effort recovery
// flush scoped to the outage window).
func (s *Service) MarkRedisOutage() {
	s.outageMu.Lock()
	defer s.outageMu.Unlock()
	if s.inOutage {
		return
	}
	_, headID, _, _, _ := s.buffer.Bounds()
	s.outageFrom = headID
	s.inOutage = true
}

// takeOutageWindow returns the ring position the open outage window starts
// after and closes the window, so one down-to-up edge flushes it once. It
// reports false when no outage was observed since the last flush.
func (s *Service) takeOutageWindow() (since uint64, open bool) {
	s.outageMu.Lock()
	defer s.outageMu.Unlock()
	if !s.inOutage {
		return 0, false
	}
	s.inOutage = false
	return s.outageFrom, true
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
