// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed environmentstore.Store,
// persisting the §10.6 per-tenant Environment registry to the
// environments table. It is a drop-in alternative to
// environmentstore.Memory.
//
// environments is tenant-scoped, so every operation runs inside a
// transaction that sets app.current_tenant for the §12.3
// lenny_tenant_guard trigger and the RLS policy. Reads additionally
// carry an explicit tenant_id predicate so the store behaves
// identically whether the connection role is the non-superuser
// lenny_app (RLS-filtered) or a superuser (RLS bypassed).
//
// Scalar fields (name, description, audit timestamps) are typed
// columns. The nested §10.6 Environment body — members, the RBAC
// roles, the runtime and connector selectors, the mcp capability
// filters, and the bilateral cross-environment declarations — is
// serialized into the body jsonb column.
package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/environment"
	"github.com/lennylabs/lenny/pkg/gateway/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/pgtenant"
)

// Store is the Postgres-backed Environment registry. Construct with
// New.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store backed by pool. The pool must point at a
// database that has the migrations/ schema applied.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

var _ environmentstore.Store = (*Store)(nil)

// selectList is the column projection for reads.
const selectList = `tenant_id, name, description, body, created_at, updated_at`

// envBody is the JSON projection of the nested §10.6 Environment
// fields stored in the body jsonb column. The scalar columns
// (tenant_id, name, description, created_at, updated_at) are not
// repeated here.
type envBody struct {
	Members                 []environmentstore.Member           `json:"members,omitempty"`
	RuntimeSelector         environment.Selector                `json:"runtimeSelector,omitempty"`
	MCPRuntimeFilters       []environmentstore.MCPRuntimeFilter `json:"mcpRuntimeFilters,omitempty"`
	ConnectorSelector       environmentstore.ConnectorSelector  `json:"connectorSelector,omitempty"`
	DefaultDelegationPolicy string                              `json:"defaultDelegationPolicy,omitempty"`
	CrossEnvOutbound        []environmentstore.CrossEnvRule     `json:"crossEnvOutbound,omitempty"`
	CrossEnvInbound         []environmentstore.CrossEnvRule     `json:"crossEnvInbound,omitempty"`
}

// bodyOf flattens the nested fields of e into the jsonb body.
func bodyOf(e environmentstore.Environment) envBody {
	return envBody{
		Members:                 e.Members,
		RuntimeSelector:         e.RuntimeSelector,
		MCPRuntimeFilters:       e.MCPRuntimeFilters,
		ConnectorSelector:       e.ConnectorSelector,
		DefaultDelegationPolicy: e.DefaultDelegationPolicy,
		CrossEnvOutbound:        e.CrossEnvOutbound,
		CrossEnvInbound:         e.CrossEnvInbound,
	}
}

// applyBody copies the decoded jsonb body fields onto e.
func applyBody(e *environmentstore.Environment, b envBody) {
	e.Members = b.Members
	e.RuntimeSelector = b.RuntimeSelector
	e.MCPRuntimeFilters = b.MCPRuntimeFilters
	e.ConnectorSelector = b.ConnectorSelector
	e.DefaultDelegationPolicy = b.DefaultDelegationPolicy
	e.CrossEnvOutbound = b.CrossEnvOutbound
	e.CrossEnvInbound = b.CrossEnvInbound
}

// marshalBody serializes the nested fields of e for the body column.
func marshalBody(e environmentstore.Environment) ([]byte, error) {
	return json.Marshal(bodyOf(e))
}

// Create inserts a new environment row. It runs the §10.6 admission
// validation and defaults the audit timestamps, mirroring
// environmentstore.Memory. Returns ErrAlreadyExists when the
// (tenant, name) pair is taken.
func (s *Store) Create(ctx context.Context, e environmentstore.Environment) error {
	if err := e.Validate(); err != nil {
		return err
	}
	now := time.Now().UTC()
	if e.CreatedAt.IsZero() {
		e.CreatedAt = now
	}
	if e.UpdatedAt.IsZero() {
		e.UpdatedAt = e.CreatedAt
	}
	body, err := marshalBody(e)
	if err != nil {
		return err
	}
	err = pgtenant.InTx(ctx, s.pool, e.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO environments (
			tenant_id, name, description, body, created_at, updated_at
		) VALUES ($1, $2, $3, $4::jsonb, $5, $6)`,
			e.TenantID, e.Name, e.Description, string(body),
			e.CreatedAt, e.UpdatedAt)
		return err
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return environmentstore.ErrAlreadyExists
	}
	return err
}

// Get returns the environment row keyed by (tenantID, name). A
// cross-tenant miss is indistinguishable from a missing row (§10.6
// isolation).
func (s *Store) Get(ctx context.Context, tenantID, name string) (environmentstore.Environment, error) {
	var out environmentstore.Environment
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+selectList+` FROM environments WHERE tenant_id = $1 AND name = $2`,
			tenantID, name)
		e, err := scanEnvironment(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return environmentstore.ErrNotFound
		}
		if err != nil {
			return err
		}
		out = e
		return nil
	})
	if err != nil {
		return environmentstore.Environment{}, err
	}
	return out, nil
}

// Update applies mutate to the row under SELECT ... FOR UPDATE,
// re-runs the §10.6 admission validation, and advances UpdatedAt.
// UpdatedAt strictly advances on every successful Update (clamped to
// the prior value + 1µs, the Postgres timestamptz resolution) so
// callers can rely on monotonicity. A mutate error or a validation
// failure rolls the transaction back, leaving the stored row intact.
func (s *Store) Update(ctx context.Context, tenantID, name string, mutate func(*environmentstore.Environment) error) (environmentstore.Environment, error) {
	var out environmentstore.Environment
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+selectList+` FROM environments WHERE tenant_id = $1 AND name = $2 FOR UPDATE`,
			tenantID, name)
		e, err := scanEnvironment(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return environmentstore.ErrNotFound
		}
		if err != nil {
			return err
		}
		prev := e.UpdatedAt
		if err := mutate(&e); err != nil {
			return err
		}
		if err := e.Validate(); err != nil {
			return err
		}
		e.UpdatedAt = pgtenant.MonotonicNext(prev, time.Now())
		body, err := marshalBody(e)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE environments SET
			description = $3, body = $4::jsonb, updated_at = $5
		WHERE tenant_id = $1 AND name = $2`,
			tenantID, name, e.Description, string(body), e.UpdatedAt); err != nil {
			return err
		}
		out = e
		return nil
	})
	if err != nil {
		return environmentstore.Environment{}, err
	}
	return out, nil
}

// List returns the tenant's environments name-ascending.
func (s *Store) List(ctx context.Context, tenantID string) ([]environmentstore.Environment, error) {
	var out []environmentstore.Environment
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT `+selectList+` FROM environments WHERE tenant_id = $1 ORDER BY name`,
			tenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			e, err := scanEnvironment(rows)
			if err != nil {
				return err
			}
			out = append(out, e)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Delete removes the environment row. Returns ErrNotFound when no
// matching (tenant, name) row exists.
func (s *Store) Delete(ctx context.Context, tenantID, name string) error {
	return pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`DELETE FROM environments WHERE tenant_id = $1 AND name = $2`, tenantID, name)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return environmentstore.ErrNotFound
		}
		return nil
	})
}

// scanEnvironment reads one row in selectList order into an
// Environment, decoding the body jsonb column into the nested fields.
func scanEnvironment(row pgx.Row) (environmentstore.Environment, error) {
	var (
		e        environmentstore.Environment
		bodyJSON []byte
	)
	if err := row.Scan(
		&e.TenantID, &e.Name, &e.Description, &bodyJSON,
		&e.CreatedAt, &e.UpdatedAt,
	); err != nil {
		return environmentstore.Environment{}, err
	}
	var b envBody
	if len(bodyJSON) > 0 {
		if err := json.Unmarshal(bodyJSON, &b); err != nil {
			return environmentstore.Environment{}, err
		}
	}
	applyBody(&e, b)
	return e, nil
}
