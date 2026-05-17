// SPDX-License-Identifier: MIT

package opsevents

import (
	"strings"
	"testing"
	"time"
)

func TestEmitFillsEnvelope(t *testing.T) {
	em := NewEmitter(NewEventBuffer(8), "replica-1")
	at := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	em.now = func() time.Time { return at }

	id := em.Emit(OperationalEvent{Type: "dev.lenny.alert_fired", Severity: "critical"})
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
	em.Emit(OperationalEvent{
		ID: "explicit-key", SpecVersion: "1.0.2", Type: "dev.lenny.x", Time: at,
	})
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
		em.Emit(OperationalEvent{Type: "dev.lenny.x"})
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
	em.Emit(OperationalEvent{Type: "dev.lenny.x"})
	got := em.Buffer().Query(0, EventFilter{}, 100).Events[0].Event
	if !strings.HasPrefix(got.ID, "gateway:") {
		t.Errorf("empty replicaID must fall back to gateway: %q", got.ID)
	}
}
