// SPDX-License-Identifier: MIT

package events

import (
	"encoding/json"
	"strings"
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

func TestOperationalEventCarriesSubject_spec_25_3_19(t *testing.T) {
	// spec: §16.6 / CloudEvents v1.0.2 subject context attribute —
	// each event's Subject names the canonical resource identifier so
	// agents filter related events without parsing the payload.
	b := NewEventBuffer(4)
	pool := OperationalEvent{
		ID:          "alert-pool",
		SpecVersion: "1.0.2",
		Type:        "dev.lenny.pool_state_changed",
		Subject:     "pool/default-gvisor",
		Time:        time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC),
	}
	session := OperationalEvent{
		ID:          "alert-session",
		SpecVersion: "1.0.2",
		Type:        "dev.lenny.session_failed",
		Subject:     "session/abc123",
		Time:        time.Date(2026, 5, 17, 12, 1, 0, 0, time.UTC),
	}
	b.Append(pool)
	b.Append(session)
	page := b.Query(0, EventFilter{}, 100)
	if len(page.Events) != 2 {
		t.Fatalf("want both events buffered, got %d", len(page.Events))
	}
	if got := page.Events[0].Event.Subject; got != "pool/default-gvisor" {
		t.Errorf("pool subject = %q, want %q", got, "pool/default-gvisor")
	}
	if got := page.Events[1].Event.Subject; got != "session/abc123" {
		t.Errorf("session subject = %q, want %q", got, "session/abc123")
	}
}

func TestOperationalEventSubjectJSONRoundTrip_spec_25_3_19(t *testing.T) {
	// spec: §16.6 — the CloudEvents subject attribute round-trips
	// through the §25.3 wire format and is omitted when empty.
	e := OperationalEvent{
		ID:          "evt-1",
		SpecVersion: "1.0.2",
		Type:        "dev.lenny.alert_fired",
		Subject:     "credential_pool/openai-prod",
	}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), `"subject":"credential_pool/openai-prod"`) {
		t.Errorf("marshalled JSON missing subject: %s", data)
	}
	empty := OperationalEvent{ID: "evt-2", SpecVersion: "1.0.2", Type: "dev.lenny.alert_resolved"}
	emptyData, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("marshal empty: %v", err)
	}
	if strings.Contains(string(emptyData), `"subject"`) {
		t.Errorf("empty Subject must be omitted from JSON: %s", emptyData)
	}
}
