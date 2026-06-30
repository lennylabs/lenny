// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// spec: §11.2.1 / §15.1 GET /v1/metering/events.
//
// The canonical §15.1 line 1228 envelope shape is {items, cursor,
// hasMore}; the metering wire schema honours both `eventType` and
// per-event fields below as it surfaces §11.2.1 billing events.

type meteringPageJSON struct {
	Items []struct {
		SequenceNumber uint64 `json:"sequenceNumber"`
		EventType      string `json:"eventType"`
		TenantID       string `json:"tenantId"`
	} `json:"items"`
	Cursor  string `json:"cursor"`
	HasMore bool   `json:"hasMore"`
}

// meteringServer builds a session server whose billing ledger holds
// count session.created events for tenant acme.
func meteringServer(t *testing.T, count int) *sessionserver.Server {
	t.Helper()
	billing := billingstore.NewMemory()
	for i := 0; i < count; i++ {
		if _, err := billing.Append(context.Background(), billingstore.Event{
			TenantID:  "acme",
			EventType: billingstore.EventSessionCreated,
		}); err != nil {
			t.Fatalf("seed billing event: %v", err)
		}
	}
	return sessionserver.New(memstore.New(), sessionserver.Options{Billing: billing})
}

func meteringRequest(t *testing.T, h http.Handler, query string, roles ...pkgauth.Role) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/metering/events"+query, nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req = req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject: "viewer@acme.com", TenantID: "acme", Roles: roles,
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func decodeMeteringPage(t *testing.T, rr *httptest.ResponseRecorder) meteringPageJSON {
	t.Helper()
	var page meteringPageJSON
	if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode metering page: %v; body=%s", err, rr.Body.String())
	}
	return page
}

// TestMeteringEventsLabelFilter_spec_14_106 drives the §14 line 106
// label-scoped billing stream end to end: events carrying distinct labels
// are seeded and GET /v1/metering/events?label=team=search returns only
// the matching events, echoing the labels on the wire. The label
// predicate is pushed into the store query so the §15.1 cursor/hasMore
// pagination stays correct under the filter. F-14.1.13.
func TestMeteringEventsLabelFilter_spec_14_106(t *testing.T) {
	billing := billingstore.NewMemory()
	seed := func(team string) {
		if _, err := billing.Append(context.Background(), billingstore.Event{
			TenantID:  "acme",
			EventType: billingstore.EventSessionCreated,
			Labels:    map[string]string{"team": team},
		}); err != nil {
			t.Fatalf("seed billing event: %v", err)
		}
	}
	for _, team := range []string{"search", "ads", "search", "ads", "search"} {
		seed(team)
	}
	srv := sessionserver.New(memstore.New(), sessionserver.Options{Billing: billing})

	rr := meteringRequest(t, srv.Handler(), "?label=team=search", pkgauth.RoleBillingViewer)
	if rr.Code != http.StatusOK {
		t.Fatalf("filtered metering: %d", rr.Code)
	}
	var page struct {
		Items []struct {
			SequenceNumber uint64            `json:"sequenceNumber"`
			Labels         map[string]string `json:"labels"`
		} `json:"items"`
		HasMore bool `json:"hasMore"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode page: %v; body=%s", err, rr.Body.String())
	}
	if len(page.Items) != 3 {
		t.Fatalf("team=search: got %d items, want 3", len(page.Items))
	}
	for _, it := range page.Items {
		if it.Labels["team"] != "search" {
			t.Errorf("item carries wrong team: %v", it.Labels)
		}
	}

	// Pagination under the filter: limit 2 returns a first page of 2
	// matching events with hasMore set.
	rr = meteringRequest(t, srv.Handler(), "?label=team=search&limit=2", pkgauth.RoleBillingViewer)
	if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode page 2: %v", err)
	}
	if len(page.Items) != 2 || !page.HasMore {
		t.Errorf("team=search limit 2: got %d items hasMore=%v, want 2/true", len(page.Items), page.HasMore)
	}
}

func TestMeteringEventsRequiresViewUsage(t *testing.T) {
	srv := meteringServer(t, 3)

	rr := meteringRequest(t, srv.Handler(), "", pkgauth.RoleUser)
	if rr.Code != http.StatusForbidden {
		t.Errorf("a plain user: status %d, want 403", rr.Code)
	}

	// §10.2: every role the matrix grants view_usage is admitted —
	// tenant-viewer included (§10.2 names it for GET /v1/metering/events).
	for _, role := range []pkgauth.Role{
		pkgauth.RoleBillingViewer, pkgauth.RoleTenantAdmin,
		pkgauth.RoleTenantViewer, pkgauth.RolePlatformAdmin,
	} {
		rr := meteringRequest(t, srv.Handler(), "", role)
		if rr.Code != http.StatusOK {
			t.Errorf("role %q: status %d, want 200", role, rr.Code)
		}
	}
}

func TestMeteringEventsReturnsLedger(t *testing.T) {
	srv := meteringServer(t, 3)
	rr := meteringRequest(t, srv.Handler(), "", pkgauth.RoleBillingViewer)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
	}
	page := decodeMeteringPage(t, rr)
	if len(page.Items) != 3 {
		t.Fatalf("got %d events, want 3", len(page.Items))
	}
	for i, e := range page.Items {
		if e.SequenceNumber != uint64(i+1) {
			t.Errorf("event %d: seq %d, want %d", i, e.SequenceNumber, i+1)
		}
		if e.EventType != "session.created" {
			t.Errorf("event %d: type %q", i, e.EventType)
		}
	}
	if page.HasMore {
		t.Error("hasMore should be false when the whole ledger fits in one page")
	}
}

func TestMeteringEventsSinceSequence(t *testing.T) {
	srv := meteringServer(t, 5)
	rr := meteringRequest(t, srv.Handler(), "?since_sequence=2", pkgauth.RoleBillingViewer)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
	}
	page := decodeMeteringPage(t, rr)
	if len(page.Items) != 3 || page.Items[0].SequenceNumber != 3 {
		t.Fatalf("since_sequence=2: got %d events starting at %d, want 3 starting at 3",
			len(page.Items), seqOf(page))
	}
}

func TestMeteringEventsPaginates(t *testing.T) {
	srv := meteringServer(t, 5)

	first := decodeMeteringPage(t, meteringRequest(t, srv.Handler(), "?limit=2", pkgauth.RoleBillingViewer))
	if len(first.Items) != 2 || !first.HasMore || first.Cursor == "" {
		t.Fatalf("page 1: events=%d hasMore=%v cursor=%q, want 2/true/non-empty",
			len(first.Items), first.HasMore, first.Cursor)
	}

	second := decodeMeteringPage(t, meteringRequest(t, srv.Handler(),
		"?cursor="+first.Cursor+"&limit=2", pkgauth.RoleBillingViewer))
	if len(second.Items) != 2 || !second.HasMore || second.Cursor == "" {
		t.Fatalf("page 2: events=%d hasMore=%v cursor=%q, want 2/true/non-empty",
			len(second.Items), second.HasMore, second.Cursor)
	}
	if second.Items[0].SequenceNumber <= first.Items[len(first.Items)-1].SequenceNumber {
		t.Errorf("page 2 starts at seq %d, want > %d", second.Items[0].SequenceNumber,
			first.Items[len(first.Items)-1].SequenceNumber)
	}

	third := decodeMeteringPage(t, meteringRequest(t, srv.Handler(),
		"?cursor="+second.Cursor+"&limit=2", pkgauth.RoleBillingViewer))
	if len(third.Items) != 1 || third.HasMore {
		t.Fatalf("page 3: events=%d hasMore=%v, want 1/false", len(third.Items), third.HasMore)
	}

	// The legacy `?since_sequence=` parameter still works for v0 clients.
	legacy := decodeMeteringPage(t, meteringRequest(t, srv.Handler(),
		"?since_sequence=2&limit=2", pkgauth.RoleBillingViewer))
	if len(legacy.Items) != 2 || legacy.Items[0].SequenceNumber != 3 {
		t.Errorf("legacy since_sequence path broke: events=%d, first seq=%d",
			len(legacy.Items), seqOf(legacy))
	}
}

func TestMeteringEventsRejectsBadSinceSequence(t *testing.T) {
	srv := meteringServer(t, 1)
	rr := meteringRequest(t, srv.Handler(), "?since_sequence=-1", pkgauth.RoleBillingViewer)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("since_sequence=-1: status %d, want 400", rr.Code)
	}
}

// TestMeteringEventsClampsLimitToSpecMax_spec_15_1_1236 confirms the
// §15.1 line 1236 [1, 200] limit clamp applies to the metering
// endpoint. F-15.1.20.
func TestMeteringEventsClampsLimitToSpecMax_spec_15_1_1236(t *testing.T) {
	srv := meteringServer(t, 250)
	rr := meteringRequest(t, srv.Handler(), "?limit=500", pkgauth.RoleBillingViewer)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body=%s", rr.Code, rr.Body.String())
	}
	page := decodeMeteringPage(t, rr)
	if len(page.Items) != 200 {
		t.Errorf("limit=500: got %d items, want 200 (spec §15.1 line 1236 clamp)", len(page.Items))
	}
	if !page.HasMore {
		t.Errorf("250-event ledger with clamped limit=200 should report hasMore=true")
	}
}

// TestMeteringEventsRejectsBadSort_spec_15_1_1236 confirms the §15.1
// line 1236 sort-validation rule rejects unknown fields with
// VALIDATION_ERROR. F-15.1.20.
func TestMeteringEventsRejectsBadSort_spec_15_1_1236(t *testing.T) {
	srv := meteringServer(t, 3)
	rr := meteringRequest(t, srv.Handler(), "?sort=nope:asc", pkgauth.RoleBillingViewer)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("unknown sort: status %d, want 400 (body=%s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "invalid_sort_field") {
		t.Errorf("unknown sort envelope: %s", rr.Body.String())
	}
}

func TestMeteringEventsWithoutBillingStore(t *testing.T) {
	srv := sessionserver.New(memstore.New(), sessionserver.Options{})
	rr := meteringRequest(t, srv.Handler(), "", pkgauth.RoleBillingViewer)
	if rr.Code != http.StatusOK {
		t.Fatalf("no billing store: status %d, want 200", rr.Code)
	}
	page := decodeMeteringPage(t, rr)
	if len(page.Items) != 0 {
		t.Errorf("no billing store: got %d events, want 0", len(page.Items))
	}
}

// seqOf returns the first event's sequence number, or 0 when the page
// is empty.
func seqOf(page meteringPageJSON) uint64 {
	if len(page.Items) == 0 {
		return 0
	}
	return page.Items[0].SequenceNumber
}
