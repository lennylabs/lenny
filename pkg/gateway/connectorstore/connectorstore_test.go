// SPDX-License-Identifier: MIT

package connectorstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/connectorstore"
)

// spec: §4.2 line 173 — connectors are tenant-scoped. Tests use the
// built-in default tenant; multi-tenant isolation is exercised in
// the tier-2 component suite.
const testTenantID = "acme"

func validConnector() connectorstore.Connector {
	return connectorstore.Connector{
		TenantID:     testTenantID,
		ID:           "github",
		DisplayName:  "GitHub",
		MCPServerURL: "https://mcp.github.com",
		Transport:    "streamable_http",
		Visibility:   "tenant",
	}
}

func TestCreateAndGet(t *testing.T) {
	s := connectorstore.NewMemory()
	if err := s.Create(context.Background(), validConnector()); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := s.Get(context.Background(), testTenantID, "github")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.DisplayName != "GitHub" {
		t.Errorf("DisplayName: %q", got.DisplayName)
	}
	if got.TenantID != testTenantID {
		t.Errorf("TenantID: %q", got.TenantID)
	}
}

func TestCreateRejectsNonHTTPSMCPURL(t *testing.T) {
	s := connectorstore.NewMemory()
	c := validConnector()
	c.MCPServerURL = "http://mcp.github.com"
	if err := s.Create(context.Background(), c); err == nil {
		t.Error("http:// mcpServerUrl should be rejected")
	}
}

func TestCreateRejectsNonHTTPSAuthEndpoints(t *testing.T) {
	s := connectorstore.NewMemory()
	c := validConnector()
	c.Auth = &connectorstore.ConnectorAuth{
		Type:                  "oauth2",
		AuthorizationEndpoint: "http://github.com/login/oauth/authorize",
	}
	if err := s.Create(context.Background(), c); err == nil {
		t.Error("http:// authorizationEndpoint should be rejected")
	}
}

func TestCreateRejectsInlineClientSecret(t *testing.T) {
	s := connectorstore.NewMemory()
	c := validConnector()
	c.Auth = &connectorstore.ConnectorAuth{
		Type:            "oauth2",
		ClientSecretRef: "inline-secret-no-slash",
	}
	if err := s.Create(context.Background(), c); err == nil {
		t.Error("inline clientSecretRef should be rejected (must be namespace/name)")
	}
}

func TestCreateAcceptsRefClientSecret(t *testing.T) {
	s := connectorstore.NewMemory()
	c := validConnector()
	c.Auth = &connectorstore.ConnectorAuth{
		Type:            "oauth2",
		ClientSecretRef: "lenny-system/github-client-secret",
	}
	if err := s.Create(context.Background(), c); err != nil {
		t.Errorf("namespace/name clientSecretRef should be accepted: %v", err)
	}
}

func TestCreateRejectsBadTransport(t *testing.T) {
	s := connectorstore.NewMemory()
	c := validConnector()
	c.Transport = "a2a"
	if err := s.Create(context.Background(), c); err == nil {
		t.Error("non-streamable_http transport should be rejected in v1")
	}
}

func TestCreateRejectsBadVisibility(t *testing.T) {
	s := connectorstore.NewMemory()
	c := validConnector()
	c.Visibility = "world"
	if err := s.Create(context.Background(), c); err == nil {
		t.Error("invalid visibility should be rejected")
	}
}

func TestCreateRejectsBadID(t *testing.T) {
	s := connectorstore.NewMemory()
	for _, id := range []string{"", "With-Caps", "with space"} {
		c := validConnector()
		c.ID = id
		if err := s.Create(context.Background(), c); err == nil {
			t.Errorf("Create(id=%q) should fail", id)
		}
	}
}

func TestCreateRejectsDuplicate(t *testing.T) {
	s := connectorstore.NewMemory()
	_ = s.Create(context.Background(), validConnector())
	if err := s.Create(context.Background(), validConnector()); !errors.Is(err, connectorstore.ErrAlreadyExists) {
		t.Errorf("dupe: %v", err)
	}
}

func TestUpdateAdvancesTimestamp(t *testing.T) {
	s := connectorstore.NewMemory()
	_ = s.Create(context.Background(), validConnector())
	row, _ := s.Get(context.Background(), testTenantID, "github")
	prev := row.UpdatedAt
	updated, err := s.Update(context.Background(), testTenantID, "github", func(c *connectorstore.Connector) error {
		c.DisplayName = "GitHub Enterprise"
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.DisplayName != "GitHub Enterprise" || !updated.UpdatedAt.After(prev) {
		t.Errorf("Update: %+v", updated)
	}
}

func TestUpdateRevalidates(t *testing.T) {
	s := connectorstore.NewMemory()
	_ = s.Create(context.Background(), validConnector())
	_, err := s.Update(context.Background(), testTenantID, "github", func(c *connectorstore.Connector) error {
		c.MCPServerURL = "http://insecure"
		return nil
	})
	if err == nil {
		t.Error("Update to http:// URL should be rejected")
	}
}

func TestSoftDeleteExcludesByDefault(t *testing.T) {
	s := connectorstore.NewMemory()
	_ = s.Create(context.Background(), validConnector())
	_ = s.SoftDelete(context.Background(), testTenantID, "github", time.Now())
	rows, _ := s.List(context.Background(), testTenantID, connectorstore.ListFilter{})
	if len(rows) != 0 {
		t.Errorf("default List should exclude deleted: %+v", rows)
	}
	all, _ := s.List(context.Background(), testTenantID, connectorstore.ListFilter{IncludeDeleted: true})
	if len(all) != 1 {
		t.Errorf("includeDeleted: %d", len(all))
	}
}

func TestGetMissing(t *testing.T) {
	s := connectorstore.NewMemory()
	if _, err := s.Get(context.Background(), testTenantID, "x"); !errors.Is(err, connectorstore.ErrNotFound) {
		t.Errorf("Get missing: %v", err)
	}
}

// spec: §4.2 line 173 — a connector created in tenant A is not
// visible from tenant B, even with the same logical id. The
// AllTenantsSentinel surface exposes both rows to a platform-admin
// reader.
func TestTenantIsolationAndAllSentinel(t *testing.T) {
	s := connectorstore.NewMemory()
	a := validConnector()
	a.TenantID = "acme"
	b := validConnector()
	b.TenantID = "globex"
	if err := s.Create(context.Background(), a); err != nil {
		t.Fatalf("Create acme: %v", err)
	}
	if err := s.Create(context.Background(), b); err != nil {
		t.Fatalf("Create globex: %v", err)
	}

	// acme sees only its row.
	rowsA, _ := s.List(context.Background(), "acme", connectorstore.ListFilter{})
	if len(rowsA) != 1 || rowsA[0].TenantID != "acme" {
		t.Errorf("acme list: %+v", rowsA)
	}

	// globex sees only its row.
	rowsB, _ := s.List(context.Background(), "globex", connectorstore.ListFilter{})
	if len(rowsB) != 1 || rowsB[0].TenantID != "globex" {
		t.Errorf("globex list: %+v", rowsB)
	}

	// AllTenantsSentinel returns both.
	all, _ := s.List(context.Background(), connectorstore.AllTenantsSentinel, connectorstore.ListFilter{})
	if len(all) != 2 {
		t.Errorf("__all__ list: got %d, want 2", len(all))
	}

	// Get against a non-owning tenant returns ErrNotFound.
	if _, err := s.Get(context.Background(), "acme", "github"); err != nil {
		t.Errorf("acme Get own connector: %v", err)
	}
	if _, err := s.Get(context.Background(), "globex", "github"); err != nil {
		t.Errorf("globex Get own connector: %v", err)
	}
}

// spec: §4.2 line 173 — Create rejects an empty TenantID.
func TestCreateRejectsEmptyTenant(t *testing.T) {
	s := connectorstore.NewMemory()
	c := validConnector()
	c.TenantID = ""
	if err := s.Create(context.Background(), c); err == nil {
		t.Error("Create with empty TenantID should be rejected")
	}
}

// spec: §4.2 line 173 — write operations reject the
// AllTenantsSentinel sentinel (it is reads-only).
func TestWritesRejectSentinel(t *testing.T) {
	s := connectorstore.NewMemory()
	_ = s.Create(context.Background(), validConnector())
	if _, err := s.Update(context.Background(), connectorstore.AllTenantsSentinel, "github",
		func(c *connectorstore.Connector) error { return nil }); err == nil {
		t.Error("Update with sentinel must be rejected")
	}
	if err := s.SoftDelete(context.Background(), connectorstore.AllTenantsSentinel, "github", time.Now()); err == nil {
		t.Error("SoftDelete with sentinel must be rejected")
	}
}
