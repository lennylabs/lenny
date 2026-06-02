// SPDX-License-Identifier: MIT

package connectorstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/capabilityinference"
	"github.com/lennylabs/lenny/pkg/gateway/connectorstore"
)

// TestCapabilityMetadataRoundTrip_spec_9_3_136 verifies the §5.1
// capability fields survive a Create/Get and that an Update mutating them
// persists the new values — the storage half of the F-9.3.8 capability
// inference.
func TestCapabilityMetadataRoundTrip_spec_9_3_136(t *testing.T) {
	s := connectorstore.NewMemory()
	c := validConnector()
	c.CapabilityInferenceMode = capabilityinference.ModePermissive
	if err := s.Create(context.Background(), c); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := s.Get(context.Background(), testTenantID, "github")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.CapabilityInferenceMode != capabilityinference.ModePermissive {
		t.Errorf("mode = %q, want permissive", got.CapabilityInferenceMode)
	}

	refreshed := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	updated, err := s.Update(context.Background(), testTenantID, "github", func(cn *connectorstore.Connector) error {
		cn.Capabilities = []capabilityinference.Capability{capabilityinference.CapRead, capabilityinference.CapWrite}
		cn.ToolCapabilities = map[string][]capabilityinference.Capability{
			"read_file": {capabilityinference.CapRead},
		}
		cn.CapabilitiesRefreshedAt = refreshed
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(updated.Capabilities) != 2 {
		t.Errorf("capabilities = %v, want [read write]", updated.Capabilities)
	}
	reread, err := s.Get(context.Background(), testTenantID, "github")
	if err != nil {
		t.Fatalf("re-Get: %v", err)
	}
	if got := reread.ToolCapabilities["read_file"]; len(got) != 1 || got[0] != capabilityinference.CapRead {
		t.Errorf("persisted read_file caps = %v, want [read]", got)
	}
	if !reread.CapabilitiesRefreshedAt.Equal(refreshed) {
		t.Errorf("refreshedAt = %v, want %v", reread.CapabilitiesRefreshedAt, refreshed)
	}
}

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

// validOAuth2 returns an oauth2 auth block populated with every
// §9.3 line 116-130 required field so a test can attach it to a
// validConnector() and only mutate the field under inspection.
func validOAuth2() *connectorstore.ConnectorAuth {
	return &connectorstore.ConnectorAuth{
		Type:                  "oauth2",
		AuthorizationEndpoint: "https://github.com/login/oauth/authorize",
		TokenEndpoint:         "https://github.com/login/oauth/access_token",
		ClientID:              "client-abc",
		ClientSecretRef:       "lenny-system/github-client-secret",
	}
}

func TestCreateRejectsNonHTTPSAuthEndpoints(t *testing.T) {
	s := connectorstore.NewMemory()
	c := validConnector()
	c.Auth = validOAuth2()
	c.Auth.AuthorizationEndpoint = "http://github.com/login/oauth/authorize"
	if err := s.Create(context.Background(), c); err == nil {
		t.Error("http:// authorizationEndpoint should be rejected")
	}
}

func TestCreateRejectsInlineClientSecret(t *testing.T) {
	s := connectorstore.NewMemory()
	c := validConnector()
	c.Auth = validOAuth2()
	c.Auth.ClientSecretRef = "inline-secret-no-slash"
	if err := s.Create(context.Background(), c); err == nil {
		t.Error("inline clientSecretRef should be rejected (must be namespace/name)")
	}
}

func TestCreateAcceptsRefClientSecret(t *testing.T) {
	s := connectorstore.NewMemory()
	c := validConnector()
	c.Auth = validOAuth2()
	if err := s.Create(context.Background(), c); err != nil {
		t.Errorf("namespace/name clientSecretRef should be accepted: %v", err)
	}
}

// spec: §9.3 line 116-130 — the oauth2 example block declares
// authorizationEndpoint, tokenEndpoint, and clientId as mandatory.
// F-9.3.6 — every field is required at registration time.
func TestCreateRejectsIncompleteOAuth2_spec_9_3_116(t *testing.T) {
	for _, tc := range []struct {
		name string
		drop func(*connectorstore.ConnectorAuth)
	}{
		{"missing authorizationEndpoint", func(a *connectorstore.ConnectorAuth) { a.AuthorizationEndpoint = "" }},
		{"missing tokenEndpoint", func(a *connectorstore.ConnectorAuth) { a.TokenEndpoint = "" }},
		{"missing clientId", func(a *connectorstore.ConnectorAuth) { a.ClientID = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := connectorstore.NewMemory()
			c := validConnector()
			c.Auth = validOAuth2()
			tc.drop(c.Auth)
			if err := s.Create(context.Background(), c); err == nil {
				t.Errorf("%s: incomplete oauth2 should be rejected", tc.name)
			}
		})
	}
}

// spec: §9.3 line 140 — all connectors must be registered before
// they can be used. An empty mcpServerUrl admits a connector that
// cannot be dialed. F-9.3.10 — registration must reject the empty
// value before the registry advertises the row.
func TestCreateRejectsEmptyMCPServerURL_spec_9_3_140(t *testing.T) {
	s := connectorstore.NewMemory()
	c := validConnector()
	c.MCPServerURL = ""
	if err := s.Create(context.Background(), c); err == nil {
		t.Error("empty mcpServerUrl should be rejected at registration")
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

// spec: §12.1 line 5 — DeleteByUser is mandatory on the Store
// interface; connectors are tenant-scoped so the call is a no-op that
// returns 0 erased rows.
func TestDeleteByUserIsNoOp_spec_12_1(t *testing.T) {
	s := connectorstore.NewMemory()
	_ = s.Create(context.Background(), validConnector())
	n, err := s.DeleteByUser(context.Background(), "acme", "alice")
	if err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}
	if n != 0 {
		t.Errorf("DeleteByUser must return 0 erased rows for tenant-scoped store, got %d", n)
	}
	// The connector survives.
	if _, err := s.Get(context.Background(), testTenantID, "github"); err != nil {
		t.Errorf("connector should survive DeleteByUser: %v", err)
	}
}

// spec: §12.1 line 5 / §12.8 Phase 4 — DeleteByTenant removes every
// connector belonging to the tenant.
func TestDeleteByTenantRemovesAll_spec_12_1(t *testing.T) {
	s := connectorstore.NewMemory()
	a := validConnector()
	a.TenantID = "acme"
	b := validConnector()
	b.TenantID = "globex"
	_ = s.Create(context.Background(), a)
	_ = s.Create(context.Background(), b)
	n, err := s.DeleteByTenant(context.Background(), "acme")
	if err != nil {
		t.Fatalf("DeleteByTenant: %v", err)
	}
	if n != 1 {
		t.Errorf("DeleteByTenant should remove 1 row, got %d", n)
	}
	// Acme connector gone; globex untouched.
	if _, err := s.Get(context.Background(), "acme", "github"); !errors.Is(err, connectorstore.ErrNotFound) {
		t.Errorf("acme connector should be gone: %v", err)
	}
	if _, err := s.Get(context.Background(), "globex", "github"); err != nil {
		t.Errorf("globex connector should survive: %v", err)
	}
}
