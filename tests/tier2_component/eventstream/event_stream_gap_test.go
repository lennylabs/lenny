//go:build component

// SPDX-License-Identifier: MIT

// Tier-2 component test for the §25.5 operational-event-stream gap
// behavior against a real MAXLEN-bounded Redis stream.
//
// This test is currently a placeholder. The §25.5 read surface
// (pkg/ops/events.Service, serving GET /v1/admin/events and
// /v1/admin/events/stream) resolves cursors and gap detection against
// its in-memory ring buffer only. Its own package comment states that
// "The Redis stream source and the Redis-down / gateway-down
// degradation matrix" remain unimplemented, and SourceKindRedis is
// defined for forward-compatibility but never produced by an actual
// Redis read source. Consequently the eviction-driven gapDetected path
// this test targets — a stale cursor against a stream that has evicted
// its referenced event, translated by scanning the Redis stream for a
// matching eventKey — cannot be exercised end to end today: the read
// surface would resolve the cursor against an empty buffer and report a
// fresh connect rather than a gap.
//
// Exercising the real path requires building the read-side Redis source
// (XREAD/XRANGE against ops:events:stream with the cross-source cursor
// translation) and wiring it into pkg/ops/events.Service, a product
// change tracked as the SSE/polling Redis source. Until that lands the
// assertions below cannot run against Redis, so the test skips rather
// than assert a path the current binary cannot reach. The buffer-side
// gapDetected / oldestAvailableCursor / OnGap-counter contract is
// already covered by pkg/ops/events (gapmetric_test.go, read_surface_test.go).
package eventstream_test

import "testing"

// TestOpsEventStreamGapDetectedOnRedisEviction XADDs operational events
// to a real MAXLEN-bounded Redis ops:events:stream past its cap so the
// oldest entries evict, records a cursor pointing at a now-evicted
// event, then polls the §25.5 read surface with that stale cursor. It
// asserts the response reports pagination.gapDetected: true, carries a
// populated pagination.oldestAvailableCursor pointing at the oldest
// retained event, and that the poll increments
// lenny_ops_events_stream_gaps_total by one.
//
// spec: 25.5 (Eviction and gap behavior) — "When events are evicted
// before a slow subscriber reads them, the subscriber's next request
// returns pagination.gapDetected: true (canonical envelope, Section
// 25.2). ... Gap rate (lenny_ops_events_stream_gaps_total) should be
// near zero in healthy operation", and 25.5 (Cursor Model) — "When a
// caller sends a cursor from one source to another that cannot honor
// it, lenny-ops translates by scanning for the first event with a
// matching eventKey. If no match is found (the event has been evicted),
// the response returns gapDetected: true per the canonical pagination
// envelope (Section 25.2) along with oldestAvailableCursor."
// diagnosis: a failure means the §25.5 read surface does not report a
// gap when its Redis-backed cursor references an evicted event — the
// gapDetected flag or the oldestAvailableCursor was absent, or the
// lenny_ops_events_stream_gaps_total counter did not increment — so a
// slow subscriber would silently miss events instead of being told to
// re-read platform state.
func TestOpsEventStreamGapDetectedOnRedisEviction(t *testing.T) {
	t.Skip("the §25.5 SSE/polling read surface (pkg/ops/events.Service) does not yet consume Redis; the read-side Redis source with cross-source cursor translation is unbuilt, so an evicted-cursor poll cannot be exercised against a real MAXLEN-bounded stream")
}
