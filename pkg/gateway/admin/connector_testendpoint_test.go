// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/connectorcredstore"
	"github.com/lennylabs/lenny/pkg/gateway/connectorinvoke"
	"github.com/lennylabs/lenny/pkg/gateway/connectorstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/policy/ratelimit"
)

// fakeTester records the connector and bearer it was handed and replays
// a canned report.
type fakeTester struct {
	report    connectorinvoke.TestReport
	gotBearer string
	gotConn   string
	callCount int
}

func (f *fakeTester) Test(_ context.Context, conn connectorstore.Connector, bearer string) connectorinvoke.TestReport {
	f.callCount++
	f.gotBearer = bearer
	f.gotConn = conn.ID
	rep := f.report
	rep.Connector = conn.ID
	return rep
}

func clk() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

func newTestEndpointAdmin(t *testing.T) (*admin.Router, *connectorstore.Memory, *connectorcredstore.Memory, *fakeTester) {
	t.Helper()
	tenants := tenantstore.NewMemory()
	connectors := connectorstore.NewMemory()
	creds := connectorcredstore.NewMemory(clk)
	tester := &fakeTester{report: connectorinvoke.TestReport{
		Overall: connectorinvoke.StagePassed,
		Stages: []connectorinvoke.TestStage{
			{Name: connectorinvoke.StageDNS, Status: connectorinvoke.StagePassed, LatencyMs: 5},
			{Name: connectorinvoke.StageTLS, Status: connectorinvoke.StagePassed, LatencyMs: 9},
			{Name: connectorinvoke.StageMCP, Status: connectorinvoke.StagePassed, LatencyMs: 20},
			{Name: connectorinvoke.StageAuth, Status: connectorinvoke.StagePassed, LatencyMs: 3},
		},
	}}
	router := admin.NewRouter(tenants, admin.Options{Clock: clk}).
		WithConnectors(connectors).
		WithConnectorTest(tester, creds, ratelimit.NewMemory())
	// Seed an active connector under tenant acme with an OAuth block.
	if err := connectors.Create(context.Background(), connectorstore.Connector{
		TenantID: "acme", ID: "github", MCPServerURL: "https://mcp.github.example",
		Transport: "streamable_http", Visibility: "tenant",
		Auth: &connectorstore.ConnectorAuth{
			Type: "oauth2", AuthorizationEndpoint: "https://gh/authorize",
			TokenEndpoint: "https://gh/token", ClientID: "c1", ClientSecretRef: "ns/s",
		},
		CreatedAt: clk(), UpdatedAt: clk(),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return router, connectors, creds, tester
}

// TestTestConnectorPlatformAdmin_spec_15_1_791 verifies the endpoint
// returns the stage report for a platform-admin caller.
func TestTestConnectorPlatformAdmin_spec_15_1_791(t *testing.T) {
	router, _, _, tester := newTestEndpointAdmin(t)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/connectors/github/test", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var rep connectorinvoke.TestReport
	if err := json.Unmarshal(rr.Body.Bytes(), &rep); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rep.Connector != "github" || rep.Overall != connectorinvoke.StagePassed || len(rep.Stages) != 4 {
		t.Errorf("unexpected report: %+v", rep)
	}
	if tester.callCount != 1 {
		t.Errorf("tester called %d times, want 1", tester.callCount)
	}
}

// TestTestConnectorTenantAdminAllowed_spec_15_1_1163 verifies a
// tenant-admin may run the test against their own connector.
func TestTestConnectorTenantAdminAllowed_spec_15_1_1163(t *testing.T) {
	router, _, _, _ := newTestEndpointAdmin(t)
	req := withTenantAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/connectors/github/test", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("tenant-admin status = %d, body=%s", rr.Code, rr.Body.String())
	}
}

// TestTestConnectorPlainUserForbidden_spec_15_1_1163 verifies a plain
// user is rejected — the endpoint requires platform-admin or
// tenant-admin.
func TestTestConnectorPlainUserForbidden_spec_15_1_1163(t *testing.T) {
	router, _, _, _ := newTestEndpointAdmin(t)
	req := withUserPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/connectors/github/test", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("plain user status = %d, want 403", rr.Code)
	}
}

// TestTestConnectorNotFound_spec_15_1_791 verifies an unknown connector
// id returns 404.
func TestTestConnectorNotFound_spec_15_1_791(t *testing.T) {
	router, _, _, _ := newTestEndpointAdmin(t)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/connectors/ghost/test", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("unknown connector status = %d, want 404", rr.Code)
	}
}

// TestTestConnectorUsesStoredCredential_spec_15_1_1180 verifies the test
// carries the caller's stored credential as the bearer and never an
// inline override.
func TestTestConnectorUsesStoredCredential_spec_15_1_1180(t *testing.T) {
	router, _, creds, tester := newTestEndpointAdmin(t)
	if err := creds.Put(context.Background(), connectorcredstore.ConnectorCredential{
		TenantID: "acme", ConnectorID: "github", UserID: "admin@acme.com", Environment: "",
		AccessToken: "stored-tok", TokenType: "Bearer", CreatedAt: clk(),
	}); err != nil {
		t.Fatalf("put cred: %v", err)
	}
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/connectors/github/test", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if tester.gotBearer != "stored-tok" {
		t.Errorf("bearer = %q, want stored-tok", tester.gotBearer)
	}
}

// TestTestConnectorRateLimited_spec_15_1_1180 verifies the 11th test
// against a connector within a minute is rejected with 429.
func TestTestConnectorRateLimited_spec_15_1_1180(t *testing.T) {
	router, _, _, _ := newTestEndpointAdmin(t)
	for i := 0; i < 10; i++ {
		req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/connectors/github/test", nil))
		rr := httptest.NewRecorder()
		router.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d status = %d, want 200", i+1, rr.Code)
		}
	}
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/connectors/github/test", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("11th request status = %d, want 429", rr.Code)
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Error("429 response missing Retry-After header")
	}
}
