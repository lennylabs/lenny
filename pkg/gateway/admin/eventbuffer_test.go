// SPDX-License-Identifier: MIT

package admin_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/breakerstore"
	"github.com/lennylabs/lenny/pkg/gateway/events"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

func newEventBufferAdmin(t *testing.T, buf *events.EventBuffer) *admin.Router {
	t.Helper()
	return admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithEventBuffer(buf)
}

func opsEvent(typ, severity string) events.OperationalEvent {
	return events.OperationalEvent{
		ID: typ, SpecVersion: "1.0.2", Type: "dev.lenny." + typ,
		Severity: severity, Time: time.Now(),
	}
}

func TestEventBufferEndpointReturnsEvents(t *testing.T) {
	buf := events.NewEventBuffer(0)
	buf.Append(opsEvent("alert_fired", "critical"))
	buf.Append(opsEvent("pool_state_changed", "info"))
	router := newEventBufferAdmin(t, buf)

	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/events/buffer", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	var page events.BufferedEventPage
	if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page.Events) != 2 || page.Cursor != 2 {
		t.Errorf("buffer page: %d events, cursor %d; want 2, 2", len(page.Events), page.Cursor)
	}
}

func TestEventBufferEndpointFiltersAndCursor(t *testing.T) {
	buf := events.NewEventBuffer(0)
	buf.Append(opsEvent("alert_fired", "critical"))
	buf.Append(opsEvent("pool_state_changed", "info"))
	buf.Append(opsEvent("alert_fired", "warning"))
	router := newEventBufferAdmin(t, buf)

	// ?eventType= narrows to one type.
	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet,
		"/v1/admin/events/buffer?eventType=alert_fired", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	var page events.BufferedEventPage
	_ = json.Unmarshal(rr.Body.Bytes(), &page)
	if len(page.Events) != 2 {
		t.Errorf("eventType filter: %d events, want 2", len(page.Events))
	}

	// ?since= advances past delivered events.
	req = withAdminPrincipal(httptest.NewRequest(http.MethodGet,
		"/v1/admin/events/buffer?since=2", nil))
	rr = httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	_ = json.Unmarshal(rr.Body.Bytes(), &page)
	if len(page.Events) != 1 || page.Events[0].ID != 3 {
		t.Errorf("since cursor: %+v", page.Events)
	}
}

func TestEventBufferSurfacesCircuitBreakerEvent(t *testing.T) {
	// §25.3: opening a circuit breaker via the admin API emits an
	// operational event into the buffer the endpoint then surfaces.
	buf := events.NewEventBuffer(0)
	emitter := events.NewEmitter(buf, "test")
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithBreakers(breakerstore.NewMemory()).
		WithEventBuffer(buf).
		WithEventEmitter(emitter)

	open := breakerReq(t, router.Handler(), http.MethodPost,
		"/v1/admin/circuit-breakers/rt-emergency/open",
		admin.OpenBreakerRequest{
			Reason: "incident", LimitTier: "runtime",
			Scope: admin.ScopePayload{Runtime: "echo"},
		})
	if open.Code != http.StatusOK {
		t.Fatalf("open breaker: status %d, body=%s", open.Code, open.Body.String())
	}

	q := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/events/buffer", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, q)
	var page events.BufferedEventPage
	if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	found := false
	for _, e := range page.Events {
		if e.Event.Type == "dev.lenny.circuit_breaker_opened" {
			found = true
		}
	}
	if !found {
		t.Errorf("opening a breaker must emit circuit_breaker_opened into the buffer: %+v", page.Events)
	}
}

func TestEventBufferEndpointRequiresAdmin(t *testing.T) {
	router := newEventBufferAdmin(t, events.NewEventBuffer(0))
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/events/buffer", nil)
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden && rr.Code != http.StatusUnauthorized {
		t.Errorf("event buffer without an admin principal: status %d, want 401/403", rr.Code)
	}
}
