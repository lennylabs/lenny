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
// This is the §9.4 "Postgres + pgvector" default backend. Write embeds
// each memory's content with the configured memorystore.Embedder and
// stores the vector in the migration-0044 agent_memory.embedding
// column; Query embeds the query text and ranks the rows that match
// the query string by the pgvector cosine-distance operator, served by
// the ivfflat index. A row with no embedding (one written before the
// embedding column existed, or one whose Embedder declined the text)
// falls back to the case-insensitive substring match ordered by
// recency.
package pgstore

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/memorystore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pgtenant"
)

// Store is the Postgres-backed §9.4 agent-memory store. Construct with
// New.
type Store struct {
	pool       *pgxpool.Pool
	maxPerUser int
	embedder   memorystore.Embedder
	// observer receives the §9.4 / §16.1 instrumentation events; nil
	// disables emission. SetObserver wires it after construction.
	observer memorystore.Observer
}

// backendLabel is the §16.1 `backend` label this Store stamps on
// every Observer call. The §9.4 default is Postgres + pgvector;
// `postgres` matches the catalog enum in §16.1 lines 151–154.
const backendLabel = "postgres"

// New returns a Store backed by pool, enforcing the §9.4 default
// per-user capacity limit and embedding memory content with
// memorystore.HashingEmbedder. The pool must point at a database that
// has the migrations/ schema applied (including migration 0044, which
// adds the pgvector embedding column).
func New(pool *pgxpool.Pool) *Store {
	return NewWithOptions(pool, memorystore.DefaultMaxMemoriesPerUser, memorystore.NewHashingEmbedder())
}

// NewWithMaxPerUser returns a Store whose Write evicts each user's
// memories down to maxPerUser, mirroring the configurable bound on
// memorystore.NewInMemory, embedding content with the default
// HashingEmbedder. A non-positive maxPerUser selects the §9.4 default.
func NewWithMaxPerUser(pool *pgxpool.Pool, maxPerUser int) *Store {
	return NewWithOptions(pool, maxPerUser, memorystore.NewHashingEmbedder())
}

// NewWithOptions returns a Store with an explicit per-user capacity
// bound and Embedder. A non-positive maxPerUser selects the §9.4
// default. A nil embedder disables semantic ranking: Write stores no
// vector and Query serves the substring match ordered by recency.
func NewWithOptions(pool *pgxpool.Pool, maxPerUser int, embedder memorystore.Embedder) *Store {
	if maxPerUser <= 0 {
		maxPerUser = memorystore.DefaultMaxMemoriesPerUser
	}
	return &Store{pool: pool, maxPerUser: maxPerUser, embedder: embedder}
}

// SetObserver wires the §9.4 / §16.1 instrumentation hook. A nil
// value clears it. The §16.1 backend label is fixed at `postgres`.
func (s *Store) SetObserver(obs memorystore.Observer) {
	s.observer = obs
}

// MaxPerUser returns the §9.4 line 202 per-user capacity bound.
func (s *Store) MaxPerUser() int {
	return s.maxPerUser
}

// observe records the §16.1 line 151 duration histogram observation
// and (on a non-nil error) the §16.1 line 152 error counter. A nil
// observer is a no-op.
func (s *Store) observe(op string, start time.Time, err *error) {
	if s.observer == nil {
		return
	}
	s.observer.ObserveOperation(op, time.Since(start).Seconds())
	if err != nil && *err != nil {
		s.observer.IncError(op, memorystore.ClassifyError(*err))
	}
}

var _ memorystore.Store = (*Store)(nil)

// selectList is the column projection for reads, in scanMemory order.
// embedding is cast to text so pgx scans it into a *string regardless
// of whether the pgvector type OID is registered on the connection;
// vectorFromText parses the literal back into a []float32.
const selectList = `id, tenant_id, user_id, agent_type, session_id,
	content, metadata, created_at, embedding::text`

// Write stores memories under the scope, mirroring
// memorystore.InMemory: it stamps the scope and a fresh id/timestamp
// on each record, computes the §9.4 semantic-search embedding from the
// content, persists them, and evicts the user's oldest memories beyond
// the §9.4 per-user capacity limit. A write whose id is already
// present overwrites the existing record, exactly like the in-memory
// map upsert.
func (s *Store) Write(ctx context.Context, scope memorystore.MemoryScope, memories []memorystore.Memory) (err error) {
	defer s.observe(memorystore.OpWrite, time.Now(), &err)
	if scope.TenantID == "" {
		return memorystore.ErrEmptyTenant
	}
	if scope.UserID == "" {
		return memorystore.ErrEmptyUser
	}
	now := time.Now().UTC()
	var (
		userCount   int
		tenantCount int
	)
	err = pgtenant.InTx(ctx, s.pool, scope.TenantID, func(tx pgx.Tx) error {
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
			if mem.Embedding == nil && s.embedder != nil {
				if vec, err := s.embedder.Embed(mem.Content); err == nil {
					mem.Embedding = vec
				}
			}
			// embedding is passed as a pgvector text literal cast to
			// vector. A nil embedding becomes a SQL NULL, which the
			// migration-0044 partial index excludes and Query treats as
			// a substring-only row.
			if _, err := tx.Exec(ctx, `INSERT INTO agent_memory (
				tenant_id, user_id, id, agent_type, session_id,
				content, metadata, created_at, embedding
			) VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8, $9::vector)
			ON CONFLICT (tenant_id, user_id, id) DO UPDATE SET
				agent_type = EXCLUDED.agent_type,
				session_id = EXCLUDED.session_id,
				content    = EXCLUDED.content,
				metadata   = EXCLUDED.metadata,
				created_at = EXCLUDED.created_at,
				embedding  = EXCLUDED.embedding`,
				mem.TenantID, mem.UserID, mem.ID, mem.AgentType, mem.SessionID,
				mem.Content, metadataArg(mem.Metadata), mem.CreatedAt.UTC(),
				vectorArg(mem.Embedding)); err != nil {
				return err
			}
		}
		if err := evictOldest(ctx, tx, scope.TenantID, scope.UserID, s.maxPerUser); err != nil {
			return err
		}
		// Sample the post-commit user and tenant counts inside the
		// transaction so the §9.4 line 202 over-threshold check and
		// record-count gauge land in the same snapshot.
		if s.observer != nil {
			c, err := countUser(ctx, tx, scope.TenantID, scope.UserID)
			if err != nil {
				return err
			}
			userCount = c
			tc, err := countTenant(ctx, tx, scope.TenantID)
			if err != nil {
				return err
			}
			tenantCount = tc
		}
		return nil
	})
	if err != nil {
		return err
	}
	if s.observer != nil {
		if userCount >= memorystore.ThresholdCount(s.maxPerUser) {
			s.observer.IncUserOverThreshold(scope.TenantID)
			memorystore.LogOverThreshold(backendLabel, scope.TenantID, scope.UserID, userCount, s.maxPerUser)
		}
		s.observer.SetRecordCount(scope.TenantID, tenantCount)
	}
	return nil
}

// countUser returns the agent_memory row count for (tenantID,
// userID). The query runs under the §12.3 tenant context already set
// by the enclosing pgtenant.InTx.
func countUser(ctx context.Context, tx pgx.Tx, tenantID, userID string) (int, error) {
	var n int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM agent_memory WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, userID).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// countTenant returns the agent_memory row count for tenantID.
func countTenant(ctx context.Context, tx pgx.Tx, tenantID string) (int, error) {
	var n int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM agent_memory WHERE tenant_id = $1`,
		tenantID).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
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
// string (case-insensitive), capped at limit. A zero limit means no
// cap. The agent type and session, when set on the scope, narrow the
// result.
//
// Among the substring matches the ordering is the §9.4 semantic
// search: when the Embedder produces a query vector, rows are ranked
// by ascending pgvector cosine distance (`embedding <=> query`) to
// that vector, with rows that carry no embedding sorted after the
// embedded ones by recency. When no Embedder is configured, or the
// query embeds to nil, every match falls back to newest-first.
func (s *Store) Query(ctx context.Context, scope memorystore.MemoryScope, query string, limit int) (_ []memorystore.Memory, err error) {
	defer s.observe(memorystore.OpQuery, time.Now(), &err)
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

	qv := s.embedQuery(query)
	if qv == nil {
		q += ` ORDER BY created_at DESC, id DESC`
	} else {
		// NULLS LAST keeps substring-only rows (no embedding) after the
		// vector-ranked rows; the recency tiebreak orders rows that
		// share a distance and the embedding-less tail.
		args = append(args, vectorArg(qv))
		q += fmt.Sprintf(
			" ORDER BY embedding <=> $%d::vector NULLS LAST, created_at DESC, id DESC",
			len(args),
		)
	}
	if limit > 0 {
		args = append(args, limit)
		q += fmt.Sprintf(" LIMIT $%d", len(args))
	}
	return s.queryMemories(ctx, scope.TenantID, q, args)
}

// embedQuery embeds the query text with the store's Embedder. It
// returns nil when no Embedder is configured, the query is blank, or
// the Embedder declines the text — Query then orders by recency.
func (s *Store) embedQuery(query string) []float32 {
	if s.embedder == nil || strings.TrimSpace(query) == "" {
		return nil
	}
	vec, err := s.embedder.Embed(query)
	if err != nil {
		return nil
	}
	return vec
}

// List returns the scope's memories, newest first, narrowed by the
// filter. A zero filter Limit means no cap.
func (s *Store) List(ctx context.Context, scope memorystore.MemoryScope, filter memorystore.MemoryFilter) (_ []memorystore.Memory, err error) {
	defer s.observe(memorystore.OpList, time.Now(), &err)
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
func (s *Store) Delete(ctx context.Context, scope memorystore.MemoryScope, ids []string) (err error) {
	defer s.observe(memorystore.OpDelete, time.Now(), &err)
	if scope.TenantID == "" {
		return memorystore.ErrEmptyTenant
	}
	if scope.UserID == "" {
		return memorystore.ErrEmptyUser
	}
	if len(ids) == 0 {
		return nil
	}
	var tenantCount int
	err = pgtenant.InTx(ctx, s.pool, scope.TenantID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`DELETE FROM agent_memory
			 WHERE tenant_id = $1 AND user_id = $2 AND id = ANY($3)`,
			scope.TenantID, scope.UserID, ids); err != nil {
			return err
		}
		if s.observer != nil {
			tc, err := countTenant(ctx, tx, scope.TenantID)
			if err != nil {
				return err
			}
			tenantCount = tc
		}
		return nil
	})
	if err != nil {
		return err
	}
	if s.observer != nil {
		s.observer.SetRecordCount(scope.TenantID, tenantCount)
	}
	return nil
}

// DeleteByUser removes every memory keyed to (tenantID, userID) — the
// §12.8 GDPR-erasure primitive. It is idempotent and rejects empty
// ids.
func (s *Store) DeleteByUser(ctx context.Context, tenantID, userID string) (err error) {
	defer s.observe(memorystore.OpDeleteByUser, time.Now(), &err)
	if tenantID == "" {
		return memorystore.ErrEmptyTenant
	}
	if userID == "" {
		return memorystore.ErrEmptyUser
	}
	var tenantCount int
	err = pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`DELETE FROM agent_memory WHERE tenant_id = $1 AND user_id = $2`,
			tenantID, userID); err != nil {
			return err
		}
		if s.observer != nil {
			tc, err := countTenant(ctx, tx, tenantID)
			if err != nil {
				return err
			}
			tenantCount = tc
		}
		return nil
	})
	if err != nil {
		return err
	}
	if s.observer != nil {
		s.observer.SetRecordCount(tenantID, tenantCount)
	}
	return nil
}

// DeleteByTenant removes every memory scoped to tenantID — the §12.8
// tenant-deletion primitive. It is idempotent and rejects an empty id.
func (s *Store) DeleteByTenant(ctx context.Context, tenantID string) (err error) {
	defer s.observe(memorystore.OpDeleteByTenant, time.Now(), &err)
	if tenantID == "" {
		return memorystore.ErrEmptyTenant
	}
	err = pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`DELETE FROM agent_memory WHERE tenant_id = $1`, tenantID)
		return err
	})
	if err != nil {
		return err
	}
	if s.observer != nil {
		// The tenant's rows are fully purged; emit 0 to drop the
		// gauge without waiting for the next periodic sample.
		s.observer.SetRecordCount(tenantID, 0)
	}
	return nil
}

// TenantRecordCounts returns the per-tenant agent_memory record count.
// The §9.4 line 202 record-count gauge sampler calls this method to
// refresh the gauge.
func (s *Store) TenantRecordCounts(ctx context.Context) (map[string]int, error) {
	out := map[string]int{}
	// pgtenant.InAllTenants opts the __all__ sentinel scan through
	// the §4.2 tenant guard. Used here for a periodic admin-style
	// poll; the caller is the gateway's record-count sampler.
	err := pgtenant.InAllTenants(ctx, s.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT tenant_id, count(*) FROM agent_memory GROUP BY tenant_id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var tenantID string
			var n int
			if err := rows.Scan(&tenantID, &n); err != nil {
				return err
			}
			out[tenantID] = n
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// scanMemory reads one row in selectList order into a Memory.
func scanMemory(row pgx.Row) (memorystore.Memory, error) {
	var (
		mem       memorystore.Memory
		metadata  []byte
		embedding *string
	)
	if err := row.Scan(
		&mem.ID, &mem.TenantID, &mem.UserID, &mem.AgentType, &mem.SessionID,
		&mem.Content, &metadata, &mem.CreatedAt, &embedding,
	); err != nil {
		return memorystore.Memory{}, err
	}
	md, err := metadataFromJSON(metadata)
	if err != nil {
		return memorystore.Memory{}, err
	}
	mem.Metadata = md
	vec, err := vectorFromText(embedding)
	if err != nil {
		return memorystore.Memory{}, err
	}
	mem.Embedding = vec
	return mem, nil
}

// vectorArg renders an embedding as the pgvector text literal
// `[v1,v2,...]`. A nil embedding becomes a nil *string so the bound
// parameter is SQL NULL. The literal is passed as a parameter and cast
// to vector in SQL, so no value reaches the query string unescaped.
func vectorArg(vec []float32) *string {
	if vec == nil {
		return nil
	}
	var b strings.Builder
	b.WriteByte('[')
	for i, v := range vec {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(v), 'f', -1, 32))
	}
	b.WriteByte(']')
	s := b.String()
	return &s
}

// vectorFromText parses a pgvector `[v1,v2,...]` text literal back into
// a []float32. A nil pointer (the column was NULL) yields a nil slice.
func vectorFromText(s *string) ([]float32, error) {
	if s == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*s)
	trimmed = strings.TrimPrefix(trimmed, "[")
	trimmed = strings.TrimSuffix(trimmed, "]")
	if trimmed == "" {
		return nil, nil
	}
	parts := strings.Split(trimmed, ",")
	vec := make([]float32, len(parts))
	for i, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 32)
		if err != nil {
			return nil, fmt.Errorf("pgstore: parse embedding component %q: %w", p, err)
		}
		vec[i] = float32(f)
	}
	return vec, nil
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
