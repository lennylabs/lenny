// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// logsTestServer builds a session-server over a running session with the
// supplied published log events, so each §24.17/§15.1 logs assertion runs
// against the same event store the /logs endpoint reads.
func logsTestServer(t *testing.T, id string, publish func(bus *sessionevents.Bus, now time.Time)) *sessionserver.Server {
	t.Helper()
	store := memstore.New()
	bus := sessionevents.NewBus(32)
	srv := sessionserver.New(store, sessionserver.Options{Events: bus})
	now := time.Now()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: "acme", State: session.StateRunning,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if publish != nil {
		publish(bus, now)
	}
	return srv
}

// TestLogsJSONReturnsCanonicalEnvelope_spec_15_1_673 confirms the
// §24.17 line 220 `session logs` target serves the §15.1 line 1228
// canonical `{items, cursor, hasMore}` envelope over the event store when
// the caller negotiates JSON. F-24.17.6.
func TestLogsJSONReturnsCanonicalEnvelope_spec_15_1_673(t *testing.T) {
	srv := logsTestServer(t, "sess_logs", func(bus *sessionevents.Bus, now time.Time) {
		for i := 0; i < 4; i++ {
			bus.PublishForTenant("acme", "sess_logs", "log", `{"line":"hello"}`, now)
		}
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess_logs/logs", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set("Accept", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body=%s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type %q, want application/json", ct)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"items"`) || !strings.Contains(body, `"hasMore"`) {
		t.Errorf("body missing canonical envelope keys: %s", body)
	}
	if strings.Contains(body, "event:") {
		t.Errorf("JSON path leaked an SSE frame: %s", body)
	}
}

// TestLogsJSONPaginates_spec_15_1_1228 verifies the logs JSON view honors
// ?limit= and reports a continuation cursor. F-24.17.6.
func TestLogsJSONPaginates_spec_15_1_1228(t *testing.T) {
	srv := logsTestServer(t, "sess_logs_pag", func(bus *sessionevents.Bus, now time.Time) {
		for i := 0; i < 7; i++ {
			bus.PublishForTenant("acme", "sess_logs_pag", "log", `{}`, now)
		}
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess_logs_pag/logs?limit=3", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set("Accept", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"hasMore":true`) {
		t.Errorf("expected hasMore=true at limit=3 over 7 logs: %s", body)
	}
	if !strings.Contains(body, `"cursor"`) {
		t.Errorf("expected a continuation cursor: %s", body)
	}
}

// TestLogsJSONSinceFilter_spec_24_17_220 confirms the §24.17 `--since`
// flag drops log entries older than the supplied RFC3339 timestamp.
// F-24.17.6.
func TestLogsJSONSinceFilter_spec_24_17_220(t *testing.T) {
	base := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	store := memstore.New()
	bus := sessionevents.NewBus(32)
	srv := sessionserver.New(store, sessionserver.Options{Events: bus})
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_since", TenantID: "acme", State: session.StateRunning,
		CreatedAt: base, UpdatedAt: base,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Two old entries, then two recent ones.
	bus.PublishForTenant("acme", "sess_since", "log", `{"n":1}`, base)
	bus.PublishForTenant("acme", "sess_since", "log", `{"n":2}`, base.Add(time.Minute))
	bus.PublishForTenant("acme", "sess_since", "log", `{"n":3}`, base.Add(10*time.Minute))
	bus.PublishForTenant("acme", "sess_since", "log", `{"n":4}`, base.Add(11*time.Minute))

	cutoff := base.Add(5 * time.Minute).Format(time.RFC3339)
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess_since/logs?since="+cutoff, nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set("Accept", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if strings.Contains(body, `"n":1`) || strings.Contains(body, `"n":2`) {
		t.Errorf("entries before --since leaked through: %s", body)
	}
	if !strings.Contains(body, `"n":3`) || !strings.Contains(body, `"n":4`) {
		t.Errorf("entries after --since missing: %s", body)
	}
}

// TestLogsJSONRejectsBadSince_spec_24_17_220 confirms a non-RFC3339
// `since` is a 400 VALIDATION_ERROR rather than a silent no-op. F-24.17.6.
func TestLogsJSONRejectsBadSince_spec_24_17_220(t *testing.T) {
	srv := logsTestServer(t, "sess_badsince", nil)

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess_badsince/logs?since=not-a-time", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set("Accept", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400 (body=%s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "VALIDATION_ERROR") {
		t.Errorf("missing VALIDATION_ERROR: %s", rr.Body.String())
	}
}

// TestLogsMissingSession404_spec_15_1_661 confirms the §15.1 line 661
// contract that /logs returns 404 RESOURCE_NOT_FOUND when no session
// record exists. F-24.17.6.
func TestLogsMissingSession404_spec_15_1_661(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{Events: sessionevents.NewBus(0)})

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/ghost/logs", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set("Accept", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404 (body=%s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "RESOURCE_NOT_FOUND") {
		t.Errorf("missing RESOURCE_NOT_FOUND: %s", rr.Body.String())
	}
}

// TestLogsTenantIsolation_spec_7_2 confirms a caller scoped to another
// tenant cannot read a session's logs (the §7.2 tenant-binding guard the
// event subscription enforces). F-24.17.6.
func TestLogsTenantIsolation_spec_7_2(t *testing.T) {
	srv := logsTestServer(t, "sess_iso", func(bus *sessionevents.Bus, now time.Time) {
		bus.PublishForTenant("acme", "sess_iso", "log", `{}`, now)
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess_iso/logs", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "globex")
	req.Header.Set("Accept", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant status %d, want 404 (body=%s)", rr.Code, rr.Body.String())
	}
}

// TestLogsSSEStreamsBacklog_spec_15_1_673 confirms the default (non-JSON)
// content negotiation returns the SSE log tail with the retained backlog
// replayed as id:/event:/data: frames. F-24.17.6.
func TestLogsSSEStreamsBacklog_spec_15_1_673(t *testing.T) {
	srv := logsTestServer(t, "sess_sse", func(bus *sessionevents.Bus, now time.Time) {
		bus.PublishForTenant("acme", "sess_sse", "log", `{"line":"one"}`, now)
	})

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess_sse/logs", nil).WithContext(ctx)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		srv.Handler().ServeHTTP(rr, req)
		close(done)
	}()
	// Give the handler time to flush the backlog, then disconnect.
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	if ct := rr.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type %q, want text/event-stream", ct)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "event: log") || !strings.Contains(body, `"line":"one"`) {
		t.Errorf("SSE backlog frame missing: %s", body)
	}
}
