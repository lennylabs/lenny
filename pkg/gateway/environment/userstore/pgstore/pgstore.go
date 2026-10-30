// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed userstore.Store, persisting
// the §10.2 per-tenant user registry to the users table. It is a
// drop-in alternative to userstore.Memory.
//
// users is tenant-scoped, so every operation runs inside a
// transaction that sets app.current_tenant for the §12.3
// lenny_tenant_guard trigger and the RLS policy.
package pgstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pgtenant"
)

// Store is the Postgres-backed user registry. Construct with New.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store backed by pool. The pool must point at a
// database that has the migrations/ schema applied.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

var _ userstore.Store = (*Store)(nil)

const selectList = `tenant_id, subject, email, display_name, roles,
	disabled, created_at, updated_at, deleted_at,
	processing_restricted, erasure_job_id, version,
	role_assigned, role_assigned_by, role_assigned_at`

// Create inserts a new user row. It validates the tenant id, subject,
// and role set, mirroring userstore.Memory. Returns ErrAlreadyExists
// when the (tenant, subject) pair is taken.
func (s *Store) Create(ctx context.Context, u userstore.User) error {
	if err := auth.ValidateTenantID(u.TenantID); err != nil {
		return err
	}
	if err := userstore.ValidateSubject(u.Subject); err != nil {
		return err
	}
	if err := validateRoles(u.Roles); err != nil {
		return err
	}
	now := time.Now().UTC()
	if u.CreatedAt.IsZero() {
		u.CreatedAt = now
	}
	if u.UpdatedAt.IsZero() {
		u.UpdatedAt = u.CreatedAt
	}
	// spec: §15.1 — a new resource is born at version 1.
	if u.Version == 0 {
		u.Version = 1
	}
	err := pgtenant.InTx(ctx, s.pool, u.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO users (
			tenant_id, subject, email, display_name, roles,
			disabled, created_at, updated_at, deleted_at,
			processing_restricted, erasure_job_id, version,
			role_assigned, role_assigned_by, role_assigned_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`,
			u.TenantID, u.Subject, u.Email, u.DisplayName, rolesToText(u.Roles),
			u.Disabled, u.CreatedAt, u.UpdatedAt, pgtenant.NullTime(u.DeletedAt),
			u.ProcessingRestricted, u.ErasureJobID, u.Version,
			u.RoleAssigned, u.RoleAssignedBy, pgtenant.NullTime(u.RoleAssignedAt))
		return err
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return userstore.ErrAlreadyExists
	}
	return err
}

// Get returns the user row keyed by (tenantID, subject). A
// cross-tenant miss is indistinguishable from a missing row.
func (s *Store) Get(ctx context.Context, tenantID, subject string) (userstore.User, error) {
	var out userstore.User
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+selectList+` FROM users WHERE tenant_id = $1 AND subject = $2`,
			tenantID, subject)
		u, err := scanUser(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return userstore.ErrNotFound
		}
		if err != nil {
			return err
		}
		out = u
		return nil
	})
	if err != nil {
		return userstore.User{}, err
	}
	return out, nil
}

// Update applies mutate to the row under SELECT ... FOR UPDATE,
// re-validates the resulting role set, and advances UpdatedAt.
func (s *Store) Update(ctx context.Context, tenantID, subject string, mutate func(*userstore.User) error) (userstore.User, error) {
	var out userstore.User
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+selectList+` FROM users WHERE tenant_id = $1 AND subject = $2 FOR UPDATE`,
			tenantID, subject)
		u, err := scanUser(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return userstore.ErrNotFound
		}
		if err != nil {
			return err
		}
		prev := u.UpdatedAt
		if err := mutate(&u); err != nil {
			return err
		}
		if err := validateRoles(u.Roles); err != nil {
			return err
		}
		u.UpdatedAt = pgtenant.MonotonicNext(prev, time.Now())
		// spec: §15.1 — bump the entity-tag version on every write.
		u.Version++
		if _, err := tx.Exec(ctx, `UPDATE users SET
			email = $3, display_name = $4, roles = $5, disabled = $6,
			updated_at = $7, deleted_at = $8,
			processing_restricted = $9, erasure_job_id = $10, version = $11,
			role_assigned = $12, role_assigned_by = $13, role_assigned_at = $14
		WHERE tenant_id = $1 AND subject = $2`,
			tenantID, subject, u.Email, u.DisplayName, rolesToText(u.Roles),
			u.Disabled, u.UpdatedAt, pgtenant.NullTime(u.DeletedAt),
			u.ProcessingRestricted, u.ErasureJobID, u.Version,
			u.RoleAssigned, u.RoleAssignedBy, pgtenant.NullTime(u.RoleAssignedAt)); err != nil {
			return err
		}
		out = u
		return nil
	})
	if err != nil {
		return userstore.User{}, err
	}
	return out, nil
}

// SetErasureRestrictionTx is the one write path for the §12.8 / GDPR
// Article 18 restriction flag on a users row, run inside the caller's
// transaction so the erasure job store writes the job row and the flag
// together. It locks the row FOR UPDATE, advances updated_at with
// pgtenant.MonotonicNext, increments version, and writes
// processing_restricted = restricted and erasure_job_id = jobID (empty when
// restricted is false). When requireJobID is non-empty it writes only when
// the locked row's erasure_job_id equals it and otherwise returns 0 rows,
// which is how a job's completion clears only its own restriction. A
// missing row returns userstore.ErrNotFound.
//
// The helper sets no trigger bypass. A clear it writes passes the migration
// 0042 trigger only when no erasure job for the user is in progress, which
// holds for a completion because the caller marks its job completed earlier
// in the same transaction. The operator clear's bypass lives in
// ClearProcessingRestrictionTx alone.
//
// spec: §12.8 (Processing restriction during erasure (GDPR Article 18)).
func SetErasureRestrictionTx(ctx context.Context, tx pgx.Tx, tenantID, subject, jobID string, restricted bool, requireJobID string) (int64, error) {
	var (
		current   string
		updatedAt time.Time
	)
	err := tx.QueryRow(ctx,
		`SELECT erasure_job_id, updated_at FROM users WHERE tenant_id = $1 AND subject = $2 FOR UPDATE`,
		tenantID, subject).Scan(&current, &updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, userstore.ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("lock user %s/%s: %w", tenantID, subject, err)
	}
	if requireJobID != "" && current != requireJobID {
		return 0, nil
	}
	if !restricted {
		jobID = ""
	}
	// spec: §15.1 — bump the entity-tag version on every write.
	tag, err := tx.Exec(ctx, `UPDATE users SET
		processing_restricted = $3, erasure_job_id = $4,
		updated_at = $5, version = version + 1
	WHERE tenant_id = $1 AND subject = $2`,
		tenantID, subject, restricted, jobID, pgtenant.MonotonicNext(updatedAt, time.Now()))
	if err != nil {
		return 0, fmt.Errorf("write restriction on user %s/%s: %w", tenantID, subject, err)
	}
	return tag.RowsAffected(), nil
}

// ClearProcessingRestrictionTx is the §15.1 operator clear of the
// restriction a failed erasure job left set, run inside the caller's
// transaction after it has locked and checked the job row. It sets the
// transaction-local lenny.clear_processing_restriction bypass so the
// migration 0042 trigger admits the clear, locks the users row, and returns
// userstore.ErrRestrictionHeldByOtherJob without writing when the row's
// erasure_job_id names a job other than jobID. Otherwise it clears the flag
// through SetErasureRestrictionTx, so one users write path remains. A
// missing row returns userstore.ErrNotFound.
//
// spec: §12.8 (Processing restriction during erasure (GDPR Article 18)),
// §15.1 (REST API).
func ClearProcessingRestrictionTx(ctx context.Context, tx pgx.Tx, tenantID, subject, jobID string) error {
	// The session-local bypass mirrors the lenny.erasure_mode guard on the
	// §11.7 immutability triggers; SET LOCAL scopes it to this transaction.
	if _, err := tx.Exec(ctx, "SET LOCAL lenny.clear_processing_restriction = 'true'"); err != nil {
		return fmt.Errorf("set restriction-clear bypass: %w", err)
	}
	var current string
	err := tx.QueryRow(ctx,
		`SELECT erasure_job_id FROM users WHERE tenant_id = $1 AND subject = $2 FOR UPDATE`,
		tenantID, subject).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return userstore.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock user %s/%s: %w", tenantID, subject, err)
	}
	if current != "" && current != jobID {
		return userstore.ErrRestrictionHeldByOtherJob
	}
	_, err = SetErasureRestrictionTx(ctx, tx, tenantID, subject, "", false, "")
	return err
}

// List returns the tenant's users subject-ascending. Soft-deleted and
// disabled rows are dropped unless the filter opts them in.
func (s *Store) List(ctx context.Context, tenantID string, filter userstore.ListFilter) ([]userstore.User, error) {
	q := `SELECT ` + selectList + ` FROM users WHERE tenant_id = $1`
	if !filter.IncludeDeleted {
		q += ` AND deleted_at IS NULL`
	}
	if !filter.IncludeDisabled {
		q += ` AND disabled = false`
	}
	q += ` ORDER BY subject`

	var out []userstore.User
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, q, tenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			u, err := scanUser(rows)
			if err != nil {
				return err
			}
			out = append(out, u)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// SoftDelete sets deleted_at on the row. It is idempotent:
// soft-deleting an already-deleted user is a no-op success.
func (s *Store) SoftDelete(ctx context.Context, tenantID, subject string, at time.Time) error {
	return pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE users SET deleted_at = $3, updated_at = $3
			 WHERE tenant_id = $1 AND subject = $2 AND deleted_at IS NULL`,
			tenantID, subject, at)
		if err != nil {
			return err
		}
		if tag.RowsAffected() > 0 {
			return nil
		}
		var exists bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM users WHERE tenant_id = $1 AND subject = $2)`,
			tenantID, subject).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return userstore.ErrNotFound
		}
		return nil
	})
}

// DeleteByUser implements the §12.1 mandatory-erasure primitive.
// Hard-deletes the user row keyed by (tenantID, userID); returns the
// number of rows removed (0 if no such row).
//
// spec: §12.1.
func (s *Store) DeleteByUser(ctx context.Context, tenantID, userID string) (int, error) {
	if tenantID == "" || userID == "" {
		return 0, errors.New("userstore: DeleteByUser requires non-empty tenant_id and user_id")
	}
	var deleted int64
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`DELETE FROM users WHERE tenant_id = $1 AND subject = $2`,
			tenantID, userID)
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

// DeleteByTenant implements the §12.1 mandatory-erasure primitive.
// Removes every user row belonging to tenantID.
//
// spec: §12.1, §12.8 Phase 4.
func (s *Store) DeleteByTenant(ctx context.Context, tenantID string) (int, error) {
	if tenantID == "" {
		return 0, errors.New("userstore: DeleteByTenant requires a concrete tenant_id")
	}
	var deleted int64
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`DELETE FROM users WHERE tenant_id = $1`, tenantID)
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

// scanUser reads one row in selectList order into a User.
func scanUser(row pgx.Row) (userstore.User, error) {
	var (
		u          userstore.User
		roles      []string
		deletedAt  *time.Time
		assignedAt *time.Time
	)
	if err := row.Scan(
		&u.TenantID, &u.Subject, &u.Email, &u.DisplayName, &roles,
		&u.Disabled, &u.CreatedAt, &u.UpdatedAt, &deletedAt,
		&u.ProcessingRestricted, &u.ErasureJobID, &u.Version,
		&u.RoleAssigned, &u.RoleAssignedBy, &assignedAt,
	); err != nil {
		return userstore.User{}, err
	}
	u.Roles = rolesFromText(roles)
	if deletedAt != nil {
		u.DeletedAt = *deletedAt
	}
	if assignedAt != nil {
		u.RoleAssignedAt = *assignedAt
	}
	return u, nil
}

// validateRoles rejects any unrecognised §10.2 RBAC role.
func validateRoles(roles []auth.Role) error {
	for _, r := range roles {
		if !r.IsValid() {
			return fmt.Errorf("userstore: role %q is not a recognised §10.2 RBAC role", r)
		}
	}
	return nil
}

// rolesToText flattens the role set for the text[] column.
func rolesToText(roles []auth.Role) []string {
	out := make([]string, len(roles))
	for i, r := range roles {
		out[i] = string(r)
	}
	return out
}

// rolesFromText reconstructs the role set from the text[] column. An
// empty column yields a nil slice, matching userstore.Memory.
func rolesFromText(ss []string) []auth.Role {
	if len(ss) == 0 {
		return nil
	}
	out := make([]auth.Role, len(ss))
	for i, s := range ss {
		out[i] = auth.Role(s)
	}
	return out
}
