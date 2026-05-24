// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/denylist"
	"github.com/lennylabs/lenny/pkg/gateway/events"
	"github.com/lennylabs/lenny/pkg/gateway/gatewaymetrics"
	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	"github.com/lennylabs/lenny/pkg/tenantkms"
)

// spec: §4.9 — the gateway's LLM reverse-proxy listener wiring.

func TestNewLLMProxyServerDisabledWhenAddrEmpty(t *testing.T) {
	if srv := newLLMProxyServer("", "2023-06-01", credleasestore.New(), credcache.New(), denylist.New(), interceptor.NewChain()); srv != nil {
		t.Errorf("newLLMProxyServer with an empty address returned %v, want nil", srv)
	}
}

func TestNewLLMProxyServerBindsConfiguredAddress(t *testing.T) {
	srv := newLLMProxyServer(":8443", "2023-06-01", credleasestore.New(), credcache.New(), denylist.New(), interceptor.NewChain())
	if srv == nil {
		t.Fatal("newLLMProxyServer returned nil for a configured address")
	}
	if srv.Addr != ":8443" {
		t.Errorf("Addr = %q, want :8443", srv.Addr)
	}
}

func TestNewLLMProxyServerRoutesTheMessagesEndpoint(t *testing.T) {
	srv := newLLMProxyServer(":8443", "2023-06-01", credleasestore.New(), credcache.New(), denylist.New(), interceptor.NewChain())
	if srv == nil {
		t.Fatal("newLLMProxyServer returned nil")
	}

	// A POST with no lease token reaches the proxy handler, which
	// rejects it — proof the route is wired to the §4.9 handler.
	req := httptest.NewRequest(http.MethodPost, "/llm-proxy/v1/messages",
		strings.NewReader(`{"model":"claude-3-5-sonnet","messages":[]}`))
	rr := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("POST /llm-proxy/v1/messages = %d, want 401 (no lease token)", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "LEASE_TOKEN_MISSING") {
		t.Errorf("body = %q, want a LEASE_TOKEN_MISSING rejection", rr.Body.String())
	}
}

func TestNewLLMProxyServerRejectsUnknownPath(t *testing.T) {
	srv := newLLMProxyServer(":8443", "2023-06-01", credleasestore.New(), credcache.New(), denylist.New(), interceptor.NewChain())
	req := httptest.NewRequest(http.MethodPost, "/llm-proxy/v1/no-such-endpoint", nil)
	rr := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("POST to an unknown proxy path = %d, want 404", rr.Code)
	}
}

func TestNewLLMProxyServerRejectsNonPost(t *testing.T) {
	srv := newLLMProxyServer(":8443", "2023-06-01", credleasestore.New(), credcache.New(), denylist.New(), interceptor.NewChain())
	req := httptest.NewRequest(http.MethodGet, "/llm-proxy/v1/messages", nil)
	rr := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /llm-proxy/v1/messages = %d, want 405", rr.Code)
	}
}

// spec: §16.6 — a §10.7 fail-closed isolation rejection is an
// operational event.

func TestExperimentRejectionReporterEmitsOperationalEvent(t *testing.T) {
	emitter := events.NewEmitter(events.NewEventBuffer(0), "replica-test")
	reporter := experimentRejectionReporter{emitter: emitter}

	reporter.ReportExperimentIsolationRejection(context.Background(), sessionserver.ExperimentIsolationRejection{
		TenantID:             "acme",
		UserID:               "alice",
		ExperimentID:         "exp_1",
		VariantID:            "treatment",
		SessionMinIsolation:  "microvm",
		VariantPoolIsolation: "sandboxed",
	})

	page := emitter.Buffer().Query(0, events.EventFilter{
		EventType: string(events.EventExperimentIsolationMismatch),
	}, 0)
	if len(page.Events) != 1 {
		t.Fatalf("buffer holds %d isolation_mismatch events, want 1", len(page.Events))
	}
	ev := page.Events[0].Event
	if ev.Type != "dev.lenny.experiment.isolation_mismatch" {
		t.Errorf("event type = %q", ev.Type)
	}
	if ev.Severity != "warning" {
		t.Errorf("severity = %q, want warning", ev.Severity)
	}
	var data struct {
		TenantID     string `json:"tenant_id"`
		ExperimentID string `json:"experiment_id"`
		SessionMin   string `json:"sessionMinIsolation"`
		VariantPool  string `json:"variantPoolIsolation"`
	}
	if err := json.Unmarshal(ev.Data, &data); err != nil {
		t.Fatalf("event data: %v", err)
	}
	if data.TenantID != "acme" || data.ExperimentID != "exp_1" {
		t.Errorf("tenant/experiment = %q/%q, want acme/exp_1", data.TenantID, data.ExperimentID)
	}
	if data.SessionMin != "microvm" || data.VariantPool != "sandboxed" {
		t.Errorf("isolation fields = %q/%q, want microvm/sandboxed", data.SessionMin, data.VariantPool)
	}
}

func TestExperimentRejectionReporterNilDependenciesAreSafe(t *testing.T) {
	// A reporter with no audit, metrics, or emitter wired must not
	// panic — every sink is best-effort.
	reporter := experimentRejectionReporter{}
	reporter.ReportExperimentIsolationRejection(context.Background(), sessionserver.ExperimentIsolationRejection{
		TenantID: "acme", ExperimentID: "exp_1",
	})
}

// spec: §4.1 / §16.1 — exportHPAGauges populates the
// lenny_gateway_active_sessions, _active_streams, and
// _request_queue_depth gauges from the live in-process state.
func TestExportHPAGaugesUpdatesAllGauges(t *testing.T) {
	store := memstore.New()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Seed two active sessions and one terminal session under the
	// "default" tenant; the exporter must count two.
	for _, s := range []sessionstore.Session{
		{ID: "s1", TenantID: "default", State: session.StateRunning, CreatedAt: now, UpdatedAt: now},
		{ID: "s2", TenantID: "default", State: session.StateCreated, CreatedAt: now, UpdatedAt: now},
		{ID: "s3", TenantID: "default", State: session.StateCompleted, CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.Create(context.Background(), s); err != nil {
			t.Fatalf("seed %s: %v", s.ID, err)
		}
	}

	bus := sessionevents.NewBus(0)
	sub := bus.Subscribe("s1", 0, 4)
	defer sub.Close()

	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}

	// Drive a request through the metrics middleware so InflightRequests
	// reports a non-zero value across the exporter call.
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusOK)
	})
	h := m.Middleware(inner, func(*http.Request) string { return "/test" })
	go func() {
		defer close(done)
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
	}()
	<-started

	// Wrap the sessionstore in a minimal tenantsLister that only
	// returns the seeded tenant; the production lister adds "default"
	// and that's all this fixture has.
	lister := staticTenantLister{[]string{"default"}}
	exportHPAGauges(context.Background(), store, lister, bus, m)

	// Now drive a second poll after the in-flight request finishes
	// so we can verify the queue-depth gauge tracks both directions.
	close(release)
	<-done

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rr.Body.String()
	for _, want := range []string{
		"lenny_gateway_active_sessions 2",
		"lenny_gateway_active_streams 1",
		"lenny_gateway_request_queue_depth 1",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q\n---\n%s", want, body)
		}
	}

	// Second poll: the in-flight request has exited, so queue depth
	// drops to 0; active sessions stay at 2 and active streams remain
	// at 1 (the subscriber is still open).
	exportHPAGauges(context.Background(), store, lister, bus, m)
	rr = httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body = rr.Body.String()
	if !strings.Contains(body, "lenny_gateway_request_queue_depth 0") {
		t.Errorf("after handler exit, queue_depth gauge did not drop to 0\n%s", body)
	}
}

// spec: §4.1 — a cancelled context returns from exportHPAGauges
// promptly without spuriously updating the gauges.
func TestExportHPAGaugesContextCancellation(t *testing.T) {
	store := memstore.New()
	bus := sessionevents.NewBus(0)
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	lister := staticTenantLister{[]string{"default"}}
	// Cancelled context: the lister call returns an empty/nil result
	// without panic.
	exportHPAGauges(ctx, store, lister, bus, m)
}

// staticTenantLister is a test-only TenantLister that returns a
// fixed slice. It is wired through tenantsLister's interface so the
// exportHPAGauges signature matches the production code path.
type staticTenantLister struct {
	tenants []string
}

func (s staticTenantLister) ListTenants(_ context.Context) ([]string, error) {
	return s.tenants, nil
}

// spec: §12.5 ll. 297-303 — the SSEKeyResolver returns
// (alias, requireKey=true, nil) for a T4 tenant, ("", false, nil) for
// any other tier, and ("", true, err) for a tenant lookup failure
// (fail-closed posture). The blobstore consumes the three branches
// and the gateway's MinIO Put dispatch wraps a T4 object under the
// per-tenant alias.
func TestNewSSEKeyResolverPicksT4AliasOrFallsBack(t *testing.T) {
	store := tenantstore.NewMemory()
	ctx := context.Background()
	if err := store.Create(ctx, tenantstore.Tenant{ID: "acme-t4", WorkspaceTier: "T4"}); err != nil {
		t.Fatalf("Create T4 tenant: %v", err)
	}
	if err := store.Create(ctx, tenantstore.Tenant{ID: "globex-t3", WorkspaceTier: "T3"}); err != nil {
		t.Fatalf("Create T3 tenant: %v", err)
	}
	if err := store.Create(ctx, tenantstore.Tenant{ID: "initech-untiered"}); err != nil {
		t.Fatalf("Create untiered tenant: %v", err)
	}

	resolver := newSSEKeyResolver(store)

	cases := []struct {
		name       string
		tenantID   string
		wantAlias  string
		wantNeed   bool
		wantErrMsg string // substring; empty means no error
	}{
		{
			name:      "T4 tenant returns per-tenant alias and requireKey=true",
			tenantID:  "acme-t4",
			wantAlias: tenantkms.AliasFor("acme-t4"),
			wantNeed:  true,
		},
		{
			name:      "T3 tenant returns empty alias and requireKey=false",
			tenantID:  "globex-t3",
			wantAlias: "",
			wantNeed:  false,
		},
		{
			name:      "untiered tenant returns empty alias and requireKey=false",
			tenantID:  "initech-untiered",
			wantAlias: "",
			wantNeed:  false,
		},
		{
			name:       "missing tenant returns error and requireKey=true (fail-closed)",
			tenantID:   "ghost",
			wantAlias:  "",
			wantNeed:   true,
			wantErrMsg: "tenant not found",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			alias, need, err := resolver(tc.tenantID)
			if alias != tc.wantAlias {
				t.Errorf("alias = %q, want %q", alias, tc.wantAlias)
			}
			if need != tc.wantNeed {
				t.Errorf("requireKey = %v, want %v", need, tc.wantNeed)
			}
			if tc.wantErrMsg == "" {
				if err != nil {
					t.Errorf("err = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErrMsg) {
				t.Errorf("err = %v, want containing %q", err, tc.wantErrMsg)
			}
			// And the error wraps tenantstore.ErrNotFound so callers
			// can match on errors.Is.
			if err != nil && !errors.Is(err, tenantstore.ErrNotFound) {
				t.Errorf("err = %v, want errors.Is(tenantstore.ErrNotFound)", err)
			}
		})
	}
}
