// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract test for the §25.5 event-subscription deliveries
// endpoint pagination. It pins the wire contract for
// GET /v1/admin/event-subscriptions/{id}/deliveries?cursor=&limit=: the
// response carries the canonical §25.2 pagination envelope with
// cursorKind "pk", a returned cursor round-trips to the adjacent page,
// and an over-max limit is clamped rather than rejected.
package ops_endpoints_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/eventsubscription"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
)

// deliveriesServer builds a §25 lenny-ops Server whose §25.5
// event-subscription service is backed by a MemoryStore seeded with one
// subscription and n delivery rows, so the deliveries endpoint has a
// keyset to walk.
func deliveriesServer(t *testing.T, n int) *opsserver.Server {
	t.Helper()
	ctx := context.Background()
	store := eventsubscription.NewMemoryStore()
	if err := store.Create(ctx, eventsubscription.Record{
		ID: "sub-1", CallbackURL: "https://hooks.acme.com/lenny",
		TenantFilter: eventsubscription.TenantFilterAll, Active: true,
	}); err != nil {
		t.Fatalf("seed subscription: %v", err)
	}
	for i := 0; i < n; i++ {
		if _, err := store.RecordDelivery(ctx, eventsubscription.Delivery{
			SubscriptionID: "sub-1", EventID: "evt", EventType: "dev.lenny.alert_fired",
			Status: eventsubscription.DeliveryDelivered, ExpiresAt: time.Now().Add(24 * time.Hour),
		}); err != nil {
			t.Fatalf("seed delivery %d: %v", i, err)
		}
	}
	return opsserver.New(opsserver.Options{EventSubscriptions: eventsubscription.NewService(store)})
}

// TestEventSubscriptionDeliveriesGapEnvelopeContract pins the wire contract
// for a deliveries cursor that can no longer be honored. Both an aged-out
// cursor (its delivery removed by the retention purge) and a cursor that does
// not resolve to a delivery position return the canonical §25.2 gap envelope:
// gapDetected, gapReason, oldestAvailableCursor, and suggestedAction "resync",
// so a client that fell behind retention has a documented recovery step rather
// than a silently empty page.
//
// spec: §25.5 (gap on aged-out deliveries cursor), §25.2 (canonical gap
// envelope).
// diagnosis: a failure means the deliveries endpoint reports a gap without the
// reason or the recovery hint the §25.2 envelope defines, so an agent that
// outruns the delivery retention window receives an empty page it cannot
// distinguish from the end of history and never resyncs.
func TestEventSubscriptionDeliveriesGapEnvelopeContract(t *testing.T) {
	ctx := context.Background()
	store := eventsubscription.NewMemoryStore()
	if err := store.Create(ctx, eventsubscription.Record{
		ID: "sub-1", CallbackURL: "https://hooks.acme.com/lenny",
		TenantFilter: eventsubscription.TenantFilterAll, Active: true,
	}); err != nil {
		t.Fatalf("seed subscription: %v", err)
	}
	// Three deliveries already past their retention expiry, then two live ones.
	for i := 0; i < 5; i++ {
		expires := time.Now().Add(24 * time.Hour)
		if i < 3 {
			expires = time.Now().Add(-time.Hour)
		}
		if _, err := store.RecordDelivery(ctx, eventsubscription.Delivery{
			SubscriptionID: "sub-1", EventID: "evt", EventType: "dev.lenny.alert_fired",
			Status: eventsubscription.DeliveryDelivered, ExpiresAt: expires,
		}); err != nil {
			t.Fatalf("seed delivery %d: %v", i, err)
		}
	}
	if purged, err := store.DeleteExpired(ctx, time.Now(), 100); err != nil || purged != 3 {
		t.Fatalf("retention purge = %d (%v), want 3", purged, err)
	}
	srv := opsserver.New(opsserver.Options{EventSubscriptions: eventsubscription.NewService(store)})

	for _, tc := range []struct {
		name   string
		cursor string
		reason string
	}{
		{"aged out under the retention purge", "2", eventsubscription.GapReasonAgedOut},
		{"unresolvable continuation token", "not-a-cursor", eventsubscription.GapReasonUnresolvable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec, body := request(t, srv, http.MethodGet,
				"/v1/admin/event-subscriptions/sub-1/deliveries?limit=2&cursor="+tc.cursor, nil, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 with a gap envelope; body=%v", rec.Code, body)
			}
			if got := deliveryCount(t, body); got != 0 {
				t.Errorf("gap response returned %d deliveries, want 0", got)
			}
			p := deliveryPagination(t, body)
			if p["gapDetected"] != true {
				t.Fatalf("gapDetected = %v, want true; envelope=%v", p["gapDetected"], p)
			}
			if p["gapReason"] != tc.reason {
				t.Errorf("gapReason = %v, want %q", p["gapReason"], tc.reason)
			}
			if p["suggestedAction"] != eventsubscription.SuggestedActionResync {
				t.Errorf("suggestedAction = %v, want %q", p["suggestedAction"], eventsubscription.SuggestedActionResync)
			}
			if p["oldestAvailableCursor"] != "4" {
				t.Errorf("oldestAvailableCursor = %v, want the oldest retained delivery cursor 4", p["oldestAvailableCursor"])
			}
		})
	}
}

// TestEventSubscriptionDeliveriesGapOnPurgedCursorOverSurvivingRowsContract
// pins the gap envelope on the wire for the case the retention split
// produces: the delivery a cursor names is purged while deliveries with
// smaller ids survive, because a failed delivery is retained longer than a
// delivered one. The continuation page is non-empty, so a store that infers
// the gap from an empty page reports a clean envelope and the caller loses the
// purged rows between its last page and this one with no signal.
//
// spec: §25.5 (gapDetected with oldestAvailableCursor when the supplied
// deliveries cursor can no longer be honored), §25.2 (canonical gap envelope).
// diagnosis: a failure means the deliveries endpoint serves a non-empty
// continuation page over a purged cursor without the gap envelope, so an agent
// walking delivery history silently skips every attempt the retention purge
// removed between the two pages and never resyncs.
func TestEventSubscriptionDeliveriesGapOnPurgedCursorOverSurvivingRowsContract(t *testing.T) {
	ctx := context.Background()
	store := eventsubscription.NewMemoryStore()
	if err := store.Create(ctx, eventsubscription.Record{
		ID: "sub-1", CallbackURL: "https://hooks.acme.com/lenny",
		TenantFilter: eventsubscription.TenantFilterAll, Active: true,
	}); err != nil {
		t.Fatalf("seed subscription: %v", err)
	}
	// ids 1 and 2 failed (retained under the longer failures-only window),
	// id 3 delivered and already expired, id 4 failed.
	for i := 0; i < 4; i++ {
		d := eventsubscription.Delivery{
			SubscriptionID: "sub-1", EventID: "evt", EventType: "dev.lenny.alert_fired",
			Status: eventsubscription.DeliveryFailed, ExpiresAt: time.Now().Add(24 * time.Hour),
		}
		if i == 2 {
			d.Status = eventsubscription.DeliveryDelivered
			d.ExpiresAt = time.Now().Add(-time.Hour)
		}
		if _, err := store.RecordDelivery(ctx, d); err != nil {
			t.Fatalf("seed delivery %d: %v", i, err)
		}
	}
	if purged, err := store.DeleteExpired(ctx, time.Now(), 100); err != nil || purged != 1 {
		t.Fatalf("retention purge = %d (%v), want the single delivered row", purged, err)
	}
	srv := opsserver.New(opsserver.Options{EventSubscriptions: eventsubscription.NewService(store)})

	rec, body := request(t, srv, http.MethodGet,
		"/v1/admin/event-subscriptions/sub-1/deliveries?limit=10&cursor=3", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", rec.Code, body)
	}
	if got := deliveryCount(t, body); got != 2 {
		t.Fatalf("purged-cursor page returned %d deliveries, want the 2 surviving older rows", got)
	}
	p := deliveryPagination(t, body)
	if p["gapDetected"] != true {
		t.Fatalf("gapDetected = %v, want true on a non-empty page whose cursor row was purged; envelope=%v",
			p["gapDetected"], p)
	}
	if p["gapReason"] != eventsubscription.GapReasonAgedOut {
		t.Errorf("gapReason = %v, want %q", p["gapReason"], eventsubscription.GapReasonAgedOut)
	}
	if p["oldestAvailableCursor"] != "1" {
		t.Errorf("oldestAvailableCursor = %v, want the oldest retained delivery cursor 1", p["oldestAvailableCursor"])
	}
	if p["suggestedAction"] != eventsubscription.SuggestedActionResync {
		t.Errorf("suggestedAction = %v, want %q", p["suggestedAction"], eventsubscription.SuggestedActionResync)
	}
}

// deliveryPagination extracts the §25.2 pagination envelope from a
// decoded deliveries response, failing the test when it is absent.
func deliveryPagination(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	p, ok := body["pagination"].(map[string]any)
	if !ok {
		t.Fatalf("deliveries response carries no pagination envelope: %v", body)
	}
	return p
}

// deliveryCount returns the number of rows in a decoded deliveries
// response body.
func deliveryCount(t *testing.T, body map[string]any) int {
	t.Helper()
	rows, ok := body["deliveries"].([]any)
	if !ok {
		return 0
	}
	return len(rows)
}

// TestEventSubscriptionDeliveriesPaginationContract pins the §25.5
// deliveries-endpoint pagination wire contract: the response carries the
// canonical §25.2 pagination envelope with cursorKind "pk", the returned
// cursor round-trips to the adjacent page with no overlap, the final page
// reports hasMore:false, and an over-max limit is clamped rather than
// rejected.
//
// spec: §25.5 (deliveries endpoint pagination envelope), §25.2 (canonical
// pagination envelope).
// diagnosis: a failure means the deliveries endpoint no longer emits the
// canonical §25.2 pagination envelope or its keyset cursor does not
// round-trip, so a paginating client cannot walk a subscription's
// delivery history without overlap, gaps, or a rejected page.
func TestEventSubscriptionDeliveriesPaginationContract(t *testing.T) {
	srv := deliveriesServer(t, 5)

	// First page of two.
	rec1, body1 := request(t, srv, http.MethodGet,
		"/v1/admin/event-subscriptions/sub-1/deliveries?limit=2", nil, nil)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first page status = %d, want 200; body=%v", rec1.Code, body1)
	}
	assertJSONContentType(t, rec1)
	if got := deliveryCount(t, body1); got != 2 {
		t.Fatalf("first page deliveries = %d, want 2", got)
	}
	p1 := deliveryPagination(t, body1)
	if p1["cursorKind"] != eventsubscription.CursorKindPK {
		t.Errorf("cursorKind = %v, want %q", p1["cursorKind"], eventsubscription.CursorKindPK)
	}
	if p1["hasMore"] != true {
		t.Errorf("first page hasMore = %v, want true", p1["hasMore"])
	}
	cursor, _ := p1["cursor"].(string)
	if cursor == "" {
		t.Fatalf("first page carries no continuation cursor: %v", p1)
	}

	// Round-trip the cursor to fetch the adjacent page.
	rec2, body2 := request(t, srv, http.MethodGet,
		"/v1/admin/event-subscriptions/sub-1/deliveries?limit=2&cursor="+cursor, nil, nil)
	if rec2.Code != http.StatusOK {
		t.Fatalf("second page status = %d, want 200; body=%v", rec2.Code, body2)
	}
	if got := deliveryCount(t, body2); got != 2 {
		t.Fatalf("second page deliveries = %d, want 2", got)
	}
	if firstID(t, body1) == firstID(t, body2) {
		t.Errorf("second page overlaps first: both start at delivery id %v", firstID(t, body1))
	}

	// An over-max limit is clamped, not rejected: a single request returns
	// every seeded row without error.
	rec3, body3 := request(t, srv, http.MethodGet,
		"/v1/admin/event-subscriptions/sub-1/deliveries?limit=99999", nil, nil)
	if rec3.Code != http.StatusOK {
		t.Fatalf("over-max-limit status = %d, want 200 (clamped); body=%v", rec3.Code, body3)
	}
	if got := deliveryCount(t, body3); got != 5 {
		t.Fatalf("over-max-limit deliveries = %d, want all 5", got)
	}
	if p3 := deliveryPagination(t, body3); p3["hasMore"] != false {
		t.Errorf("full-page hasMore = %v, want false", p3["hasMore"])
	}
}

// firstID returns the ID of the first delivery row in a decoded response.
func firstID(t *testing.T, body map[string]any) float64 {
	t.Helper()
	rows, ok := body["deliveries"].([]any)
	if !ok || len(rows) == 0 {
		t.Fatalf("response carries no deliveries: %v", body)
	}
	first, _ := rows[0].(map[string]any)
	id, _ := first["ID"].(float64)
	return id
}
