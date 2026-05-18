// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed credentialstore.Store,
// persisting the §4.9 / §15.1 end-user credential registry to the
// credentials table. It is a drop-in alternative to
// credentialstore.Memory.
//
// credentials is tenant-scoped, so every operation runs inside a
// transaction that sets app.current_tenant for the §12.3
// lenny_tenant_guard trigger and the RLS policy.
//
// The credentialstore.Credential struct has no updated_at field; the
// credentials table carries an updated_at column (the tenant-scoped
// table convention) that every mutation advances with
// pgtenant.MonotonicNext, but the value is not surfaced on the struct.
//
// The store never returns secret material on the wire; the secret
// column is read back so the REST handlers can project a secret-free
// payload, mirroring credentialstore.Memory. KMS-envelope encryption
// of the secret column is a later phase.
package pgstore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentialstore"
	"github.com/lennylabs/lenny/pkg/gateway/pgtenant"
)

// Store is the Postgres-backed credential registry. Construct with New.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store backed by pool. The pool must point at a
// database that has the migrations/ schema applied.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

var _ credentialstore.Store = (*Store)(nil)

// selectList is the column set scanCredential reads. updated_at is
// last: scanCredential reads it into a local so the strictly-advancing
// value is available without a Credential field for it.
const selectList = `tenant_id, ref, user_id, provider, secret, status,
	created_at, rotated_at, revoked_at, updated_at`

// Register stores (or replaces) the credential for the
// (tenant, user, provider) triple, returning the credential_ref.
// Re-registering the same triple replaces the secret, refreshes
// RotatedAt, and clears the revoked state per §15.1, mirroring
// credentialstore.Memory.
func (s *Store) Register(ctx context.Context, tenantID, userID string, provider credential.Provider, secret string) (credentialstore.Credential, error) {
	if !provider.IsValid() {
		return credentialstore.Credential{}, errors.New("credentialstore: unknown provider " + string(provider))
	}
	var out credentialstore.Credential
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		now := time.Now().UTC().Truncate(time.Microsecond)

		// Replace path: the (tenant, user, provider) triple already
		// holds a credential. Lock the row, refresh the secret, and
		// clear the revoked state, exactly like the in-memory store.
		existing := tx.QueryRow(ctx,
			`SELECT `+selectList+` FROM credentials
			 WHERE tenant_id = $1 AND user_id = $2 AND provider = $3 FOR UPDATE`,
			tenantID, userID, string(provider))
		c, prevUpdated, err := scanCredential(existing)
		if err == nil {
			c.Secret = secret
			c.Status = credentialstore.StatusActive
			c.RotatedAt = now
			c.RevokedAt = time.Time{}
			updatedAt := pgtenant.MonotonicNext(prevUpdated, now)
			if _, err := tx.Exec(ctx, `UPDATE credentials SET
				secret = $3, status = $4, rotated_at = $5,
				revoked_at = $6, updated_at = $7
			WHERE tenant_id = $1 AND ref = $2`,
				tenantID, c.Ref, c.Secret, string(c.Status), c.RotatedAt,
				pgtenant.NullTime(c.RevokedAt), updatedAt); err != nil {
				return err
			}
			out = c
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		// Insert path: mint a fresh opaque ref and persist a new row.
		c = credentialstore.Credential{
			Ref:       "cred_" + randomHex(8),
			TenantID:  tenantID,
			UserID:    userID,
			Provider:  provider,
			Secret:    secret,
			Status:    credentialstore.StatusActive,
			CreatedAt: now,
		}
		if _, err := tx.Exec(ctx, `INSERT INTO credentials (
			tenant_id, ref, user_id, provider, secret, status,
			created_at, rotated_at, revoked_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			c.TenantID, c.Ref, c.UserID, string(c.Provider), c.Secret, string(c.Status),
			c.CreatedAt, pgtenant.NullTime(c.RotatedAt),
			pgtenant.NullTime(c.RevokedAt), now); err != nil {
			return err
		}
		out = c
		return nil
	})
	if err != nil {
		return credentialstore.Credential{}, err
	}
	return out, nil
}

// Get returns the credential keyed by (tenantID, ref). A cross-tenant
// miss is indistinguishable from a missing row.
func (s *Store) Get(ctx context.Context, tenantID, ref string) (credentialstore.Credential, error) {
	var out credentialstore.Credential
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+selectList+` FROM credentials WHERE tenant_id = $1 AND ref = $2`,
			tenantID, ref)
		c, _, err := scanCredential(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return credentialstore.ErrNotFound
		}
		if err != nil {
			return err
		}
		out = c
		return nil
	})
	if err != nil {
		return credentialstore.Credential{}, err
	}
	return out, nil
}

// List returns the user's registered credentials, ordered by ref
// ascending, mirroring credentialstore.Memory.
func (s *Store) List(ctx context.Context, tenantID, userID string) ([]credentialstore.Credential, error) {
	out := make([]credentialstore.Credential, 0)
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT `+selectList+` FROM credentials
			 WHERE tenant_id = $1 AND user_id = $2 ORDER BY ref`,
			tenantID, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			c, _, err := scanCredential(rows)
			if err != nil {
				return err
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Rotate replaces the secret material for an existing credential. It
// refreshes RotatedAt, restores the active status, and clears the
// revoked state. Returns ErrNotFound when the credential does not
// exist.
func (s *Store) Rotate(ctx context.Context, tenantID, ref, newSecret string) (credentialstore.Credential, error) {
	var out credentialstore.Credential
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+selectList+` FROM credentials
			 WHERE tenant_id = $1 AND ref = $2 FOR UPDATE`,
			tenantID, ref)
		c, prevUpdated, err := scanCredential(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return credentialstore.ErrNotFound
		}
		if err != nil {
			return err
		}
		now := time.Now().UTC().Truncate(time.Microsecond)
		c.Secret = newSecret
		c.RotatedAt = now
		c.Status = credentialstore.StatusActive
		c.RevokedAt = time.Time{}
		updatedAt := pgtenant.MonotonicNext(prevUpdated, now)
		if _, err := tx.Exec(ctx, `UPDATE credentials SET
			secret = $3, status = $4, rotated_at = $5,
			revoked_at = $6, updated_at = $7
		WHERE tenant_id = $1 AND ref = $2`,
			tenantID, ref, c.Secret, string(c.Status), c.RotatedAt,
			pgtenant.NullTime(c.RevokedAt), updatedAt); err != nil {
			return err
		}
		out = c
		return nil
	})
	if err != nil {
		return credentialstore.Credential{}, err
	}
	return out, nil
}

// Revoke marks the credential revoked. Returns ErrNotFound when the
// credential does not exist.
func (s *Store) Revoke(ctx context.Context, tenantID, ref string) (credentialstore.Credential, error) {
	var out credentialstore.Credential
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+selectList+` FROM credentials
			 WHERE tenant_id = $1 AND ref = $2 FOR UPDATE`,
			tenantID, ref)
		c, prevUpdated, err := scanCredential(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return credentialstore.ErrNotFound
		}
		if err != nil {
			return err
		}
		now := time.Now().UTC().Truncate(time.Microsecond)
		c.Status = credentialstore.StatusRevoked
		c.RevokedAt = now
		updatedAt := pgtenant.MonotonicNext(prevUpdated, now)
		if _, err := tx.Exec(ctx, `UPDATE credentials SET
			status = $3, revoked_at = $4, updated_at = $5
		WHERE tenant_id = $1 AND ref = $2`,
			tenantID, ref, string(c.Status), c.RevokedAt, updatedAt); err != nil {
			return err
		}
		out = c
		return nil
	})
	if err != nil {
		return credentialstore.Credential{}, err
	}
	return out, nil
}

// Delete removes the credential row. Returns ErrNotFound when the
// credential does not exist.
func (s *Store) Delete(ctx context.Context, tenantID, ref string) error {
	return pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`DELETE FROM credentials WHERE tenant_id = $1 AND ref = $2`,
			tenantID, ref)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return credentialstore.ErrNotFound
		}
		return nil
	})
}

// scanCredential reads one row in selectList order into a Credential.
// The credentials table's updated_at column has no Credential field;
// it is returned separately so mutations can advance it with
// pgtenant.MonotonicNext.
func scanCredential(row pgx.Row) (credentialstore.Credential, time.Time, error) {
	var (
		c                    credentialstore.Credential
		provider, status     string
		rotatedAt, revokedAt *time.Time
		updatedAt            time.Time
	)
	if err := row.Scan(
		&c.TenantID, &c.Ref, &c.UserID, &provider, &c.Secret, &status,
		&c.CreatedAt, &rotatedAt, &revokedAt, &updatedAt,
	); err != nil {
		return credentialstore.Credential{}, time.Time{}, err
	}
	c.Provider = credential.Provider(provider)
	c.Status = credentialstore.Status(status)
	if rotatedAt != nil {
		c.RotatedAt = *rotatedAt
	}
	if revokedAt != nil {
		c.RevokedAt = *revokedAt
	}
	return c, updatedAt, nil
}

// randomHex returns n random bytes hex-encoded. It mints the opaque
// credential_ref suffix, matching credentialstore.Memory.
func randomHex(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}
