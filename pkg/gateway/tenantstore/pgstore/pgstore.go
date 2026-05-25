// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed tenantstore.Store, persisting
// the platform tenant registry to the tenants table. It is a drop-in
// alternative to the in-memory tenantstore.Memory and, like it,
// satisfies auth.TenantRegistry so the §10.2 tenant-claim extractor
// can resolve tenants against it.
//
// The tenants table is platform-global (§12.6): it is the RLS anchor,
// not an RLS-protected table, so operations here run as plain
// queries without an app.current_tenant context.
package pgstore

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/experiment"
	"github.com/lennylabs/lenny/pkg/gateway/pgtenant"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// Store is the Postgres-backed tenant registry. Construct with New.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store backed by pool. The pool must point at a
// database that has the migrations/ schema applied.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

var (
	_ tenantstore.Store   = (*Store)(nil)
	_ auth.TenantRegistry = (*Store)(nil)
)

const selectList = `id, display_name, compliance_profile, data_residency_region,
	workspace_tier, max_concurrent_sessions, storage_quota_bytes,
	created_at, updated_at, deleted_at, min_isolation_profile,
	elicitation_content_integrity, billing_erasure_policy, no_environment_policy,
	experiment_targeting, credential_policy`

// marshalTargeting encodes a tenant's §10.7 experimentTargeting block
// for the jsonb experiment_targeting column. A zero config encodes to
// the JSON object {}.
func marshalTargeting(c experiment.TargetingConfig) ([]byte, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("tenantstore: encode experimentTargeting: %w", err)
	}
	return b, nil
}

// marshalCredentialPolicy encodes a tenant's §4.9 credentialPolicy for
// the jsonb credential_policy column. A zero policy encodes to the JSON
// object {}.
func marshalCredentialPolicy(c credential.CredentialPolicy) ([]byte, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("tenantstore: encode credentialPolicy: %w", err)
	}
	return b, nil
}

// Create inserts a new tenant row. The §11.7 per-tenant audit genesis
// nonce is generated here, at tenant-creation time. Returns
// ErrAlreadyExists when the id is taken.
func (s *Store) Create(ctx context.Context, t tenantstore.Tenant) error {
	if err := auth.ValidateTenantID(t.ID); err != nil {
		return err
	}
	now := time.Now().UTC()
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	if t.UpdatedAt.IsZero() {
		t.UpdatedAt = t.CreatedAt
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("tenantstore: generate genesis nonce: %w", err)
	}
	targeting, err := marshalTargeting(t.ExperimentTargeting)
	if err != nil {
		return err
	}
	credPolicy, err := marshalCredentialPolicy(t.CredentialPolicy)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO tenants (
		id, display_name, compliance_profile, data_residency_region,
		workspace_tier, max_concurrent_sessions, storage_quota_bytes,
		genesis_nonce, created_at, updated_at, deleted_at, min_isolation_profile,
		elicitation_content_integrity, billing_erasure_policy, no_environment_policy,
		experiment_targeting, credential_policy
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)`,
		t.ID, t.DisplayName, t.ComplianceProfile, t.DataResidencyRegion,
		t.WorkspaceTier, t.MaxConcurrentSessions, t.StorageQuotaBytes,
		nonce, t.CreatedAt, t.UpdatedAt, pgtenant.NullTime(t.DeletedAt), t.MinIsolationProfile,
		t.ElicitationContentIntegrity, t.BillingErasurePolicy, t.NoEnvironmentPolicy,
		targeting, credPolicy)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return tenantstore.ErrAlreadyExists
	}
	return err
}

// Get returns the tenant row keyed by id. Soft-deleted rows are
// returned (callers consult Tenant.IsActive()); a missing row is
// ErrNotFound.
func (s *Store) Get(ctx context.Context, id string) (tenantstore.Tenant, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+selectList+` FROM tenants WHERE id = $1`, id)
	t, err := scanTenant(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return tenantstore.Tenant{}, tenantstore.ErrNotFound
	}
	if err != nil {
		return tenantstore.Tenant{}, err
	}
	return t, nil
}

// Update applies mutate to the row under SELECT ... FOR UPDATE.
// UpdatedAt strictly advances on every successful Update.
func (s *Store) Update(ctx context.Context, id string, mutate func(*tenantstore.Tenant) error) (tenantstore.Tenant, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return tenantstore.Tenant{}, fmt.Errorf("tenantstore: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	row := tx.QueryRow(ctx,
		`SELECT `+selectList+` FROM tenants WHERE id = $1 FOR UPDATE`, id)
	t, err := scanTenant(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return tenantstore.Tenant{}, tenantstore.ErrNotFound
	}
	if err != nil {
		return tenantstore.Tenant{}, err
	}
	prev := t.UpdatedAt
	if err := mutate(&t); err != nil {
		return tenantstore.Tenant{}, err
	}
	t.UpdatedAt = pgtenant.MonotonicNext(prev, time.Now())
	targeting, err := marshalTargeting(t.ExperimentTargeting)
	if err != nil {
		return tenantstore.Tenant{}, err
	}
	credPolicy, err := marshalCredentialPolicy(t.CredentialPolicy)
	if err != nil {
		return tenantstore.Tenant{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE tenants SET
		display_name = $2, compliance_profile = $3, data_residency_region = $4,
		workspace_tier = $5, max_concurrent_sessions = $6, storage_quota_bytes = $7,
		updated_at = $8, deleted_at = $9, min_isolation_profile = $10,
		elicitation_content_integrity = $11, billing_erasure_policy = $12,
		no_environment_policy = $13, experiment_targeting = $14,
		credential_policy = $15 WHERE id = $1`,
		id, t.DisplayName, t.ComplianceProfile, t.DataResidencyRegion,
		t.WorkspaceTier, t.MaxConcurrentSessions, t.StorageQuotaBytes,
		t.UpdatedAt, pgtenant.NullTime(t.DeletedAt), t.MinIsolationProfile,
		t.ElicitationContentIntegrity, t.BillingErasurePolicy, t.NoEnvironmentPolicy,
		targeting, credPolicy); err != nil {
		return tenantstore.Tenant{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return tenantstore.Tenant{}, fmt.Errorf("tenantstore: commit: %w", err)
	}
	return t, nil
}

// List returns every tenant row created-at-descending. Soft-deleted
// rows are dropped unless filter.IncludeDeleted is set.
func (s *Store) List(ctx context.Context, filter tenantstore.ListFilter) ([]tenantstore.Tenant, error) {
	q := `SELECT ` + selectList + ` FROM tenants`
	if !filter.IncludeDeleted {
		q += ` WHERE deleted_at IS NULL`
	}
	q += ` ORDER BY created_at DESC, id`

	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []tenantstore.Tenant
	for rows.Next() {
		t, err := scanTenant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// SoftDelete sets deleted_at on the row per §12.8. It is idempotent:
// soft-deleting an already-deleted tenant is a no-op success.
func (s *Store) SoftDelete(ctx context.Context, id string, at time.Time) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE tenants SET deleted_at = $2, updated_at = $2
		 WHERE id = $1 AND deleted_at IS NULL`, id, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	// No row updated: either the tenant is absent, or it was already
	// soft-deleted (an idempotent no-op).
	var exists bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM tenants WHERE id = $1)`, id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return tenantstore.ErrNotFound
	}
	return nil
}

// IsRegistered implements auth.TenantRegistry. A soft-deleted tenant
// reports false so the §10.2 tenant-claim extractor rejects requests
// against it with TENANT_NOT_FOUND.
func (s *Store) IsRegistered(tenantID string) (bool, error) {
	var active bool
	err := s.pool.QueryRow(context.Background(),
		`SELECT deleted_at IS NULL FROM tenants WHERE id = $1`, tenantID).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return active, nil
}

// scanTenant reads one row in selectList order into a Tenant.
func scanTenant(row pgx.Row) (tenantstore.Tenant, error) {
	var (
		t          tenantstore.Tenant
		deletedAt  *time.Time
		targeting  []byte
		credPolicy []byte
	)
	if err := row.Scan(
		&t.ID, &t.DisplayName, &t.ComplianceProfile, &t.DataResidencyRegion,
		&t.WorkspaceTier, &t.MaxConcurrentSessions, &t.StorageQuotaBytes,
		&t.CreatedAt, &t.UpdatedAt, &deletedAt, &t.MinIsolationProfile,
		&t.ElicitationContentIntegrity, &t.BillingErasurePolicy, &t.NoEnvironmentPolicy,
		&targeting, &credPolicy,
	); err != nil {
		return tenantstore.Tenant{}, err
	}
	if deletedAt != nil {
		t.DeletedAt = *deletedAt
	}
	if len(targeting) > 0 {
		if err := json.Unmarshal(targeting, &t.ExperimentTargeting); err != nil {
			return tenantstore.Tenant{}, fmt.Errorf("tenantstore: decode experimentTargeting: %w", err)
		}
	}
	if len(credPolicy) > 0 {
		if err := json.Unmarshal(credPolicy, &t.CredentialPolicy); err != nil {
			return tenantstore.Tenant{}, fmt.Errorf("tenantstore: decode credentialPolicy: %w", err)
		}
	}
	return t, nil
}
