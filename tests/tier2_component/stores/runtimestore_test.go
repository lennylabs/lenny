//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §5.1 runtime registry, exercising the
// Postgres-backed pkg/gateway/runtimestore/pgstore against a real
// container. Covers CRUD, name validation, the soft-delete lifecycle,
// and the IncludeDeleted / Type list filters.
package stores_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	runtimepg "github.com/lennylabs/lenny/pkg/gateway/runtimestore/pgstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

func runtimeName(t *testing.T) string {
	t.Helper()
	return "rt-" + newUUID(t)[:8]
}

func sampleRuntime(name string) runtimestore.Runtime {
	return runtimestore.Runtime{
		Name:             name,
		Type:             runtimestore.TypeAgent,
		Image:            "ghcr.io/acme/echo@sha256:" + strings.Repeat("a", 64),
		ExecutionMode:    runtimestore.ExecutionModeSession,
		IsolationProfile: isolation.ProfileStandard,
		IntegrationLevel: runtimestore.IntegrationLevelBasic,
		Description:      "echo reference runtime",
	}
}

func TestRuntimeStoreContract(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := runtimepg.New(pg.Pool)
	ctx := context.Background()

	t.Run("create and get round-trip", func(t *testing.T) {
		want := sampleRuntime(runtimeName(t))
		if err := store.Create(ctx, want); err != nil {
			t.Fatalf("Create: %v", err)
		}
		got, err := store.Get(ctx, want.Name)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Type != want.Type || got.Image != want.Image ||
			got.ExecutionMode != want.ExecutionMode || got.IsolationProfile != want.IsolationProfile ||
			got.IntegrationLevel != want.IntegrationLevel || got.Description != want.Description {
			t.Errorf("field mismatch:\n got %+v\nwant %+v", got, want)
		}
		if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
			t.Error("Create did not default the audit timestamps")
		}
		if !got.IsActive() {
			t.Error("freshly created runtime reports inactive")
		}
	})

	t.Run("duplicate and invalid names are rejected", func(t *testing.T) {
		r := sampleRuntime(runtimeName(t))
		if err := store.Create(ctx, r); err != nil {
			t.Fatalf("first Create: %v", err)
		}
		if err := store.Create(ctx, r); !errors.Is(err, runtimestore.ErrAlreadyExists) {
			t.Errorf("duplicate Create: got %v, want ErrAlreadyExists", err)
		}
		bad := sampleRuntime("Invalid Name")
		if err := store.Create(ctx, bad); err == nil {
			t.Error("Create with an invalid runtime name should be rejected")
		}
	})

	t.Run("get missing returns ErrNotFound", func(t *testing.T) {
		if _, err := store.Get(ctx, runtimeName(t)); !errors.Is(err, runtimestore.ErrNotFound) {
			t.Errorf("Get missing: got %v, want ErrNotFound", err)
		}
	})

	t.Run("update mutates and advances updated_at", func(t *testing.T) {
		r := sampleRuntime(runtimeName(t))
		if err := store.Create(ctx, r); err != nil {
			t.Fatalf("Create: %v", err)
		}
		before, _ := store.Get(ctx, r.Name)
		updated, err := store.Update(ctx, r.Name, func(rt *runtimestore.Runtime) error {
			rt.Image = "ghcr.io/acme/echo@sha256:" + strings.Repeat("b", 64)
			rt.IntegrationLevel = runtimestore.IntegrationLevelFull
			return nil
		})
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if updated.IntegrationLevel != runtimestore.IntegrationLevelFull {
			t.Errorf("Update result not applied: %+v", updated)
		}
		if !updated.UpdatedAt.After(before.UpdatedAt) {
			t.Errorf("UpdatedAt did not advance: before=%v after=%v", before.UpdatedAt, updated.UpdatedAt)
		}
		if _, err := store.Update(ctx, runtimeName(t), func(*runtimestore.Runtime) error {
			return nil
		}); !errors.Is(err, runtimestore.ErrNotFound) {
			t.Errorf("Update missing: got %v, want ErrNotFound", err)
		}
	})

	t.Run("list applies type, delete, and ordering", func(t *testing.T) {
		marker := newUUID(t)[:8]
		names := []string{marker + "-a", marker + "-b", marker + "-c"}
		for i, name := range names {
			r := sampleRuntime(name)
			if i == 2 {
				r.Type = runtimestore.TypeMCP
			}
			if err := store.Create(ctx, r); err != nil {
				t.Fatalf("Create %s: %v", name, err)
			}
		}
		marked := func(filter runtimestore.ListFilter) []runtimestore.Runtime {
			t.Helper()
			all, err := store.List(ctx, filter)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			var out []runtimestore.Runtime
			for _, rt := range all {
				if strings.HasPrefix(rt.Name, marker) {
					out = append(out, rt)
				}
			}
			return out
		}
		all := marked(runtimestore.ListFilter{})
		if len(all) != 3 {
			t.Fatalf("List: %d marked runtimes, want 3", len(all))
		}
		if all[0].Name != names[0] || all[2].Name != names[2] {
			t.Errorf("List not name-ascending: %s..%s", all[0].Name, all[2].Name)
		}
		if mcp := marked(runtimestore.ListFilter{Type: runtimestore.TypeMCP}); len(mcp) != 1 {
			t.Errorf("List Type=mcp: %d marked, want 1", len(mcp))
		}

		if err := store.SoftDelete(ctx, names[0], time.Now().UTC()); err != nil {
			t.Fatalf("SoftDelete: %v", err)
		}
		if err := store.SoftDelete(ctx, names[0], time.Now().UTC()); err != nil {
			t.Errorf("idempotent SoftDelete: %v", err)
		}
		if err := store.SoftDelete(ctx, runtimeName(t), time.Now().UTC()); !errors.Is(err, runtimestore.ErrNotFound) {
			t.Errorf("SoftDelete missing: got %v, want ErrNotFound", err)
		}
		if n := len(marked(runtimestore.ListFilter{})); n != 2 {
			t.Errorf("List default after delete: %d marked, want 2", n)
		}
		if n := len(marked(runtimestore.ListFilter{IncludeDeleted: true})); n != 3 {
			t.Errorf("List IncludeDeleted after delete: %d marked, want 3", n)
		}
		deleted, err := store.Get(ctx, names[0])
		if err != nil || deleted.IsActive() {
			t.Errorf("Get soft-deleted: %+v err=%v; want inactive", deleted, err)
		}
	})
}
