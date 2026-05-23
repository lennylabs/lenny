// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed tenantaccessstore.Store,
// persisting the §4 runtime/pool tenant-access registry to the
// runtime_tenant_access and pool_tenant_access join tables. It is a
// drop-in alternative to tenantaccessstore.Memory.
//
// The join tables are platform-global: runtimes and pools carry no
// tenant_id, and a grant's tenant_id is a foreign key rather than an
// RLS scoping column (the §15.1 List endpoint returns every tenant
// granted access to a resource). Operations therefore run as plain
// queries without an app.current_tenant context, mirroring the
// platform-global connector registry.
package pgstore

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/pgtenant"
	"github.com/lennylabs/lenny/pkg/gateway/tenantaccessstore"
)

// Store is the Postgres-backed tenant-access registry. Construct with
// New.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store backed by pool. The pool must point at a
// database that has the migrations/ schema applied.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

var _ tenantaccessstore.Store = (*Store)(nil)

// joinTable maps a resource kind to its join table and resource-name
// column. ok is false for an unrecognised kind, matching the
// ResourceKind.IsValid guard in tenantaccessstore.
func joinTable(kind tenantaccessstore.ResourceKind) (table, resourceColumn string, ok bool) {
	switch kind {
	case tenantaccessstore.KindRuntime:
		return "runtime_tenant_access", "runtime_name", true
	case tenantaccessstore.KindPool:
		return "pool_tenant_access", "pool_name", true
	default:
		return "", "", false
	}
}

// Grant records tenant access to a resource. It is idempotent: a
// duplicate (kind, resource, tenant) collides on the primary key and
// reports created=false rather than an error, mirroring
// tenantaccessstore.Memory.
//
// spec: §4.2 line 177 — writes to runtime_tenant_access /
// pool_tenant_access are guarded by the lenny_admin_mode_required
// trigger. The write runs inside an InAdminMode transaction so the
// trigger admits it; the platform-admin RBAC check at the admin-API
// boundary is the caller's responsibility.
func (s *Store) Grant(ctx context.Context, kind tenantaccessstore.ResourceKind, resource, tenantID, grantedBy string, at time.Time) (bool, error) {
	table, resourceColumn, ok := joinTable(kind)
	if !ok || resource == "" || tenantID == "" {
		return false, tenantaccessstore.ErrInvalidArg
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	at = at.UTC()
	var created bool
	err := pgtenant.InAdminMode(ctx, s.pool, func(tx pgx.Tx) error {
		// Use ON CONFLICT DO NOTHING so a duplicate (kind, resource,
		// tenant) does not abort the transaction. RowsAffected reports
		// whether the row was newly inserted.
		tag, ierr := tx.Exec(ctx,
			`INSERT INTO `+table+` (`+resourceColumn+`, tenant_id, granted_by, granted_at)
			 VALUES ($1, $2, $3, $4)
			 ON CONFLICT DO NOTHING`,
			resource, tenantID, grantedBy, at)
		if ierr != nil {
			var pgErr *pgconn.PgError
			if errors.As(ierr, &pgErr) && pgErr.Code == "23505" {
				created = false
				return nil
			}
			return ierr
		}
		created = tag.RowsAffected() > 0
		return nil
	})
	if err != nil {
		return false, err
	}
	return created, nil
}

// Revoke removes a grant. Returns ErrNotFound when no grant exists for
// the (kind, resource, tenant).
//
// spec: §4.2 line 177 — runs inside an InAdminMode transaction so the
// lenny_admin_mode_required trigger admits the DELETE.
func (s *Store) Revoke(ctx context.Context, kind tenantaccessstore.ResourceKind, resource, tenantID string) error {
	table, resourceColumn, ok := joinTable(kind)
	if !ok || resource == "" || tenantID == "" {
		return tenantaccessstore.ErrInvalidArg
	}
	var revoked bool
	err := pgtenant.InAdminMode(ctx, s.pool, func(tx pgx.Tx) error {
		tag, ierr := tx.Exec(ctx,
			`DELETE FROM `+table+` WHERE `+resourceColumn+` = $1 AND tenant_id = $2`,
			resource, tenantID)
		if ierr != nil {
			return ierr
		}
		revoked = tag.RowsAffected() > 0
		return nil
	})
	if err != nil {
		return err
	}
	if !revoked {
		return tenantaccessstore.ErrNotFound
	}
	return nil
}

// List returns a resource's grants, tenant-id ascending. An unknown
// resource yields an empty slice.
func (s *Store) List(ctx context.Context, kind tenantaccessstore.ResourceKind, resource string) ([]tenantaccessstore.Grant, error) {
	table, resourceColumn, ok := joinTable(kind)
	if !ok || resource == "" {
		return nil, tenantaccessstore.ErrInvalidArg
	}
	rows, err := s.pool.Query(ctx,
		`SELECT tenant_id, granted_by, granted_at FROM `+table+`
		 WHERE `+resourceColumn+` = $1 ORDER BY tenant_id`,
		resource)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]tenantaccessstore.Grant, 0)
	for rows.Next() {
		var g tenantaccessstore.Grant
		if err := rows.Scan(&g.TenantID, &g.GrantedBy, &g.GrantedAt); err != nil {
			return nil, err
		}
		g.GrantedAt = g.GrantedAt.UTC()
		out = append(out, g)
	}
	return out, rows.Err()
}

// ListForTenant returns the names of every resource of the given kind
// the tenant has been granted, name ascending. It is the inverse of
// List, used to filter a tenant-admin's runtime/pool reads to the
// resources they may see (§4).
func (s *Store) ListForTenant(ctx context.Context, kind tenantaccessstore.ResourceKind, tenantID string) ([]string, error) {
	table, resourceColumn, ok := joinTable(kind)
	if !ok || tenantID == "" {
		return nil, tenantaccessstore.ErrInvalidArg
	}
	rows, err := s.pool.Query(ctx,
		`SELECT `+resourceColumn+` FROM `+table+`
		 WHERE tenant_id = $1 ORDER BY `+resourceColumn,
		tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}
