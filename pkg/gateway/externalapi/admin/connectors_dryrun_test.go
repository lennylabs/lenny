// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/connectorstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
)

// spec: §15.1 line 1140 — ?dryRun=true validates without persisting or
// auditing, returns the computed resource, and sets X-Dry-Run: true.

func TestCreateConnectorDryRun_spec_15_1(t *testing.T) {
	router, store, audit := newConnectorAdmin(t)
	rr := connReq(t, router.Handler(), http.MethodPost, "/v1/admin/connectors?dryRun=true", admin.ConnectorPayload{
		TenantID:     "acme",
		ID:           "github",
		DisplayName:  "GitHub",
		MCPServerURL: "https://mcp.github.com",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("X-Dry-Run"); got != "true" {
		t.Errorf("X-Dry-Run = %q, want true", got)
	}
	var resp admin.ConnectorPayload
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ID != "github" || resp.Transport != "streamable_http" {
		t.Errorf("computed resource: %+v", resp)
	}
	// No persistence: the connector was not created.
	if _, err := store.Get(context.Background(), "acme", "github"); err == nil {
		t.Error("dry-run create must not persist the connector")
	}
	if len(audit.snapshot()) != 0 {
		t.Errorf("dry-run must not emit audit events: %+v", audit.snapshot())
	}
}

func TestUpdateConnectorDryRun_spec_15_1(t *testing.T) {
	router, store, audit := newConnectorAdmin(t)
	if err := store.Create(context.Background(), connectorstore.Connector{
		TenantID: "acme", ID: "github", MCPServerURL: "https://mcp.github.com",
		Transport: "streamable_http", DisplayName: "original",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rr := connReq(t, router.Handler(), http.MethodPut, "/v1/admin/connectors/github?dryRun=true&tenant_id=acme",
		admin.ConnectorPayload{DisplayName: "GitHub Enterprise"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("X-Dry-Run"); got != "true" {
		t.Errorf("X-Dry-Run = %q, want true", got)
	}
	var resp admin.ConnectorPayload
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.DisplayName != "GitHub Enterprise" {
		t.Errorf("preview displayName = %q, want the merged value", resp.DisplayName)
	}
	// The preview merges onto the current record: mcpServerUrl is preserved.
	if resp.MCPServerURL != "https://mcp.github.com" {
		t.Errorf("preview mcpServerUrl = %q, want the stored value", resp.MCPServerURL)
	}
	// No persistence: the stored connector is unchanged.
	row, _ := store.Get(context.Background(), "acme", "github")
	if row.DisplayName != "original" {
		t.Errorf("dry-run update must not persist: stored displayName = %q", row.DisplayName)
	}
	if len(audit.snapshot()) != 0 {
		t.Errorf("dry-run must not emit audit events: %+v", audit.snapshot())
	}
}

// Validation still runs under dryRun: an http:// URL returns 400.
func TestCreateConnectorDryRunValidates_spec_15_1(t *testing.T) {
	router, _, _ := newConnectorAdmin(t)
	rr := connReq(t, router.Handler(), http.MethodPost, "/v1/admin/connectors?dryRun=true", admin.ConnectorPayload{
		TenantID:     "acme",
		ID:           "evil",
		MCPServerURL: "http://internal-metadata-server",
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("dry-run with an invalid body: status %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}
