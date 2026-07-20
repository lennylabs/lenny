// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local coverage for the §25.5 SSE resume position when the
// replica-level recovery flush fires underneath an open connection.
//
// The flush re-emits the lenny-ops events buffered during a Redis outage, so
// those entries arrive at the head of the recovered stream carrying eventKeys
// that order before entries an open connection already consumed. The
// connection's resume position is what keeps a later source switch
// exactly-once, so it must only ever move forward: a position that rewinds to a
// re-emitted key makes the next switch replay every event after it.
//
// spec: §25.5 (best-effort recovery flush, cross-switch no-drop ordering,
// independent per-connection read cursor).

package tier7a_load_local_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gwevents "github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
	opsstream "github.com/lennylabs/lenny/pkg/ops/events"
	"github.com/lennylabs/lenny/pkg/ops/gateway"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// newReplicaServer serves one gateway replica's §25.3 buffer window over
// GET /v1/admin/events/buffer, so the fan-out fall-back reads a window that
// overlaps the Redis stream.
func newReplicaServer(window *replicaWindow) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(window.page())
	}))
}

// TestOpsEventStreamResumeDoesNotRewindOnRecoveryFlush holds SSE connections
// open across a Redis recovery flush, then drives a source switch, and asserts
// every event is delivered exactly once. The flushed entries carry older
// eventKeys than the live tail has already delivered, so a resume position that
// takes the last frame unconditionally rewinds and the switch replays the
// newer window.
//
// diagnosis: a failure means the §25.5 resume position is not monotonic: an
// event re-emitted by the recovery flush moved an open connection's cursor
// backward, and the next source switch re-delivered every event after it, so a
// consumer receives duplicate operational events after every Redis recovery it
// straddles.
func TestOpsEventStreamResumeDoesNotRewindOnRecoveryFlush(t *testing.T) {
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx := context.Background()

	const streamKey = "ops:events:stream:flushresume"
	const connections = 4

	window := &replicaWindow{}
	gwSrv := newReplicaServer(window)
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

	health := &switchHealth{}
	health.redis.Store(true)
	health.gateway.Store(true)
	svc := opsstream.New(opsstream.Options{
		RedisClient:    rd.Client,
		RedisStreamKey: streamKey,
		SourceHealth:   health,
		ReplicaID:      "ops-1",
	})
	svc.SetGatewayBufferSource(gwClient)

	emitter := eventbuffer.NewStreamEmitter(eventbuffer.StreamEmitterOptions{
		Client:    rd.Client,
		Buffer:    eventbuffer.NewEventBuffer(0),
		StreamKey: streamKey,
		MaxLen:    1000,
		ReplicaID: "gw-1",
	})

	var order []string
	// emit XADDs one event with an explicit eventKey and mirrors it into the
	// gateway replica window, so both source windows overlap the way they do in
	// production.
	emit := func(key string, at time.Time) {
		t.Helper()
		ev := gwevents.OperationalEvent{
			ID:              key,
			Type:            gwevents.EventType("alert_fired").CloudEventsType(),
			Subject:         "pool/" + key,
			Severity:        "warning",
			SpecVersion:     gwevents.CloudEventsSpecVersion,
			Time:            at.UTC(),
			DataContentType: gwevents.ContentTypeJSON,
			Data:            json.RawMessage(`{"alert":"x"}`),
		}
		if err := emitter.Emit(ctx, ev); err != nil {
			t.Fatalf("emit %s: %v", key, err)
		}
		window.add(ev)
		order = append(order, key)
	}

	// Two events already on the stream when the connections open.
	emit("gw-1:2000:1", time.Unix(2000, 0))
	emit("gw-1:3000:1", time.Unix(3000, 0))

	readers := make([]*sseReader, connections)
	for i := range readers {
		readers[i] = openSSEReader(svc)
	}
	defer func() {
		for _, r := range readers {
			r.close()
		}
	}()
	awaitAll(t, readers, len(order), "the Redis-served backlog")

	// Redis recovers underneath the open connections and the replica-level
	// flush re-emits an event lenny-ops buffered during the outage. It is
	// XADDed now, so it arrives last, but its eventKey orders before everything
	// already delivered.
	emit("ops-1:1000:1", time.Unix(1000, 0))
	awaitAll(t, readers, len(order), "the recovery-flushed event")

	// A source switch out to the gateway-buffer fall-back and back. Each
	// connection resumes from the position it carries, which must still be the
	// newest key it delivered rather than the flushed one.
	health.redis.Store(false)
	time.Sleep(3 * time.Second)
	health.redis.Store(true)
	emit("gw-1:4000:1", time.Unix(4000, 0))
	awaitAll(t, readers, len(order), "the post-switch event")
	// Give a rewound session time to replay the window before asserting.
	time.Sleep(3 * time.Second)

	// The flushed entry sits physically after the events it orders before, so a
	// resume that keys on stream position may hand it back once; that is the
	// stream-position resume working as specified. What must not happen is the
	// window newer than it being replayed, which is what a rewound resume
	// position causes.
	for i, r := range readers {
		got := r.delivered()
		seen := map[string]int{}
		for _, k := range got {
			seen[k]++
		}
		for _, k := range []string{"gw-1:2000:1", "gw-1:3000:1", "gw-1:4000:1"} {
			if seen[k] != 1 {
				t.Fatalf("connection %d delivered %s %d times; want exactly 1 — the resume position rewound to the flushed event and replayed the newer window (delivered %v)", i, k, seen[k], got)
			}
		}
		if seen["ops-1:1000:1"] == 0 {
			t.Fatalf("connection %d never received the recovery-flushed event (delivered %v)", i, got)
		}
	}
}
