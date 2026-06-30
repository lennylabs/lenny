// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/connectorinvoke"
	"github.com/lennylabs/lenny/pkg/gateway/connectorstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/policy/ratelimit"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/capabilityinference"
)

// fakeRefresher records its call and replays a canned result or error.
type fakeRefresher struct {
	result    connectorinvoke.CapabilityRefreshResult
	err       error
	gotConn   string
	gotUser   string
	callCount int
}

func (f *fakeRefresher) RefreshCapabilities(_ context.Context, _, connectorID, userID, _ string) (connectorinvoke.CapabilityRefreshResult, error) {
	f.callCount++
	f.gotConn = connectorID
	f.gotUser = userID
	return f.result, f.err
}

func newRefreshAdmin(t *testing.T, refresher admin.ConnectorCapabilityRefresher) (*admin.Router, *connectorstore.Memory) {
	t.Helper()
	tenants := tenantstore.NewMemory()
	connectors := connectorstore.NewMemory()
	router := admin.NewRouter(tenants, admin.Options{Clock: clk}).
		WithConnectors(connectors).
		WithConnectorRefresh(refresher, ratelimit.NewMemory())
	if err := connectors.Create(context.Background(), connectorstore.Connector{
		TenantID: "acme", ID: "github", MCPServerURL: "https://mcp.github.example",
		Transport: "streamable_http", Visibility: "tenant",
		CreatedAt: clk(), UpdatedAt: clk(),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return router, connectors
}

// TestRefreshConnectorPlatformAdmin_spec_9_3_136 verifies a platform-admin
// caller drives the refresh and receives the inferred capability block.
func TestRefreshConnectorPlatformAdmin_spec_9_3_136(t *testing.T) {
	refresher := &fakeRefresher{result: connectorinvoke.CapabilityRefreshResult{
		Mode:         capabilityinference.ModeStrict,
		Capabilities: []capabilityinference.Capability{capabilityinference.CapRead, capabilityinference.CapWrite},
		ToolCapabilities: map[string][]capabilityinference.Capability{
			"read_file": {capabilityinference.CapRead},
		},
		UnannotatedAdminTools: []string{"mystery"},
	}}
	router, _ := newRefreshAdmin(t, refresher)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/connectors/github/refresh", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		Connector               string                                      `json:"connector"`
		CapabilityInferenceMode string                                      `json:"capabilityInferenceMode"`
		Capabilities            []string                                    `json:"capabilities"`
		ToolCapabilities        map[string][]capabilityinference.Capability `json:"toolCapabilities"`
		UnannotatedAdminTools   []string                                    `json:"unannotatedAdminTools"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Connector != "github" || body.CapabilityInferenceMode != "strict" {
		t.Errorf("unexpected body: %+v", body)
	}
	if len(body.Capabilities) != 2 || len(body.UnannotatedAdminTools) != 1 {
		t.Errorf("unexpected capabilities: %+v", body)
	}
	if refresher.callCount != 1 || refresher.gotConn != "github" {
		t.Errorf("refresher call = %+v, want one call for github", refresher)
	}
}

// TestRefreshConnectorTenantAdminAllowed_spec_9_3_136 verifies a
// tenant-admin may refresh their own connector.
func TestRefreshConnectorTenantAdminAllowed_spec_9_3_136(t *testing.T) {
	router, _ := newRefreshAdmin(t, &fakeRefresher{})
	req := withTenantAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/connectors/github/refresh", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("tenant-admin status = %d, body=%s", rr.Code, rr.Body.String())
	}
}

// TestRefreshConnectorPlainUserForbidden_spec_9_3_136 verifies a plain
// user is rejected — the endpoint requires platform-admin or
// tenant-admin like the live test.
func TestRefreshConnectorPlainUserForbidden_spec_9_3_136(t *testing.T) {
	router, _ := newRefreshAdmin(t, &fakeRefresher{})
	req := withUserPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/connectors/github/refresh", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("plain user status = %d, want 403", rr.Code)
	}
}

// TestRefreshConnectorNotFound_spec_9_3_136 verifies an unknown connector
// returns 404 without calling the refresher.
func TestRefreshConnectorNotFound_spec_9_3_136(t *testing.T) {
	refresher := &fakeRefresher{}
	router, _ := newRefreshAdmin(t, refresher)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/connectors/ghost/refresh", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("unknown connector status = %d, want 404", rr.Code)
	}
	if refresher.callCount != 0 {
		t.Errorf("refresher called %d times for unknown connector, want 0", refresher.callCount)
	}
}

// TestRefreshConnectorUnreachable_spec_9_3_136 verifies a refresher error
// (the external endpoint is unreachable) surfaces as 502.
func TestRefreshConnectorUnreachable_spec_9_3_136(t *testing.T) {
	router, _ := newRefreshAdmin(t, &fakeRefresher{err: errors.New("dial timeout")})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/connectors/github/refresh", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadGateway {
		t.Errorf("unreachable status = %d, want 502", rr.Code)
	}
}

// TestRefreshConnectorRateLimited_spec_9_3_136 verifies the 11th refresh
// of a connector within a minute is rejected with 429.
func TestRefreshConnectorRateLimited_spec_9_3_136(t *testing.T) {
	router, _ := newRefreshAdmin(t, &fakeRefresher{})
	for i := 0; i < 10; i++ {
		req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/connectors/github/refresh", nil))
		rr := httptest.NewRecorder()
		router.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d status = %d, want 200", i+1, rr.Code)
		}
	}
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/connectors/github/refresh", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("11th request status = %d, want 429", rr.Code)
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Error("429 response missing Retry-After header")
	}
}
