//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §9.3 connector registry, exercising the
// Postgres-backed pkg/gateway/connectorstore/pgstore against a real
// container. Covers CRUD, the §9.3 SSRF / inline-secret validation,
// the soft-delete lifecycle, and the IncludeDeleted list filter.
package stores_test

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/connectorstore"
	connectorpg "github.com/lennylabs/lenny/pkg/gateway/connectorstore/pgstore"
)

func connectorID(t *testing.T) string {
	t.Helper()
	return "conn-" + newUUID(t)[:8]
}

// spec: 9.3
// diagnosis: the Postgres-backed connector registry in
// pkg/gateway/connectorstore/pgstore did not behave as specified.
// Create and Get must round-trip a connector, the §9.3 validation must
// reject SSRF-prone http:// endpoints and inline client secrets,
// Update must re-validate and advance updated_at, and the soft-delete
// lifecycle must honor the IncludeDeleted list filter.
func TestConnectorStoreContract(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := connectorpg.New(pg.Pool)
	ctx := context.Background()

	t.Run("create and get round-trip", func(t *testing.T) {
		want := connectorstore.Connector{
			ID:           connectorID(t),
			DisplayName:  "GitHub",
			MCPServerURL: "https://mcp.github.com",
			Transport:    "streamable_http",
			Visibility:   "tenant",
			Auth: &connectorstore.ConnectorAuth{
				Type:                  "oauth2",
				AuthorizationEndpoint: "https://github.com/login/oauth/authorize",
				TokenEndpoint:         "https://github.com/login/oauth/access_token",
				ClientID:              "client-abc",
				ClientSecretRef:       "lenny-secrets/github-oauth",
				Scopes:                []string{"repo", "read:user"},
			},
			Labels: map[string]string{"env": "prod", "team": "platform"},
		}
		if err := store.Create(ctx, want); err != nil {
			t.Fatalf("Create: %v", err)
		}
		got, err := store.Get(ctx, want.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.DisplayName != want.DisplayName || got.MCPServerURL != want.MCPServerURL ||
			got.Transport != want.Transport || got.Visibility != want.Visibility {
			t.Errorf("scalar mismatch:\n got %+v\nwant %+v", got, want)
		}
		if !maps.Equal(got.Labels, want.Labels) {
			t.Errorf("Labels mismatch: got %v want %v", got.Labels, want.Labels)
		}
		if got.Auth == nil {
			t.Fatal("Auth lost in round-trip")
		}
		if got.Auth.Type != want.Auth.Type || got.Auth.ClientID != want.Auth.ClientID ||
			got.Auth.ClientSecretRef != want.Auth.ClientSecretRef ||
			!slices.Equal(got.Auth.Scopes, want.Auth.Scopes) {
			t.Errorf("Auth mismatch:\n got %+v\nwant %+v", got.Auth, want.Auth)
		}
		if !got.IsActive() {
			t.Error("freshly created connector reports inactive")
		}
	})

	t.Run("duplicate, invalid id, and SSRF URLs are rejected", func(t *testing.T) {
		c := connectorstore.Connector{
			ID: connectorID(t), MCPServerURL: "https://mcp.example.com",
			Transport: "streamable_http",
		}
		if err := store.Create(ctx, c); err != nil {
			t.Fatalf("first Create: %v", err)
		}
		if err := store.Create(ctx, c); !errors.Is(err, connectorstore.ErrAlreadyExists) {
			t.Errorf("duplicate Create: got %v, want ErrAlreadyExists", err)
		}
		if err := store.Create(ctx, connectorstore.Connector{
			ID: "Invalid ID", MCPServerURL: "https://mcp.example.com",
		}); err == nil {
			t.Error("Create with an invalid id should be rejected")
		}
		// §9.3 security: an http:// endpoint must be rejected so a
		// connector cannot become an SSRF pivot.
		err := store.Create(ctx, connectorstore.Connector{
			ID: connectorID(t), MCPServerURL: "http://internal.metadata.local",
		})
		if err == nil || !strings.Contains(err.Error(), "https") {
			t.Errorf("Create with an http:// URL should be rejected for SSRF, got %v", err)
		}
		// §9.3 security: the client secret must be a reference, never inline.
		if err := store.Create(ctx, connectorstore.Connector{
			ID: connectorID(t), MCPServerURL: "https://mcp.example.com",
			Auth: &connectorstore.ConnectorAuth{Type: "oauth2", ClientSecretRef: "rawsecretvalue"},
		}); err == nil {
			t.Error("Create with an inline client secret should be rejected")
		}
	})

	t.Run("get missing returns ErrNotFound", func(t *testing.T) {
		if _, err := store.Get(ctx, connectorID(t)); !errors.Is(err, connectorstore.ErrNotFound) {
			t.Errorf("Get missing: got %v, want ErrNotFound", err)
		}
	})

	t.Run("update mutates, advances updated_at, and re-validates", func(t *testing.T) {
		id := connectorID(t)
		if err := store.Create(ctx, connectorstore.Connector{
			ID: id, MCPServerURL: "https://mcp.example.com", Transport: "streamable_http",
		}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		before, _ := store.Get(ctx, id)
		updated, err := store.Update(ctx, id, func(c *connectorstore.Connector) error {
			c.DisplayName = "Renamed"
			return nil
		})
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if updated.DisplayName != "Renamed" {
			t.Errorf("Update result not applied: %+v", updated)
		}
		if !updated.UpdatedAt.After(before.UpdatedAt) {
			t.Errorf("UpdatedAt did not advance: before=%v after=%v", before.UpdatedAt, updated.UpdatedAt)
		}
		// A mutation to an http:// URL must be rejected by Validate.
		if _, err := store.Update(ctx, id, func(c *connectorstore.Connector) error {
			c.MCPServerURL = "http://evil.local"
			return nil
		}); err == nil {
			t.Error("Update to an http:// URL should be rejected")
		}
		if _, err := store.Update(ctx, connectorID(t), func(*connectorstore.Connector) error {
			return nil
		}); !errors.Is(err, connectorstore.ErrNotFound) {
			t.Errorf("Update missing: got %v, want ErrNotFound", err)
		}
	})

	t.Run("list ordering, delete, and idempotency", func(t *testing.T) {
		marker := newUUID(t)[:8]
		ids := []string{marker + "-a", marker + "-b", marker + "-c"}
		for _, id := range ids {
			if err := store.Create(ctx, connectorstore.Connector{
				ID: id, MCPServerURL: "https://mcp.example.com", Transport: "streamable_http",
			}); err != nil {
				t.Fatalf("Create %s: %v", id, err)
			}
		}
		marked := func(filter connectorstore.ListFilter) []connectorstore.Connector {
			t.Helper()
			all, err := store.List(ctx, filter)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			var out []connectorstore.Connector
			for _, c := range all {
				if strings.HasPrefix(c.ID, marker) {
					out = append(out, c)
				}
			}
			return out
		}
		all := marked(connectorstore.ListFilter{})
		if len(all) != 3 || all[0].ID != ids[0] || all[2].ID != ids[2] {
			t.Fatalf("List: not id-ascending or wrong count: %d", len(all))
		}

		if err := store.SoftDelete(ctx, ids[0], time.Now().UTC()); err != nil {
			t.Fatalf("SoftDelete: %v", err)
		}
		if err := store.SoftDelete(ctx, ids[0], time.Now().UTC()); err != nil {
			t.Errorf("idempotent SoftDelete: %v", err)
		}
		if err := store.SoftDelete(ctx, connectorID(t), time.Now().UTC()); !errors.Is(err, connectorstore.ErrNotFound) {
			t.Errorf("SoftDelete missing: got %v, want ErrNotFound", err)
		}
		if n := len(marked(connectorstore.ListFilter{})); n != 2 {
			t.Errorf("List default after delete: %d marked, want 2", n)
		}
		if n := len(marked(connectorstore.ListFilter{IncludeDeleted: true})); n != 3 {
			t.Errorf("List IncludeDeleted after delete: %d marked, want 3", n)
		}
		deleted, err := store.Get(ctx, ids[0])
		if err != nil || deleted.IsActive() {
			t.Errorf("Get soft-deleted: %+v err=%v; want inactive", deleted, err)
		}
	})
}
