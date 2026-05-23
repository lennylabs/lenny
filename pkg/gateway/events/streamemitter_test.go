// SPDX-License-Identifier: MIT

package events_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/events"
)

// newMiniRedisClient returns a miniredis-backed *redis.Client. The
// caller is responsible for closing the client; miniredis is stopped
// via t.Cleanup.
func newMiniRedisClient(t *testing.T) *redis.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// spec §25.5: every event written through the StreamEmitter lands on
// the §25.5 Redis stream ops:events:stream via XADD, with the local
// in-memory buffer carrying a tee'd copy.
func TestStreamEmitterWritesToRedisAndBuffer(t *testing.T) {
	client := newMiniRedisClient(t)
	buf := events.NewEventBuffer(0)
	em := events.NewStreamEmitter(events.StreamEmitterOptions{
		Client: client, Buffer: buf, ReplicaID: "replica-1",
	})

	id, err := em.Emit(context.Background(), events.OperationalEvent{
		Type: "dev.lenny.alert_fired", Severity: "critical",
	})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if id != 1 {
		t.Errorf("Emit returned local id %d, want 1", id)
	}

	// The event made it to the local buffer.
	page := buf.Query(0, events.EventFilter{}, 100)
	if len(page.Events) != 1 {
		t.Fatalf("local buffer holds %d events, want 1", len(page.Events))
	}

	// And to the Redis stream under the §25.5 default key.
	entries, err := client.XRange(context.Background(), events.DefaultStreamKey, "-", "+").Result()
	if err != nil {
		t.Fatalf("XRange %s: %v", events.DefaultStreamKey, err)
	}
	if len(entries) != 1 {
		t.Fatalf("stream holds %d entries, want 1", len(entries))
	}
	raw, ok := entries[0].Values["event"].(string)
	if !ok {
		t.Fatalf("stream entry has no event field: %+v", entries[0].Values)
	}
	var got events.OperationalEvent
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("decode stream payload: %v", err)
	}
	if got.Type != "dev.lenny.alert_fired" {
		t.Errorf("stream event type = %q, want dev.lenny.alert_fired", got.Type)
	}
	if got.Severity != "critical" {
		t.Errorf("stream event severity = %q, want critical", got.Severity)
	}
	if got.SpecVersion != events.CloudEventsSpecVersion {
		t.Errorf("stream event specversion = %q, want %q", got.SpecVersion, events.CloudEventsSpecVersion)
	}
	if got.ID == "" {
		t.Error("stream event eventKey is empty")
	}
}

// spec §25.5: XADD is approximate-trim (MAXLEN ~ N) so the stream
// caps near streamMaxLen without paying the exact-trim cost on every
// write.
func TestStreamEmitterCapsMaxLen(t *testing.T) {
	client := newMiniRedisClient(t)
	buf := events.NewEventBuffer(0)
	em := events.NewStreamEmitter(events.StreamEmitterOptions{
		Client: client, Buffer: buf, MaxLen: 5,
	})

	for i := 0; i < 20; i++ {
		if _, err := em.Emit(context.Background(), events.OperationalEvent{
			Type: "dev.lenny.alert_fired",
		}); err != nil {
			t.Fatalf("Emit %d: %v", i, err)
		}
	}
	length, err := client.XLen(context.Background(), events.DefaultStreamKey).Result()
	if err != nil {
		t.Fatalf("XLen: %v", err)
	}
	// miniredis honors MAXLEN exactly even under approximate-trim.
	if length > 5 {
		t.Errorf("stream length = %d, want <= 5 under MAXLEN ~ 5", length)
	}
}

// failingRedis returns a Redis error from XAdd to exercise the §25.5
// fall-back behavior: the StreamEmitter still records the event in the
// local buffer so a downstream watchdog can recover via the gateway
// buffer.
type failingRedis struct{}

func (failingRedis) XAdd(ctx context.Context, args *redis.XAddArgs) *redis.StringCmd {
	cmd := redis.NewStringCmd(ctx, "xadd", args.Stream)
	cmd.SetErr(errors.New("redis unavailable"))
	return cmd
}

// spec §25.5: when Redis is unavailable the StreamEmitter falls back to
// the local in-process buffer; the caller surfaces the Redis error so
// operators see the regression. The event is preserved on the local
// path so a watchdog reading the gateway buffer continues to function.
func TestStreamEmitterFallsBackToLocalBufferOnRedisFailure(t *testing.T) {
	buf := events.NewEventBuffer(0)
	em := events.NewStreamEmitter(events.StreamEmitterOptions{
		Client: failingRedis{}, Buffer: buf,
	})
	id, err := em.Emit(context.Background(), events.OperationalEvent{Type: "dev.lenny.alert_fired"})
	if err == nil {
		t.Fatal("Emit on Redis failure returned nil error; want a non-nil error so the caller surfaces the regression")
	}
	if id != 1 {
		t.Errorf("local id = %d, want 1 even on Redis failure (event must reach the local buffer)", id)
	}
	page := buf.Query(0, events.EventFilter{}, 100)
	if len(page.Events) != 1 {
		t.Errorf("local buffer holds %d events, want 1 (fall-back path must preserve the event)", len(page.Events))
	}
}

// spec §4.0: ctx cancellation is honored before the Redis round-trip so
// shutdown does not pin on a hung XADD.
func TestStreamEmitterRespectsContextCancellation(t *testing.T) {
	client := newMiniRedisClient(t)
	buf := events.NewEventBuffer(0)
	em := events.NewStreamEmitter(events.StreamEmitterOptions{
		Client: client, Buffer: buf,
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := em.Emit(ctx, events.OperationalEvent{Type: "dev.lenny.x"}); !errors.Is(err, context.Canceled) {
		t.Errorf("Emit on cancelled ctx returned %v, want context.Canceled", err)
	}
}

// spec §25.5: the Source field defaults to the configured prefix when
// the caller did not stamp one. A caller-set Source is preserved.
func TestStreamEmitterStampsSource(t *testing.T) {
	client := newMiniRedisClient(t)
	buf := events.NewEventBuffer(0)
	em := events.NewStreamEmitter(events.StreamEmitterOptions{
		Client: client, Buffer: buf, Source: "//lenny.dev/gateway/test",
	})
	if _, err := em.Emit(context.Background(), events.OperationalEvent{Type: "dev.lenny.x"}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if _, err := em.Emit(context.Background(), events.OperationalEvent{
		Type: "dev.lenny.x", Source: "//lenny.dev/explicit",
	}); err != nil {
		t.Fatalf("Emit with explicit source: %v", err)
	}
	page := buf.Query(0, events.EventFilter{}, 100)
	if len(page.Events) != 2 {
		t.Fatalf("buffer holds %d events, want 2", len(page.Events))
	}
	if page.Events[0].Event.Source != "//lenny.dev/gateway/test" {
		t.Errorf("default source = %q, want //lenny.dev/gateway/test", page.Events[0].Event.Source)
	}
	if page.Events[1].Event.Source != "//lenny.dev/explicit" {
		t.Errorf("explicit source = %q, want //lenny.dev/explicit", page.Events[1].Event.Source)
	}
}

// spec §25.5: NewStreamEmitter requires a non-nil Client and Buffer —
// the §25.5 fall-back guarantees rely on both being present.
func TestNewStreamEmitterRequiresClientAndBuffer(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("NewStreamEmitter(nil Client) did not panic")
		}
	}()
	events.NewStreamEmitter(events.StreamEmitterOptions{Buffer: events.NewEventBuffer(0)})
}

func TestNewStreamEmitterRequiresBuffer(t *testing.T) {
	client := newMiniRedisClient(t)
	defer func() {
		if r := recover(); r == nil {
			t.Error("NewStreamEmitter(nil Buffer) did not panic")
		}
	}()
	events.NewStreamEmitter(events.StreamEmitterOptions{Client: client})
}
