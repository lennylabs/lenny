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
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/kms/envelope"
)

// Store is the Postgres-backed tenant registry. Construct with New.
type Store struct {
	pool *pgxpool.Pool
	// kms envelope-encrypts the §12.8 erasure_salt at rest. A nil
	// provider rejects any write that carries a non-empty salt: §12.8
	// line 845 forbids storing the salt in plaintext. F-12.8.5.
	kms kms.Provider
}

// Option configures a Store at construction.
type Option func(*Store)

// WithKMS wires the §12.8 line 845 envelope-encryption provider used to
// seal the per-tenant erasure_salt at rest. Without it, a tenant write
// carrying a non-empty ErasureSalt is rejected rather than persisted in
// plaintext. F-12.8.5.
func WithKMS(p kms.Provider) Option { return func(s *Store) { s.kms = p } }

// SetSaltKMS injects the §12.8 line 845 envelope-encryption provider after
// construction. It is a startup-only wiring hook for the gateway binary,
// where the KMS provider is resolved after the tenant store is built; it
// must be called before any erasure-salt read or write and is not safe to
// call concurrently with store operations. F-12.8.5.
func (s *Store) SetSaltKMS(p kms.Provider) { s.kms = p }

// New returns a Store backed by pool. The pool must point at a
// database that has the migrations/ schema applied.
func New(pool *pgxpool.Pool, opts ...Option) *Store {
	s := &Store{pool: pool}
	for _, o := range opts {
		o(s)
	}
	return s
}

// saltKEKAlias is the per-tenant KEK alias used to envelope-encrypt the
// §12.8 erasure_salt, matching the credential and connector-credential
// stores so a tenant's KEK wraps all of its T4 secret material.
func saltKEKAlias(tenantID string) string { return "tenant:" + tenantID }

// sealSalt envelope-encrypts a §12.8 erasure salt for the erasure_salt
// column. An empty salt seals to a NULL column — the §12.8 line 850
// destroyed state. A non-empty salt with no KMS provider is rejected:
// §12.8 line 845 forbids storing the salt in plaintext. F-12.8.5.
func (s *Store) sealSalt(ctx context.Context, tenantID string, salt []byte) ([]byte, error) {
	if len(salt) == 0 {
		return nil, nil
	}
	if s.kms == nil {
		return nil, errors.New("tenantstore/pgstore: erasure_salt requires a KMS provider; §12.8 line 845 forbids plaintext salt storage")
	}
	c, err := envelope.New(s.kms, saltKEKAlias(tenantID))
	if err != nil {
		return nil, err
	}
	sealed, err := c.Seal(ctx, salt)
	if err != nil {
		return nil, fmt.Errorf("tenantstore/pgstore: seal erasure_salt: %w", err)
	}
	return envelope.Encode(sealed)
}

// openSalt reverses sealSalt. A NULL/empty blob is the §12.8 line 850
// destroyed state and opens to a nil salt. F-12.8.5.
func (s *Store) openSalt(ctx context.Context, tenantID string, blob []byte) ([]byte, error) {
	if len(blob) == 0 {
		return nil, nil
	}
	if s.kms == nil {
		return nil, errors.New("tenantstore/pgstore: erasure_salt is present but no KMS provider is wired to decrypt it")
	}
	c, err := envelope.New(s.kms, saltKEKAlias(tenantID))
	if err != nil {
		return nil, err
	}
	sealed, err := envelope.Decode(blob)
	if err != nil {
		return nil, fmt.Errorf("tenantstore/pgstore: decode erasure_salt: %w", err)
	}
	return c.Open(ctx, sealed)
}

var (
	_ tenantstore.Store   = (*Store)(nil)
	_ auth.TenantRegistry = (*Store)(nil)
)

const selectList = `id, display_name, compliance_profile, data_residency_region,
	workspace_tier, max_concurrent_sessions, storage_quota_bytes,
	created_at, updated_at, deleted_at, min_isolation_profile,
	elicitation_content_integrity, billing_erasure_policy, no_environment_policy,
	experiment_targeting, credential_policy, rbac_config,
	token_quota_per_window, quota_reset_period, state, gc_priority,
	erasure_salt, version`

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

// gcPriorityOrDefault maps an empty §12.5 GCPriority to the `normal`
// default so a write never sends the empty string the gc_priority CHECK
// constraint (the column is NOT NULL DEFAULT 'normal') would reject.
// spec: §12.5 line 317.
func gcPriorityOrDefault(s string) string {
	if s == "" {
		return tenantstore.GCPriorityNormal
	}
	return s
}

// updateState maps an empty §12.8 TenantState to the `active` default
// so an Update never writes the empty string the state CHECK constraint
// rejects. A row read through scanTenant always carries a non-empty
// state (the column is NOT NULL DEFAULT 'active'), so this only guards
// a caller that zeroed the field.
func updateState(s string) string {
	if s == "" {
		return tenantstore.TenantStateActive
	}
	return s
}

// marshalRBACConfig encodes a tenant's §10.6 RBACConfig for the jsonb
// rbac_config column. A zero config encodes to the JSON object {}.
func marshalRBACConfig(c tenantstore.RBACConfig) ([]byte, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("tenantstore: encode rbacConfig: %w", err)
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
	rbacConfig, err := marshalRBACConfig(t.RBACConfig)
	if err != nil {
		return err
	}
	// §12.8: a new tenant is born `active`; the deletion controller
	// advances the state from there.
	state := t.State
	if state == "" {
		state = tenantstore.TenantStateActive
	}
	// §12.8 line 845: a tenant carrying an erasure_salt at create time
	// (rare; the salt is normally minted during an erasure job) has it
	// envelope-encrypted before persist.
	saltBlob, err := s.sealSalt(ctx, t.ID, t.ErasureSalt)
	if err != nil {
		return err
	}
	// spec: §15.1 line 1207 — a new resource is born at version 1.
	version := t.Version
	if version == 0 {
		version = 1
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO tenants (
		id, display_name, compliance_profile, data_residency_region,
		workspace_tier, max_concurrent_sessions, storage_quota_bytes,
		genesis_nonce, created_at, updated_at, deleted_at, min_isolation_profile,
		elicitation_content_integrity, billing_erasure_policy, no_environment_policy,
		experiment_targeting, credential_policy, rbac_config,
		token_quota_per_window, quota_reset_period, state, gc_priority, erasure_salt, version
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24)`,
		t.ID, t.DisplayName, t.ComplianceProfile, t.DataResidencyRegion,
		t.WorkspaceTier, t.MaxConcurrentSessions, t.StorageQuotaBytes,
		nonce, t.CreatedAt, t.UpdatedAt, pgtenant.NullTime(t.DeletedAt), t.MinIsolationProfile,
		t.ElicitationContentIntegrity, t.BillingErasurePolicy, t.NoEnvironmentPolicy,
		targeting, credPolicy, rbacConfig,
		t.TokenQuotaPerWindow, t.QuotaResetPeriod, state, gcPriorityOrDefault(t.GCPriority), saltBlob, version)
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
	t, saltBlob, err := scanTenant(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return tenantstore.Tenant{}, tenantstore.ErrNotFound
	}
	if err != nil {
		return tenantstore.Tenant{}, err
	}
	if t.ErasureSalt, err = s.openSalt(ctx, t.ID, saltBlob); err != nil {
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
	t, saltBlob, err := scanTenant(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return tenantstore.Tenant{}, tenantstore.ErrNotFound
	}
	if err != nil {
		return tenantstore.Tenant{}, err
	}
	if t.ErasureSalt, err = s.openSalt(ctx, t.ID, saltBlob); err != nil {
		return tenantstore.Tenant{}, err
	}
	prev := t.UpdatedAt
	if err := mutate(&t); err != nil {
		return tenantstore.Tenant{}, err
	}
	t.UpdatedAt = pgtenant.MonotonicNext(prev, time.Now())
	// spec: §15.1 line 1207 — bump the entity-tag version on every write.
	t.Version++
	targeting, err := marshalTargeting(t.ExperimentTargeting)
	if err != nil {
		return tenantstore.Tenant{}, err
	}
	credPolicy, err := marshalCredentialPolicy(t.CredentialPolicy)
	if err != nil {
		return tenantstore.Tenant{}, err
	}
	rbacConfig, err := marshalRBACConfig(t.RBACConfig)
	if err != nil {
		return tenantstore.Tenant{}, err
	}
	// §12.8 line 845/850: re-seal the (possibly mutated) erasure_salt;
	// a destroyed salt (nil) writes NULL, removing the KMS-wrapped
	// ciphertext from the row.
	newSaltBlob, err := s.sealSalt(ctx, id, t.ErasureSalt)
	if err != nil {
		return tenantstore.Tenant{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE tenants SET
		display_name = $2, compliance_profile = $3, data_residency_region = $4,
		workspace_tier = $5, max_concurrent_sessions = $6, storage_quota_bytes = $7,
		updated_at = $8, deleted_at = $9, min_isolation_profile = $10,
		elicitation_content_integrity = $11, billing_erasure_policy = $12,
		no_environment_policy = $13, experiment_targeting = $14,
		credential_policy = $15, rbac_config = $16,
		token_quota_per_window = $17, quota_reset_period = $18,
		state = $19, gc_priority = $20, erasure_salt = $21, version = $22 WHERE id = $1`,
		id, t.DisplayName, t.ComplianceProfile, t.DataResidencyRegion,
		t.WorkspaceTier, t.MaxConcurrentSessions, t.StorageQuotaBytes,
		t.UpdatedAt, pgtenant.NullTime(t.DeletedAt), t.MinIsolationProfile,
		t.ElicitationContentIntegrity, t.BillingErasurePolicy, t.NoEnvironmentPolicy,
		targeting, credPolicy, rbacConfig,
		t.TokenQuotaPerWindow, t.QuotaResetPeriod, updateState(t.State),
		gcPriorityOrDefault(t.GCPriority), newSaltBlob, t.Version); err != nil {
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
		// §12.8 line 847: only the erasure job reads the salt, so List
		// (the admin tenant inventory) leaves ErasureSalt unopened rather
		// than running a KMS decrypt per row.
		t, _, err := scanTenant(rows)
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
	// §12.8 Phase 6: a soft-deleted tenant is a tombstone, so its
	// TenantState advances to `deleted` alongside the deleted_at marker.
	// spec: §15.1 line 1207 — a soft-delete is a write, so it bumps the tag.
	tag, err := s.pool.Exec(ctx,
		`UPDATE tenants SET deleted_at = $2, updated_at = $2, state = 'deleted',
		 version = version + 1 WHERE id = $1 AND deleted_at IS NULL`, id, at)
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

// scanTenant reads one row in selectList order into a Tenant. The raw
// (still envelope-encrypted) erasure_salt blob is returned separately so
// the caller can openSalt it with the request context; ErasureSalt on
// the returned Tenant is left nil. F-12.8.5.
func scanTenant(row pgx.Row) (tenantstore.Tenant, []byte, error) {
	var (
		t          tenantstore.Tenant
		deletedAt  *time.Time
		targeting  []byte
		credPolicy []byte
		rbacConfig []byte
		saltBlob   []byte
	)
	if err := row.Scan(
		&t.ID, &t.DisplayName, &t.ComplianceProfile, &t.DataResidencyRegion,
		&t.WorkspaceTier, &t.MaxConcurrentSessions, &t.StorageQuotaBytes,
		&t.CreatedAt, &t.UpdatedAt, &deletedAt, &t.MinIsolationProfile,
		&t.ElicitationContentIntegrity, &t.BillingErasurePolicy, &t.NoEnvironmentPolicy,
		&targeting, &credPolicy, &rbacConfig,
		&t.TokenQuotaPerWindow, &t.QuotaResetPeriod, &t.State, &t.GCPriority, &saltBlob,
		&t.Version,
	); err != nil {
		return tenantstore.Tenant{}, nil, err
	}
	if deletedAt != nil {
		t.DeletedAt = *deletedAt
	}
	if len(targeting) > 0 {
		if err := json.Unmarshal(targeting, &t.ExperimentTargeting); err != nil {
			return tenantstore.Tenant{}, nil, fmt.Errorf("tenantstore: decode experimentTargeting: %w", err)
		}
	}
	if len(credPolicy) > 0 {
		if err := json.Unmarshal(credPolicy, &t.CredentialPolicy); err != nil {
			return tenantstore.Tenant{}, nil, fmt.Errorf("tenantstore: decode credentialPolicy: %w", err)
		}
	}
	if len(rbacConfig) > 0 {
		if err := json.Unmarshal(rbacConfig, &t.RBACConfig); err != nil {
			return tenantstore.Tenant{}, nil, fmt.Errorf("tenantstore: decode rbacConfig: %w", err)
		}
	}
	return t, saltBlob, nil
}
