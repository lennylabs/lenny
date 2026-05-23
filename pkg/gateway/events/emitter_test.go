// SPDX-License-Identifier: MIT

package events

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestEmitFillsEnvelope(t *testing.T) {
	em := NewEmitter(NewEventBuffer(8), "replica-1")
	at := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	em.now = func() time.Time { return at }

	id, err := em.Emit(context.Background(), OperationalEvent{Type: "dev.lenny.alert_fired", Severity: "critical"})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if id != 1 {
		t.Errorf("first emit id = %d, want 1", id)
	}
	page := em.Buffer().Query(0, EventFilter{}, 100)
	if len(page.Events) != 1 {
		t.Fatalf("buffer holds %d events, want 1", len(page.Events))
	}
	got := page.Events[0].Event
	if got.SpecVersion != CloudEventsSpecVersion {
		t.Errorf("specversion = %q, want %q", got.SpecVersion, CloudEventsSpecVersion)
	}
	if !got.Time.Equal(at) {
		t.Errorf("time = %v, want %v", got.Time, at)
	}
	if !strings.HasPrefix(got.ID, "replica-1:") {
		t.Errorf("eventKey = %q, want a replica-1: prefix", got.ID)
	}
}

func TestEmitPreservesCallerSetFields(t *testing.T) {
	em := NewEmitter(NewEventBuffer(8), "replica-1")
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := em.Emit(context.Background(), OperationalEvent{
		ID: "explicit-key", SpecVersion: "1.0.2", Type: "dev.lenny.x", Time: at,
	}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	got := em.Buffer().Query(0, EventFilter{}, 100).Events[0].Event
	if got.ID != "explicit-key" {
		t.Errorf("caller-set ID overwritten: %q", got.ID)
	}
	if !got.Time.Equal(at) {
		t.Errorf("caller-set Time overwritten: %v", got.Time)
	}
}

func TestEmitEventKeysAreUniqueAndMonotonic(t *testing.T) {
	em := NewEmitter(NewEventBuffer(64), "replica-1")
	keys := map[string]bool{}
	for i := 0; i < 50; i++ {
		if _, err := em.Emit(context.Background(), OperationalEvent{Type: "dev.lenny.x"}); err != nil {
			t.Fatalf("Emit: %v", err)
		}
	}
	page := em.Buffer().Query(0, EventFilter{}, 100)
	for _, e := range page.Events {
		if keys[e.Event.ID] {
			t.Errorf("duplicate eventKey: %q", e.Event.ID)
		}
		keys[e.Event.ID] = true
	}
	if len(keys) != 50 {
		t.Errorf("distinct eventKeys = %d, want 50", len(keys))
	}
}

func TestNewEmitterDefaultsReplicaID(t *testing.T) {
	em := NewEmitter(NewEventBuffer(8), "")
	if _, err := em.Emit(context.Background(), OperationalEvent{Type: "dev.lenny.x"}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	got := em.Buffer().Query(0, EventFilter{}, 100).Events[0].Event
	if !strings.HasPrefix(got.ID, "gateway:") {
		t.Errorf("empty replicaID must fall back to gateway: %q", got.ID)
	}
}

// spec §4.0: ctx cancellation is honored by Emit so a shutdown does not
// pin on an in-process buffer write.
func TestEmitRespectsContextCancellation(t *testing.T) {
	em := NewEmitter(NewEventBuffer(8), "replica-1")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := em.Emit(ctx, OperationalEvent{Type: "dev.lenny.x"}); !errors.Is(err, context.Canceled) {
		t.Errorf("Emit on cancelled ctx returned %v, want context.Canceled", err)
	}
	page := em.Buffer().Query(0, EventFilter{}, 100)
	if len(page.Events) != 0 {
		t.Errorf("Emit on cancelled ctx wrote %d events, want 0", len(page.Events))
	}
}
