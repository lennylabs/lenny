// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed credentialpoolstore.Store,
// persisting the §4.9 per-tenant CredentialPool registry to the
// credential_pools table. It is a drop-in alternative to
// credentialpoolstore.Memory.
//
// credential_pools is tenant-scoped, so every operation runs inside a
// transaction that sets app.current_tenant for the §12.3
// lenny_tenant_guard trigger and the RLS policy. Create and Update run
// credentialpoolstore.Validate so the §4.9 structural invariants hold
// regardless of backend.
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

	"github.com/lennylabs/lenny/pkg/gateway/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/pgtenant"
)

// Store is the Postgres-backed CredentialPool registry. Construct with
// New.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store backed by pool. The pool must point at a
// database that has the migrations/ schema applied.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

var _ credentialpoolstore.Store = (*Store)(nil)

// selectList is the column projection for reads, in the order
// scanPool consumes.
const selectList = `tenant_id, name, provider, credentials,
	assignment_strategy, max_concurrent_sessions, cooldown_on_rate_limit_seconds,
	lease_ttl_seconds, renew_before_buffer_seconds, host_patterns, cache_scope,
	delivery_mode, proxy_dialect, proxy_endpoint,
	created_at, updated_at, deleted_at`

// Create inserts a new credential-pool row after running the §4.9
// validation. It stamps CreatedAt / UpdatedAt when unset, mirroring
// credentialpoolstore.Memory. Returns ErrAlreadyExists when the
// (tenant, name) pair is taken.
func (s *Store) Create(ctx context.Context, p credentialpoolstore.CredentialPool) error {
	if err := credentialpoolstore.Validate(p); err != nil {
		return err
	}
	now := time.Now().UTC()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = p.CreatedAt
	}
	creds, err := credentialsJSON(p.Credentials)
	if err != nil {
		return err
	}
	hosts, err := hostPatternsJSON(p.HostPatterns)
	if err != nil {
		return err
	}
	err = pgtenant.InTx(ctx, s.pool, p.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO credential_pools (
			tenant_id, name, provider, credentials,
			assignment_strategy, max_concurrent_sessions, cooldown_on_rate_limit_seconds,
			lease_ttl_seconds, renew_before_buffer_seconds, host_patterns, cache_scope,
			delivery_mode, proxy_dialect, proxy_endpoint,
			created_at, updated_at, deleted_at
		) VALUES ($1, $2, $3, $4::jsonb, $5, $6, $7, $8, $9, $10::jsonb, $11, $12, $13, $14, $15, $16, $17)`,
			p.TenantID, p.Name, p.Provider, creds,
			p.AssignmentStrategy, p.MaxConcurrentSessions, p.CooldownOnRateLimitSeconds,
			p.LeaseTTLSeconds, p.RenewBeforeBufferSeconds, hosts, p.CacheScope,
			p.DeliveryMode, p.ProxyDialect, p.ProxyEndpoint,
			p.CreatedAt, p.UpdatedAt, pgtenant.NullTime(p.DeletedAt))
		return err
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return credentialpoolstore.ErrAlreadyExists
	}
	return err
}

// Get returns the credential-pool row keyed by (tenantID, name).
// Soft-deleted pools are returned; callers consult
// CredentialPool.IsActive to filter. A cross-tenant miss is
// indistinguishable from a missing row (§4.9 isolation).
func (s *Store) Get(ctx context.Context, tenantID, name string) (credentialpoolstore.CredentialPool, error) {
	var out credentialpoolstore.CredentialPool
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+selectList+` FROM credential_pools WHERE tenant_id = $1 AND name = $2`,
			tenantID, name)
		p, err := scanPool(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return credentialpoolstore.ErrNotFound
		}
		if err != nil {
			return err
		}
		out = p
		return nil
	})
	if err != nil {
		return credentialpoolstore.CredentialPool{}, err
	}
	return out, nil
}

// Update applies mutate to the row under SELECT ... FOR UPDATE,
// re-runs the §4.9 validation, and strictly advances UpdatedAt
// (clamped to the prior value + 1µs, the Postgres timestamptz
// resolution).
func (s *Store) Update(ctx context.Context, tenantID, name string, mutate func(*credentialpoolstore.CredentialPool) error) (credentialpoolstore.CredentialPool, error) {
	var out credentialpoolstore.CredentialPool
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+selectList+` FROM credential_pools WHERE tenant_id = $1 AND name = $2 FOR UPDATE`,
			tenantID, name)
		p, err := scanPool(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return credentialpoolstore.ErrNotFound
		}
		if err != nil {
			return err
		}
		prev := p.UpdatedAt
		if err := mutate(&p); err != nil {
			return err
		}
		if err := credentialpoolstore.Validate(p); err != nil {
			return err
		}
		p.UpdatedAt = pgtenant.MonotonicNext(prev, time.Now())
		creds, err := credentialsJSON(p.Credentials)
		if err != nil {
			return err
		}
		hosts, err := hostPatternsJSON(p.HostPatterns)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE credential_pools SET
			provider = $3, credentials = $4::jsonb, assignment_strategy = $5,
			max_concurrent_sessions = $6, cooldown_on_rate_limit_seconds = $7,
			lease_ttl_seconds = $8, renew_before_buffer_seconds = $9,
			host_patterns = $10::jsonb, cache_scope = $11,
			delivery_mode = $14, proxy_dialect = $15, proxy_endpoint = $16,
			updated_at = $12, deleted_at = $13
		WHERE tenant_id = $1 AND name = $2`,
			tenantID, name, p.Provider, creds, p.AssignmentStrategy,
			p.MaxConcurrentSessions, p.CooldownOnRateLimitSeconds,
			p.LeaseTTLSeconds, p.RenewBeforeBufferSeconds, hosts, p.CacheScope,
			p.UpdatedAt, pgtenant.NullTime(p.DeletedAt),
			p.DeliveryMode, p.ProxyDialect, p.ProxyEndpoint); err != nil {
			return err
		}
		out = p
		return nil
	})
	if err != nil {
		return credentialpoolstore.CredentialPool{}, err
	}
	return out, nil
}

// List returns the tenant's credential pools name-ascending.
// Soft-deleted rows are dropped unless filter.IncludeDeleted is set.
func (s *Store) List(ctx context.Context, tenantID string, filter credentialpoolstore.ListFilter) ([]credentialpoolstore.CredentialPool, error) {
	q := `SELECT ` + selectList + ` FROM credential_pools WHERE tenant_id = $1`
	if !filter.IncludeDeleted {
		q += ` AND deleted_at IS NULL`
	}
	q += ` ORDER BY name`

	var out []credentialpoolstore.CredentialPool
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, q, tenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			p, err := scanPool(rows)
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
// soft-deleting an already-deleted pool is a no-op success.
func (s *Store) SoftDelete(ctx context.Context, tenantID, name string, at time.Time) error {
	return pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE credential_pools SET deleted_at = $3, updated_at = $3
			 WHERE tenant_id = $1 AND name = $2 AND deleted_at IS NULL`,
			tenantID, name, at)
		if err != nil {
			return err
		}
		if tag.RowsAffected() > 0 {
			return nil
		}
		var exists bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM credential_pools WHERE tenant_id = $1 AND name = $2)`,
			tenantID, name).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return credentialpoolstore.ErrNotFound
		}
		return nil
	})
}

// scanPool reads one row in selectList order into a CredentialPool.
func scanPool(row pgx.Row) (credentialpoolstore.CredentialPool, error) {
	var (
		p                  credentialpoolstore.CredentialPool
		credsRaw, hostsRaw []byte
		deletedAt          *time.Time
	)
	if err := row.Scan(
		&p.TenantID, &p.Name, &p.Provider, &credsRaw,
		&p.AssignmentStrategy, &p.MaxConcurrentSessions, &p.CooldownOnRateLimitSeconds,
		&p.LeaseTTLSeconds, &p.RenewBeforeBufferSeconds, &hostsRaw, &p.CacheScope,
		&p.DeliveryMode, &p.ProxyDialect, &p.ProxyEndpoint,
		&p.CreatedAt, &p.UpdatedAt, &deletedAt,
	); err != nil {
		return credentialpoolstore.CredentialPool{}, err
	}
	if len(credsRaw) > 0 {
		if err := json.Unmarshal(credsRaw, &p.Credentials); err != nil {
			return credentialpoolstore.CredentialPool{}, fmt.Errorf("credentialpoolstore: decode credentials: %w", err)
		}
	}
	if len(hostsRaw) > 0 {
		if err := json.Unmarshal(hostsRaw, &p.HostPatterns); err != nil {
			return credentialpoolstore.CredentialPool{}, fmt.Errorf("credentialpoolstore: decode host patterns: %w", err)
		}
	}
	if deletedAt != nil {
		p.DeletedAt = *deletedAt
	}
	return p, nil
}

// credentialsJSON marshals the §4.9 credential set for the credentials
// jsonb column. A nil slice becomes an empty JSON array so the column
// stays a well-formed array and the scan path reads it back as nil.
func credentialsJSON(creds []credentialpoolstore.Credential) (string, error) {
	if len(creds) == 0 {
		return "[]", nil
	}
	b, err := json.Marshal(creds)
	if err != nil {
		return "", fmt.Errorf("credentialpoolstore: encode credentials: %w", err)
	}
	return string(b), nil
}

// hostPatternsJSON marshals the §4.9 VCS host-pattern list for the
// host_patterns jsonb column. A nil slice becomes an empty JSON array.
func hostPatternsJSON(patterns []string) (string, error) {
	if len(patterns) == 0 {
		return "[]", nil
	}
	b, err := json.Marshal(patterns)
	if err != nil {
		return "", fmt.Errorf("credentialpoolstore: encode host patterns: %w", err)
	}
	return string(b), nil
}
