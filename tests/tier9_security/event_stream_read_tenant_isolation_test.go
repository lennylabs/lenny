// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security test for the §25.5 tenant-isolation contract on the
// operational-event-stream read surface. It drives GET /v1/admin/events
// through a real *opsserver.Server wired with the §25.4 auth stack (an HMAC
// verifier standing in for OIDC, matching the authedOpsServer precedent in this
// package) and genuine minted bearer tokens, so the caller's tenant scope is
// resolved from the verified principal the same way a deployed lenny-ops caller
// reaches it. The read endpoint already admits a tenant-admin at the route gate
// (schema required-role tenant-admin); this test pins the per-event tenant
// filter that keeps that admission safe, so a tenant-admin caller observes only
// its own tenant's events and never a cross-tenant or platform-scoped one.
//
// spec: §25.5 (Tenant Isolation).

package tier9_security_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	gwevents "github.com/lennylabs/lenny/pkg/events"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	opsstream "github.com/lennylabs/lenny/pkg/ops/events"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
)

// authedEventStreamServer returns a real *opsserver.Server wired with the
// §25.4 auth stack (HMAC verifier standing in for OIDC) and an in-memory event
// stream seeded with an acme-labeled, a globex-labeled, and a platform-scoped
// (no-label) event. Multi-tenant auth is enabled with the permissive
// isoTenantRegistry so a minted tenant claim is honored rather than collapsed
// to the single-tenant default, which is what makes the per-tenant read filter
// under test observable.
func authedEventStreamServer() (*opsserver.Server, *jwt.HMACSigner) {
	stream := opsstream.New(opsstream.Options{})
	publish := func(tenant string) {
		e := gwevents.OperationalEvent{Type: gwevents.EventType("alert_fired").CloudEventsType(), Subject: "pool/" + tenant}
		if tenant != "" {
			e.Extensions = map[string]string{"lennytenantid": tenant}
		}
		stream.Publish(context.Background(), e)
	}
	publish("acme")
	publish("globex")
	publish("") // platform-scoped, no tenant label

	signer := jwt.NewHMACSigner("event-stream-isolation-test", []byte("event-stream-isolation-test-secret"))
	srv := opsserver.New(opsserver.Options{
		EventStream: stream,
		Auth: &opsserver.AuthConfig{
			Options: authmw.Options{
				Verifier:    signer,
				MultiTenant: true,
				Registry:    isoTenantRegistry{},
			},
			RateLimiter: opsserver.NewRateLimiter(1000, 1000),
		},
	})
	return srv, signer
}

// eventPollTenants drives GET /v1/admin/events with the given bearer and
// returns the response status and the lennytenantid label of each served event.
func eventPollTenants(t *testing.T, srv *opsserver.Server, bearer string) (int, []string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/events", nil)
	req.Header.Set("Authorization", "Bearer "+bearer)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		return rec.Code, nil
	}
	var page struct {
		Items []struct {
			Event gwevents.OperationalEvent `json:"event"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode poll page %s: %v", rec.Body.String(), err)
	}
	labels := make([]string, len(page.Items))
	for i, it := range page.Items {
		labels[i] = it.Event.Extensions["lennytenantid"]
	}
	return rec.Code, labels
}

// spec: §25.5 (Tenant Isolation) — "SSE and polling endpoints apply the same
// filter: tenant-scoped callers only see events matching their tenant or
// carrying no tenant label if the caller has permission for platform-scoped
// events (typically platform-admin only)."
//
// diagnosis: a tenant-admin querying the live §25.5 operational event poll
// endpoint (through the genuine JWT auth stack, not an injected scope) received
// another tenant's operational event or a platform-scoped (no-label) event, or
// was rejected with a 403 instead of a silently narrowed page. Either the read
// path applies no per-event tenant filter (a cross-tenant operational-event
// leak on the deployed lenny-ops read surface) or it enforces isolation with
// the create-only SUBSCRIPTION_TENANT_FORBIDDEN error rather than a silent
// drop.
func TestOpsEventPollTenantAdminScopedToOwnTenant_spec_25_5(t *testing.T) {
	srv, signer := authedEventStreamServer()

	// A platform-admin observes every event across tenants and the
	// platform-scoped one.
	adminTok := mintOpsRBACToken(t, signer, "root@platform", "acme", "", auth.RolePlatformAdmin)
	code, labels := eventPollTenants(t, srv, adminTok)
	if code != http.StatusOK {
		t.Fatalf("platform-admin poll: status %d, want 200", code)
	}
	if len(labels) != 3 {
		t.Fatalf("platform-admin poll served %v, want all 3 events (acme, globex, platform-scoped)", labels)
	}

	// A tenant-admin for acme observes only the acme-labeled event: never
	// globex's, never the platform-scoped one, and with a 200 (silent filter,
	// not a 403).
	acmeTok := mintOpsRBACToken(t, signer, "alice@acme.com", "acme", "", auth.RoleTenantAdmin)
	code, labels = eventPollTenants(t, srv, acmeTok)
	if code != http.StatusOK {
		t.Fatalf("tenant-admin acme poll: status %d, want 200 (the read filter is a silent drop, not a 403)", code)
	}
	if len(labels) != 1 || labels[0] != "acme" {
		t.Fatalf("tenant-admin acme poll served %v, want exactly the acme-labeled event; a cross-tenant or platform-scoped event leaked", labels)
	}

	// A tenant-admin for globex symmetrically observes only its own event.
	globexTok := mintOpsRBACToken(t, signer, "carol@globex.com", "globex", "", auth.RoleTenantAdmin)
	code, labels = eventPollTenants(t, srv, globexTok)
	if code != http.StatusOK {
		t.Fatalf("tenant-admin globex poll: status %d, want 200", code)
	}
	if len(labels) != 1 || labels[0] != "globex" {
		t.Fatalf("tenant-admin globex poll served %v, want exactly the globex-labeled event", labels)
	}
}

// eventStreamFrames drives GET /v1/admin/events/stream with the given bearer
// over a context cancelled shortly after the backlog replay, and returns the
// response status together with the lennytenantid label of each emitted frame.
// The SSE handler blocks until its request context is done, so the cancellation
// is what ends the read.
func eventStreamFrames(t *testing.T, srv *opsserver.Server, bearer string) (int, []string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/events/stream", nil).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+bearer)
	rec := httptest.NewRecorder()
	timer := time.AfterFunc(500*time.Millisecond, cancel)
	defer timer.Stop()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		return rec.Code, nil
	}
	var labels []string
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		var ev gwevents.OperationalEvent
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			t.Fatalf("decode SSE frame %q: %v", data, err)
		}
		labels = append(labels, ev.Extensions["lennytenantid"])
	}
	return rec.Code, labels
}

// spec: §25.5 (Tenant Isolation) — "SSE and polling endpoints apply the same
// filter: tenant-scoped callers only see events matching their tenant or
// carrying no tenant label if the caller has permission for platform-scoped
// events (typically platform-admin only)." The SSE endpoint enforces that
// predicate at three points the poll endpoint does not share (the backlog
// replay, the live tail, and the gateway-buffer fall-back frames), and all
// three are reachable only when the route boundary resolves the caller's scope
// onto the request context. Driving the endpoint through the real auth stack
// is what covers that resolution: a regression at the route boundary would fail
// open on the stream while the poll test stayed green.
//
// diagnosis: a tenant-admin subscribing to the live §25.5 operational event
// stream (through the genuine JWT auth stack, not an injected scope) received
// another tenant's operational event or a platform-scoped (no-label) event, or
// was rejected with a 403 instead of a silently narrowed stream. Either the SSE
// route no longer resolves the caller's tenant scope onto the request context —
// a cross-tenant operational-event leak on the deployed lenny-ops stream
// endpoint — or it enforces isolation with the create-only
// SUBSCRIPTION_TENANT_FORBIDDEN error rather than a silent drop.
func TestOpsEventStreamTenantAdminSSEScopedToOwnTenant_spec_25_5(t *testing.T) {
	srv, signer := authedEventStreamServer()

	// A platform-admin observes every event on the stream, including the
	// platform-scoped one.
	adminTok := mintOpsRBACToken(t, signer, "root@platform", "acme", "", auth.RolePlatformAdmin)
	code, labels := eventStreamFrames(t, srv, adminTok)
	if code != http.StatusOK {
		t.Fatalf("platform-admin stream: status %d, want 200", code)
	}
	if len(labels) != 3 {
		t.Fatalf("platform-admin stream emitted %v, want all 3 events (acme, globex, platform-scoped)", labels)
	}

	// A tenant-admin for acme receives only the acme-labeled frame: never
	// globex's, never the platform-scoped one, and with a 200 rather than a 403.
	acmeTok := mintOpsRBACToken(t, signer, "alice@acme.com", "acme", "", auth.RoleTenantAdmin)
	code, labels = eventStreamFrames(t, srv, acmeTok)
	if code != http.StatusOK {
		t.Fatalf("tenant-admin acme stream: status %d, want 200 (the read filter is a silent drop, not a 403)", code)
	}
	if len(labels) != 1 || labels[0] != "acme" {
		t.Fatalf("tenant-admin acme stream emitted %v, want exactly the acme-labeled frame; a cross-tenant or platform-scoped event leaked", labels)
	}

	// A tenant-admin for globex symmetrically receives only its own frame.
	globexTok := mintOpsRBACToken(t, signer, "carol@globex.com", "globex", "", auth.RoleTenantAdmin)
	code, labels = eventStreamFrames(t, srv, globexTok)
	if code != http.StatusOK {
		t.Fatalf("tenant-admin globex stream: status %d, want 200", code)
	}
	if len(labels) != 1 || labels[0] != "globex" {
		t.Fatalf("tenant-admin globex stream emitted %v, want exactly the globex-labeled frame", labels)
	}
}
