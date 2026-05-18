// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed memorystore.Store, persisting
// the §9.4 agent-memory records to the agent_memory table. It is a
// drop-in alternative to memorystore.InMemory.
//
// agent_memory is tenant-scoped, so every operation runs inside a
// transaction that sets app.current_tenant for the §12.3
// lenny_tenant_guard trigger and the row-level security policy. The
// store is additionally user-scoped: every read and write carries a
// user_id predicate so a user never observes another user's memory
// within the same tenant.
//
// This is the plain-Postgres backend. The §9.4 pgvector embedding
// column and semantic search are a later wave; Query here is the
// case-insensitive substring match that memorystore.InMemory performs.
package pgstore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/memorystore"
	"github.com/lennylabs/lenny/pkg/gateway/pgtenant"
)

// Store is the Postgres-backed §9.4 agent-memory store. Construct with
// New.
type Store struct {
	pool       *pgxpool.Pool
	maxPerUser int
}

// New returns a Store backed by pool, enforcing the §9.4 default
// per-user capacity limit. The pool must point at a database that has
// the migrations/ schema applied.
func New(pool *pgxpool.Pool) *Store {
	return NewWithMaxPerUser(pool, memorystore.DefaultMaxMemoriesPerUser)
}

// NewWithMaxPerUser returns a Store whose Write evicts each user's
// memories down to maxPerUser, mirroring the configurable bound on
// memorystore.NewInMemory. A non-positive maxPerUser selects the §9.4
// default.
func NewWithMaxPerUser(pool *pgxpool.Pool, maxPerUser int) *Store {
	if maxPerUser <= 0 {
		maxPerUser = memorystore.DefaultMaxMemoriesPerUser
	}
	return &Store{pool: pool, maxPerUser: maxPerUser}
}

var _ memorystore.Store = (*Store)(nil)

// selectList is the column projection for reads, in scanMemory order.
const selectList = `id, tenant_id, user_id, agent_type, session_id,
	content, metadata, created_at`

// Write stores memories under the scope, mirroring
// memorystore.InMemory: it stamps the scope and a fresh id/timestamp
// on each record, persists them, and evicts the user's oldest
// memories beyond the §9.4 per-user capacity limit. A write whose id
// is already present overwrites the existing record, exactly like the
// in-memory map upsert.
func (s *Store) Write(ctx context.Context, scope memorystore.MemoryScope, memories []memorystore.Memory) error {
	if scope.TenantID == "" {
		return memorystore.ErrEmptyTenant
	}
	if scope.UserID == "" {
		return memorystore.ErrEmptyUser
	}
	now := time.Now().UTC()
	return pgtenant.InTx(ctx, s.pool, scope.TenantID, func(tx pgx.Tx) error {
		for _, mem := range memories {
			mem.TenantID = scope.TenantID
			mem.UserID = scope.UserID
			mem.AgentType = scope.AgentType
			mem.SessionID = scope.SessionID
			if mem.ID == "" {
				mem.ID = memorystore.NewID()
			}
			if mem.CreatedAt.IsZero() {
				mem.CreatedAt = now
			}
			if _, err := tx.Exec(ctx, `INSERT INTO agent_memory (
				tenant_id, user_id, id, agent_type, session_id,
				content, metadata, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8)
			ON CONFLICT (tenant_id, user_id, id) DO UPDATE SET
				agent_type = EXCLUDED.agent_type,
				session_id = EXCLUDED.session_id,
				content    = EXCLUDED.content,
				metadata   = EXCLUDED.metadata,
				created_at = EXCLUDED.created_at`,
				mem.TenantID, mem.UserID, mem.ID, mem.AgentType, mem.SessionID,
				mem.Content, metadataArg(mem.Metadata), mem.CreatedAt.UTC()); err != nil {
				return err
			}
		}
		return evictOldest(ctx, tx, scope.TenantID, scope.UserID, s.maxPerUser)
	})
}

// evictOldest trims (tenantID, userID) down to maxPerUser records,
// deleting the oldest by created_at. The id breaks created_at ties so
// the eviction is deterministic. The caller runs it inside the write
// transaction.
func evictOldest(ctx context.Context, tx pgx.Tx, tenantID, userID string, maxPerUser int) error {
	_, err := tx.Exec(ctx, `DELETE FROM agent_memory
		WHERE tenant_id = $1 AND user_id = $2 AND id IN (
			SELECT id FROM agent_memory
			WHERE tenant_id = $1 AND user_id = $2
			ORDER BY created_at DESC, id DESC
			OFFSET $3
		)`, tenantID, userID, maxPerUser)
	return err
}

// Query returns the scope's memories whose content contains the query
// string (case-insensitive), newest first, capped at limit. A zero
// limit means no cap. The agent type and session, when set on the
// scope, narrow the result.
func (s *Store) Query(ctx context.Context, scope memorystore.MemoryScope, query string, limit int) ([]memorystore.Memory, error) {
	if scope.TenantID == "" {
		return nil, memorystore.ErrEmptyTenant
	}
	if scope.UserID == "" {
		return nil, memorystore.ErrEmptyUser
	}
	q, args := scopeQuery(scope)
	if query != "" {
		args = append(args, query)
		// ILIKE with the parameter wrapped in % positions performs the
		// case-insensitive substring match memorystore.InMemory does
		// with strings.Contains over lower-cased text.
		q += fmt.Sprintf(" AND content ILIKE '%%' || $%d || '%%'", len(args))
	}
	q += ` ORDER BY created_at DESC, id DESC`
	if limit > 0 {
		args = append(args, limit)
		q += fmt.Sprintf(" LIMIT $%d", len(args))
	}
	return s.queryMemories(ctx, scope.TenantID, q, args)
}

// List returns the scope's memories, newest first, narrowed by the
// filter. A zero filter Limit means no cap.
func (s *Store) List(ctx context.Context, scope memorystore.MemoryScope, filter memorystore.MemoryFilter) ([]memorystore.Memory, error) {
	if scope.TenantID == "" {
		return nil, memorystore.ErrEmptyTenant
	}
	if scope.UserID == "" {
		return nil, memorystore.ErrEmptyUser
	}
	q, args := scopeQuery(scope)
	q += ` ORDER BY created_at DESC, id DESC`
	if filter.Limit > 0 {
		args = append(args, filter.Limit)
		q += fmt.Sprintf(" LIMIT $%d", len(args))
	}
	return s.queryMemories(ctx, scope.TenantID, q, args)
}

// scopeQuery builds the SELECT and positional arguments common to
// Query and List: the tenant and user predicate, narrowed by the
// agent type and session when the scope sets them. The caller appends
// any further predicates, the ORDER BY, and the LIMIT.
func scopeQuery(scope memorystore.MemoryScope) (string, []any) {
	q := `SELECT ` + selectList + ` FROM agent_memory
		WHERE tenant_id = $1 AND user_id = $2`
	args := []any{scope.TenantID, scope.UserID}
	if scope.AgentType != "" {
		args = append(args, scope.AgentType)
		q += fmt.Sprintf(" AND agent_type = $%d", len(args))
	}
	if scope.SessionID != "" {
		args = append(args, scope.SessionID)
		q += fmt.Sprintf(" AND session_id = $%d", len(args))
	}
	return q, args
}

// queryMemories runs q under the tenant context and scans the rows
// into Memory values.
func (s *Store) queryMemories(ctx context.Context, tenantID, q string, args []any) ([]memorystore.Memory, error) {
	var out []memorystore.Memory
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			mem, err := scanMemory(rows)
			if err != nil {
				return err
			}
			out = append(out, mem)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Delete removes the identified memories that fall within the scope. A
// memory is removed only when its (tenant_id, user_id) matches the
// scope: an id belonging to another tenant or user is never deleted,
// mirroring memorystore.InMemory.
func (s *Store) Delete(ctx context.Context, scope memorystore.MemoryScope, ids []string) error {
	if scope.TenantID == "" {
		return memorystore.ErrEmptyTenant
	}
	if scope.UserID == "" {
		return memorystore.ErrEmptyUser
	}
	if len(ids) == 0 {
		return nil
	}
	return pgtenant.InTx(ctx, s.pool, scope.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`DELETE FROM agent_memory
			 WHERE tenant_id = $1 AND user_id = $2 AND id = ANY($3)`,
			scope.TenantID, scope.UserID, ids)
		return err
	})
}

// DeleteByUser removes every memory keyed to (tenantID, userID) — the
// §12.8 GDPR-erasure primitive. It is idempotent and rejects empty
// ids.
func (s *Store) DeleteByUser(ctx context.Context, tenantID, userID string) error {
	if tenantID == "" {
		return memorystore.ErrEmptyTenant
	}
	if userID == "" {
		return memorystore.ErrEmptyUser
	}
	return pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`DELETE FROM agent_memory WHERE tenant_id = $1 AND user_id = $2`,
			tenantID, userID)
		return err
	})
}

// DeleteByTenant removes every memory scoped to tenantID — the §12.8
// tenant-deletion primitive. It is idempotent and rejects an empty id.
func (s *Store) DeleteByTenant(ctx context.Context, tenantID string) error {
	if tenantID == "" {
		return memorystore.ErrEmptyTenant
	}
	return pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`DELETE FROM agent_memory WHERE tenant_id = $1`, tenantID)
		return err
	})
}

// scanMemory reads one row in selectList order into a Memory.
func scanMemory(row pgx.Row) (memorystore.Memory, error) {
	var (
		mem      memorystore.Memory
		metadata []byte
	)
	if err := row.Scan(
		&mem.ID, &mem.TenantID, &mem.UserID, &mem.AgentType, &mem.SessionID,
		&mem.Content, &metadata, &mem.CreatedAt,
	); err != nil {
		return memorystore.Memory{}, err
	}
	md, err := metadataFromJSON(metadata)
	if err != nil {
		return memorystore.Memory{}, err
	}
	mem.Metadata = md
	return mem, nil
}

// metadataArg renders a Memory.Metadata map as a jsonb query argument.
// A nil or empty map becomes the empty object so the NOT NULL column
// always has a value.
func metadataArg(md map[string]any) string {
	if len(md) == 0 {
		return "{}"
	}
	b, err := json.Marshal(md)
	if err != nil {
		// A map[string]any assembled by the caller is expected to
		// marshal; fall back to the empty object rather than failing
		// the write on an unmarshalable value.
		return "{}"
	}
	return string(b)
}

// metadataFromJSON reconstructs a Memory.Metadata map from the jsonb
// column. An empty object yields a nil map, matching the in-memory
// store, where a memory written without metadata reads back with a nil
// Metadata field.
func metadataFromJSON(b []byte) (map[string]any, error) {
	if len(b) == 0 {
		return nil, nil
	}
	var md map[string]any
	if err := json.Unmarshal(b, &md); err != nil {
		return nil, err
	}
	if len(md) == 0 {
		return nil, nil
	}
	return md, nil
}
