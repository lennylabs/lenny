// SPDX-License-Identifier: MIT

package sessionevents_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
)

// spec: §4.4 line 225 — durable event cursors across replicas.
// spec: §12.3.7 — Redis-backed event bus substrate.

func newMiniRedis(t *testing.T) (*miniredis.Miniredis, redis.UniversalClient) {
	t.Helper()
	mr := miniredis.RunT(t)
	cli := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() {
		_ = cli.Close()
		mr.Close()
	})
	return mr, cli
}

// TestRelayPublishWritesToStream confirms PublishEvent appends a
// stream entry the read path can decode back into the original Event.
func TestRelayPublishWritesToStream(t *testing.T) {
	_, cli := newMiniRedis(t)
	relay := sessionevents.NewRedisRelay(cli)
	ctx := context.Background()

	ev := sessionevents.Event{
		Seq:       1,
		SessionID: "sess-1",
		Type:      "message_delivered",
		Data:      `{"hello":"world"}`,
		Timestamp: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	relay.PublishEvent(ctx, ev)

	got, err := relay.History(ctx, "sess-1", 0)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("History returned %d entries, want 1", len(got))
	}
	if got[0].Seq != ev.Seq || got[0].Type != ev.Type || got[0].Data != ev.Data {
		t.Errorf("History entry = %+v, want %+v", got[0], ev)
	}
}

// TestRelayHistoryFiltersBySeq confirms the cursor-based filter
// excludes events ≤ afterSeq.
func TestRelayHistoryFiltersBySeq(t *testing.T) {
	_, cli := newMiniRedis(t)
	relay := sessionevents.NewRedisRelay(cli)
	ctx := context.Background()
	for i := uint64(1); i <= 5; i++ {
		relay.PublishEvent(ctx, sessionevents.Event{
			Seq:       i,
			SessionID: "sess-1",
			Type:      "t",
			Data:      "{}",
			Timestamp: time.Now(),
		})
	}
	got, err := relay.History(ctx, "sess-1", 3)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("History after Seq 3 returned %d entries, want 2", len(got))
	}
	if got[0].Seq != 4 || got[1].Seq != 5 {
		t.Errorf("History seqs = %d, %d, want 4, 5", got[0].Seq, got[1].Seq)
	}
}

// TestRelayNilClientIsNoOp confirms PublishEvent / History tolerate a
// nil Redis client (the single-replica dev-mode wiring).
func TestRelayNilClientIsNoOp(t *testing.T) {
	relay := sessionevents.NewRedisRelay(nil)
	relay.PublishEvent(context.Background(), sessionevents.Event{Seq: 1, SessionID: "x"})
	got, err := relay.History(context.Background(), "x", 0)
	if err != nil {
		t.Errorf("History on nil-client = %v, want nil", err)
	}
	if got != nil {
		t.Errorf("History on nil-client = %v, want nil", got)
	}
}

// TestBusWithRelayPublishFanout confirms the Bus.Publish path fans out
// to the Redis relay so a reader on a sibling replica reading via
// Relay.History sees the event.
//
// spec: §4.4 line 225 — durable cursors across replicas.
func TestBusWithRelayPublishFanout(t *testing.T) {
	_, cli := newMiniRedis(t)
	relay := sessionevents.NewRedisRelay(cli)
	bus := sessionevents.NewBus(0).WithRedisRelay(relay)

	now := time.Now().UTC()
	bus.Publish("sess-X", "message_delivered", `{"k":"v"}`, now)

	// The other replica reads via the relay directly.
	got, err := relay.History(context.Background(), "sess-X", 0)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("History returned %d entries, want 1", len(got))
	}
	if got[0].Seq != 1 {
		t.Errorf("Seq = %d, want 1", got[0].Seq)
	}
	if got[0].Type != "message_delivered" {
		t.Errorf("Type = %q", got[0].Type)
	}
}

// TestBusSubscribeMergesCrossReplicaHistory confirms a reconnecting
// client on this replica sees events originally published on a
// sibling replica via the cross-replica history merge.
//
// spec: §4.4 line 225 — durable cursors across replicas.
func TestBusSubscribeMergesCrossReplicaHistory(t *testing.T) {
	_, cli := newMiniRedis(t)
	relay := sessionevents.NewRedisRelay(cli)

	// Sibling replica publishes via its own Bus + same relay.
	publisher := sessionevents.NewBus(0).WithRedisRelay(relay)
	now := time.Now().UTC()
	publisher.Publish("sess-2", "type-a", `{"i":1}`, now)
	publisher.Publish("sess-2", "type-b", `{"i":2}`, now)

	// This replica has its own Bus with no local history for sess-2
	// (the publishes happened on the other replica). Subscribe with
	// afterSeq=0 should still produce the backlog from the cross-
	// replica relay.
	reader := sessionevents.NewBus(0).WithRedisRelay(relay)
	sub := reader.Subscribe("sess-2", 0, 4)
	defer sub.Close()
	if len(sub.Backlog) != 2 {
		t.Fatalf("Backlog merged size = %d, want 2 (cross-replica history)", len(sub.Backlog))
	}
	if sub.Backlog[0].Seq != 1 || sub.Backlog[1].Seq != 2 {
		t.Errorf("Backlog seqs = %d, %d, want 1, 2", sub.Backlog[0].Seq, sub.Backlog[1].Seq)
	}
}

// TestBusSubscribePrefersLocalEntryOnSeqCollision confirms the merge
// keeps the local copy when both lists carry the same Seq — the
// publishing replica's payload wins over the relay re-encode.
func TestBusSubscribePrefersLocalEntryOnSeqCollision(t *testing.T) {
	_, cli := newMiniRedis(t)
	relay := sessionevents.NewRedisRelay(cli)
	bus := sessionevents.NewBus(0).WithRedisRelay(relay)

	now := time.Now().UTC()
	bus.Publish("sess-3", "local-type", `{"src":"local"}`, now)
	// The publish fans out to Redis; subscribe should not double-
	// count the event.
	sub := bus.Subscribe("sess-3", 0, 4)
	defer sub.Close()
	if len(sub.Backlog) != 1 {
		t.Fatalf("Backlog dedup size = %d, want 1", len(sub.Backlog))
	}
	if sub.Backlog[0].Data != `{"src":"local"}` {
		t.Errorf("local payload lost on dedup: %q", sub.Backlog[0].Data)
	}
}

// TestBusWithoutRelayKeepsExistingBehaviour confirms the in-memory
// path is untouched when no relay is wired.
func TestBusWithoutRelayKeepsExistingBehaviour(t *testing.T) {
	bus := sessionevents.NewBus(0) // no relay
	now := time.Now().UTC()
	ev := bus.Publish("sess-4", "type-x", "{}", now)
	if ev.Seq != 1 {
		t.Errorf("Seq = %d, want 1", ev.Seq)
	}
	sub := bus.Subscribe("sess-4", 0, 4)
	defer sub.Close()
	if len(sub.Backlog) != 1 {
		t.Errorf("Backlog len = %d, want 1", len(sub.Backlog))
	}
}

// TestRelayStreamRetentionIsBounded confirms the relay applies the
// MaxLen trim so a long-running session does not grow the Redis
// stream unbounded.
//
// spec: §4.4 line 225 — the cross-replica replay buffer is bounded.
func TestRelayStreamRetentionIsBounded(t *testing.T) {
	_, cli := newMiniRedis(t)
	relay := &sessionevents.RedisRelay{
		Client: cli,
		MaxLen: 4, // hard cap so the test exercises the trim
	}
	ctx := context.Background()
	for i := uint64(1); i <= 10; i++ {
		relay.PublishEvent(ctx, sessionevents.Event{
			Seq:       i,
			SessionID: "sess-cap",
			Type:      "t",
			Data:      "{}",
			Timestamp: time.Now(),
		})
	}
	got, err := relay.History(ctx, "sess-cap", 0)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	// miniredis honours the approximate trim by capping near the
	// requested MaxLen; assert the stream length is bounded.
	if len(got) > 6 {
		t.Errorf("History returned %d entries, want ≤ 6 (MAXLEN ~ 4)", len(got))
	}
}
