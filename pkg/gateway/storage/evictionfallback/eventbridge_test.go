// SPDX-License-Identifier: MIT

package evictionfallback_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/storage/evictionfallback"
)

// spec: §4.4 line 285 — `session.lost` event.

func TestSessionEventsBridgePublishesToBus(t *testing.T) {
	bus := sessionevents.NewBus(0)
	bridge := &evictionfallback.SessionEventsBridge{
		Bus: bus,
		Now: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}

	bridge.EmitSessionLost(context.Background(), "sess-1", "eviction_total_loss",
		map[string]any{
			"tenant_id":      "acme",
			"session_id":     "sess-1",
			"minio_error":    "x",
			"postgres_error": "y",
		})

	events := bus.History("sess-1", 0)
	if len(events) != 1 {
		t.Fatalf("history len = %d, want 1 event", len(events))
	}
	ev := events[0]
	if ev.Type != "session.lost" {
		t.Errorf("type = %q, want session.lost", ev.Type)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(ev.Data), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload["reason"] != "eviction_total_loss" {
		t.Errorf("reason = %v, want eviction_total_loss", payload["reason"])
	}
	if payload["minio_error"] != "x" || payload["postgres_error"] != "y" {
		t.Errorf("payload fields lost: %+v", payload)
	}
}

func TestSessionEventsBridgeNilBusIsLoggedNoOp(t *testing.T) {
	bridge := &evictionfallback.SessionEventsBridge{Bus: nil}
	// No panic — the contract is "logged only" when the bus is
	// unavailable. The test does not inspect log output; the
	// non-panic is the assertion.
	bridge.EmitSessionLost(context.Background(), "sess-1", "eviction_total_loss",
		map[string]any{"k": "v"})
}

func TestSessionEventsBridgeIncludesAllFields(t *testing.T) {
	bus := sessionevents.NewBus(0)
	bridge := &evictionfallback.SessionEventsBridge{Bus: bus}
	bridge.EmitSessionLost(context.Background(), "sess-1", "eviction_total_loss",
		map[string]any{"generation": int64(7), "tenant_id": "acme"})
	events := bus.History("sess-1", 0)
	if len(events) != 1 {
		t.Fatalf("history len = %d", len(events))
	}
	if !strings.Contains(events[0].Data, "\"generation\"") {
		t.Errorf("event data missing generation field: %q", events[0].Data)
	}
}
