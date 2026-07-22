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
// the last failure the flush encountered (a failed retained-key scan or a
// failed re-emit), nil when the whole flush succeeded. A nil re-emit
// path, a nil Redis source, or a recovery with no observed outage makes the
// flush a no-op.
//
// A flush that failed to re-emit reopens the outage window at the first event
// it could not place, so a later edge retries from there. The window is the
// only record of which events still need re-emitting, and both edges that fire
// this flush report a transition rather than a level: consuming the window
// ahead of the re-emits meant that a flush racing Redis back down (a flapping
// endpoint, a cluster slot migration, an XADD timeout) abandoned that outage's
// events for good, since the next outage opens a fresh window at the current
// ring head. Reopening keeps the window closed only when the events it names
// actually reached the stream. spec: §25.5 (best-effort recovery flush,
// eventKey dedup).
func (s *Service) FlushBufferedToRedis(ctx context.Context) (int, error) {
	if s.redisReEmit == nil || s.redis == nil {
		return 0, nil
	}
	if !s.outageOpen() {
		// No down edge was observed, so this replica buffered nothing that
		// failed to reach Redis and has nothing to re-emit.
		return 0, nil
	}

	since, outage := s.takeOutageWindow()
	if !outage {
		return 0, nil
	}

	// The ring is snapshotted before the retained-key scan runs, so the scan can
	// only be newer than the set of events it is filtering. Two triggers fire
	// this flush (the source-health probe's down-to-up edge and the first XADD
	// that succeeds after one failed), so an emit whose XADD succeeds runs
	// concurrently with it: scanning first would capture the stream before that
	// XADD landed, and the event would then be inside the queried window,
	// missing from the scanned set, and re-emitted onto the stream a second
	// time. The webhook worker pages the stream by position with no eventKey
	// dedup of its own, so that duplicate entry becomes a duplicate webhook
	// delivery. spec: §25.5 (best-effort recovery flush, eventKey dedup).
	buffered := s.buffer.Query(since, gwevents.EventFilter{}, DefaultBufferCapacity).Events

	// A scan failure drops the optimization rather than the window: the flush
	// re-emits everything buffered and consumer-side eventKey deduplication
	// collapses whatever already reached the stream. Abandoning the window
	// instead would lose it for good, because the down-to-up edge fires once per
	// transition. spec: §25.5 (best-effort recovery flush, eventKey dedup).
	present, scanErr := s.redis.retainedEventKeys(ctx)
	if scanErr != nil {
		present = nil
		scanErr = fmt.Errorf("read retained eventKeys: %w", scanErr)
	}

	flushed := 0
	seen := make(map[string]struct{}, len(buffered))
	lastErr := scanErr
	// firstFailed is the local ring position of the earliest event this flush
	// could not place on the stream. It anchors the window the flush reopens,
	// so a retry resumes at the first event still owed to the stream rather
	// than replaying the ones already re-emitted.
	var firstFailed uint64
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
			if firstFailed == 0 {
				firstFailed = ev.ID
			}
			continue
		}
		flushed++
	}
	if firstFailed > 0 {
		// The window this flush consumed still holds events that never reached
		// the stream, so it is reopened at the first of them. A failed
		// retained-key scan alone does not reopen it: every event it named was
		// re-emitted, so the window has done its job.
		s.openOutageWindow(firstFailed - 1)
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
// after. An open window keeps the earliest position either signal reported
// rather than the first one recorded: the two openers race, and the probe's
// head-anchored window is the narrower of the two. When the probe observes the
// outage in the instant between an event's local publish and its failed XADD,
// it anchors the window at that event's own ring position, so the write failure
// that follows finds a window already open and its earlier anchor would be
// dropped. The flush would then query past the event whose XADD failed and
// abandon it, which is the loss the window exists to prevent. Widening to the
// minimum keeps the window the whole outage while still refusing to let a later
// signal narrow it. spec: §25.5 (best-effort recovery flush scoped to the
// outage window).
func (s *Service) openOutageWindow(since uint64) {
	s.outageMu.Lock()
	defer s.outageMu.Unlock()
	if s.inOutage {
		if since < s.outageFrom {
			s.outageFrom = since
		}
		return
	}
	s.outageFrom = since
	s.inOutage = true
}

// outageOpen reports whether a recovery-flush outage window is currently open,
// without consuming it. The flush checks it before doing any Redis work so a
// recovery with nothing buffered costs no round trip.
func (s *Service) outageOpen() bool {
	s.outageMu.Lock()
	defer s.outageMu.Unlock()
	return s.inOutage
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
// against a duplicate stream entry.
//
// It reads the same head-anchored window every other eventKey scan resolves
// against (see retainedWindow). Scanning forwards from the tail instead would
// silently shrink the set whenever the writer's approximate trim leaves the
// stream over-long, and the dedup guard would then miss an event that is on the
// stream and re-emit it as a duplicate entry. spec: §25.5 (best-effort recovery
// flush, eventKey dedup).
func (rs *redisSource) retainedEventKeys(ctx context.Context) (map[string]struct{}, error) {
	msgs, err := rs.retainedWindow(ctx)
	if err != nil {
		return nil, err
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
