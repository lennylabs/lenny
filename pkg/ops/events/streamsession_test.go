// SPDX-License-Identifier: MIT

package events

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	gwevents "github.com/lennylabs/lenny/pkg/events"
)

// keyedFrame builds a BufferedEvent carrying eventKey.
func keyedFrame(eventKey string) gwevents.BufferedEvent {
	return gwevents.BufferedEvent{Event: gwevents.OperationalEvent{
		ID:          eventKey,
		Type:        "dev.lenny.escalation_created",
		SpecVersion: gwevents.CloudEventsSpecVersion,
	}}
}

// spec: 25.5 (eventKey dedup across sources, exactly-once across the source
// switch) — the connection's delivered set spans every source it serves from,
// so an event the recovery flush re-emits onto the Redis stream after the
// connection already received it from the local ring during the outage is not
// written a second time. The flushed entries carry their original eventKeys and
// land at the stream tail, ordering before entries already delivered, so a
// Redis stint that writes a frame for every entry it reads delivers them again.
func TestDeliverOnce_SkipsKeysAlreadyDeliveredOnThisConnection_spec_25_5(t *testing.T) {
	rec := httptest.NewRecorder()
	st := &streamSession{w: rec, flusher: rec, scope: readerScope{platformAdmin: true}}

	// The connection delivers two lenny-ops events from the local ring while
	// Redis is down.
	for _, key := range []string{"ops:20:1", "ops:25:1"} {
		if !st.deliverOnce(keyedFrame(key)) {
			t.Fatalf("event %s was not delivered on a fresh connection", key)
		}
	}
	// Redis recovers and the flush re-emits both with their original keys, so
	// they reach the connection again on the Redis tail.
	for _, key := range []string{"ops:20:1", "ops:25:1"} {
		if st.deliverOnce(keyedFrame(key)) {
			t.Errorf("the flushed event %s was delivered a second time on the same connection", key)
		}
	}
	// A post-recovery event with a later key still flows.
	if !st.deliverOnce(keyedFrame("gw:30:1")) {
		t.Error("a post-recovery event was not delivered")
	}

	if got := strings.Count(rec.Body.String(), "id: "); got != 3 {
		t.Fatalf("wrote %d frames; want 3 (each event once):\n%s", got, rec.Body.String())
	}
	if st.lastKey != "gw:30:1" {
		t.Errorf("resume position = %q; want gw:30:1 (the flush must not rewind it)", st.lastKey)
	}
}

// spec: 25.5 (eventKey dedup across sources) — an event the connection has not
// been written is delivered even when its eventKey orders before everything it
// already received. That is the ordinary state after a recovery flush for a
// connection that opened between Redis becoming reachable and the flush
// running: the outage-window events are new to it and arrive out of order. A
// dedup keyed on ordering rather than on what was actually delivered drops them.
func TestDeliverOnce_DeliversAnOlderKeyTheConnectionHasNotSeen_spec_25_5(t *testing.T) {
	rec := httptest.NewRecorder()
	st := &streamSession{w: rec, flusher: rec, scope: readerScope{platformAdmin: true}}

	if !st.deliverOnce(keyedFrame("gw:30:1")) {
		t.Fatal("the post-recovery event was not delivered")
	}
	if !st.deliverOnce(keyedFrame("ops:20:1")) {
		t.Fatal("a flushed outage-window event the connection never received was dropped")
	}
	// The resume position stays forward-only even though an older key arrived.
	if st.lastKey != "gw:30:1" {
		t.Errorf("resume position = %q; want gw:30:1", st.lastKey)
	}
}

// spec: 25.5 (eventKey dedup across sources) — the delivered set is bounded so
// a long-lived SSE connection's memory does not grow with everything it has
// ever been written. The retained window still covers every replay the read
// side can produce, so a key inside it is still deduplicated.
func TestDeliveredKeys_BoundsTheRetainedWindow_spec_25_5(t *testing.T) {
	d := deliveredKeys{window: bufferReplayWindow}
	for i := 0; i < bufferReplayWindow+10; i++ {
		if !d.add(fmt.Sprintf("ops:%d:1", i)) {
			t.Fatalf("key %d was reported as already delivered", i)
		}
	}
	if got := len(d.order); got != bufferReplayWindow {
		t.Errorf("retained %d keys; want the window bound %d", got, bufferReplayWindow)
	}
	if len(d.seen) != bufferReplayWindow {
		t.Errorf("the index holds %d keys; want %d (an evicted key must leave the index)", len(d.seen), bufferReplayWindow)
	}
	if !d.has("ops:15:1") {
		t.Error("a key inside the retained window is no longer deduplicated")
	}
	if d.has("ops:0:1") {
		t.Error("the oldest key was not evicted once the window filled")
	}
	// An event with no eventKey cannot be deduplicated against anything.
	if !d.add("") || !d.add("") {
		t.Error("an event carrying no eventKey must not be collapsed into a single delivery")
	}
}

// spec: 25.5 (eventKey dedup across sources, exactly-once across the source
// switch) — the delivered set must cover the largest window a session can
// replay. Whenever Redis is wired that is the stream's retained window, so the
// bound tracks the configured stream length rather than the local ring
// capacity, which is smaller.
func TestDeliveredKeyWindow_CoversTheRedisRetainedWindow_spec_25_5(t *testing.T) {
	local := New(Options{})
	if got := local.deliveredKeyWindow(); got != bufferReplayWindow {
		t.Errorf("no Redis wired: window = %d, want the buffer replay bound %d", got, bufferReplayWindow)
	}

	const maxLen = int64(bufferReplayWindow) * 3
	wired := New(Options{RedisClient: &fakeStream{}, RedisStreamMaxLen: maxLen})
	if got := wired.deliveredKeyWindow(); int64(got) != maxLen {
		t.Errorf("Redis wired at maxlen %d: window = %d, want %d; a session that replays the retained window would re-deliver everything past the bound", maxLen, got, maxLen)
	}

	// A stream shorter than the local replay window does not shrink the bound:
	// the gateway-buffer and local-ring windows are still replayable.
	short := New(Options{RedisClient: &fakeStream{}, RedisStreamMaxLen: 10})
	if got := short.deliveredKeyWindow(); got != bufferReplayWindow {
		t.Errorf("short stream: window = %d, want the buffer replay bound %d", got, bufferReplayWindow)
	}
}
