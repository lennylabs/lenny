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
	if len(page.Events) != 5 || page.Pagination.Cursor != 5 {
		t.Fatalf("full query: %d events, cursor %d; want 5, 5", len(page.Events), page.Pagination.Cursor)
	}
	// since=3 returns only the events after id 3.
	page = b.Query(3, EventFilter{}, 100)
	if len(page.Events) != 2 || page.Events[0].ID != 4 || page.Pagination.Cursor != 5 {
		t.Errorf("cursor query: %+v", page)
	}
}

func TestQueryEmptyBuffer(t *testing.T) {
	page := NewEventBuffer(8).Query(0, EventFilter{}, 100)
	if len(page.Events) != 0 || page.Pagination.Cursor != 0 || page.Pagination.GapDetected {
		t.Errorf("empty buffer query: %+v", page)
	}
}

func TestQueryLimitAndHasMore(t *testing.T) {
	b := NewEventBuffer(16)
	for i := 0; i < 10; i++ {
		b.Append(ev("pool_state_changed", "info"))
	}
	page := b.Query(0, EventFilter{}, 4)
	if len(page.Events) != 4 || !page.Pagination.HasMore || page.Pagination.Cursor != 4 {
		t.Errorf("limited query: %d events, hasMore %v, cursor %d", len(page.Events), page.Pagination.HasMore, page.Pagination.Cursor)
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
	if !page.Pagination.GapDetected {
		t.Errorf("a cursor past the eviction horizon must report gapDetected: %+v", page)
	}
	if page.Pagination.OldestAvailableCursor != 7 {
		t.Errorf("oldestAvailableCursor = %d, want 7", page.Pagination.OldestAvailableCursor)
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

func TestEventFilterMatchesCSV_spec_25_3_15(t *testing.T) {
	// spec: §25.2 lines 210-211 — eventType and severity accept the CSV
	// form; a query matches the union of the comma-separated tokens.
	b := NewEventBuffer(16)
	b.Append(ev("alert_fired", "critical"))
	b.Append(ev("session_failed", "warning"))
	b.Append(ev("pool_state_changed", "info"))

	// A CSV severity matches the union of its tokens.
	page := b.Query(0, EventFilter{Severity: "critical,warning"}, 100)
	if len(page.Events) != 2 {
		t.Errorf("severity CSV union: %d events, want 2", len(page.Events))
	}
	// A CSV eventType (short names) matches the union; whitespace tolerated.
	page = b.Query(0, EventFilter{EventType: "alert_fired, pool_state_changed"}, 100)
	if len(page.Events) != 2 {
		t.Errorf("eventType CSV union: %d events, want 2", len(page.Events))
	}
	// The two CSV dimensions intersect (severity AND type).
	page = b.Query(0, EventFilter{EventType: "alert_fired,session_failed", Severity: "warning"}, 100)
	if len(page.Events) != 1 || page.Events[0].Event.Type != "dev.lenny.session_failed" {
		t.Errorf("combined CSV intersection: %+v", page.Events)
	}
	// A CSV of only empty tokens imposes no constraint.
	page = b.Query(0, EventFilter{Severity: " , "}, 100)
	if len(page.Events) != 3 {
		t.Errorf("all-empty CSV must not filter: %d events, want 3", len(page.Events))
	}
	// A single value still works (no regression).
	page = b.Query(0, EventFilter{EventType: "alert_fired"}, 100)
	if len(page.Events) != 1 {
		t.Errorf("single eventType: %d events, want 1", len(page.Events))
	}
}

func TestQueryPaginationEnvelope_spec_25_3_17(t *testing.T) {
	// spec: §25.2 lines 245-275 / §25.3 line 750 — buffer queries carry
	// the canonical pagination envelope: a buffer-seq cursorKind, the
	// head cursor, and on eviction the gapReason + resync suggestedAction.
	b := NewEventBuffer(4)
	for i := 0; i < 3; i++ {
		b.Append(ev("alert_fired", "warning"))
	}
	page := b.Query(0, EventFilter{}, 2)
	if page.Pagination.CursorKind != "buffer-seq" {
		t.Errorf("cursorKind = %q, want buffer-seq", page.Pagination.CursorKind)
	}
	if page.Pagination.HeadCursor != 3 {
		t.Errorf("headCursor = %d, want 3", page.Pagination.HeadCursor)
	}
	if page.Pagination.Limit != 2 {
		t.Errorf("limit = %d, want 2", page.Pagination.Limit)
	}
	if !page.Pagination.HasMore {
		t.Error("hasMore must be true when the page is capped below the head")
	}
	if page.Pagination.GapDetected {
		t.Error("no gap on an in-buffer cursor")
	}

	// Force an eviction so the gap-recovery fields populate.
	for i := 0; i < 10; i++ {
		b.Append(ev("alert_fired", "warning")) // ids climb past the 4-slot buffer
	}
	gap := b.Query(2, EventFilter{}, 100) // cursor 2 long evicted
	if !gap.Pagination.GapDetected {
		t.Fatal("a cursor past the eviction horizon must report gapDetected")
	}
	if gap.Pagination.GapReason == "" {
		t.Error("a gap must carry a human-readable gapReason")
	}
	if gap.Pagination.SuggestedAction != "resync" {
		t.Errorf("gap suggestedAction = %q, want resync", gap.Pagination.SuggestedAction)
	}
	if gap.Pagination.OldestAvailableCursor == 0 {
		t.Error("a gap must carry oldestAvailableCursor for recovery")
	}
}

func TestBufferedEventPageJSONShape_spec_25_3_17(t *testing.T) {
	// spec: §25.3 line 750 — the gap fields ride under the canonical
	// pagination envelope, not at the response root.
	b := NewEventBuffer(4)
	for i := 0; i < 10; i++ {
		b.Append(ev("alert_fired", "warning"))
	}
	data, err := json.Marshal(b.Query(2, EventFilter{}, 100))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("unmarshal root: %v", err)
	}
	if _, ok := root["pagination"]; !ok {
		t.Error("response must carry a pagination envelope")
	}
	for _, k := range []string{"gapDetected", "cursor", "hasMore", "oldestAvailableCursor"} {
		if _, ok := root[k]; ok {
			t.Errorf("%q must live under pagination, not at the response root", k)
		}
	}
	var env struct {
		Pagination Pagination `json:"pagination"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("unmarshal pagination: %v", err)
	}
	if !env.Pagination.GapDetected || env.Pagination.SuggestedAction != "resync" {
		t.Errorf("pagination.gapDetected/suggestedAction not set: %+v", env.Pagination)
	}
}

func TestOperationalEventExtensionsRoundTrip_spec_25_3_13(t *testing.T) {
	// spec: §25.3 line 650 / §12.6 — CloudEvents extension attributes
	// flatten into the top-level object and round-trip back into
	// Extensions, mirroring pkg/gateway/eventbus.Event.
	e := OperationalEvent{
		ID: "evt-1", SpecVersion: "1.0.2", Type: "dev.lenny.session_failed",
		Subject: "session/abc123",
		Extensions: map[string]string{
			"lennytenantid":      "acme",
			"lennyrootsessionid": "root-1",
		},
	}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("unmarshal root: %v", err)
	}
	if _, ok := root["lennytenantid"]; !ok {
		t.Error("extension lennytenantid must flatten to the top-level CloudEvents object")
	}
	if _, ok := root["extensions"]; ok {
		t.Error("extensions must not serialize under a nested key")
	}
	var got OperationalEvent
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Extensions["lennytenantid"] != "acme" || got.Extensions["lennyrootsessionid"] != "root-1" {
		t.Errorf("extensions did not round-trip: %+v", got.Extensions)
	}
	// Structured attributes are never captured as extensions.
	if _, leaked := got.Extensions["subject"]; leaked {
		t.Error("subject is a structured attribute and must not appear in Extensions")
	}
	if got.Subject != "session/abc123" {
		t.Errorf("subject = %q, want session/abc123", got.Subject)
	}
}

func TestOperationalEventExtensionNeverClobbersKnownAttribute_spec_25_3_13(t *testing.T) {
	// A stray extension keyed on a structured attribute name must not
	// overwrite the first-class field on the wire.
	e := OperationalEvent{
		ID: "evt-2", SpecVersion: "1.0.2", Type: "dev.lenny.alert_fired",
		Severity:   "critical",
		Extensions: map[string]string{"severity": "info"},
	}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got OperationalEvent
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Severity != "critical" {
		t.Errorf("structured severity = %q, want critical (extension must not clobber)", got.Severity)
	}
}
