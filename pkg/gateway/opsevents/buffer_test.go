// SPDX-License-Identifier: MIT

package opsevents

import (
	"testing"
	"time"
)

func ev(typ, severity string) OperationalEvent {
	return OperationalEvent{
		ID:          typ + "-" + severity,
		SpecVersion: "1.0.2",
		Type:        "dev.lenny." + typ,
		Severity:    severity,
		Time:        time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC),
	}
}

func TestAppendAssignsMonotonicIDs(t *testing.T) {
	b := NewEventBuffer(8)
	if id := b.Append(ev("alert_fired", "critical")); id != 1 {
		t.Errorf("first id = %d, want 1", id)
	}
	if id := b.Append(ev("alert_resolved", "warning")); id != 2 {
		t.Errorf("second id = %d, want 2", id)
	}
}

func TestQueryReturnsEventsAfterCursor(t *testing.T) {
	b := NewEventBuffer(8)
	for i := 0; i < 5; i++ {
		b.Append(ev("alert_fired", "warning"))
	}
	// since=0 returns the whole buffer.
	page := b.Query(0, EventFilter{}, 100)
	if len(page.Events) != 5 || page.Cursor != 5 {
		t.Fatalf("full query: %d events, cursor %d; want 5, 5", len(page.Events), page.Cursor)
	}
	// since=3 returns only the events after id 3.
	page = b.Query(3, EventFilter{}, 100)
	if len(page.Events) != 2 || page.Events[0].ID != 4 || page.Cursor != 5 {
		t.Errorf("cursor query: %+v", page)
	}
}

func TestQueryEmptyBuffer(t *testing.T) {
	page := NewEventBuffer(8).Query(0, EventFilter{}, 100)
	if len(page.Events) != 0 || page.Cursor != 0 || page.GapDetected {
		t.Errorf("empty buffer query: %+v", page)
	}
}

func TestQueryLimitAndHasMore(t *testing.T) {
	b := NewEventBuffer(16)
	for i := 0; i < 10; i++ {
		b.Append(ev("pool_state_changed", "info"))
	}
	page := b.Query(0, EventFilter{}, 4)
	if len(page.Events) != 4 || !page.HasMore || page.Cursor != 4 {
		t.Errorf("limited query: %d events, hasMore %v, cursor %d", len(page.Events), page.HasMore, page.Cursor)
	}
	// The default limit applies when limit is non-positive.
	page = b.Query(0, EventFilter{}, 0)
	if len(page.Events) != 10 {
		t.Errorf("default-limit query: %d events, want 10", len(page.Events))
	}
}

func TestQueryFilters(t *testing.T) {
	b := NewEventBuffer(16)
	b.Append(ev("alert_fired", "critical"))
	b.Append(ev("pool_state_changed", "info"))
	b.Append(ev("alert_fired", "warning"))

	byType := b.Query(0, EventFilter{EventType: "alert_fired"}, 100)
	if len(byType.Events) != 2 {
		t.Errorf("eventType filter: %d events, want 2", len(byType.Events))
	}
	// The short-name suffix matches the full dev.lenny.<name> type.
	bySeverity := b.Query(0, EventFilter{Severity: "critical"}, 100)
	if len(bySeverity.Events) != 1 || bySeverity.Events[0].Event.Severity != "critical" {
		t.Errorf("severity filter: %+v", bySeverity.Events)
	}
}

func TestQueryGapDetection(t *testing.T) {
	b := NewEventBuffer(4)
	for i := 0; i < 10; i++ { // wraps the 4-slot buffer; ids 7..10 retained
		b.Append(ev("alert_fired", "warning"))
	}
	// A poller resuming from id 2 — long evicted — gets a gap signal.
	page := b.Query(2, EventFilter{}, 100)
	if !page.GapDetected {
		t.Errorf("a cursor past the eviction horizon must report gapDetected: %+v", page)
	}
	if page.OldestAvailableCursor != 7 {
		t.Errorf("oldestAvailableCursor = %d, want 7", page.OldestAvailableCursor)
	}
	if len(page.Events) != 4 {
		t.Errorf("gap query still returns the retained events: %d, want 4", len(page.Events))
	}
}

func TestQueryReportsBufferAge(t *testing.T) {
	b := NewEventBuffer(8)
	b.now = func() time.Time { return time.Date(2026, 5, 17, 14, 0, 0, 0, time.UTC) }
	b.Append(ev("alert_fired", "warning")) // event Time is 12:00
	page := b.Query(0, EventFilter{}, 100)
	if page.BufferAge != "2h0m0s" {
		t.Errorf("bufferAge = %q, want 2h0m0s", page.BufferAge)
	}
}
