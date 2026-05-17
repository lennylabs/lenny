// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed runtimestore.Store,
// persisting the §5.1 runtime registry to the runtime_definitions
// table. It is a drop-in alternative to runtimestore.Memory.
//
// runtime_definitions is platform-global (§5.1): runtimes are
// platform-wide records keyed by name, so operations here run as
// plain queries without an app.current_tenant context.
package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/capabilityinference"
	"github.com/lennylabs/lenny/pkg/gateway/pgtenant"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// Store is the Postgres-backed runtime registry. Construct with New.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store backed by pool. The pool must point at a
// database that has the migrations/ schema applied.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

var _ runtimestore.Store = (*Store)(nil)

const selectList = `name, type, image, execution_mode, isolation_profile,
	integration_level, description, created_at, updated_at, deleted_at, labels,
	agent_interface, published_metadata, capability_inference_mode,
	tool_capability_overrides`

// labelsJSON marshals a runtime's §5.1 label map to its jsonb text
// form. A nil or empty map is stored as an empty JSON object so the
// labels column never holds JSON null.
func labelsJSON(m map[string]string) string {
	if len(m) == 0 {
		return "{}"
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// agentInterfaceJSON marshals a runtime's §5.1 agentInterface descriptor
// to its jsonb text form. A nil descriptor is stored as SQL NULL, which
// scanRuntime reads back as a nil pointer.
func agentInterfaceJSON(a *runtimestore.AgentInterface) any {
	if a == nil {
		return nil
	}
	b, _ := json.Marshal(a)
	return string(b)
}

// publishedMetadataJSON marshals a runtime's §5.1 publishedMetadata list
// to its jsonb text form. An empty list is stored as SQL NULL, which
// scanRuntime reads back as a nil slice.
func publishedMetadataJSON(entries []runtimestore.PublishedMetadataEntry) any {
	if len(entries) == 0 {
		return nil
	}
	b, _ := json.Marshal(entries)
	return string(b)
}

// capabilityInferenceMode renders the §5.1 capabilityInferenceMode for
// storage, mapping an empty value to the strict default so the column
// always holds a valid mode.
func capabilityInferenceMode(m capabilityinference.Mode) string {
	if m == "" {
		return string(capabilityinference.DefaultMode)
	}
	return string(m)
}

// toolCapabilityOverridesJSON marshals a runtime's §5.1
// toolCapabilityOverrides map to its jsonb text form. An empty map is
// stored as SQL NULL, which scanRuntime reads back as a nil map.
func toolCapabilityOverridesJSON(m map[string][]capabilityinference.Capability) any {
	if len(m) == 0 {
		return nil
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// Create inserts a new runtime row. Returns ErrAlreadyExists when the
// name is taken.
func (s *Store) Create(ctx context.Context, r runtimestore.Runtime) error {
	if err := runtimestore.ValidateName(r.Name); err != nil {
		return err
	}
	now := time.Now().UTC()
	if r.CreatedAt.IsZero() {
		r.CreatedAt = now
	}
	if r.UpdatedAt.IsZero() {
		r.UpdatedAt = r.CreatedAt
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO runtime_definitions (
		name, type, image, execution_mode, isolation_profile,
		integration_level, description, created_at, updated_at, deleted_at, labels,
		agent_interface, published_metadata, capability_inference_mode,
		tool_capability_overrides
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`,
		r.Name, string(r.Type), r.Image, string(r.ExecutionMode),
		string(r.IsolationProfile), string(r.IntegrationLevel), r.Description,
		r.CreatedAt, r.UpdatedAt, pgtenant.NullTime(r.DeletedAt), labelsJSON(r.Labels),
		agentInterfaceJSON(r.AgentInterface), publishedMetadataJSON(r.PublishedMetadata),
		capabilityInferenceMode(r.CapabilityInferenceMode),
		toolCapabilityOverridesJSON(r.ToolCapabilityOverrides))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return runtimestore.ErrAlreadyExists
	}
	return err
}

// Get returns the runtime row keyed by name. Soft-deleted rows are
// returned (callers consult Runtime.IsActive()); a missing row is
// ErrNotFound.
func (s *Store) Get(ctx context.Context, name string) (runtimestore.Runtime, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+selectList+` FROM runtime_definitions WHERE name = $1`, name)
	r, err := scanRuntime(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return runtimestore.Runtime{}, runtimestore.ErrNotFound
	}
	if err != nil {
		return runtimestore.Runtime{}, err
	}
	return r, nil
}

// Update applies mutate to the row under SELECT ... FOR UPDATE.
// UpdatedAt strictly advances on every successful Update.
func (s *Store) Update(ctx context.Context, name string, mutate func(*runtimestore.Runtime) error) (runtimestore.Runtime, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return runtimestore.Runtime{}, fmt.Errorf("runtimestore: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	row := tx.QueryRow(ctx,
		`SELECT `+selectList+` FROM runtime_definitions WHERE name = $1 FOR UPDATE`, name)
	r, err := scanRuntime(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return runtimestore.Runtime{}, runtimestore.ErrNotFound
	}
	if err != nil {
		return runtimestore.Runtime{}, err
	}
	prev := r.UpdatedAt
	if err := mutate(&r); err != nil {
		return runtimestore.Runtime{}, err
	}
	r.UpdatedAt = pgtenant.MonotonicNext(prev, time.Now())
	if _, err := tx.Exec(ctx, `UPDATE runtime_definitions SET
		type = $2, image = $3, execution_mode = $4, isolation_profile = $5,
		integration_level = $6, description = $7, updated_at = $8, deleted_at = $9,
		labels = $10, agent_interface = $11, published_metadata = $12,
		capability_inference_mode = $13, tool_capability_overrides = $14
	WHERE name = $1`,
		name, string(r.Type), r.Image, string(r.ExecutionMode),
		string(r.IsolationProfile), string(r.IntegrationLevel), r.Description,
		r.UpdatedAt, pgtenant.NullTime(r.DeletedAt), labelsJSON(r.Labels),
		agentInterfaceJSON(r.AgentInterface), publishedMetadataJSON(r.PublishedMetadata),
		capabilityInferenceMode(r.CapabilityInferenceMode),
		toolCapabilityOverridesJSON(r.ToolCapabilityOverrides)); err != nil {
		return runtimestore.Runtime{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return runtimestore.Runtime{}, fmt.Errorf("runtimestore: commit: %w", err)
	}
	return r, nil
}

// List returns the runtime rows name-ascending. Soft-deleted rows are
// dropped unless filter.IncludeDeleted is set; filter.Type narrows by
// runtime type.
func (s *Store) List(ctx context.Context, filter runtimestore.ListFilter) ([]runtimestore.Runtime, error) {
	q := `SELECT ` + selectList + ` FROM runtime_definitions`
	var conds []string
	var args []any
	if !filter.IncludeDeleted {
		conds = append(conds, "deleted_at IS NULL")
	}
	if filter.Type != "" {
		args = append(args, string(filter.Type))
		conds = append(conds, fmt.Sprintf("type = $%d", len(args)))
	}
	for i, c := range conds {
		if i == 0 {
			q += " WHERE "
		} else {
			q += " AND "
		}
		q += c
	}
	q += " ORDER BY name"

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []runtimestore.Runtime
	for rows.Next() {
		r, err := scanRuntime(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SoftDelete sets deleted_at on the row. It is idempotent:
// soft-deleting an already-deleted runtime is a no-op success.
func (s *Store) SoftDelete(ctx context.Context, name string, at time.Time) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE runtime_definitions SET deleted_at = $2, updated_at = $2
		 WHERE name = $1 AND deleted_at IS NULL`, name, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	var exists bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM runtime_definitions WHERE name = $1)`, name).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return runtimestore.ErrNotFound
	}
	return nil
}

// scanRuntime reads one row in selectList order into a Runtime.
func scanRuntime(row pgx.Row) (runtimestore.Runtime, error) {
	var (
		r                                          runtimestore.Runtime
		typ, execMode, isoProf, level, description string
		capInferMode                               string
		deletedAt                                  *time.Time
		labelsRaw, agentIfaceRaw, publishedMetaRaw []byte
		toolOverridesRaw                           []byte
	)
	if err := row.Scan(
		&r.Name, &typ, &r.Image, &execMode, &isoProf, &level, &description,
		&r.CreatedAt, &r.UpdatedAt, &deletedAt, &labelsRaw, &agentIfaceRaw, &publishedMetaRaw,
		&capInferMode, &toolOverridesRaw,
	); err != nil {
		return runtimestore.Runtime{}, err
	}
	r.Type = runtimestore.RuntimeType(typ)
	r.ExecutionMode = runtimestore.ExecutionMode(execMode)
	r.IsolationProfile = isolation.Profile(isoProf)
	r.IntegrationLevel = runtimestore.IntegrationLevel(level)
	r.Description = description
	r.CapabilityInferenceMode = capabilityinference.Mode(capInferMode)
	if deletedAt != nil {
		r.DeletedAt = *deletedAt
	}
	if len(labelsRaw) > 0 {
		if err := json.Unmarshal(labelsRaw, &r.Labels); err != nil {
			return runtimestore.Runtime{}, fmt.Errorf("runtimestore: decode labels: %w", err)
		}
	}
	if len(agentIfaceRaw) > 0 {
		if err := json.Unmarshal(agentIfaceRaw, &r.AgentInterface); err != nil {
			return runtimestore.Runtime{}, fmt.Errorf("runtimestore: decode agentInterface: %w", err)
		}
	}
	if len(publishedMetaRaw) > 0 {
		if err := json.Unmarshal(publishedMetaRaw, &r.PublishedMetadata); err != nil {
			return runtimestore.Runtime{}, fmt.Errorf("runtimestore: decode publishedMetadata: %w", err)
		}
	}
	if len(toolOverridesRaw) > 0 {
		if err := json.Unmarshal(toolOverridesRaw, &r.ToolCapabilityOverrides); err != nil {
			return runtimestore.Runtime{}, fmt.Errorf("runtimestore: decode toolCapabilityOverrides: %w", err)
		}
	}
	return r, nil
}
