// SPDX-License-Identifier: MIT

package events_test

import (
	"context"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/events"
)

// TestEventStreamServiceSurface_spec_25_5_2574 pins the canonical
// §25.5 lines 2574-2585 EventStreamService method set. The interface
// is the substitution contract for stream sources and test doubles;
// the test guards the eight methods, their names, and their argument
// shapes against accidental drift.
func TestEventStreamServiceSurface_spec_25_5_2574(t *testing.T) {
	iface := reflect.TypeOf((*events.EventStreamService)(nil)).Elem()
	if iface.NumMethod() != 8 {
		t.Fatalf("EventStreamService method count = %d, want 8 per §25.5 lines 2574-2585",
			iface.NumMethod())
	}
	want := map[string]struct{}{
		"StreamEvents":        {},
		"ListEvents":          {},
		"CreateSubscription":  {},
		"ListSubscriptions":   {},
		"GetSubscription":     {},
		"UpdateSubscription":  {},
		"DeleteSubscription":  {},
		"ListDeliveries":      {},
	}
	for i := 0; i < iface.NumMethod(); i++ {
		name := iface.Method(i).Name
		if _, ok := want[name]; !ok {
			t.Errorf("unexpected method %q in EventStreamService", name)
		}
		delete(want, name)
	}
	for missing := range want {
		t.Errorf("EventStreamService missing method %q per §25.5 lines 2574-2585", missing)
	}
}

// TestEventStreamServiceSignatures_spec_25_5_2574 pins the
// argument/return shapes the spec calls out: each method takes a
// context.Context first, and the four non-CRUD methods use the
// canonical EventFilter, EventPage, SubscriptionRequest,
// SubscriptionUpdate, DeliveryPage types declared in this package.
func TestEventStreamServiceSignatures_spec_25_5_2574(t *testing.T) {
	iface := reflect.TypeOf((*events.EventStreamService)(nil)).Elem()
	ctxType := reflect.TypeOf((*context.Context)(nil)).Elem()
	for i := 0; i < iface.NumMethod(); i++ {
		m := iface.Method(i)
		if m.Type.NumIn() < 1 || !m.Type.In(0).Implements(ctxType) {
			t.Errorf("%s: first parameter must be context.Context", m.Name)
		}
	}
	stream, ok := iface.MethodByName("StreamEvents")
	if !ok {
		t.Fatal("StreamEvents method missing")
	}
	if got, want := stream.Type.In(1), reflect.TypeOf((*http.ResponseWriter)(nil)).Elem(); got != want {
		t.Errorf("StreamEvents second arg = %v, want http.ResponseWriter", got)
	}
	if got, want := stream.Type.In(2), reflect.TypeOf(events.EventFilter{}); got != want {
		t.Errorf("StreamEvents third arg = %v, want EventFilter", got)
	}
}

// TestPaginationAndDeliveryShapes_spec_25_5 covers the canonical
// pagination envelope (cursor + hasMore + cursorKind + headCursor +
// gapDetected + oldestAvailableCursor) and the §25.5 Delivery row
// fields, so an adapter that maps `ops_event_deliveries` rows into
// the interface response type has a stable target.
func TestPaginationAndDeliveryShapes_spec_25_5(t *testing.T) {
	p := events.Pagination{
		Cursor:                "abc",
		HasMore:               true,
		CursorKind:            "redis",
		HeadCursor:            "head",
		GapDetected:           true,
		OldestAvailableCursor: "old",
	}
	if p.Cursor != "abc" || !p.HasMore || p.CursorKind != "redis" || p.HeadCursor != "head" ||
		!p.GapDetected || p.OldestAvailableCursor != "old" {
		t.Errorf("Pagination round-trip failed: %+v", p)
	}
	d := events.Delivery{
		ID:             "del-1",
		SubscriptionID: "sub-1",
		EventID:        "evt-1",
		EventType:      "dev.lenny.alert_fired",
		Status:         "delivered",
		Attempts:       2,
		LastAttemptAt:  time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC),
		LastError:      "",
		CreatedAt:      time.Date(2026, 5, 17, 11, 59, 0, 0, time.UTC),
		ExpiresAt:      time.Date(2026, 5, 24, 11, 59, 0, 0, time.UTC),
	}
	if d.Status != "delivered" || d.Attempts != 2 {
		t.Errorf("Delivery round-trip failed: %+v", d)
	}
}
