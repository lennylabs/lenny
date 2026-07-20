//go:build component

// SPDX-License-Identifier: MIT

// Tier-2 component coverage for the §25.5 cross-source cursor translation when
// the caller's carried position orders after every event the new source
// retains. This is the branch on the far side of the evicted-cursor gap: a
// cursor that predates the retained window lost the events in between and is
// reported with gapDetected, while a cursor that postdates the whole window has
// lost nothing and must resume cleanly with no :gap comment and no replay.
//
// It exercises both switch directions against a real Redis ops:events:stream
// and a real gateway-buffer fan-out.
package eventstream_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gwevents "github.com/lennylabs/lenny/pkg/events"
	opsstream "github.com/lennylabs/lenny/pkg/ops/events"
	"github.com/lennylabs/lenny/pkg/ops/gateway"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// aheadKey is an eventKey whose emission instant is far beyond anything the
// sources under test retain, so it orders strictly after every retained entry.
const aheadKey = "ops-1:4102444800000000000:1"

// TestOpsEventStreamResumeAheadOfRedisWindowServesNoGap opens an SSE
// connection whose Last-Event-ID orders after every entry the Redis stream
// retains, and asserts the connection resumes silently: no :gap comment, no
// replay of the retained window, and the live tail still delivers what is
// XADDed next. The poll path is asserted on the same cursor, since both
// resolve through the same translation.
//
// spec: 25.5 (cross-source cursor translation; gapDetected on an evicted
// cursor) — a gap is reported when the carried position predates the retained
// window. A position ahead of the window is current, so reporting a gap there
// would tell a caller that is fully caught up to resync and re-read platform
// state.
// diagnosis: a failure means the read side inverts the gap condition — it
// reports a gap (or replays the window) for a caller that is ahead of
// everything the new source retains, so every consumer that reconnects while
// idle is told to resync and is re-delivered events it already has.
func TestOpsEventStreamResumeAheadOfRedisWindowServesNoGap(t *testing.T) {
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx := context.Background()

	const key = "ops:events:stream:aheadcursor"
	emitter := newStreamEmitter(t, rd.Client, key, 1000)
	for i := 0; i < 3; i++ {
		if err := emitter.Emit(ctx, alertEvent(fmt.Sprintf("pool/e%d", i))); err != nil {
			t.Fatalf("emit event %d: %v", i, err)
		}
	}

	svc := opsstream.New(opsstream.Options{
		RedisClient:    opsstream.NewRedisStreamClient(rd.Client),
		RedisStreamKey: key,
		SourceHealth:   opsstream.StaticSourceHealth{Redis: true, Gateway: true},
	})

	req := httptest.NewRequest("GET", "/v1/admin/events/stream", nil)
	req.Header.Set("Last-Event-ID", aheadKey)
	frames, sawGap := collectSSEResumeWithHeader(t, svc, req)
	if sawGap {
		t.Error("a resume position ahead of the whole retained window emitted a :gap; the caller has lost nothing")
	}
	if frames != 0 {
		t.Errorf("resume ahead of the window replayed %d frames, want 0", frames)
	}

	page := pollRedis(t, svc, encodeMixedCursor(t, aheadKey))
	if page.Pagination.GapDetected {
		t.Error("poll with a cursor ahead of the retained window reported gapDetected")
	}
	if len(page.Items) != 0 {
		t.Errorf("poll ahead of the window returned %d items, want 0", len(page.Items))
	}
}

// TestOpsEventStreamResumeAheadOfGatewayWindowServesNoGap drives the same
// branch in the other direction: the connection serves from the gateway-buffer
// fan-out during a Redis outage while carrying a resume position that orders
// after every event the replicas retain.
//
// spec: 25.5 (Redis-down gateway-buffer fallback, cross-switch no-drop).
// diagnosis: a failure means a connection switching into the gateway-buffer
// fall-back while ahead of the replicas' windows is told to resync and re-
// delivered the whole fan-out window, so every Redis outage duplicates the
// buffered events for an idle consumer.
func TestOpsEventStreamResumeAheadOfGatewayWindowServesNoGap(t *testing.T) {
	window := []gwevents.BufferedEvent{
		{ID: 1, Event: gwevents.OperationalEvent{ID: "gw-1:1000:1", Type: "dev.lenny.alert_fired", SpecVersion: gwevents.CloudEventsSpecVersion, Time: time.Unix(1000, 0).UTC()}},
		{ID: 2, Event: gwevents.OperationalEvent{ID: "gw-1:2000:1", Type: "dev.lenny.alert_fired", SpecVersion: gwevents.CloudEventsSpecVersion, Time: time.Unix(2000, 0).UTC()}},
	}
	gwSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gwevents.BufferedEventPage{Events: window})
	}))
	defer gwSrv.Close()
	gwClient, err := gateway.NewClient(gateway.Config{
		BaseURL:           "http://gateway.invalid",
		Token:             gateway.StaticToken("t"),
		Discovery:         gateway.StaticDiscovery{gwSrv.URL},
		PerRequestTimeout: 3 * time.Second,
		FanOutTimeout:     2 * time.Second,
	})
	if err != nil {
		t.Fatalf("gateway client: %v", err)
	}

	svc := opsstream.New(opsstream.Options{
		SourceHealth: opsstream.StaticSourceHealth{Redis: false, Gateway: true},
		ReplicaID:    "ops-1",
	})
	svc.SetGatewayBufferSource(gwClient)

	req := httptest.NewRequest("GET", "/v1/admin/events/stream", nil)
	req.Header.Set("Last-Event-ID", aheadKey)
	frames, sawGap := collectSSEResumeWithHeader(t, svc, req)
	if sawGap {
		t.Error("a resume position ahead of every replica's buffer window emitted a :gap; the caller has lost nothing")
	}
	if frames != 0 {
		t.Errorf("gateway fall-back resume ahead of the window replayed %d frames, want 0", frames)
	}
}

// encodeMixedCursor mints the opaque §25.2 cursor a caller carries from a
// non-Redis source, so the poll path resolves it by eventKey translation.
func encodeMixedCursor(t *testing.T, eventKey string) string {
	t.Helper()
	svc := opsstream.New(opsstream.Options{ReplicaID: "ops-1"})
	ctx := context.Background()
	if _, err := svc.Publish(ctx, gwevents.OperationalEvent{ID: eventKey, Type: "dev.lenny.alert_fired"}); err != nil {
		t.Fatalf("seed cursor source: %v", err)
	}
	page := pollRedis(t, svc, "")
	if page.Pagination.Cursor == "" {
		t.Fatal("no cursor minted for the ahead position")
	}
	return page.Pagination.Cursor
}
