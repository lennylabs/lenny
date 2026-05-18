// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed delegationpolicystore.Store,
// persisting the §8.3 DelegationPolicy registry to the
// delegation_policies table. It is a drop-in alternative to
// delegationpolicystore.Memory.
//
// delegation_policies is platform-global (§8.3): a policy is
// referenced by runtimes and delegation leases across tenants, so the
// table carries no tenant_id and operations run as plain queries
// without an app.current_tenant context. Create and Update run
// delegationpolicystore.Validate so the §8.3 structural invariants —
// including the scanExportedFiles / interceptorRef dependency — hold
// regardless of backend.
//
// The policy body (the tag-matched Rules, the contentPolicy block, and
// allowSelfRecursion) is stored in a single jsonb column; the policy
// name and the audit timestamps are typed columns.
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

// selectList is the column projection for reads.
const selectList = `name, policy, created_at, updated_at, deleted_at`

// policyBody is the jsonb-serialized §8.3 policy body. The policy name
// and audit timestamps live in their own columns; everything that
// describes the policy's behavior is carried here so a future field
// addition does not require a migration.
type policyBody struct {
	Rules              []delegationpolicystore.Rule        `json:"rules,omitempty"`
	ContentPolicy      delegationpolicystore.ContentPolicy `json:"contentPolicy"`
	AllowSelfRecursion bool                                `json:"allowSelfRecursion,omitempty"`
}

// Create inserts a new delegation-policy row after running the §8.3
// validation. Returns ErrAlreadyExists when the name is taken.
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
	body, err := bodyJSON(p)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO delegation_policies (
		name, policy, created_at, updated_at, deleted_at
	) VALUES ($1, $2::jsonb, $3, $4, $5)`,
		p.Name, body, p.CreatedAt, p.UpdatedAt, pgtenant.NullTime(p.DeletedAt))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return delegationpolicystore.ErrAlreadyExists
	}
	return err
}

// Get returns the delegation-policy row keyed by name. Soft-deleted
// rows are returned (callers consult DelegationPolicy.IsActive()); a
// missing row is ErrNotFound.
func (s *Store) Get(ctx context.Context, name string) (delegationpolicystore.DelegationPolicy, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+selectList+` FROM delegation_policies WHERE name = $1`, name)
	p, err := scanPolicy(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return delegationpolicystore.DelegationPolicy{}, delegationpolicystore.ErrNotFound
	}
	if err != nil {
		return delegationpolicystore.DelegationPolicy{}, err
	}
	return p, nil
}

// Update applies mutate to the row under SELECT ... FOR UPDATE,
// re-runs the §8.3 validation, and strictly advances UpdatedAt.
func (s *Store) Update(ctx context.Context, name string, mutate func(*delegationpolicystore.DelegationPolicy) error) (delegationpolicystore.DelegationPolicy, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return delegationpolicystore.DelegationPolicy{}, fmt.Errorf("delegationpolicystore: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	row := tx.QueryRow(ctx,
		`SELECT `+selectList+` FROM delegation_policies WHERE name = $1 FOR UPDATE`, name)
	p, err := scanPolicy(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return delegationpolicystore.DelegationPolicy{}, delegationpolicystore.ErrNotFound
	}
	if err != nil {
		return delegationpolicystore.DelegationPolicy{}, err
	}
	prev := p.UpdatedAt
	if err := mutate(&p); err != nil {
		return delegationpolicystore.DelegationPolicy{}, err
	}
	if err := delegationpolicystore.Validate(p); err != nil {
		return delegationpolicystore.DelegationPolicy{}, err
	}
	p.UpdatedAt = pgtenant.MonotonicNext(prev, time.Now())
	body, err := bodyJSON(p)
	if err != nil {
		return delegationpolicystore.DelegationPolicy{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE delegation_policies SET
		policy = $2::jsonb, updated_at = $3, deleted_at = $4
	WHERE name = $1`,
		name, body, p.UpdatedAt, pgtenant.NullTime(p.DeletedAt)); err != nil {
		return delegationpolicystore.DelegationPolicy{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return delegationpolicystore.DelegationPolicy{}, fmt.Errorf("delegationpolicystore: commit: %w", err)
	}
	return p, nil
}

// List returns the delegation-policy rows name-ascending. Soft-deleted
// rows are dropped unless filter.IncludeDeleted is set.
func (s *Store) List(ctx context.Context, filter delegationpolicystore.ListFilter) ([]delegationpolicystore.DelegationPolicy, error) {
	q := `SELECT ` + selectList + ` FROM delegation_policies`
	if !filter.IncludeDeleted {
		q += ` WHERE deleted_at IS NULL`
	}
	q += ` ORDER BY name`

	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []delegationpolicystore.DelegationPolicy
	for rows.Next() {
		p, err := scanPolicy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SoftDelete sets deleted_at on the row. It is idempotent:
// soft-deleting an already-deleted policy is a no-op success. The
// timestamp is truncated to the Postgres timestamptz microsecond
// resolution so a read-back compares equal to the supplied instant.
func (s *Store) SoftDelete(ctx context.Context, name string, at time.Time) error {
	at = at.UTC().Truncate(time.Microsecond)
	tag, err := s.pool.Exec(ctx,
		`UPDATE delegation_policies SET deleted_at = $2, updated_at = $2
		 WHERE name = $1 AND deleted_at IS NULL`, name, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	var exists bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM delegation_policies WHERE name = $1)`, name).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return delegationpolicystore.ErrNotFound
	}
	return nil
}

// scanPolicy reads one row in selectList order into a DelegationPolicy.
func scanPolicy(row pgx.Row) (delegationpolicystore.DelegationPolicy, error) {
	var (
		p         delegationpolicystore.DelegationPolicy
		bodyRaw   []byte
		deletedAt *time.Time
	)
	if err := row.Scan(&p.Name, &bodyRaw, &p.CreatedAt, &p.UpdatedAt, &deletedAt); err != nil {
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
	}
	if deletedAt != nil {
		p.DeletedAt = *deletedAt
	}
	return p, nil
}

// bodyJSON marshals the §8.3 policy body (Rules, ContentPolicy,
// AllowSelfRecursion) to a JSON string for the policy jsonb column.
func bodyJSON(p delegationpolicystore.DelegationPolicy) (string, error) {
	b, err := json.Marshal(policyBody{
		Rules:              p.Rules,
		ContentPolicy:      p.ContentPolicy,
		AllowSelfRecursion: p.AllowSelfRecursion,
	})
	if err != nil {
		return "", fmt.Errorf("delegationpolicystore: encode policy: %w", err)
	}
	return string(b), nil
}
