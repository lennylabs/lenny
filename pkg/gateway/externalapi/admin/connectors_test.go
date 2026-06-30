// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/connectorstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
)

func newConnectorAdmin(t *testing.T) (*admin.Router, *connectorstore.Memory, *recordingAudit) {
	t.Helper()
	tenants := tenantstore.NewMemory()
	connectors := connectorstore.NewMemory()
	audit := &recordingAudit{}
	router := admin.NewRouter(tenants, admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Audit: audit,
	}).WithConnectors(connectors)
	return router, connectors, audit
}

func connReq(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		buf = bytes.NewReader(b)
	} else {
		buf = bytes.NewReader(nil)
	}
	req := withAdminPrincipal(httptest.NewRequest(method, path, buf))
	// spec: §15.1 lines 1207-1211 — the connector PUT enforces If-Match.
	// Cases that do not exercise the precondition directly reach the handler
	// by carrying the resource's current ETag, fetched via a GET against the
	// same resource. A test driving the precondition sets If-Match itself.
	if method == http.MethodPut && req.Header.Get("If-Match") == "" {
		if getPath := adminETagGetPath(path); getPath != "" {
			g := withAdminPrincipal(httptest.NewRequest(http.MethodGet, getPath, nil))
			grr := httptest.NewRecorder()
			h.ServeHTTP(grr, g)
			if etag := grr.Header().Get("ETag"); etag != "" {
				req.Header.Set("If-Match", etag)
			}
		}
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestCreateConnectorHappyPath(t *testing.T) {
	router, store, audit := newConnectorAdmin(t)
	// platform-admin creates the connector under tenant `acme`.
	rr := connReq(t, router.Handler(), http.MethodPost, "/v1/admin/connectors", admin.ConnectorPayload{
		TenantID:     "acme",
		ID:           "github",
		DisplayName:  "GitHub",
		MCPServerURL: "https://mcp.github.com",
		Auth: &admin.ConnectorAuthPayload{
			Type:                  "oauth2",
			AuthorizationEndpoint: "https://github.com/login/oauth/authorize",
			TokenEndpoint:         "https://github.com/login/oauth/access_token",
			ClientID:              "client-abc",
			ClientSecretRef:       "lenny-system/github-client-secret",
			Scopes:                []string{"repo"},
		},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	row, err := store.Get(context.Background(), "acme", "github")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if row.Transport != "streamable_http" {
		t.Errorf("default transport: %q", row.Transport)
	}
	if row.TenantID != "acme" {
		t.Errorf("TenantID: %q, want acme", row.TenantID)
	}
	if len(audit.snapshot()) != 1 || audit.snapshot()[0].Type != "admin.connector.created" {
		t.Errorf("audit: %+v", audit.snapshot())
	}
}

func TestCreateConnectorRejectsHTTPURL(t *testing.T) {
	router, _, _ := newConnectorAdmin(t)
	rr := connReq(t, router.Handler(), http.MethodPost, "/v1/admin/connectors", admin.ConnectorPayload{
		TenantID:     "acme",
		ID:           "evil",
		MCPServerURL: "http://internal-metadata-server",
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("http:// URL: got %d, want 400", rr.Code)
	}
}

func TestCreateConnectorRejectsInlineSecret(t *testing.T) {
	router, _, _ := newConnectorAdmin(t)
	rr := connReq(t, router.Handler(), http.MethodPost, "/v1/admin/connectors", admin.ConnectorPayload{
		TenantID:     "acme",
		ID:           "github",
		MCPServerURL: "https://mcp.github.com",
		Auth: &admin.ConnectorAuthPayload{
			Type:                  "oauth2",
			AuthorizationEndpoint: "https://github.com/login/oauth/authorize",
			TokenEndpoint:         "https://github.com/login/oauth/access_token",
			ClientID:              "client-abc",
			ClientSecretRef:       "raw-inline-secret",
		},
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("inline secret: got %d, want 400", rr.Code)
	}
}

// spec: §9.3 line 116-130 — admin Create rejects oauth2 blocks
// missing authorizationEndpoint, tokenEndpoint, or clientId.
// F-9.3.6 — validator surfaces the failure at registration time
// instead of CONNECTOR_OAUTH_INCOMPLETE at authorize time.
func TestCreateConnectorRejectsIncompleteOAuth2_spec_9_3_116(t *testing.T) {
	router, _, _ := newConnectorAdmin(t)
	rr := connReq(t, router.Handler(), http.MethodPost, "/v1/admin/connectors", admin.ConnectorPayload{
		TenantID:     "acme",
		ID:           "github",
		MCPServerURL: "https://mcp.github.com",
		Auth: &admin.ConnectorAuthPayload{
			Type:            "oauth2",
			ClientSecretRef: "lenny-system/github-client-secret",
		},
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("incomplete oauth2 block: got %d, want 400 (body=%s)", rr.Code, rr.Body.String())
	}
}

// spec: §9.3 line 140 — every connector must be registered with a
// dialable MCP endpoint. F-9.3.10 — admin Create rejects an empty
// mcpServerUrl at registration.
func TestCreateConnectorRejectsEmptyMCPServerURL_spec_9_3_140(t *testing.T) {
	router, _, _ := newConnectorAdmin(t)
	rr := connReq(t, router.Handler(), http.MethodPost, "/v1/admin/connectors", admin.ConnectorPayload{
		TenantID: "acme",
		ID:       "github",
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("missing mcpServerUrl: got %d, want 400 (body=%s)", rr.Code, rr.Body.String())
	}
}

func TestListConnectors(t *testing.T) {
	router, store, _ := newConnectorAdmin(t)
	_ = store.Create(context.Background(), connectorstore.Connector{
		TenantID: "acme", ID: "github", MCPServerURL: "https://mcp.github.com", Transport: "streamable_http",
	})
	rr := connReq(t, router.Handler(), http.MethodGet, "/v1/admin/connectors", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var resp struct {
		Connectors []admin.ConnectorPayload `json:"items"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.Connectors) != 1 {
		t.Errorf("List: %+v", resp.Connectors)
	}
}

func TestUpdateConnector(t *testing.T) {
	router, store, _ := newConnectorAdmin(t)
	_ = store.Create(context.Background(), connectorstore.Connector{
		TenantID: "acme", ID: "github", MCPServerURL: "https://mcp.github.com", Transport: "streamable_http",
	})
	rr := connReq(t, router.Handler(), http.MethodPut, "/v1/admin/connectors/github?tenant_id=acme",
		admin.ConnectorPayload{DisplayName: "GitHub Enterprise"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(context.Background(), "acme", "github")
	if row.DisplayName != "GitHub Enterprise" {
		t.Errorf("DisplayName: %q", row.DisplayName)
	}
}

func TestDeleteConnector(t *testing.T) {
	router, store, audit := newConnectorAdmin(t)
	_ = store.Create(context.Background(), connectorstore.Connector{
		TenantID: "acme", ID: "github", MCPServerURL: "https://mcp.github.com", Transport: "streamable_http",
	})
	rr := connReq(t, router.Handler(), http.MethodDelete, "/v1/admin/connectors/github?tenant_id=acme", nil)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status: %d", rr.Code)
	}
	row, _ := store.Get(context.Background(), "acme", "github")
	if row.DeletedAt.IsZero() {
		t.Errorf("DeletedAt should be set")
	}
	if len(audit.snapshot()) != 1 || audit.snapshot()[0].Type != "admin.connector.soft_deleted" {
		t.Errorf("audit: %+v", audit.snapshot())
	}
}

func TestGetConnectorMissing(t *testing.T) {
	router, _, _ := newConnectorAdmin(t)
	rr := connReq(t, router.Handler(), http.MethodGet, "/v1/admin/connectors/missing", nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestConnectorEndpointsRequirePlatformAdmin(t *testing.T) {
	router, store, _ := newConnectorAdmin(t)
	_ = store.Create(context.Background(), connectorstore.Connector{
		ID: "github", MCPServerURL: "https://mcp.github.com", Transport: "streamable_http",
	})
	for _, c := range []struct {
		method, path string
	}{
		{http.MethodPost, "/v1/admin/connectors"},
		{http.MethodGet, "/v1/admin/connectors"},
		{http.MethodGet, "/v1/admin/connectors/github"},
		{http.MethodPut, "/v1/admin/connectors/github"},
		{http.MethodDelete, "/v1/admin/connectors/github"},
	} {
		req := withTenantAdminPrincipal(httptest.NewRequest(c.method, c.path, bytes.NewReader([]byte("{}"))))
		rr := httptest.NewRecorder()
		router.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Errorf("%s %s: got %d, want 403", c.method, c.path, rr.Code)
		}
	}
}
