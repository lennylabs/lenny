//go:build component

// SPDX-License-Identifier: MIT

// Tier-2 component coverage for the §25.5 cross-source cursor translation when
// the caller's carried position orders after every event the new source
// retains. §25.5 makes that a gap: the new source has no event with a
// greater-or-equal eventKey, so it cannot locate a continuation point and the
// caller is told to re-read platform state. The gap is a signal rather than a
// redelivery, so the retained window is not replayed behind it.
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

// TestOpsEventStreamResumeAheadOfRedisWindowReportsGap opens an SSE connection
// whose Last-Event-ID orders after every entry the Redis stream retains, and
// asserts the connection is told about the gap without being re-served the
// window. The poll path is asserted on the same cursor, since both resolve
// through the same translation.
//
// spec: 25.5 (cursor transition safety — the handler emits a :gap comment when
// no event in the new source has a greater-or-equal eventKey) — the stream
// cannot locate a continuation point for a position ahead of everything it
// retains, so the caller is signalled rather than silently resumed.
// diagnosis: a failure means the read side reports the gap only for a position
// that predates the retained window, so a caller whose position the new source
// cannot honour resumes silently and never learns to re-read platform state; or
// the gap now replays the retained window and duplicates events the caller
// already holds.
func TestOpsEventStreamResumeAheadOfRedisWindowReportsGap(t *testing.T) {
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
	if !sawGap {
		t.Error("a resume position the stream cannot locate emitted no :gap comment")
	}
	if frames != 0 {
		t.Errorf("resume ahead of the window replayed %d frames, want 0", frames)
	}

	page := pollRedis(t, svc, encodeMixedCursor(t, aheadKey))
	if !page.Pagination.GapDetected {
		t.Error("poll with a cursor ahead of the retained window reported no gapDetected")
	}
	if len(page.Items) != 0 {
		t.Errorf("poll ahead of the window returned %d items, want 0", len(page.Items))
	}
}

// TestOpsEventStreamResumeAheadOfGatewayWindowReportsGap drives the same branch
// in the other direction: the connection serves from the gateway-buffer fan-out
// during a Redis outage while carrying a resume position that orders after
// every event the replicas retain.
//
// spec: 25.5 (Redis-down gateway-buffer fallback; a :gap comment when no event
// in the new source has a greater-or-equal eventKey).
// diagnosis: a failure means a connection switching into the gateway-buffer
// fall-back with a position the replicas cannot honour is resumed silently, so
// the consumer never learns to re-read platform state; or the fall-back replays
// the whole fan-out window behind the gap and duplicates events the connection
// already delivered.
func TestOpsEventStreamResumeAheadOfGatewayWindowReportsGap(t *testing.T) {
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
	if !sawGap {
		t.Error("a resume position ahead of every replica's buffer window emitted no :gap comment")
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
