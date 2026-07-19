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
