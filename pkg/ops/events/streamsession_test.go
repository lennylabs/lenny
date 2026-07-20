// SPDX-License-Identifier: MIT

package events

import (
	"net/http/httptest"
	"strings"
	"testing"

	gwevents "github.com/lennylabs/lenny/pkg/events"
)

// spec: 25.5 (eventKey dedup across sources, exactly-once across the source
// switch) — the Redis-served SSE path writes a frame only for an event whose
// eventKey orders strictly after the last position the connection delivered.
// The best-effort recovery flush re-emits the events a replica buffered during
// a Redis outage with their original eventKeys, so they reach an open
// connection's live tail carrying keys it already received from the local ring
// or the gateway-buffer fall-back during that outage. The pre-fix path wrote a
// frame for every entry the backlog scan and the tail produced, so those events
// were delivered a second time on the switch back to Redis.
func TestDeliverForward_SkipsKeysAlreadyDeliveredOnThisConnection_spec_25_5(t *testing.T) {
	rec := httptest.NewRecorder()
	st := &streamSession{w: rec, flusher: rec}

	frame := func(key string) gwevents.BufferedEvent {
		return gwevents.BufferedEvent{Event: gwevents.OperationalEvent{
			ID:          key,
			Type:        "dev.lenny.escalation_created",
			SpecVersion: gwevents.CloudEventsSpecVersion,
		}}
	}

	// The connection delivers two events while serving the outage from the
	// local ring.
	for _, key := range []string{"ops:20:1", "ops:25:1"} {
		if !st.deliverForward(frame(key)) {
			t.Fatalf("event %s was not delivered on a fresh connection", key)
		}
	}
	// Redis recovers and the flush re-emits both with their original keys, so
	// they arrive again on the Redis tail.
	for _, key := range []string{"ops:20:1", "ops:25:1"} {
		if st.deliverForward(frame(key)) {
			t.Errorf("the flushed event %s was delivered a second time on the same connection", key)
		}
	}
	// A post-recovery event with a later key still flows.
	if !st.deliverForward(frame("gw:30:1")) {
		t.Error("a post-recovery event ordering after the resume position was not delivered")
	}

	if got := strings.Count(rec.Body.String(), "id: "); got != 3 {
		t.Fatalf("wrote %d frames; want 3 (each event once):\n%s", got, rec.Body.String())
	}
	if st.lastKey != "gw:30:1" {
		t.Errorf("resume position = %q; want gw:30:1 (the flush must not rewind it)", st.lastKey)
	}
}
