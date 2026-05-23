// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/environment"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/connectorstore"
	"github.com/lennylabs/lenny/pkg/gateway/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// spec: §10.6 / §15.1 GET /v1/admin/environments/{name}/runtime-exposure.

func TestEnvironmentRuntimeExposure(t *testing.T) {
	runtimes := runtimestore.NewMemory()
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "security-scanner", Type: runtimestore.TypeAgent,
		Labels: map[string]string{"team": "security"},
	})
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "research-agent", Type: runtimestore.TypeAgent,
		Labels: map[string]string{"team": "research"},
	})
	connectors := connectorstore.NewMemory()
	_ = connectors.Create(context.Background(), connectorstore.Connector{
		TenantID: "acme", ID: "sec-vault",
		MCPServerURL: "https://sec-vault.example.com",
		Labels:       map[string]string{"team": "security"},
	})
	_ = connectors.Create(context.Background(), connectorstore.Connector{
		TenantID: "acme", ID: "research-db",
		MCPServerURL: "https://research-db.example.com",
		Labels:       map[string]string{"team": "research"},
	})
	envs := environmentstore.NewMemory()
	if err := envs.Create(context.Background(), environmentstore.Environment{
		Name: "security-team", TenantID: "acme",
		RuntimeSelector:   environment.Selector{MatchLabels: map[string]string{"team": "security"}},
		ConnectorSelector: environment.Selector{MatchLabels: map[string]string{"team": "security"}},
	}); err != nil {
		t.Fatalf("seed environment: %v", err)
	}

	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC) },
	}).WithEnvironments(envs).WithRuntimes(runtimes).WithConnectors(connectors)

	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet,
		"/v1/admin/environments/security-team/runtime-exposure?tenantId=acme", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp admin.RuntimeExposurePayload
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Runtimes) != 1 || resp.Runtimes[0].Name != "security-scanner" {
		t.Errorf("runtimes in scope: got %+v, want only security-scanner", resp.Runtimes)
	}
	if resp.Runtimes[0].Type != "agent" {
		t.Errorf("runtime type: got %q, want agent", resp.Runtimes[0].Type)
	}
	if len(resp.Connectors) != 1 || resp.Connectors[0].ID != "sec-vault" {
		t.Errorf("connectors in scope: got %+v, want only sec-vault", resp.Connectors)
	}
}

func TestEnvironmentRuntimeExposureExcludeOverride(t *testing.T) {
	// The §10.6 Exclude override force-rejects a runtime the label
	// selector would otherwise admit.
	runtimes := runtimestore.NewMemory()
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "approved-scanner", Labels: map[string]string{"team": "security"},
	})
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "unstable-scanner", Labels: map[string]string{"team": "security"},
	})
	envs := environmentstore.NewMemory()
	if err := envs.Create(context.Background(), environmentstore.Environment{
		Name: "security-team", TenantID: "acme",
		RuntimeSelector: environment.Selector{
			MatchLabels: map[string]string{"team": "security"},
			Exclude:     []string{"unstable-scanner"},
		},
	}); err != nil {
		t.Fatalf("seed environment: %v", err)
	}

	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC) },
	}).WithEnvironments(envs).WithRuntimes(runtimes).WithConnectors(connectorstore.NewMemory())

	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet,
		"/v1/admin/environments/security-team/runtime-exposure?tenantId=acme", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp admin.RuntimeExposurePayload
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.Runtimes) != 1 || resp.Runtimes[0].Name != "approved-scanner" {
		t.Errorf("Exclude override not applied: got %+v", resp.Runtimes)
	}
}

func TestEnvironmentRuntimeExposureNotFound(t *testing.T) {
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC) },
	}).WithEnvironments(environmentstore.NewMemory()).
		WithRuntimes(runtimestore.NewMemory()).
		WithConnectors(connectorstore.NewMemory())

	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet,
		"/v1/admin/environments/ghost/runtime-exposure?tenantId=acme", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("missing environment: got %d, want 404", rr.Code)
	}
}
