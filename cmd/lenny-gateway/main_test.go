// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/denylist"
	"github.com/lennylabs/lenny/pkg/gateway/opsevents"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// spec: §4.9 — the gateway's LLM reverse-proxy listener wiring.

func TestNewLLMProxyServerDisabledWhenAddrEmpty(t *testing.T) {
	if srv := newLLMProxyServer("", "2023-06-01", credleasestore.New(), credcache.New(), denylist.New()); srv != nil {
		t.Errorf("newLLMProxyServer with an empty address returned %v, want nil", srv)
	}
}

func TestNewLLMProxyServerBindsConfiguredAddress(t *testing.T) {
	srv := newLLMProxyServer(":8443", "2023-06-01", credleasestore.New(), credcache.New(), denylist.New())
	if srv == nil {
		t.Fatal("newLLMProxyServer returned nil for a configured address")
	}
	if srv.Addr != ":8443" {
		t.Errorf("Addr = %q, want :8443", srv.Addr)
	}
}

func TestNewLLMProxyServerRoutesTheMessagesEndpoint(t *testing.T) {
	srv := newLLMProxyServer(":8443", "2023-06-01", credleasestore.New(), credcache.New(), denylist.New())
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
	srv := newLLMProxyServer(":8443", "2023-06-01", credleasestore.New(), credcache.New(), denylist.New())
	req := httptest.NewRequest(http.MethodPost, "/llm-proxy/v1/no-such-endpoint", nil)
	rr := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("POST to an unknown proxy path = %d, want 404", rr.Code)
	}
}

func TestNewLLMProxyServerRejectsNonPost(t *testing.T) {
	srv := newLLMProxyServer(":8443", "2023-06-01", credleasestore.New(), credcache.New(), denylist.New())
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
	emitter := opsevents.NewEmitter(opsevents.NewEventBuffer(0), "replica-test")
	reporter := experimentRejectionReporter{emitter: emitter}

	reporter.ReportExperimentIsolationRejection(context.Background(), sessionserver.ExperimentIsolationRejection{
		TenantID:             "acme",
		UserID:               "alice",
		ExperimentID:         "exp_1",
		VariantID:            "treatment",
		SessionMinIsolation:  "microvm",
		VariantPoolIsolation: "sandboxed",
	})

	page := emitter.Buffer().Query(0, opsevents.EventFilter{
		EventType: string(opsevents.EventExperimentIsolationMismatch),
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
