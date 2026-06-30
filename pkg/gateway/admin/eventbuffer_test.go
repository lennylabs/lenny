// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/breakerstore"
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	corr "github.com/lennylabs/lenny/pkg/observability/correlation"
)

func newEventBufferAdmin(t *testing.T, buf *eventbuffer.EventBuffer) *admin.Router {
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
	buf := eventbuffer.NewEventBuffer(0)
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
	if len(page.Events) != 2 || page.Pagination.Cursor != 2 {
		t.Errorf("buffer page: %d events, cursor %d; want 2, 2", len(page.Events), page.Pagination.Cursor)
	}
}

func TestEventBufferEndpointFiltersAndCursor(t *testing.T) {
	buf := eventbuffer.NewEventBuffer(0)
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
	buf := eventbuffer.NewEventBuffer(0)
	emitter := eventbuffer.NewEmitter(buf, "test")
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

// spec: §15.1 lines 937-938 — operation_id and agent_name from the
// correlation context are propagated to operational events as
// CloudEvents extension attributes. Opening a circuit breaker under a
// correlated request emits an event carrying both. F-15.1.10.
func TestOpsEventCarriesCorrelationExtensions_spec_15_1_937(t *testing.T) {
	buf := eventbuffer.NewEventBuffer(0)
	emitter := eventbuffer.NewEmitter(buf, "test")
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithBreakers(breakerstore.NewMemory()).
		WithEventBuffer(buf).
		WithEventEmitter(emitter)

	body, _ := json.Marshal(admin.OpenBreakerRequest{
		Reason: "incident", LimitTier: "runtime",
		Scope: admin.ScopePayload{Runtime: "echo"},
	})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost,
		"/v1/admin/circuit-breakers/rt-emergency/open", bytes.NewReader(body)))
	ctx := corr.With(req.Context(), corr.Fields{OperationID: "op-77", AgentName: "alice-agent"})
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req.WithContext(ctx))
	if rr.Code != http.StatusOK {
		t.Fatalf("open breaker: status %d, body=%s", rr.Code, rr.Body.String())
	}

	page := buf.Query(0, events.EventFilter{}, 0)
	var found bool
	for _, e := range page.Events {
		if e.Event.Type != "dev.lenny.circuit_breaker_opened" {
			continue
		}
		found = true
		if e.Event.Extensions["lennyoperationid"] != "op-77" {
			t.Errorf("lennyoperationid = %q, want op-77", e.Event.Extensions["lennyoperationid"])
		}
		if e.Event.Extensions["lennyagentname"] != "alice-agent" {
			t.Errorf("lennyagentname = %q, want alice-agent", e.Event.Extensions["lennyagentname"])
		}
	}
	if !found {
		t.Fatalf("circuit_breaker_opened event not found: %+v", page.Events)
	}
}

func TestEventBufferEndpointCSVFilter_spec_25_3_15(t *testing.T) {
	// spec: §25.2 lines 210-211 — ?severity= and ?eventType= accept the
	// canonical CSV form; the endpoint returns the union of the tokens
	// rather than the empty page the literal-match path produced.
	buf := eventbuffer.NewEventBuffer(0)
	buf.Append(opsEvent("alert_fired", "critical"))
	buf.Append(opsEvent("pool_state_changed", "info"))
	buf.Append(opsEvent("session_failed", "warning"))
	router := newEventBufferAdmin(t, buf)

	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet,
		"/v1/admin/events/buffer?severity=critical,warning&eventType=alert_fired,session_failed", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	var page events.BufferedEventPage
	if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page.Events) != 2 {
		t.Errorf("CSV severity+eventType union: %d events, want 2 (critical alert_fired, warning session_failed)", len(page.Events))
	}
	// The canonical pagination envelope rides on the wire response.
	if page.Pagination.CursorKind != "buffer-seq" {
		t.Errorf("cursorKind = %q, want buffer-seq", page.Pagination.CursorKind)
	}
}

func TestEventBufferEndpointRequiresAdmin(t *testing.T) {
	router := newEventBufferAdmin(t, eventbuffer.NewEventBuffer(0))
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/events/buffer", nil)
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden && rr.Code != http.StatusUnauthorized {
		t.Errorf("event buffer without an admin principal: status %d, want 401/403", rr.Code)
	}
}
