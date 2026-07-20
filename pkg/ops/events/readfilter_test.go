// SPDX-License-Identifier: MIT

package events

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gwevents "github.com/lennylabs/lenny/pkg/events"
)

// tenantEvent builds an operational event labeled with tenant (empty tenant =
// platform-scoped, no lennytenantid extension) so the read-filter tests can
// publish a mixed-tenant window.
func tenantEvent(typ, tenant string) gwevents.OperationalEvent {
	e := gwevents.OperationalEvent{Type: typ}
	if tenant != "" {
		e.Extensions = map[string]string{tenantLabelExtension: tenant}
	}
	return e
}

// pollScoped drives HandlePoll with a resolved read scope on the request
// context, the same way the opsserver boundary threads it.
func pollScoped(t *testing.T, s *Service, target, tenant string, platformAdmin bool) EventPage {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	ctx := WithReaderScope(req.Context(), "", tenant, platformAdmin)
	s.HandlePoll(rec, req.WithContext(ctx))
	if rec.Code != http.StatusOK {
		t.Fatalf("poll %s: status %d (%s)", target, rec.Code, rec.Body.String())
	}
	var page EventPage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("poll %s: decode: %v", target, err)
	}
	return page
}

// runStreamScoped drives HandleStream with a resolved read scope on the request
// context and returns the SSE frames observed before the ~20ms cancel.
func runStreamScoped(s *Service, target, tenant string, platformAdmin bool) []sseFrame {
	r := flushRec{httptest.NewRecorder()}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	ctx, cancel := context.WithCancel(WithReaderScope(req.Context(), "", tenant, platformAdmin))
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	s.HandleStream(r, req.WithContext(ctx))
	return parseFrames(r.Body.String())
}

// TestReaderScopeAdmits pins the §25.5 read-time tenant predicate: a
// platform-admin admits every event, a tenant-admin admits only events labeled
// with its own tenant, and a platform-scoped (no-label) event is dropped for a
// tenant-admin. A non-admin caller with no tenant admits nothing (fail closed).
//
// spec: 25.5 — "SSE and polling endpoints apply the same filter: tenant-scoped
// callers only see events matching their tenant or carrying no tenant label if
// the caller has permission for platform-scoped events (typically platform-
// admin only)."
func TestReaderScopeAdmits_spec_25_5(t *testing.T) {
	acme := tenantEvent("alert_fired", "acme")
	globex := tenantEvent("alert_fired", "globex")
	platform := tenantEvent("alert_fired", "")

	cases := []struct {
		name                   string
		tenant                 string
		platformAdmin          bool
		acme, globex, platform bool
	}{
		{"platform-admin sees all", "", true, true, true, true},
		{"tenant-admin acme", "acme", false, true, false, false},
		{"tenant-admin globex", "globex", false, false, true, false},
		{"non-admin no tenant sees nothing", "", false, false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sc := readerScope{tenantID: tc.tenant, platformAdmin: tc.platformAdmin}
			if got := sc.admits(acme); got != tc.acme {
				t.Errorf("admits(acme) = %v, want %v", got, tc.acme)
			}
			if got := sc.admits(globex); got != tc.globex {
				t.Errorf("admits(globex) = %v, want %v", got, tc.globex)
			}
			if got := sc.admits(platform); got != tc.platform {
				t.Errorf("admits(platform-scoped) = %v, want %v", got, tc.platform)
			}
		})
	}
}

// TestReaderScopeFromUnset confirms an unscoped context (an in-process caller
// that did not pass the opsserver authorization boundary) reports no scope, so
// the read path applies no tenant filter.
//
// spec: 25.5 (read-endpoint tenant filter is resolved at the boundary).
func TestReaderScopeFromUnset(t *testing.T) {
	if _, ok := readerScopeFrom(context.Background()); ok {
		t.Fatal("readerScopeFrom(background) reported a scope; want none")
	}
	ctx := WithReaderScope(context.Background(), "alice@acme.com", "acme", false)
	sc, ok := readerScopeFrom(ctx)
	if !ok || sc.subject != "alice@acme.com" || sc.tenantID != "acme" || sc.platformAdmin {
		t.Fatalf("readerScopeFrom = %+v, %v; want {alice@acme.com acme false}, true", sc, ok)
	}
}

// TestHandlePollTenantFilter drives the §25.5 poll surface over a mixed-tenant
// buffer window and asserts a tenant-admin caller sees only its own tenant's
// events (the cross-tenant and platform-scoped events are silently dropped)
// while a platform-admin sees the whole window. The status stays 200 for the
// tenant-admin: the drop is a filter, not a 403.
//
// spec: 25.5 (polling applies the same tenant filter as delivery; silent drop).
func TestHandlePollTenantFilter_spec_25_5(t *testing.T) {
	s := New(Options{Capacity: 16, Now: ts})
	s.Publish(context.Background(), tenantEvent("alert_fired", "acme"))
	s.Publish(context.Background(), tenantEvent("alert_fired", "globex"))
	s.Publish(context.Background(), tenantEvent("alert_fired", ""))

	admin := pollScoped(t, s, "/v1/admin/events", "", true)
	if len(admin.Items) != 3 {
		t.Fatalf("platform-admin poll: %d items, want 3", len(admin.Items))
	}

	tenant := pollScoped(t, s, "/v1/admin/events", "acme", false)
	if len(tenant.Items) != 1 {
		t.Fatalf("tenant-admin poll: %d items, want 1 (acme only)", len(tenant.Items))
	}
	if got := tenant.Items[0].Event.Extensions[tenantLabelExtension]; got != "acme" {
		t.Errorf("tenant-admin poll item tenant = %q, want acme", got)
	}
}

// TestHandleStreamTenantFilter drives the §25.5 SSE surface over a mixed-tenant
// buffer window and asserts a tenant-admin caller's backlog replay carries only
// its own tenant's events, while a platform-admin replays the whole window.
//
// spec: 25.5 (SSE applies the same tenant filter as delivery; silent drop).
func TestHandleStreamTenantFilter_spec_25_5(t *testing.T) {
	s := New(Options{Capacity: 16, Now: ts})
	s.Publish(context.Background(), tenantEvent("alert_fired", "acme"))
	s.Publish(context.Background(), tenantEvent("alert_fired", "globex"))
	s.Publish(context.Background(), tenantEvent("alert_fired", ""))

	admin := runStreamScoped(s, "/v1/admin/events/stream", "", true)
	if len(admin) != 3 {
		t.Fatalf("platform-admin stream: %d frames, want 3", len(admin))
	}

	frames := runStreamScoped(s, "/v1/admin/events/stream", "acme", false)
	if len(frames) != 1 {
		t.Fatalf("tenant-admin stream: %d frames, want 1 (acme only)", len(frames))
	}
	var ev gwevents.OperationalEvent
	if err := json.Unmarshal([]byte(frames[0].data), &ev); err != nil {
		t.Fatalf("decode frame: %v", err)
	}
	if got := ev.Extensions[tenantLabelExtension]; got != "acme" {
		t.Errorf("tenant-admin stream frame tenant = %q, want acme", got)
	}
}
