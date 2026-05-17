// SPDX-License-Identifier: MIT

package admin_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/opsevents"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

func newEventBufferAdmin(t *testing.T, buf *opsevents.EventBuffer) *admin.Router {
	t.Helper()
	return admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithEventBuffer(buf)
}

func opsEvent(typ, severity string) opsevents.OperationalEvent {
	return opsevents.OperationalEvent{
		ID: typ, SpecVersion: "1.0.2", Type: "dev.lenny." + typ,
		Severity: severity, Time: time.Now(),
	}
}

func TestEventBufferEndpointReturnsEvents(t *testing.T) {
	buf := opsevents.NewEventBuffer(0)
	buf.Append(opsEvent("alert_fired", "critical"))
	buf.Append(opsEvent("pool_state_changed", "info"))
	router := newEventBufferAdmin(t, buf)

	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/events/buffer", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	var page opsevents.BufferedEventPage
	if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page.Events) != 2 || page.Cursor != 2 {
		t.Errorf("buffer page: %d events, cursor %d; want 2, 2", len(page.Events), page.Cursor)
	}
}

func TestEventBufferEndpointFiltersAndCursor(t *testing.T) {
	buf := opsevents.NewEventBuffer(0)
	buf.Append(opsEvent("alert_fired", "critical"))
	buf.Append(opsEvent("pool_state_changed", "info"))
	buf.Append(opsEvent("alert_fired", "warning"))
	router := newEventBufferAdmin(t, buf)

	// ?eventType= narrows to one type.
	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet,
		"/v1/admin/events/buffer?eventType=alert_fired", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	var page opsevents.BufferedEventPage
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

func TestEventBufferEndpointRequiresAdmin(t *testing.T) {
	router := newEventBufferAdmin(t, opsevents.NewEventBuffer(0))
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/events/buffer", nil)
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden && rr.Code != http.StatusUnauthorized {
		t.Errorf("event buffer without an admin principal: status %d, want 401/403", rr.Code)
	}
}
