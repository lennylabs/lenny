// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed customrolestore.Store,
// persisting the §10.2 per-tenant custom-role registry to the
// custom_roles table. It is a drop-in alternative to
// customrolestore.Memory.
//
// custom_roles is tenant-scoped, so every operation runs inside a
// transaction that sets app.current_tenant for the §12.3
// lenny_tenant_guard trigger and the RLS policy.
package pgstore

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/customrolestore"
	"github.com/lennylabs/lenny/pkg/gateway/pgtenant"
)

// Store is the Postgres-backed custom-role registry. Construct with
// New.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store backed by pool. The pool must point at a
// database that has the migrations/ schema applied.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

var _ customrolestore.Store = (*Store)(nil)

// selectList is the column projection for reads. version is the §15.1
// optimistic-concurrency counter and is read last.
const selectList = `tenant_id, name, permissions, created_at, updated_at, version`

// Create inserts a new custom-role row. It validates the §10.2
// structural invariants, mirroring customrolestore.Memory. Returns
// ErrAlreadyExists when the (tenant, name) pair is taken.
func (s *Store) Create(ctx context.Context, r customrolestore.CustomRole) error {
	if err := customrolestore.Validate(r); err != nil {
		return err
	}
	now := time.Now().UTC()
	if r.CreatedAt.IsZero() {
		r.CreatedAt = now
	}
	if r.UpdatedAt.IsZero() {
		r.UpdatedAt = r.CreatedAt
	}
	// spec: §15.1 line 1207 — every admin resource version starts at 1.
	if r.Version == 0 {
		r.Version = 1
	}
	err := pgtenant.InTx(ctx, s.pool, r.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO custom_roles (
			tenant_id, name, permissions, created_at, updated_at, version
		) VALUES ($1, $2, $3, $4, $5, $6)`,
			r.TenantID, r.Name, permsToText(r.Permissions),
			r.CreatedAt, r.UpdatedAt, r.Version)
		return err
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return customrolestore.ErrAlreadyExists
	}
	return err
}

// Get returns the custom-role row keyed by (tenantID, name). A
// cross-tenant miss is indistinguishable from a missing row.
func (s *Store) Get(ctx context.Context, tenantID, name string) (customrolestore.CustomRole, error) {
	var out customrolestore.CustomRole
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+selectList+` FROM custom_roles WHERE tenant_id = $1 AND name = $2`,
			tenantID, name)
		r, err := scanRole(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return customrolestore.ErrNotFound
		}
		if err != nil {
			return err
		}
		out = r
		return nil
	})
	if err != nil {
		return customrolestore.CustomRole{}, err
	}
	return out, nil
}

// Update applies mutate to the row under SELECT ... FOR UPDATE,
// re-validates the §10.2 structural invariants, and advances
// UpdatedAt.
func (s *Store) Update(ctx context.Context, tenantID, name string, mutate func(*customrolestore.CustomRole) error) (customrolestore.CustomRole, error) {
	var out customrolestore.CustomRole
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+selectList+` FROM custom_roles WHERE tenant_id = $1 AND name = $2 FOR UPDATE`,
			tenantID, name)
		r, err := scanRole(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return customrolestore.ErrNotFound
		}
		if err != nil {
			return err
		}
		prev := r.UpdatedAt
		if err := mutate(&r); err != nil {
			return err
		}
		if err := customrolestore.Validate(r); err != nil {
			return err
		}
		r.UpdatedAt = pgtenant.MonotonicNext(prev, time.Now())
		// spec: §15.1 line 1207 — bump the optimistic-concurrency version on
		// every successful Update so the next If-Match precondition compares
		// against the new value.
		r.Version++
		if _, err := tx.Exec(ctx, `UPDATE custom_roles SET
			permissions = $3, updated_at = $4, version = $5
		WHERE tenant_id = $1 AND name = $2`,
			tenantID, name, permsToText(r.Permissions), r.UpdatedAt, r.Version); err != nil {
			return err
		}
		out = r
		return nil
	})
	if err != nil {
		return customrolestore.CustomRole{}, err
	}
	return out, nil
}

// List returns the tenant's custom roles in name order.
func (s *Store) List(ctx context.Context, tenantID string) ([]customrolestore.CustomRole, error) {
	var out []customrolestore.CustomRole
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT `+selectList+` FROM custom_roles WHERE tenant_id = $1 ORDER BY name`,
			tenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			r, err := scanRole(rows)
			if err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Delete removes the custom-role row keyed by (tenantID, name).
// Deleting a missing role returns ErrNotFound.
func (s *Store) Delete(ctx context.Context, tenantID, name string) error {
	return pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`DELETE FROM custom_roles WHERE tenant_id = $1 AND name = $2`,
			tenantID, name)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return customrolestore.ErrNotFound
		}
		return nil
	})
}

// DeleteByUser implements the §12.1 mandatory-erasure primitive.
// Custom roles are tenant-scoped, not user-scoped, so the method
// returns (0, nil); the role definition itself is not removed when a
// user inside the tenant is erased.
//
// spec: §12.1 line 5.
func (s *Store) DeleteByUser(_ context.Context, _, _ string) (int, error) {
	return 0, nil
}

// DeleteByTenant implements the §12.1 mandatory-erasure primitive.
// Removes every custom-role row belonging to tenantID.
//
// spec: §12.1 line 5, §12.8 Phase 4.
func (s *Store) DeleteByTenant(ctx context.Context, tenantID string) (int, error) {
	if tenantID == "" {
		return 0, errors.New("customrolestore: DeleteByTenant requires a concrete tenant_id")
	}
	var deleted int64
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`DELETE FROM custom_roles WHERE tenant_id = $1`, tenantID)
		if err != nil {
			return err
		}
		deleted = tag.RowsAffected()
		return nil
	})
	if err != nil {
		return 0, err
	}
	return int(deleted), nil
}

// scanRole reads one row in selectList order into a CustomRole.
func scanRole(row pgx.Row) (customrolestore.CustomRole, error) {
	var (
		r     customrolestore.CustomRole
		perms []string
	)
	if err := row.Scan(
		&r.TenantID, &r.Name, &perms, &r.CreatedAt, &r.UpdatedAt, &r.Version,
	); err != nil {
		return customrolestore.CustomRole{}, err
	}
	r.Permissions = permsFromText(perms)
	return r, nil
}

// permsToText flattens the permission set for the text[] column.
func permsToText(perms []auth.Permission) []string {
	out := make([]string, len(perms))
	for i, p := range perms {
		out[i] = string(p)
	}
	return out
}

// permsFromText reconstructs the permission set from the text[]
// column. An empty column yields a nil slice, matching
// customrolestore.Memory.
func permsFromText(ss []string) []auth.Permission {
	if len(ss) == 0 {
		return nil
	}
	out := make([]auth.Permission, len(ss))
	for i, s := range ss {
		out[i] = auth.Permission(s)
	}
	return out
}
