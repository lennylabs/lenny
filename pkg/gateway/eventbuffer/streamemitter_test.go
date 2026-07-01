// SPDX-License-Identifier: MIT

package eventbuffer_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
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
	buf := eventbuffer.NewEventBuffer(0)
	em := eventbuffer.NewStreamEmitter(eventbuffer.StreamEmitterOptions{
		Client: client, Buffer: buf, ReplicaID: "replica-1",
	})

	if err := em.Emit(context.Background(), events.OperationalEvent{
		Type: "dev.lenny.alert_fired", Severity: "critical",
	}); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	// The event made it to the local buffer; the §25.3 error-only
	// contract reads the assigned id back via the buffer cursor.
	page := buf.Query(0, events.EventFilter{}, 100)
	if len(page.Events) != 1 {
		t.Fatalf("local buffer holds %d events, want 1", len(page.Events))
	}
	if page.Events[0].ID != 1 {
		t.Errorf("local buffer id = %d, want 1", page.Events[0].ID)
	}

	// And to the Redis stream under the §25.5 default key.
	entries, err := client.XRange(context.Background(), eventbuffer.DefaultStreamKey, "-", "+").Result()
	if err != nil {
		t.Fatalf("XRange %s: %v", eventbuffer.DefaultStreamKey, err)
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
	buf := eventbuffer.NewEventBuffer(0)
	em := eventbuffer.NewStreamEmitter(eventbuffer.StreamEmitterOptions{
		Client: client, Buffer: buf, MaxLen: 5,
	})

	for i := 0; i < 20; i++ {
		if err := em.Emit(context.Background(), events.OperationalEvent{
			Type: "dev.lenny.alert_fired",
		}); err != nil {
			t.Fatalf("Emit %d: %v", i, err)
		}
	}
	length, err := client.XLen(context.Background(), eventbuffer.DefaultStreamKey).Result()
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
	buf := eventbuffer.NewEventBuffer(0)
	em := eventbuffer.NewStreamEmitter(eventbuffer.StreamEmitterOptions{
		Client: failingRedis{}, Buffer: buf,
	})
	if err := em.Emit(context.Background(), events.OperationalEvent{Type: "dev.lenny.alert_fired"}); err == nil {
		t.Fatal("Emit on Redis failure returned nil error; want a non-nil error so the caller surfaces the regression")
	}
	page := buf.Query(0, events.EventFilter{}, 100)
	if len(page.Events) != 1 {
		t.Errorf("local buffer holds %d events, want 1 (fall-back path must preserve the event)", len(page.Events))
	}
	if page.Events[0].ID != 1 {
		t.Errorf("local id = %d, want 1 even on Redis failure (event must reach the local buffer)", page.Events[0].ID)
	}
}

// spec §4.0: ctx cancellation is honored before the Redis round-trip so
// shutdown does not pin on a hung XADD.
func TestStreamEmitterRespectsContextCancellation(t *testing.T) {
	client := newMiniRedisClient(t)
	buf := eventbuffer.NewEventBuffer(0)
	em := eventbuffer.NewStreamEmitter(eventbuffer.StreamEmitterOptions{
		Client: client, Buffer: buf,
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := em.Emit(ctx, events.OperationalEvent{Type: "dev.lenny.x"}); !errors.Is(err, context.Canceled) {
		t.Errorf("Emit on cancelled ctx returned %v, want context.Canceled", err)
	}
}

// spec §25.5: the Source field defaults to the configured prefix when
// the caller did not stamp one. A caller-set Source is preserved.
func TestStreamEmitterStampsSource(t *testing.T) {
	client := newMiniRedisClient(t)
	buf := eventbuffer.NewEventBuffer(0)
	em := eventbuffer.NewStreamEmitter(eventbuffer.StreamEmitterOptions{
		Client: client, Buffer: buf, Source: "//lenny.dev/gateway/test",
	})
	if err := em.Emit(context.Background(), events.OperationalEvent{Type: "dev.lenny.x"}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if err := em.Emit(context.Background(), events.OperationalEvent{
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
	eventbuffer.NewStreamEmitter(eventbuffer.StreamEmitterOptions{Buffer: eventbuffer.NewEventBuffer(0)})
}

func TestNewStreamEmitterRequiresBuffer(t *testing.T) {
	client := newMiniRedisClient(t)
	defer func() {
		if r := recover(); r == nil {
			t.Error("NewStreamEmitter(nil Buffer) did not panic")
		}
	}()
	eventbuffer.NewStreamEmitter(eventbuffer.StreamEmitterOptions{Client: client})
}

// spec §4.0 / §25.5: multiple processes (gateway, controller,
// lenny-ops, token-service) write to the same platform-scoped Redis
// stream `ops:events:stream`. The stable eventKey carries the
// per-process replicaID so a downstream consumer can attribute each
// event to its emitting process. miniredis stands in for the Redis
// stream; the two emitters share the client and read back both records
// in order.
func TestStreamEmitterMultiProcessReplay(t *testing.T) {
	client := newMiniRedisClient(t)
	gatewayBuf := eventbuffer.NewEventBuffer(0)
	controllerBuf := eventbuffer.NewEventBuffer(0)
	gatewayEmitter := eventbuffer.NewStreamEmitter(eventbuffer.StreamEmitterOptions{
		Client: client, Buffer: gatewayBuf,
		Source: "//lenny.dev/gateway/replica-1", ReplicaID: "gateway-replica-1",
	})
	controllerEmitter := eventbuffer.NewStreamEmitter(eventbuffer.StreamEmitterOptions{
		Client: client, Buffer: controllerBuf,
		Source: "//lenny.dev/controller/replica-1", ReplicaID: "controller-replica-1",
	})

	if err := gatewayEmitter.Emit(context.Background(), events.OperationalEvent{
		Type: "dev.lenny.session_failed",
	}); err != nil {
		t.Fatalf("gateway Emit: %v", err)
	}
	if err := controllerEmitter.Emit(context.Background(), events.OperationalEvent{
		Type: "dev.lenny.pool_state_changed",
	}); err != nil {
		t.Fatalf("controller Emit: %v", err)
	}

	entries, err := client.XRange(context.Background(), eventbuffer.DefaultStreamKey, "-", "+").Result()
	if err != nil {
		t.Fatalf("XRange: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("stream holds %d entries, want 2 (one per process)", len(entries))
	}

	// The gateway-emitted event lands in the gateway buffer only;
	// the controller-emitted event lands in the controller buffer only.
	// The per-process local buffer is private, even though the Redis
	// destination is shared.
	if got := len(gatewayBuf.Query(0, events.EventFilter{}, 100).Events); got != 1 {
		t.Errorf("gateway local buffer holds %d events, want 1", got)
	}
	if got := len(controllerBuf.Query(0, events.EventFilter{}, 100).Events); got != 1 {
		t.Errorf("controller local buffer holds %d events, want 1", got)
	}

	// Each stream entry's eventKey ID prefix identifies its emitting
	// process, so lenny-ops can attribute and replay it.
	var payloads []map[string]any
	for _, entry := range entries {
		raw, ok := entry.Values["event"].(string)
		if !ok {
			t.Fatalf("stream entry %s missing event payload", entry.ID)
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(raw), &ev); err != nil {
			t.Fatalf("unmarshal stream entry %s: %v", entry.ID, err)
		}
		payloads = append(payloads, ev)
	}
	if !strings.HasPrefix(payloads[0]["id"].(string), "gateway-replica-1:") {
		t.Errorf("first event id = %q, want prefix gateway-replica-1:", payloads[0]["id"])
	}
	if !strings.HasPrefix(payloads[1]["id"].(string), "controller-replica-1:") {
		t.Errorf("second event id = %q, want prefix controller-replica-1:", payloads[1]["id"])
	}
}
