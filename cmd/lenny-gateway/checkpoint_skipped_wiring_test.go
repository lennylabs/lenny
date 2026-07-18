// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
)

// spec: §4.4 line 255 — on the adapter's workspace-size-probe rejection the
// gateway emits a checkpoint.skipped{reason} session event on the session-event
// bus so the client is aware. This pins the gateway-side emitter the
// checkpointer's SkippedEventFunc is wired to. Against the pre-fix wiring, where
// SkippedEventFunc was left nil and no gateway-side emitter existed, no event
// reaches the bus and this test fails.
func TestCheckpointSkippedEmitterPublishesOnTheSessionBus(t *testing.T) {
	bus := sessionevents.NewBus(16)
	sub, err := bus.SubscribeForTenant("acme", "s1", 0, 8)
	if err != nil {
		t.Fatalf("SubscribeForTenant: %v", err)
	}
	defer sub.Close()

	emit := newCheckpointSkippedEmitter(bus)
	emit(context.Background(), "acme", "s1", "workspace_size_limit")

	select {
	case ev := <-sub.Events():
		if ev.Type != "checkpoint.skipped" {
			t.Errorf("event type = %q, want checkpoint.skipped", ev.Type)
		}
		if ev.Data != `{"reason":"workspace_size_limit"}` {
			t.Errorf("event data = %q, want {\"reason\":\"workspace_size_limit\"}", ev.Data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no checkpoint.skipped event delivered; the gateway-side size-limit emitter did not publish")
	}
}
