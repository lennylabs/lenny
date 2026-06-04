// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed delegationpolicystore.Store,
// persisting the §8.3 DelegationPolicy registry to the
// delegation_policies table. It is a drop-in alternative to
// delegationpolicystore.Memory.
//
// spec: §4.2 line 172 — delegation policies are tenant-scoped. Each
// row carries a `tenant_id` column under the standard
// lenny_tenant_guard trigger and lenny_tenant_isolation RLS policy.
// Every operation runs inside a pgtenant transaction that sets
// app.current_tenant to the caller's tenant id (concrete value for
// tenant-admin paths, the AllTenantsSentinel for platform-admin
// reads).
//
// The policy body (the tag-matched Rules, the contentPolicy block, and
// allowSelfRecursion) is stored in a single jsonb column; the
// (tenant_id, name) tuple and the audit timestamps are typed columns.
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

	"github.com/lennylabs/lenny/pkg/gateway/delegationpolicystore"
	"github.com/lennylabs/lenny/pkg/gateway/pgtenant"
)

// Store is the Postgres-backed DelegationPolicy registry. Construct
// with New.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store backed by pool. The pool must point at a
// database that has the migrations/ schema applied.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

var _ delegationpolicystore.Store = (*Store)(nil)

// selectList is the column projection for reads. version is the §15.1
// optimistic-concurrency counter and is read last.
const selectList = `tenant_id, name, policy, created_at, updated_at, deleted_at, version`

// policyBody is the jsonb-serialized §8.3 policy body. The tuple
// (tenant_id, name) and the audit timestamps live in their own
// columns; everything that describes the policy's behavior is carried
// here so a future field addition does not require a migration.
type policyBody struct {
	Rules              []delegationpolicystore.Rule        `json:"rules,omitempty"`
	ContentPolicy      delegationpolicystore.ContentPolicy `json:"contentPolicy"`
	AllowSelfRecursion bool                                `json:"allowSelfRecursion,omitempty"`
	// ScanExportedFilesWeakenedAt is the §8.3 line 181 server-minted
	// transition timestamp persisted on the policy row. The admin
	// Update handler stamps it on `scanExportedFiles: true → false`;
	// the delegation Service reads it at `delegate_task` time to
	// enforce INTERCEPTOR_WEAKENING_COOLDOWN. omitempty keeps the
	// jsonb payload from growing for policies that never weakened.
	// F-8.7.12 / F-13.5.7.
	ScanExportedFilesWeakenedAt *time.Time `json:"scanExportedFilesWeakenedAt,omitempty"`
}

// Create inserts a new delegation-policy row after running the §8.3
// validation. Returns ErrAlreadyExists when (tenant_id, name)
// collides.
func (s *Store) Create(ctx context.Context, p delegationpolicystore.DelegationPolicy) error {
	if err := delegationpolicystore.Validate(p); err != nil {
		return err
	}
	now := time.Now().UTC()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = p.CreatedAt
	}
	// spec: §15.1 line 1207 — every admin resource version starts at 1.
	if p.Version == 0 {
		p.Version = 1
	}
	body, err := bodyJSON(p)
	if err != nil {
		return err
	}
	return pgtenant.InTx(ctx, s.pool, p.TenantID, func(tx pgx.Tx) error {
		_, ierr := tx.Exec(ctx, `INSERT INTO delegation_policies (
			tenant_id, name, policy, created_at, updated_at, deleted_at, version
		) VALUES ($1, $2, $3::jsonb, $4, $5, $6, $7)`,
			p.TenantID, p.Name, body, p.CreatedAt, p.UpdatedAt, pgtenant.NullTime(p.DeletedAt), p.Version)
		var pgErr *pgconn.PgError
		if errors.As(ierr, &pgErr) && pgErr.Code == "23505" {
			return delegationpolicystore.ErrAlreadyExists
		}
		return ierr
	})
}

// Get returns the delegation-policy row keyed by (tenantID, name).
// Soft-deleted rows are returned (callers consult
// DelegationPolicy.IsActive()); a missing row is ErrNotFound.
func (s *Store) Get(ctx context.Context, tenantID, name string) (delegationpolicystore.DelegationPolicy, error) {
	var out delegationpolicystore.DelegationPolicy
	err := s.runRead(ctx, tenantID, func(tx pgx.Tx) error {
		var row pgx.Row
		if tenantID == delegationpolicystore.AllTenantsSentinel {
			row = tx.QueryRow(ctx,
				`SELECT `+selectList+` FROM delegation_policies WHERE name = $1`, name)
		} else {
			row = tx.QueryRow(ctx,
				`SELECT `+selectList+` FROM delegation_policies WHERE name = $1 AND tenant_id = $2`,
				name, tenantID)
		}
		p, err := scanPolicy(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return delegationpolicystore.ErrNotFound
		}
		if err != nil {
			return err
		}
		out = p
		return nil
	})
	if err != nil {
		return delegationpolicystore.DelegationPolicy{}, err
	}
	return out, nil
}

// Update applies mutate to the row under SELECT ... FOR UPDATE,
// re-runs the §8.3 validation, and strictly advances UpdatedAt.
func (s *Store) Update(ctx context.Context, tenantID, name string, mutate func(*delegationpolicystore.DelegationPolicy) error) (delegationpolicystore.DelegationPolicy, error) {
	if tenantID == "" || tenantID == delegationpolicystore.AllTenantsSentinel {
		return delegationpolicystore.DelegationPolicy{},
			errors.New("delegationpolicystore: Update requires a concrete tenant_id")
	}
	var out delegationpolicystore.DelegationPolicy
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+selectList+` FROM delegation_policies
			 WHERE name = $1 AND tenant_id = $2 FOR UPDATE`, name, tenantID)
		p, err := scanPolicy(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return delegationpolicystore.ErrNotFound
		}
		if err != nil {
			return err
		}
		prev := p.UpdatedAt
		if err := mutate(&p); err != nil {
			return err
		}
		p.TenantID = tenantID
		if err := delegationpolicystore.Validate(p); err != nil {
			return err
		}
		p.UpdatedAt = pgtenant.MonotonicNext(prev, time.Now())
		// spec: §15.1 line 1207 — bump the optimistic-concurrency version on
		// every successful Update so the next If-Match precondition compares
		// against the new value.
		p.Version++
		body, err := bodyJSON(p)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE delegation_policies SET
			policy = $3::jsonb, updated_at = $4, deleted_at = $5, version = $6
		WHERE name = $1 AND tenant_id = $2`,
			name, tenantID, body, p.UpdatedAt, pgtenant.NullTime(p.DeletedAt), p.Version); err != nil {
			return err
		}
		out = p
		return nil
	})
	if err != nil {
		return delegationpolicystore.DelegationPolicy{}, err
	}
	return out, nil
}

// List returns the delegation-policy rows for the supplied tenant id
// (or across every tenant under the AllTenantsSentinel), ordered by
// (tenant_id, name). Soft-deleted rows are dropped unless
// filter.IncludeDeleted is set.
func (s *Store) List(ctx context.Context, tenantID string, filter delegationpolicystore.ListFilter) ([]delegationpolicystore.DelegationPolicy, error) {
	var out []delegationpolicystore.DelegationPolicy
	err := s.runRead(ctx, tenantID, func(tx pgx.Tx) error {
		q := `SELECT ` + selectList + ` FROM delegation_policies`
		if !filter.IncludeDeleted {
			q += ` WHERE deleted_at IS NULL`
		}
		q += ` ORDER BY tenant_id, name`

		rows, err := tx.Query(ctx, q)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			p, err := scanPolicy(rows)
			if err != nil {
				return err
			}
			out = append(out, p)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// SoftDelete sets deleted_at on the row. It is idempotent:
// soft-deleting an already-deleted policy is a no-op success.
func (s *Store) SoftDelete(ctx context.Context, tenantID, name string, at time.Time) error {
	if tenantID == "" || tenantID == delegationpolicystore.AllTenantsSentinel {
		return errors.New("delegationpolicystore: SoftDelete requires a concrete tenant_id")
	}
	at = at.UTC().Truncate(time.Microsecond)
	return pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE delegation_policies SET deleted_at = $3, updated_at = $3
			 WHERE name = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
			name, tenantID, at)
		if err != nil {
			return err
		}
		if tag.RowsAffected() > 0 {
			return nil
		}
		var exists bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM delegation_policies
			 WHERE name = $1 AND tenant_id = $2)`, name, tenantID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return delegationpolicystore.ErrNotFound
		}
		return nil
	})
}

// runRead wraps fn in either a tenant-scoped or all-tenants transaction
// depending on tenantID. The AllTenantsSentinel value invokes the §4.2
// platform-admin bypass; concrete tenant ids invoke the per-tenant
// context.
func (s *Store) runRead(ctx context.Context, tenantID string, fn func(pgx.Tx) error) error {
	if tenantID == delegationpolicystore.AllTenantsSentinel {
		return pgtenant.InAllTenants(ctx, s.pool, fn)
	}
	if tenantID == "" {
		return fmt.Errorf("delegationpolicystore: read requires a concrete tenant_id or AllTenantsSentinel")
	}
	return pgtenant.InTx(ctx, s.pool, tenantID, fn)
}

// scanPolicy reads one row in selectList order into a DelegationPolicy.
func scanPolicy(row pgx.Row) (delegationpolicystore.DelegationPolicy, error) {
	var (
		p         delegationpolicystore.DelegationPolicy
		bodyRaw   []byte
		deletedAt *time.Time
	)
	if err := row.Scan(&p.TenantID, &p.Name, &bodyRaw, &p.CreatedAt, &p.UpdatedAt, &deletedAt, &p.Version); err != nil {
		return delegationpolicystore.DelegationPolicy{}, err
	}
	if len(bodyRaw) > 0 {
		var b policyBody
		if err := json.Unmarshal(bodyRaw, &b); err != nil {
			return delegationpolicystore.DelegationPolicy{}, fmt.Errorf("delegationpolicystore: decode policy: %w", err)
		}
		p.Rules = b.Rules
		p.ContentPolicy = b.ContentPolicy
		p.AllowSelfRecursion = b.AllowSelfRecursion
		if b.ScanExportedFilesWeakenedAt != nil {
			p.ScanExportedFilesWeakenedAt = b.ScanExportedFilesWeakenedAt.UTC()
		}
	}
	if deletedAt != nil {
		p.DeletedAt = *deletedAt
	}
	return p, nil
}

// bodyJSON marshals the §8.3 policy body (Rules, ContentPolicy,
// AllowSelfRecursion, and the F-8.7.12 scanExportedFiles weakening
// timestamp) to a JSON string for the policy jsonb column.
func bodyJSON(p delegationpolicystore.DelegationPolicy) (string, error) {
	body := policyBody{
		Rules:              p.Rules,
		ContentPolicy:      p.ContentPolicy,
		AllowSelfRecursion: p.AllowSelfRecursion,
	}
	if !p.ScanExportedFilesWeakenedAt.IsZero() {
		t := p.ScanExportedFilesWeakenedAt.UTC()
		body.ScanExportedFilesWeakenedAt = &t
	}
	b, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("delegationpolicystore: encode policy: %w", err)
	}
	return string(b), nil
}
