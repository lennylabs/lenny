// SPDX-License-Identifier: MIT

package credentialpoolstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/credentialpoolstore"
)

// spec: §4.9 CredentialPool registry.

func samplePool(tenant, name string) credentialpoolstore.CredentialPool {
	return credentialpoolstore.CredentialPool{
		TenantID: tenant,
		Name:     name,
		Provider: "anthropic_direct",
		Credentials: []credentialpoolstore.Credential{
			{ID: "key-1", SecretRef: "lenny-system/anthropic-key-1"},
			{ID: "key-2", SecretRef: "lenny-system/anthropic-key-2"},
		},
		AssignmentStrategy:         "least-loaded",
		MaxConcurrentSessions:      10,
		CooldownOnRateLimitSeconds: 60,
	}
}

func TestCreateAndGet(t *testing.T) {
	ctx := context.Background()
	store := credentialpoolstore.NewMemory()
	if err := store.Create(ctx, samplePool("acme", "claude-direct-prod")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := store.Get(ctx, "acme", "claude-direct-prod")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Provider != "anthropic_direct" || len(got.Credentials) != 2 {
		t.Errorf("got %+v, want provider=anthropic_direct with 2 credentials", got)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Error("Create must stamp CreatedAt and UpdatedAt")
	}
}

func TestGetIsTenantScoped(t *testing.T) {
	ctx := context.Background()
	store := credentialpoolstore.NewMemory()
	if err := store.Create(ctx, samplePool("acme", "pool-1")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// The same pool name under a different tenant must not resolve.
	if _, err := store.Get(ctx, "globex", "pool-1"); !errors.Is(err, credentialpoolstore.ErrNotFound) {
		t.Errorf("cross-tenant Get: got %v, want ErrNotFound", err)
	}
}

func TestCreateRejectsDuplicate(t *testing.T) {
	ctx := context.Background()
	store := credentialpoolstore.NewMemory()
	if err := store.Create(ctx, samplePool("acme", "pool-1")); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	if err := store.Create(ctx, samplePool("acme", "pool-1")); !errors.Is(err, credentialpoolstore.ErrAlreadyExists) {
		t.Errorf("duplicate Create: got %v, want ErrAlreadyExists", err)
	}
	// The same name under another tenant is not a duplicate.
	if err := store.Create(ctx, samplePool("globex", "pool-1")); err != nil {
		t.Errorf("same name, different tenant: got %v, want nil", err)
	}
}

func TestCreateRejectsInvalidInput(t *testing.T) {
	ctx := context.Background()
	store := credentialpoolstore.NewMemory()
	cases := map[string]func(*credentialpoolstore.CredentialPool){
		"empty tenant":          func(p *credentialpoolstore.CredentialPool) { p.TenantID = "" },
		"invalid name":          func(p *credentialpoolstore.CredentialPool) { p.Name = "Bad Name" },
		"empty provider":        func(p *credentialpoolstore.CredentialPool) { p.Provider = "" },
		"negative concurrency":  func(p *credentialpoolstore.CredentialPool) { p.MaxConcurrentSessions = -1 },
		"credential missing id": func(p *credentialpoolstore.CredentialPool) { p.Credentials[0].ID = "" },
		"duplicate credential id": func(p *credentialpoolstore.CredentialPool) {
			p.Credentials[1].ID = p.Credentials[0].ID
		},
	}
	for name, mutate := range cases {
		p := samplePool("acme", "pool-1")
		mutate(&p)
		if err := store.Create(ctx, p); err == nil {
			t.Errorf("%s: expected a validation error", name)
		}
	}
}

func TestUpdateMutatesPool(t *testing.T) {
	ctx := context.Background()
	store := credentialpoolstore.NewMemory()
	if err := store.Create(ctx, samplePool("acme", "pool-1")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	updated, err := store.Update(ctx, "acme", "pool-1", func(p *credentialpoolstore.CredentialPool) error {
		p.MaxConcurrentSessions = 25
		p.Credentials = append(p.Credentials, credentialpoolstore.Credential{
			ID: "key-3", SecretRef: "lenny-system/anthropic-key-3",
		})
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.MaxConcurrentSessions != 25 || len(updated.Credentials) != 3 {
		t.Errorf("updated = %+v, want maxConcurrentSessions=25 with 3 credentials", updated)
	}
	if !updated.UpdatedAt.After(updated.CreatedAt) {
		t.Error("Update must advance UpdatedAt past CreatedAt")
	}
}

func TestUpdateRejectsInvalidMutation(t *testing.T) {
	ctx := context.Background()
	store := credentialpoolstore.NewMemory()
	if err := store.Create(ctx, samplePool("acme", "pool-1")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err := store.Update(ctx, "acme", "pool-1", func(p *credentialpoolstore.CredentialPool) error {
		p.Provider = "" // a pool must keep a provider
		return nil
	})
	if err == nil {
		t.Error("Update producing an invalid pool must be rejected")
	}
}

func TestUpdateMissing(t *testing.T) {
	store := credentialpoolstore.NewMemory()
	_, err := store.Update(context.Background(), "acme", "ghost", func(*credentialpoolstore.CredentialPool) error {
		return nil
	})
	if !errors.Is(err, credentialpoolstore.ErrNotFound) {
		t.Errorf("Update missing pool: got %v, want ErrNotFound", err)
	}
}

func TestListIsTenantScopedAndExcludesDeleted(t *testing.T) {
	ctx := context.Background()
	store := credentialpoolstore.NewMemory()
	if err := store.Create(ctx, samplePool("acme", "pool-a")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.Create(ctx, samplePool("acme", "pool-b")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.Create(ctx, samplePool("globex", "pool-c")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.SoftDelete(ctx, "acme", "pool-b", time.Now().UTC()); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}

	active, _ := store.List(ctx, "acme", credentialpoolstore.ListFilter{})
	if len(active) != 1 || active[0].Name != "pool-a" {
		t.Errorf("List acme: got %+v, want only pool-a", active)
	}
	all, _ := store.List(ctx, "acme", credentialpoolstore.ListFilter{IncludeDeleted: true})
	if len(all) != 2 {
		t.Errorf("List acme includeDeleted: got %d pools, want 2", len(all))
	}
	globex, _ := store.List(ctx, "globex", credentialpoolstore.ListFilter{})
	if len(globex) != 1 || globex[0].Name != "pool-c" {
		t.Errorf("List globex: got %+v, want only pool-c", globex)
	}
}

func TestSoftDeleteMissing(t *testing.T) {
	store := credentialpoolstore.NewMemory()
	if err := store.SoftDelete(context.Background(), "acme", "ghost", time.Now()); !errors.Is(err, credentialpoolstore.ErrNotFound) {
		t.Errorf("SoftDelete missing: got %v, want ErrNotFound", err)
	}
}

func TestStoredPoolIsolatedFromCallerMutation(t *testing.T) {
	ctx := context.Background()
	store := credentialpoolstore.NewMemory()
	if err := store.Create(ctx, samplePool("acme", "pool-1")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, _ := store.Get(ctx, "acme", "pool-1")
	got.Credentials[0].SecretRef = "tampered"

	fresh, _ := store.Get(ctx, "acme", "pool-1")
	if fresh.Credentials[0].SecretRef != "lenny-system/anthropic-key-1" {
		t.Errorf("stored pool was mutated through a returned copy: %q", fresh.Credentials[0].SecretRef)
	}
}
