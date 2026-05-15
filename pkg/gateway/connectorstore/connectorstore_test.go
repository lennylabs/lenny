// SPDX-License-Identifier: MIT

package connectorstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/connectorstore"
)

func validConnector() connectorstore.Connector {
	return connectorstore.Connector{
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
	got, err := s.Get(context.Background(), "github")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.DisplayName != "GitHub" {
		t.Errorf("DisplayName: %q", got.DisplayName)
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
	row, _ := s.Get(context.Background(), "github")
	prev := row.UpdatedAt
	updated, err := s.Update(context.Background(), "github", func(c *connectorstore.Connector) error {
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
	_, err := s.Update(context.Background(), "github", func(c *connectorstore.Connector) error {
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
	_ = s.SoftDelete(context.Background(), "github", time.Now())
	rows, _ := s.List(context.Background(), connectorstore.ListFilter{})
	if len(rows) != 0 {
		t.Errorf("default List should exclude deleted: %+v", rows)
	}
	all, _ := s.List(context.Background(), connectorstore.ListFilter{IncludeDeleted: true})
	if len(all) != 1 {
		t.Errorf("includeDeleted: %d", len(all))
	}
}

func TestGetMissing(t *testing.T) {
	s := connectorstore.NewMemory()
	if _, err := s.Get(context.Background(), "x"); !errors.Is(err, connectorstore.ErrNotFound) {
		t.Errorf("Get missing: %v", err)
	}
}
