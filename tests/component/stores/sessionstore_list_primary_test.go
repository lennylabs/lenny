//go:build component

// SPDX-License-Identifier: MIT

package stores_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	sessionpg "github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// TestSessionStoreListPrimaryReadsPastReplicaLag_spec_12_8 builds the
// store over two pools: the primary and, through WithReadPool, a second
// migrated database standing in for a read replica that has not yet
// replicated a freshly inserted row. A List with Primary set returns the
// row and a List without it does not, so the lifecycle drain's listing
// cannot miss a row the creation fence admitted.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 12.3 (Postgres HA Requirements: Read replicas)
// diagnosis: a failure means pgstore.List ignores ListFilter.Primary: either
// the primary listing reads the read pool, so a drain can miss a live session
// the replica has not replicated and report the scope drained, or every
// listing reads the primary, so the §12.3 read routing of session-status
// listings is lost.
func TestSessionStoreListPrimaryReadsPastReplicaLag_spec_12_8(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	primary := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	replica := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	store := sessionpg.New(primary.Pool, sessionpg.WithReadPool(replica.Pool))

	tenant := freshTenant(t, ctx, primary)
	id := newUUID(t)
	other := newUUID(t)
	for _, s := range []sessionstore.Session{
		{ID: id, TenantID: tenant, UserID: "alice", RuntimeRef: "echo", State: session.StateRunning},
		{ID: other, TenantID: tenant, UserID: "bob", RuntimeRef: "echo", State: session.StateRunning},
	} {
		if err := store.Create(ctx, s); err != nil {
			t.Fatalf("Create %s: %v", s.ID, err)
		}
	}

	t.Run("primary listing returns the row the replica lacks", func(t *testing.T) {
		got, err := store.List(ctx, tenant, sessionstore.ListFilter{UserID: "alice", Primary: true})
		if err != nil {
			t.Fatalf("List primary: %v", err)
		}
		if len(got) != 1 || got[0].ID != id || got[0].State != session.StateRunning {
			t.Fatalf("primary listing = %+v, want exactly alice's running row %s", got, id)
		}
	})

	t.Run("read-pool listing does not return the row", func(t *testing.T) {
		got, err := store.List(ctx, tenant, sessionstore.ListFilter{UserID: "alice"})
		if err != nil {
			t.Fatalf("List read pool: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("read-pool listing = %+v, want none (the replica has not seen the row)", got)
		}
	})

	t.Run("primary listing of the whole tenant returns every row", func(t *testing.T) {
		got, err := store.List(ctx, tenant, sessionstore.ListFilter{Primary: true})
		if err != nil {
			t.Fatalf("List primary: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("tenant listing = %d rows, want 2", len(got))
		}
	})
}
