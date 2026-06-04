// SPDX-License-Identifier: MIT

package connectorstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/connectorstore"
)

// TestConnectorVersionRoundTrip_spec_15_1_1207 covers the §15.1 lines
// 1207-1213 optimistic-concurrency version: it starts at 1 on Create and
// increments on every successful Update and SoftDelete.
func TestConnectorVersionRoundTrip_spec_15_1_1207(t *testing.T) {
	ctx := context.Background()
	m := connectorstore.NewMemory()
	c := connectorstore.Connector{
		TenantID:     "acme",
		ID:           "github",
		MCPServerURL: "https://mcp.github.com",
		Transport:    "streamable_http",
	}
	if err := m.Create(ctx, c); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := m.Get(ctx, "acme", "github")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Version != 1 {
		t.Fatalf("create version = %d, want 1", got.Version)
	}

	updated, err := m.Update(ctx, "acme", "github", func(c *connectorstore.Connector) error {
		c.DisplayName = "GitHub"
		return nil
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Version != 2 {
		t.Errorf("update version = %d, want 2", updated.Version)
	}

	if err := m.SoftDelete(ctx, "acme", "github", time.Now()); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	got, _ = m.Get(ctx, "acme", "github")
	if got.Version != 3 {
		t.Errorf("soft-delete version = %d, want 3", got.Version)
	}
}
