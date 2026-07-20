//go:build component

// SPDX-License-Identifier: MIT

// Tier-2 component test for the §25.5 tenant-isolation contract on the
// operational-event-stream read surface (GET /v1/admin/events and
// /v1/admin/events/stream), driven against a real Redis ops:events:stream
// so the Redis-served read path enforces the same tenant filter the
// buffer-served path does. The read handlers resolve the caller's tenant
// scope from the request context the opsserver boundary populates
// (opsstream.WithReaderScope), then intersect the served events with that
// scope: a platform-admin observes every event, a tenant-admin observes only
// events labeled with its own tenant, and a platform-scoped (no-label) event
// is dropped for a tenant-admin. The drop is silent (the poll stays 200 with a
// narrowed page), not a 403.
package eventstream_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gwevents "github.com/lennylabs/lenny/pkg/events"
	opsstream "github.com/lennylabs/lenny/pkg/ops/events"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// tenantAlertEvent builds an alert event labeled with tenant via the
// lennytenantid CloudEvents extension. An empty tenant leaves the event
// platform-scoped (no label), which only a platform-admin read caller observes.
func tenantAlertEvent(subject, tenant string) gwevents.OperationalEvent {
	e := alertEvent(subject)
	if tenant != "" {
		e.Extensions = map[string]string{"lennytenantid": tenant}
	}
	return e
}

// pollScopedRedis drives GET /v1/admin/events with the caller's tenant scope on
// the request context, mirroring how the opsserver route boundary threads it,
// and decodes the §25.5 poll envelope. The status is asserted 200 so the tenant
// filter reads as a silent drop rather than a 403.
func pollScopedRedis(t *testing.T, s *opsstream.Service, tenant string, platformAdmin bool) opsstream.EventPage {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/admin/events", nil)
	ctx := opsstream.WithReaderScope(req.Context(), "", tenant, platformAdmin)
	s.HandlePoll(rec, req.WithContext(ctx))
	if rec.Code != http.StatusOK {
		t.Fatalf("scoped poll (tenant=%q admin=%v): status %d, want 200 (silent tenant filter, not a 403); body=%s",
			tenant, platformAdmin, rec.Code, rec.Body.String())
	}
	var page opsstream.EventPage
	if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
		t.Fatalf("decode scoped poll body: %v", err)
	}
	return page
}

// TestOpsEventStreamReadSurfaceTenantScopedCallerIsolation stands up the §25.5
// read surface over a real Redis ops:events:stream carrying an acme-labeled, a
// globex-labeled, and a platform-scoped (no-label) event, then polls and
// streams as each caller class. It asserts a tenant-admin for acme receives the
// acme-labeled event, never the globex-labeled event, and never the
// platform-scoped event; a platform-admin receives all three; and the
// tenant-scoped read is a silent filter (HTTP 200 with a narrowed page), not a
// 403.
//
// spec: 25.5 (Tenant Isolation) — "SSE and polling endpoints apply the same
// filter: tenant-scoped callers only see events matching their tenant or
// carrying no tenant label if the caller has permission for platform-scoped
// events (typically platform-admin only)."
// diagnosis: a failure means the §25.5 read surface leaks another tenant's
// operational events to a tenant-scoped caller, or exposes platform-scoped
// (no-tenant-label) events to a caller without platform-scoped-event
// permission, or rejects the tenant-scoped caller with a 403 instead of
// silently narrowing the page — a cross-tenant data-isolation breach on the
// SSE/polling read path, or a contract break on the create-only
// SUBSCRIPTION_TENANT_FORBIDDEN error.
func TestOpsEventStreamReadSurfaceTenantScopedCallerIsolation(t *testing.T) {
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx := context.Background()

	const key = "ops:events:stream:tenantrbac"
	emitter := newStreamEmitter(t, rd.Client, key, 1000)
	for _, ev := range []gwevents.OperationalEvent{
		tenantAlertEvent("pool/acme", "acme"),
		tenantAlertEvent("pool/globex", "globex"),
		tenantAlertEvent("pool/platform", ""),
	} {
		if err := emitter.Emit(ctx, ev); err != nil {
			t.Fatalf("emit %s: %v", ev.Subject, err)
		}
	}

	svc := opsstream.New(opsstream.Options{
		RedisClient:    opsstream.NewRedisStreamClient(rd.Client),
		RedisStreamKey: key,
		SourceHealth:   opsstream.StaticSourceHealth{Redis: true, Gateway: true},
	})

	// Platform-admin: the whole cross-tenant window.
	admin := pollScopedRedis(t, svc, "", true)
	if len(admin.Items) != 3 {
		t.Fatalf("platform-admin poll: %d items, want all 3", len(admin.Items))
	}

	// Tenant-admin for acme: only the acme-labeled event.
	acme := pollScopedRedis(t, svc, "acme", false)
	if len(acme.Items) != 1 {
		t.Fatalf("tenant-admin acme poll: %d items, want 1 (acme only)", len(acme.Items))
	}
	if got := acme.Items[0].Event.Extensions["lennytenantid"]; got != "acme" {
		t.Fatalf("tenant-admin acme poll served a %q-labeled event; the read surface leaked a cross-tenant or platform-scoped event", got)
	}

	// Tenant-admin for globex: only the globex-labeled event, never acme's, and
	// never the platform-scoped event.
	globex := pollScopedRedis(t, svc, "globex", false)
	if len(globex.Items) != 1 || globex.Items[0].Event.Extensions["lennytenantid"] != "globex" {
		t.Fatalf("tenant-admin globex poll = %d items %v, want exactly the globex-labeled event",
			len(globex.Items), tenantLabels(globex.Items))
	}

	// SSE backlog replay enforces the same isolation: a tenant-admin for acme
	// replays only the acme-labeled event.
	frames := streamScopedRedisBacklog(t, svc, "acme", false)
	if len(frames) != 1 {
		t.Fatalf("tenant-admin acme SSE backlog: %d frames, want 1 (acme only)", len(frames))
	}
	if got := frames[0].Extensions["lennytenantid"]; got != "acme" {
		t.Fatalf("tenant-admin acme SSE served a %q-labeled frame; the SSE read path leaked a cross-tenant or platform-scoped event", got)
	}
}

// tenantLabels renders the tenant labels of a page's items for a failure
// message.
func tenantLabels(items []gwevents.BufferedEvent) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Event.Extensions["lennytenantid"]
	}
	return out
}

// streamScopedRedisBacklog drives GET /v1/admin/events/stream with the caller's
// tenant scope on the request context over a live, cancellable context (the
// Redis backlog XRANGE read needs a live context), reads the SSE backlog replay
// until the stream goes idle, then cancels and returns the decoded events.
func streamScopedRedisBacklog(t *testing.T, s *opsstream.Service, tenant string, platformAdmin bool) []gwevents.OperationalEvent {
	t.Helper()
	ctx, cancel := context.WithCancel(opsstream.WithReaderScope(context.Background(), "", tenant, platformAdmin))
	pr, pw := io.Pipe()
	rw := &pipeResponseWriter{hdr: http.Header{}, w: pw}
	req := httptest.NewRequest("GET", "/v1/admin/events/stream", nil).WithContext(ctx)
	go func() {
		s.HandleStream(rw, req)
		_ = pw.Close()
	}()

	ch := make(chan sseFrame, 16)
	go readSSE(pr, ch)

	var out []gwevents.OperationalEvent
	timer := time.NewTimer(1500 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case f, ok := <-ch:
			if !ok {
				cancel()
				return out
			}
			out = append(out, f.event)
			timer.Reset(1500 * time.Millisecond)
		case <-timer.C:
			cancel()
			for range ch {
			}
			return out
		}
	}
}
