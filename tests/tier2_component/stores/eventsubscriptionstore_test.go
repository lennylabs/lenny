//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §25.5 webhook subscription registry,
// exercising the Postgres-backed pkg/ops/eventsubscription/pgstore
// against a real container with the production migrations applied.
// Covers Create + Get round-trip, the types-array JSONB shape, List
// ordering, the typed NotFound error, idempotent Delete, and the
// no-RLS / platform-scoped contract the table establishes (lenny-ops
// is not tenant-scoped at the §25 boundary).
package stores_test

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/eventsubscription"
	subspg "github.com/lennylabs/lenny/pkg/ops/eventsubscription/pgstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// startSubsStore brings up a Postgres container with the production
// migrations and returns the eventsubscription pgstore plus the raw
// handle. The eventsubscription table is platform-scoped (no tenant
// column), so this helper does not seed a tenant row.
func startSubsStore(t *testing.T) (*subspg.Store, *containers.Postgres) {
	t.Helper()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	idCounter := 0
	idFn := func() string {
		idCounter++
		// Deterministic ids so List ordering assertions are stable.
		return "sub_" + time.Now().UTC().Format("20060102150405") + "_" +
			itoa(idCounter)
	}
	store := subspg.New(pg.Pool, subspg.WithIDFunc(idFn))
	return store, pg
}

func itoa(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}

// spec: 25.5
// diagnosis: the Postgres-backed §25.5 webhook subscription registry
// in pkg/ops/eventsubscription/pgstore did not behave as specified.
// Create must round-trip a row, Get must return ErrCodeNotFound on a
// missing id, List must order by id, Delete must be idempotent on a
// missing id (returning NotFound), and the types array must survive
// the JSONB round trip.
func TestEventSubscriptionStoreContract(t *testing.T) {
	t.Parallel()
	store, _ := startSubsStore(t)
	ctx := context.Background()

	t.Run("create and get round-trip", func(t *testing.T) {
		req := eventsubscription.CreateRequest{
			CallbackURL: "https://acme.example/webhooks/lenny",
			Types:       []string{"session.completed", "session.failed"},
			Secret:      "shhh",
		}
		// The Service normalizes types; the store also tolerates an
		// unsorted slice at the API level, but the spec calls for the
		// Service to do the sort. Pre-sort here so the round-trip
		// assertion is exact.
		sort.Strings(req.Types)
		created, err := store.Create(ctx, req)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if created.ID == "" {
			t.Errorf("Create returned empty id")
		}
		if created.CreatedAt.IsZero() {
			t.Errorf("Create returned zero CreatedAt")
		}
		got, err := store.Get(ctx, created.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.CallbackURL != req.CallbackURL {
			t.Errorf("CallbackURL: got %q, want %q", got.CallbackURL, req.CallbackURL)
		}
		if len(got.Types) != 2 || got.Types[0] != "session.completed" {
			t.Errorf("types not preserved: %+v", got.Types)
		}
		if got.Secret != req.Secret {
			t.Errorf("Secret: got %q, want %q", got.Secret, req.Secret)
		}
	})

	t.Run("get returns NotFound for missing id", func(t *testing.T) {
		_, err := store.Get(ctx, "sub_missing")
		if err == nil {
			t.Fatal("Get missing: expected error")
		}
		if code := eventsubscription.CodeOf(err); code != eventsubscription.ErrCodeNotFound {
			t.Errorf("Get missing code: got %q, want %q", code, eventsubscription.ErrCodeNotFound)
		}
	})

	t.Run("list returns rows in id order", func(t *testing.T) {
		store, _ := startSubsStore(t)
		a, _ := store.Create(ctx, eventsubscription.CreateRequest{
			CallbackURL: "https://acme.example/a", Types: []string{"a"},
		})
		b, _ := store.Create(ctx, eventsubscription.CreateRequest{
			CallbackURL: "https://acme.example/b", Types: []string{"b"},
		})
		out, err := store.List(ctx)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(out) != 2 {
			t.Fatalf("List len: got %d, want 2", len(out))
		}
		ids := []string{out[0].ID, out[1].ID}
		want := []string{a.ID, b.ID}
		sort.Strings(want)
		if !equalStrings(ids, want) {
			t.Errorf("List order: got %v, want %v", ids, want)
		}
	})

	t.Run("delete is followed by NotFound", func(t *testing.T) {
		created, err := store.Create(ctx, eventsubscription.CreateRequest{
			CallbackURL: "https://acme.example/del", Types: []string{"x"},
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if err := store.Delete(ctx, created.ID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := store.Get(ctx, created.ID); !isNotFound(err) {
			t.Errorf("Get after Delete: got %v, want NotFound", err)
		}
		if err := store.Delete(ctx, created.ID); !isNotFound(err) {
			t.Errorf("Delete after Delete: got %v, want NotFound", err)
		}
	})

	t.Run("empty types array round-trips as empty slice", func(t *testing.T) {
		created, err := store.Create(ctx, eventsubscription.CreateRequest{
			CallbackURL: "https://acme.example/empty",
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		got, err := store.Get(ctx, created.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if len(got.Types) != 0 {
			t.Errorf("empty types: got %v, want nil/empty", got.Types)
		}
	})
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	var typed *eventsubscription.Error
	if errors.As(err, &typed) {
		return typed.Code == eventsubscription.ErrCodeNotFound
	}
	return false
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
