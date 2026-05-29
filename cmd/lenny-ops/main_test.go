// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/events"
	opsLogging "github.com/lennylabs/lenny/pkg/observability/logging"
	opsstream "github.com/lennylabs/lenny/pkg/ops/events"
	"github.com/lennylabs/lenny/pkg/ops/opsservice"
)

// TestNoopElectorNeverLeads confirms the §25.4 fallback Elector — used
// when lenny-ops has no Kubernetes connection — never grants
// leadership, so the leader-only background loops stay idle.
func TestNoopElectorNeverLeads(t *testing.T) {
	var elector opsservice.Elector = noopElector{}
	if elector.IsLeader() {
		t.Error("noopElector.IsLeader() = true, want false")
	}

	started := false
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		elector.Run(
			ctx,
			func(context.Context) { started = true },
			func() {},
		)
		close(done)
	}()
	// Run must block until the context is cancelled and never invoke the
	// leader-acquired callback.
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("noopElector.Run did not return after context cancellation")
	}
	if started {
		t.Error("noopElector invoked the leader-acquired callback, want never")
	}
}

// TestEmptySourcesYieldNothing confirms the §25.5 cold-start sources —
// used until the Redis stream and subscription cache are wired — yield
// no events and no subscriptions, so the webhook worker delivers
// nothing rather than panicking.
func TestEmptySourcesYieldNothing(t *testing.T) {
	events, err := emptyEventSource{}.Poll(context.Background())
	if err != nil || len(events) != 0 {
		t.Errorf("emptyEventSource.Poll = %v, %v; want no events, nil", events, err)
	}
	if subs := (emptySubscriptionSource{}).Subscriptions(); len(subs) != 0 {
		t.Errorf("emptySubscriptionSource.Subscriptions = %v, want none", subs)
	}
}

// spec §25.5: an event published through the redisFanOutEmitter lands
// on the platform-scoped ops:events:stream and also reaches the local
// opsstream.Service buffer so SSE subscribers and the polling cursor see
// it without a separate consumer loop.
func TestRedisFanOutEmitterPublishesToStreamAndLocalBuffer(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	local := opsstream.New(opsstream.Options{})

	em := newRedisFanOutEmitter(client, local, "ops-replica-1")
	if err := em.Emit(context.Background(), events.OperationalEvent{
		Type: "dev.lenny.escalation_created", Severity: "critical",
	}); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	// The local opsstream.Service buffer holds the event; the §25.3
	// error-only contract reads the assigned cursor back via the buffer.
	page := local.Query(0, events.EventFilter{}, 100)
	if len(page.Events) != 1 {
		t.Fatalf("local buffer holds %d events, want 1", len(page.Events))
	}
	if page.Pagination.Cursor == 0 {
		t.Error("local Publish must assign a non-zero buffer cursor")
	}
	if page.Events[0].Event.Type != "dev.lenny.escalation_created" {
		t.Errorf("local event type = %q, want dev.lenny.escalation_created", page.Events[0].Event.Type)
	}

	// The Redis stream carries the same event.
	entries, err := client.XRange(context.Background(), events.DefaultStreamKey, "-", "+").Result()
	if err != nil {
		t.Fatalf("XRange: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("stream length = %d, want 1", len(entries))
	}
	raw, ok := entries[0].Values["event"].(string)
	if !ok {
		t.Fatalf("stream entry missing event field: %+v", entries[0].Values)
	}
	var streamed events.OperationalEvent
	if err := json.Unmarshal([]byte(raw), &streamed); err != nil {
		t.Fatalf("decode stream event: %v", err)
	}
	if streamed.Type != "dev.lenny.escalation_created" {
		t.Errorf("stream event type = %q, want dev.lenny.escalation_created", streamed.Type)
	}
}

// TestEnvHelpers covers the flag-default helpers.
func TestEnvHelpers(t *testing.T) {
	t.Setenv("LENNY_OPS_TEST_STR", "value")
	if got := envOr("LENNY_OPS_TEST_STR", "fallback"); got != "value" {
		t.Errorf("envOr with a set var = %q, want value", got)
	}
	if got := envOr("LENNY_OPS_TEST_UNSET", "fallback"); got != "fallback" {
		t.Errorf("envOr with an unset var = %q, want fallback", got)
	}

	t.Setenv("LENNY_OPS_TEST_INT", "4096")
	if got := envInt64("LENNY_OPS_TEST_INT", 0); got != 4096 {
		t.Errorf("envInt64 with a valid var = %d, want 4096", got)
	}
	t.Setenv("LENNY_OPS_TEST_BADINT", "not-a-number")
	if got := envInt64("LENNY_OPS_TEST_BADINT", 99); got != 99 {
		t.Errorf("envInt64 with a malformed var = %d, want the fallback 99", got)
	}
}

// TestConfigureStructuredLoggingInstallsOpsBridge_spec_25_4_2512 covers the
// §25.4 / §16.4 stdlib-log → slog bridge that configureStructuredLogging
// now wires through the shared logging.Setup helper. Every legacy log.Printf
// call must surface as a structured JSON record carrying component=lenny-ops
// and the renamed ts field so no log line escapes the §25.4 format. The
// generic bridge and ts/UTC behavior are covered in the logging package;
// this asserts the ops binary's component label and stdlib routing.
func TestConfigureStructuredLoggingInstallsOpsBridge_spec_25_4_2512(t *testing.T) {
	var buf bytes.Buffer
	prevLogger := slog.Default()
	prevFlags := log.Flags()
	prevOut := log.Writer()
	t.Cleanup(func() {
		slog.SetDefault(prevLogger)
		log.SetFlags(prevFlags)
		log.SetOutput(prevOut)
	})

	opsLogging.Setup(&buf, "lenny-ops")
	log.Printf("lenny-ops: %s", "bridged")

	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Fatalf("expected structured JSON line, got empty buffer")
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("log line is not JSON: %v\nline: %s", err, line)
	}
	if got["msg"] != "lenny-ops: bridged" {
		t.Errorf("msg = %v, want %q", got["msg"], "lenny-ops: bridged")
	}
	if got["component"] != "lenny-ops" {
		t.Errorf("component = %v, want %q", got["component"], "lenny-ops")
	}
	if got["level"] != "INFO" {
		t.Errorf("level = %v, want INFO", got["level"])
	}
	if _, ok := got["ts"]; !ok {
		t.Errorf("bridged record missing ts: %v", got)
	}
}
