// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// spec: §11.2.1 / §15.1 GET /v1/metering/events.

type meteringPageJSON struct {
	Events []struct {
		SequenceNumber uint64 `json:"sequenceNumber"`
		EventType      string `json:"eventType"`
		TenantID       string `json:"tenantId"`
	} `json:"events"`
	NextCursor string `json:"nextCursor"`
	HasMore    bool   `json:"hasMore"`
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
	if len(page.Events) != 3 {
		t.Fatalf("got %d events, want 3", len(page.Events))
	}
	for i, e := range page.Events {
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
	if len(page.Events) != 3 || page.Events[0].SequenceNumber != 3 {
		t.Fatalf("since_sequence=2: got %d events starting at %d, want 3 starting at 3",
			len(page.Events), seqOf(page))
	}
}

func TestMeteringEventsPaginates(t *testing.T) {
	srv := meteringServer(t, 5)

	first := decodeMeteringPage(t, meteringRequest(t, srv.Handler(), "?limit=2", pkgauth.RoleBillingViewer))
	if len(first.Events) != 2 || !first.HasMore || first.NextCursor != "2" {
		t.Fatalf("page 1: events=%d hasMore=%v nextCursor=%q, want 2/true/\"2\"",
			len(first.Events), first.HasMore, first.NextCursor)
	}

	second := decodeMeteringPage(t, meteringRequest(t, srv.Handler(),
		"?since_sequence="+first.NextCursor+"&limit=2", pkgauth.RoleBillingViewer))
	if len(second.Events) != 2 || !second.HasMore || second.NextCursor != "4" {
		t.Fatalf("page 2: events=%d hasMore=%v nextCursor=%q, want 2/true/\"4\"",
			len(second.Events), second.HasMore, second.NextCursor)
	}

	third := decodeMeteringPage(t, meteringRequest(t, srv.Handler(),
		"?since_sequence="+second.NextCursor+"&limit=2", pkgauth.RoleBillingViewer))
	if len(third.Events) != 1 || third.HasMore {
		t.Fatalf("page 3: events=%d hasMore=%v, want 1/false", len(third.Events), third.HasMore)
	}
}

func TestMeteringEventsRejectsBadSinceSequence(t *testing.T) {
	srv := meteringServer(t, 1)
	rr := meteringRequest(t, srv.Handler(), "?since_sequence=-1", pkgauth.RoleBillingViewer)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("since_sequence=-1: status %d, want 400", rr.Code)
	}
}

func TestMeteringEventsWithoutBillingStore(t *testing.T) {
	srv := sessionserver.New(memstore.New(), sessionserver.Options{})
	rr := meteringRequest(t, srv.Handler(), "", pkgauth.RoleBillingViewer)
	if rr.Code != http.StatusOK {
		t.Fatalf("no billing store: status %d, want 200", rr.Code)
	}
	page := decodeMeteringPage(t, rr)
	if len(page.Events) != 0 {
		t.Errorf("no billing store: got %d events, want 0", len(page.Events))
	}
}

// seqOf returns the first event's sequence number, or 0 when the page
// is empty.
func seqOf(page meteringPageJSON) uint64 {
	if len(page.Events) == 0 {
		return 0
	}
	return page.Events[0].SequenceNumber
}
