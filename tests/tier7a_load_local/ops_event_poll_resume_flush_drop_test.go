// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local coverage for the §25.5 polling continuation point across a
// switch back to the Redis ops:events:stream when a recovery flush lands behind
// the peer-replica writes that followed the recovery.
//
// The flush re-emits the events a replica buffered during a Redis outage with
// their original eventKeys, so they land at the stream tail carrying keys older
// than the peer-replica entries XADDed after recovery. Stream order and
// eventKey order then disagree, and the continuation point a carried cursor
// resolves to decides whether the post-recovery entries are delivered at all.
// The polling surface carries no per-connection delivered set, so an event the
// resolved position skips is lost to that caller permanently.
//
// spec: §25.5 (cross-source cursor translation — the handler locates the
// continuation point in the new source; exactly-once across the source switch,
// where eventKey deduplication prevents duplicate consumer-side delivery;
// best-effort recovery flush).

package tier7a_load_local_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	gwevents "github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
	opsstream "github.com/lennylabs/lenny/pkg/ops/events"
	"github.com/lennylabs/lenny/pkg/ops/gateway"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// TestOpsEventPollDeliversPostRecoveryEventsAcrossTheFlushedTail drives a
// polling caller across a Redis outage: it takes a cursor from the
// gateway-buffer fan-out page served while Redis is down, then polls again
// after Redis recovers with the flushed outage window sitting at the stream
// tail behind the peer-replica entries written since. Every event ordering at
// or after the carried cursor must reach the caller.
//
// spec: 25.5 (cross-source cursor translation — the handler locates the
// continuation point in the new source; every event is delivered in eventKey
// order across the transition; eventKey deduplication prevents duplicate
// consumer-side delivery)
// diagnosis: a failure means the continuation point resolves to the last entry
// ordering at or before the cursor rather than the first ordering at or after
// it. The recovery flush puts the oldest keys at the newest stream positions, so
// that rule lands the read position on the flushed tail and starts the page
// after it, silently skipping every peer-replica event XADDed since recovery. A
// polling agent has no delivered set to recover from the drop and never sees
// those events again.
func TestOpsEventPollDeliversPostRecoveryEventsAcrossTheFlushedTail(t *testing.T) {
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx := context.Background()

	const streamKey = "ops:events:stream:pollflushdrop"

	// Redis down, gateway up: the poll serves the gateway-buffer fan-out and
	// mints a cursor from another source, which is the cursor a switch back to
	// Redis has to translate by eventKey.
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
	health.redis.Store(false)
	health.gateway.Store(true)
	svc := opsstream.New(opsstream.Options{
		RedisClient:    opsstream.NewRedisStreamClient(rd.Client),
		RedisStreamKey: streamKey,
		SourceHealth:   health,
		ReplicaID:      "ops-1",
	})
	svc.SetGatewayBufferSource(gwClient)

	base := time.Unix(1700000000, 0).UTC()
	emitted := func(replica string, sec int, kind string) gwevents.OperationalEvent {
		return gwevents.OperationalEvent{
			ID:          fmt.Sprintf("%s:%d:1", replica, 1700000000+sec),
			Type:        kind,
			SpecVersion: gwevents.CloudEventsSpecVersion,
			Time:        base.Add(time.Duration(sec) * time.Second),
		}
	}

	// A pre-outage event both sources hold, then the outage-window events this
	// replica buffers locally because Redis is unreachable.
	peer := eventbuffer.NewStreamEmitter(eventbuffer.StreamEmitterOptions{
		Client:    rd.Client,
		Buffer:    eventbuffer.NewEventBuffer(0),
		StreamKey: streamKey,
		ReplicaID: "gw-1",
	})
	pre := emitted("gw-1", 100, "dev.lenny.alert_fired")
	if err := peer.Emit(ctx, pre); err != nil {
		t.Fatalf("seed the pre-outage stream entry: %v", err)
	}

	// The probe observes Redis go away, which opens the recovery-flush outage
	// window at the current ring head.
	svc.MarkRedisOutage()

	// This replica's own events, buffered locally because Redis is unreachable.
	// They are what the recovery flush later re-emits onto the stream tail with
	// their original, older keys.
	outageWindow := []gwevents.OperationalEvent{
		emitted("ops-1", 200, "dev.lenny.escalation_created"),
		emitted("ops-1", 201, "dev.lenny.drift_detected"),
	}
	for _, ev := range outageWindow {
		if _, err := svc.Publish(ctx, ev); err != nil {
			t.Fatalf("buffer the outage-window event %s: %v", ev.ID, err)
		}
	}
	// A gateway-originated event the fan-out serves during the outage, newer
	// than everything this replica buffered.
	window.add(emitted("gw-1", 250, "dev.lenny.alert_fired"))

	// The caller polls during the outage and carries away the cursor the
	// fan-out page minted, which sits on the newest event it was served.
	degraded, _ := pollPageAt(t, svc, "")
	cursor := pageCursor(t, degraded)
	if got := pageEventKeys(t, degraded); len(got) == 0 {
		t.Fatalf("the degraded fan-out page served nothing: %v", degraded)
	}

	// Redis recovers. The peer replica resumes XADDing while this replica's
	// best-effort flush re-emits the outage window, so the flushed entries land
	// behind the post-recovery ones in stream order while carrying older keys.
	health.redis.Store(true)
	svc.SetRedisReEmitter(peer.Emit)

	postRecovery := []gwevents.OperationalEvent{
		emitted("gw-1", 300, "dev.lenny.pool_state_changed"),
		emitted("gw-1", 301, "dev.lenny.alert_fired"),
	}
	// The peer replica writes first and the flush lands behind it. That is the
	// ordering the flush is defined to produce whenever a peer replica notices
	// the recovery first, and it is the one that puts the oldest keys at the
	// newest stream positions. Running the flush first would leave stream order
	// and eventKey order in agreement, where every continuation rule behaves
	// alike.
	for _, ev := range postRecovery {
		if err := peer.Emit(ctx, ev); err != nil {
			t.Fatalf("peer replica XADD %s: %v", ev.ID, err)
		}
	}
	if _, err := svc.FlushBufferedToRedis(ctx); err != nil {
		t.Fatalf("recovery flush: %v", err)
	}

	// The caller resumes with the cursor it carried out of the outage. Every
	// retained event ordering after it is owed to this page.
	recovered, _ := pollPageAt(t, svc, cursor)
	served := make(map[string]bool)
	for _, key := range pageEventKeys(t, recovered) {
		served[key] = true
	}
	for _, ev := range postRecovery {
		if !served[ev.ID] {
			t.Errorf("the post-recovery event %s was never served to a caller resuming from the outage cursor; the continuation point resolved past it onto the flushed tail (page=%v)",
				ev.ID, pageEventKeys(t, recovered))
		}
	}
}

// pageCursor returns the pagination cursor a poll envelope carries, failing the
// test when the page minted none.
func pageCursor(t *testing.T, body map[string]any) string {
	t.Helper()
	pagination, ok := body["pagination"].(map[string]any)
	if !ok {
		t.Fatalf("poll envelope carries no pagination object: %v", body)
	}
	cursor, ok := pagination["cursor"].(string)
	if !ok || cursor == "" {
		t.Fatalf("poll envelope minted no cursor: %v", pagination)
	}
	return cursor
}

// pageEventKeys returns the canonical eventKey of every item on a poll page, in
// page order.
func pageEventKeys(t *testing.T, body map[string]any) []string {
	t.Helper()
	raw, ok := body["items"].([]any)
	if !ok {
		t.Fatalf("poll envelope carries no items array: %v", body)
	}
	keys := make([]string, 0, len(raw))
	for i, it := range raw {
		item, ok := it.(map[string]any)
		if !ok {
			t.Fatalf("item %d is not an object: %v", i, it)
		}
		ev, ok := item["event"].(map[string]any)
		if !ok {
			t.Fatalf("item %d carries no CloudEvents record: %v", i, item)
		}
		id, ok := ev["id"].(string)
		if !ok {
			t.Fatalf("item %d CloudEvents record carries no id: %v", i, ev)
		}
		keys = append(keys, id)
	}
	return keys
}
