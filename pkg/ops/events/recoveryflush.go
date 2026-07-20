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
// The flush is bounded by the outage window: the window opens at the first
// event whose XADD failed (MarkRedisWriteFailure) or, when the probe observes
// the outage before this replica emits anything into it, at the ring head
// (MarkRedisOutage), and only the events buffered after that position are
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

// MarkRedisWriteFailure opens the §25.5 recovery-flush outage window at the
// event whose XADD to the shared stream failed. bufferID is that event's local
// ring position, so the window starts immediately before it and the flush
// re-emits it along with everything buffered after it.
//
// The observed write failure is the true start of the outage. The replica-level
// source-health probe refreshes on an interval, so it observes Redis going away
// up to one interval late; anchoring the window at that observation would
// exclude every event emitted in between, which is exactly the set whose XADD
// failed and which therefore reaches the shared stream only through the flush.
// spec: §25.5 (best-effort recovery flush).
func (s *Service) MarkRedisWriteFailure(bufferID uint64) {
	since := uint64(0)
	if bufferID > 0 {
		since = bufferID - 1
	}
	s.openOutageWindow(since)
}

// MarkRedisOutage opens the §25.5 recovery-flush outage window at the current
// local ring head, for the case where the replica-level source-health probe
// observes Redis unreachable before this replica has emitted anything into the
// outage. An emit that fails its XADD first opens the window earlier, at the
// failed event, and this call then leaves that wider window alone. spec: §25.5
// (best-effort recovery flush scoped to the outage window).
func (s *Service) MarkRedisOutage() {
	_, headID, _, _, _ := s.buffer.Bounds()
	s.openOutageWindow(headID)
}

// openOutageWindow records since as the position the recovery flush queries
// after, unless a window is already open. Keeping the first position is what
// makes the window the whole outage: a later signal (a flapping probe, or a
// second failed XADD) must not narrow it past events it has yet to flush.
func (s *Service) openOutageWindow(since uint64) {
	s.outageMu.Lock()
	defer s.outageMu.Unlock()
	if s.inOutage {
		return
	}
	s.outageFrom = since
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
