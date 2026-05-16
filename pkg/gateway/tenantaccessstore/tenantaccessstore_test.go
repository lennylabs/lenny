// SPDX-License-Identifier: MIT

package tenantaccessstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/tenantaccessstore"
)

// spec: §4 runtime/pool tenant-access join tables.

func TestGrantAndList(t *testing.T) {
	ctx := context.Background()
	store := tenantaccessstore.NewMemory()
	at := time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)

	created, err := store.Grant(ctx, tenantaccessstore.KindRuntime, "claude-code", "acme", "admin@platform", at)
	if err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if !created {
		t.Error("first Grant must report created=true")
	}
	grants, err := store.List(ctx, tenantaccessstore.KindRuntime, "claude-code")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(grants) != 1 {
		t.Fatalf("List: got %d grants, want 1", len(grants))
	}
	if grants[0].TenantID != "acme" || grants[0].GrantedBy != "admin@platform" {
		t.Errorf("grant = %+v, want acme / admin@platform", grants[0])
	}
	if !grants[0].GrantedAt.Equal(at) {
		t.Errorf("GrantedAt = %v, want %v", grants[0].GrantedAt, at)
	}
}

func TestGrantIsIdempotent(t *testing.T) {
	ctx := context.Background()
	store := tenantaccessstore.NewMemory()
	if _, err := store.Grant(ctx, tenantaccessstore.KindPool, "pool-a", "acme", "admin", time.Time{}); err != nil {
		t.Fatalf("first Grant: %v", err)
	}
	created, err := store.Grant(ctx, tenantaccessstore.KindPool, "pool-a", "acme", "admin", time.Time{})
	if err != nil {
		t.Fatalf("second Grant: %v", err)
	}
	if created {
		t.Error("re-granting an existing grant must report created=false")
	}
	grants, _ := store.List(ctx, tenantaccessstore.KindPool, "pool-a")
	if len(grants) != 1 {
		t.Errorf("a re-grant must not duplicate the entry: got %d", len(grants))
	}
}

func TestGrantsAreScopedByKindAndResource(t *testing.T) {
	ctx := context.Background()
	store := tenantaccessstore.NewMemory()
	// The same name under runtime and pool kinds is independent.
	if _, err := store.Grant(ctx, tenantaccessstore.KindRuntime, "shared-name", "acme", "a", time.Time{}); err != nil {
		t.Fatalf("Grant runtime: %v", err)
	}
	if _, err := store.Grant(ctx, tenantaccessstore.KindPool, "shared-name", "globex", "a", time.Time{}); err != nil {
		t.Fatalf("Grant pool: %v", err)
	}
	rt, _ := store.List(ctx, tenantaccessstore.KindRuntime, "shared-name")
	if len(rt) != 1 || rt[0].TenantID != "acme" {
		t.Errorf("runtime grants = %+v, want only acme", rt)
	}
	pool, _ := store.List(ctx, tenantaccessstore.KindPool, "shared-name")
	if len(pool) != 1 || pool[0].TenantID != "globex" {
		t.Errorf("pool grants = %+v, want only globex", pool)
	}
}

func TestRevoke(t *testing.T) {
	ctx := context.Background()
	store := tenantaccessstore.NewMemory()
	if _, err := store.Grant(ctx, tenantaccessstore.KindRuntime, "rt", "acme", "a", time.Time{}); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if err := store.Revoke(ctx, tenantaccessstore.KindRuntime, "rt", "acme"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	grants, _ := store.List(ctx, tenantaccessstore.KindRuntime, "rt")
	if len(grants) != 0 {
		t.Errorf("after Revoke: got %d grants, want 0", len(grants))
	}
}

func TestRevokeMissing(t *testing.T) {
	store := tenantaccessstore.NewMemory()
	err := store.Revoke(context.Background(), tenantaccessstore.KindRuntime, "rt", "ghost")
	if !errors.Is(err, tenantaccessstore.ErrNotFound) {
		t.Errorf("Revoke missing grant: got %v, want ErrNotFound", err)
	}
}

func TestInvalidArguments(t *testing.T) {
	ctx := context.Background()
	store := tenantaccessstore.NewMemory()
	if _, err := store.Grant(ctx, "bogus", "rt", "acme", "a", time.Time{}); !errors.Is(err, tenantaccessstore.ErrInvalidArg) {
		t.Errorf("Grant with a bad kind: got %v, want ErrInvalidArg", err)
	}
	if _, err := store.Grant(ctx, tenantaccessstore.KindRuntime, "", "acme", "a", time.Time{}); !errors.Is(err, tenantaccessstore.ErrInvalidArg) {
		t.Errorf("Grant with an empty resource: got %v, want ErrInvalidArg", err)
	}
	if _, err := store.Grant(ctx, tenantaccessstore.KindRuntime, "rt", "", "a", time.Time{}); !errors.Is(err, tenantaccessstore.ErrInvalidArg) {
		t.Errorf("Grant with an empty tenant: got %v, want ErrInvalidArg", err)
	}
}

func TestListUnknownResourceIsEmpty(t *testing.T) {
	grants, err := tenantaccessstore.NewMemory().List(context.Background(), tenantaccessstore.KindPool, "ghost")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(grants) != 0 {
		t.Errorf("unknown resource: got %d grants, want 0", len(grants))
	}
}
