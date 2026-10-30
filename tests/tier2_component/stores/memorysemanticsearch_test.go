//go:build component

// SPDX-License-Identifier: MIT

// Component test for the §9.4 pgvector semantic search in the
// Postgres-backed MemoryStore. It exercises pkg/gateway/memorystore/
// pgstore against a real pgvector-enabled Postgres with the production
// migrations applied (including migration 0044, which adds the
// agent_memory.embedding column and the ivfflat index). Covers the
// embedding round-trip, the vector-distance ranking of query results,
// the substring fallback for rows with no embedding, and the §12.3
// RLS / tenant-guard path the vector query inherits.
package stores_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/session/memorystore"
	memorypg "github.com/lennylabs/lenny/pkg/gateway/session/memorystore/pgstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// spec: 9.4, 12.3
// diagnosis: the §9.4 pgvector semantic search in
// pkg/gateway/memorystore/pgstore did not behave as specified. Write
// must persist the content embedding into the migration-0044
// agent_memory.embedding column, Query must rank the rows that match
// the query string by pgvector cosine distance to the embedded query,
// and the vector query must still run inside the §12.3 tenant-guard
// transaction.
func TestMemoryPgvectorSemanticSearch(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := memorypg.New(pg.Pool)
	ctx := context.Background()

	t.Run("the pgvector extension and embedding column exist", func(t *testing.T) {
		var hasExt bool
		if err := pg.Pool.QueryRow(
			ctx,
			`SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'vector')`,
		).Scan(&hasExt); err != nil {
			t.Fatalf("check vector extension: %v", err)
		}
		if !hasExt {
			t.Fatal("migration 0044 did not install the pgvector extension")
		}
		var udt string
		if err := pg.Pool.QueryRow(
			ctx, `
			SELECT udt_name FROM information_schema.columns
			WHERE table_name = 'agent_memory' AND column_name = 'embedding'`,
		).Scan(&udt); err != nil {
			t.Fatalf("check embedding column: %v", err)
		}
		if udt != "vector" {
			t.Errorf("agent_memory.embedding type = %q, want vector", udt)
		}
	})

	t.Run("write persists the embedding into the vector column", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		scope := memorystore.MemoryScope{TenantID: tenant, UserID: "alice"}
		if err := store.Write(ctx, scope, []memorystore.Memory{
			{Content: "the deploy key lives in vault"},
		}); err != nil {
			t.Fatalf("Write: %v", err)
		}
		// Read the column back directly — it must be a non-null vector
		// of the declared width.
		var dims int
		if err := execTenantQuery(ctx, pg, tenant,
			`SELECT vector_dims(embedding) FROM agent_memory
			 WHERE tenant_id = $1 AND user_id = 'alice'`, &dims, tenant); err != nil {
			t.Fatalf("read embedding dims: %v", err)
		}
		if dims != memorystore.EmbeddingDim {
			t.Errorf("stored embedding width = %d, want %d", dims, memorystore.EmbeddingDim)
		}
		// Query reads the embedding back into the Memory value.
		got, err := store.Query(ctx, scope, "deploy", 0)
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		if len(got) != 1 || len(got[0].Embedding) != memorystore.EmbeddingDim {
			t.Errorf("Query did not round-trip the embedding: %+v", got)
		}
	})

	t.Run("query ranks substring matches by vector distance", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		scope := memorystore.MemoryScope{TenantID: tenant, UserID: "alice"}
		base := time.Date(2026, 5, 19, 8, 0, 0, 0, time.UTC)
		// Both rows contain the substring "deploy key vault". The
		// first row's content is exactly the query phrase, so its
		// embedding equals the query embedding (cosine distance 0);
		// the second carries extra tokens and is farther. The first
		// row is the older write, so a recency-only ordering would
		// rank it last — the pgvector `embedding <=> query` ordering
		// must override that.
		if err := store.Write(ctx, scope, []memorystore.Memory{
			{Content: "deploy key vault", CreatedAt: base},
			{Content: "deploy key vault rotation schedule audit review log", CreatedAt: base.Add(time.Hour)},
		}); err != nil {
			t.Fatalf("Write: %v", err)
		}
		got, err := store.Query(ctx, scope, "deploy key vault", 0)
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("Query returned %d, want 2", len(got))
		}
		if got[0].Content != "deploy key vault" {
			t.Errorf("vector ranking = %v, want the exact-match row first despite being the older write",
				memoryContentsOf(got))
		}
	})

	t.Run("a row with no embedding falls back to the substring match", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		scope := memorystore.MemoryScope{TenantID: tenant, UserID: "alice"}
		if err := store.Write(ctx, scope, []memorystore.Memory{
			{Content: "legacy memory with no embedding"},
		}); err != nil {
			t.Fatalf("Write: %v", err)
		}
		// Simulate a row written before migration 0044 by nulling the
		// embedding column directly.
		if err := execTenantExec(ctx, pg, tenant,
			`UPDATE agent_memory SET embedding = NULL
			 WHERE tenant_id = $1 AND user_id = 'alice'`, tenant); err != nil {
			t.Fatalf("null the embedding: %v", err)
		}
		got, err := store.Query(ctx, scope, "legacy", 0)
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		if len(got) != 1 || got[0].Content != "legacy memory with no embedding" {
			t.Errorf("substring fallback Query = %v, want the legacy row", memoryContentsOf(got))
		}
		if got[0].Embedding != nil {
			t.Errorf("a NULL-embedding row should read back with a nil Embedding, got %v", got[0].Embedding)
		}
	})

	t.Run("the ivfflat embedding index is present", func(t *testing.T) {
		var hasIdx bool
		if err := pg.Pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM pg_indexes
				WHERE tablename = 'agent_memory' AND indexname = 'agent_memory_embedding_idx'
			)`).Scan(&hasIdx); err != nil {
			t.Fatalf("check embedding index: %v", err)
		}
		if !hasIdx {
			t.Error("migration 0044 did not create agent_memory_embedding_idx")
		}
	})
}

// execTenantQuery runs a single-row query inside a transaction that has
// set app.current_tenant, scanning the one result into dest. The §12.3
// RLS policy filters agent_memory by app.current_tenant, so a direct
// read needs the tenant context set.
func execTenantQuery(ctx context.Context, pg *containers.Postgres, tenant, sql string, dest any, args ...any) error {
	tx, err := pg.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", tenant); err != nil {
		return err
	}
	if err := tx.QueryRow(ctx, sql, args...).Scan(dest); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// execTenantExec runs a statement inside a transaction with
// app.current_tenant set, for direct agent_memory mutation under RLS.
func execTenantExec(ctx context.Context, pg *containers.Postgres, tenant, sql string, args ...any) error {
	tx, err := pg.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", tenant); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, sql, args...); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
