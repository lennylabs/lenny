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
// Envelope encryption. §4.9 / §12.9 classify a user-supplied API key
// as T4 Restricted, requiring AES-256-GCM envelope encryption with a
// per-record data-encryption-key (DEK) wrapped by a KMS key-encryption-
// key (KEK). The store envelope-encrypts the secret on write and
// decrypts it on read: the secret column holds the
// pkg/kms/envelope-encoded ciphertext blob (the wrapped DEK, the GCM
// nonce, and the record ciphertext) and the secret_key_version column
// records the §4.9.1 KEK version that wrapped the row's DEK. The KEK
// alias is per-tenant ("tenant:{tenant_id}"), so each tenant's
// credentials are wrapped under an independent KEK. The plaintext
// secret never reaches Postgres. The store still never returns secret
// material on the wire; it decrypts the secret on read so the REST
// handlers can project a secret-free payload, mirroring
// credentialstore.Memory.
package pgstore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentialstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pgtenant"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/kms/envelope"
)

// Store is the Postgres-backed credential registry. Construct with New.
type Store struct {
	pool *pgxpool.Pool
	kms  kms.Provider
}

// New returns a Store backed by pool, envelope-encrypting credential
// secrets under KEKs from provider. The pool must point at a database
// that has the migrations/ schema applied (migration 0039 or later for
// the envelope columns). provider must not be nil: §12.9 requires T4
// data to be stored under envelope encryption, so a Store with no KEK
// provider cannot satisfy the contract.
func New(pool *pgxpool.Pool, provider kms.Provider) (*Store, error) {
	if pool == nil {
		return nil, errors.New("credentialstore/pgstore: nil pool")
	}
	if provider == nil {
		return nil, errors.New("credentialstore/pgstore: nil kms provider; T4 credential secrets require envelope encryption")
	}
	return &Store{pool: pool, kms: provider}, nil
}

var _ credentialstore.Store = (*Store)(nil)

// kekAlias is the per-tenant KEK alias. Each tenant's credential
// secrets are envelope-encrypted under an independent KEK, so the
// §4.9.1 rotation procedure and the §12.8 cryptographic-erasure model
// operate per tenant.
func kekAlias(tenantID string) string { return "tenant:" + tenantID }

// cipher returns the envelope Cipher for tenantID's KEK alias.
func (s *Store) cipher(tenantID string) (*envelope.Cipher, error) {
	return envelope.New(s.kms, kekAlias(tenantID))
}

// sealSecret envelope-encrypts a plaintext secret for tenantID,
// returning the encoded ciphertext blob for the secret column and the
// §4.9.1 KEK version for the secret_key_version column.
func (s *Store) sealSecret(ctx context.Context, tenantID, plaintext string) ([]byte, int, error) {
	c, err := s.cipher(tenantID)
	if err != nil {
		return nil, 0, err
	}
	sealed, err := c.Seal(ctx, []byte(plaintext))
	if err != nil {
		return nil, 0, fmt.Errorf("credentialstore/pgstore: seal secret: %w", err)
	}
	blob, err := envelope.Encode(sealed)
	if err != nil {
		return nil, 0, fmt.Errorf("credentialstore/pgstore: encode sealed secret: %w", err)
	}
	return blob, sealed.KEKVersion, nil
}

// openSecret reverses sealSecret: it decodes the secret column blob
// and decrypts it under tenantID's KEK. An empty blob (the column
// default for a row with no stored secret) decrypts to the empty
// string.
func (s *Store) openSecret(ctx context.Context, tenantID string, blob []byte, keyVersion int) (string, error) {
	if len(blob) == 0 {
		return "", nil
	}
	c, err := s.cipher(tenantID)
	if err != nil {
		return "", err
	}
	sealed, err := envelope.Decode(blob)
	if err != nil {
		return "", fmt.Errorf("credentialstore/pgstore: decode sealed secret: %w", err)
	}
	if sealed.KEKVersion != keyVersion {
		// The secret_key_version column and the version embedded in the
		// blob disagree: the row was written inconsistently. Fail
		// closed rather than decrypt under a guessed version.
		return "", fmt.Errorf("credentialstore/pgstore: secret_key_version %d does not match sealed blob version %d",
			keyVersion, sealed.KEKVersion)
	}
	plain, err := c.Open(ctx, sealed)
	if err != nil {
		return "", fmt.Errorf("credentialstore/pgstore: open secret: %w", err)
	}
	return string(plain), nil
}

// selectList is the column set scanCredential reads. updated_at is
// last: scanCredential reads it into a local so the strictly-advancing
// value is available without a Credential field for it. secret holds
// the envelope ciphertext blob and secret_key_version the §4.9.1 KEK
// version. environment is the §4.3 line 202 scoping column.
const selectList = `tenant_id, ref, user_id, provider, environment, secret, secret_key_version,
	status, created_at, rotated_at, revoked_at, last_used_at, updated_at`

// Register stores (or replaces) the credential for the
// (tenant, user, provider, environment) four-tuple, returning the
// credential_ref. Re-registering the same four-tuple replaces the
// secret, refreshes RotatedAt, and clears the revoked state per §15.1,
// mirroring credentialstore.Memory.
// spec: §4.3 line 202.
func (s *Store) Register(ctx context.Context, tenantID, userID string, provider credential.Provider, environment, secret string) (credentialstore.Credential, error) {
	if !provider.IsValid() {
		return credentialstore.Credential{}, errors.New("credentialstore: unknown provider " + string(provider))
	}
	// Envelope-encrypt the secret before opening the transaction: the
	// plaintext never reaches a SQL statement.
	secretBlob, keyVersion, err := s.sealSecret(ctx, tenantID, secret)
	if err != nil {
		return credentialstore.Credential{}, err
	}
	var out credentialstore.Credential
	txErr := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		now := time.Now().UTC().Truncate(time.Microsecond)

		// Replace path: the (tenant, user, provider, environment)
		// four-tuple already holds a credential. Lock the row, refresh
		// the secret, and clear the revoked state, exactly like the
		// in-memory store.
		existing := tx.QueryRow(ctx,
			`SELECT `+selectList+` FROM credentials
			 WHERE tenant_id = $1 AND user_id = $2 AND provider = $3 AND environment = $4 FOR UPDATE`,
			tenantID, userID, string(provider), environment)
		c, _, _, prevUpdated, err := scanCredential(existing)
		if err == nil {
			c.Secret = secret
			c.Status = credentialstore.StatusActive
			c.RotatedAt = now
			c.RevokedAt = time.Time{}
			updatedAt := pgtenant.MonotonicNext(prevUpdated, now)
			if _, err := tx.Exec(ctx, `UPDATE credentials SET
				secret = $3, secret_key_version = $4, status = $5, rotated_at = $6,
				revoked_at = $7, updated_at = $8
			WHERE tenant_id = $1 AND ref = $2`,
				tenantID, c.Ref, secretBlob, keyVersion, string(c.Status), c.RotatedAt,
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
			Ref:         "cred_" + randomHex(8),
			TenantID:    tenantID,
			UserID:      userID,
			Environment: environment,
			Provider:    provider,
			Secret:      secret,
			Status:      credentialstore.StatusActive,
			CreatedAt:   now,
		}
		if _, err := tx.Exec(ctx, `INSERT INTO credentials (
			tenant_id, ref, user_id, provider, environment, secret, secret_key_version, status,
			created_at, rotated_at, revoked_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
			c.TenantID, c.Ref, c.UserID, string(c.Provider), c.Environment, secretBlob, keyVersion, string(c.Status),
			c.CreatedAt, pgtenant.NullTime(c.RotatedAt),
			pgtenant.NullTime(c.RevokedAt), now); err != nil {
			return err
		}
		out = c
		return nil
	})
	if txErr != nil {
		return credentialstore.Credential{}, txErr
	}
	return out, nil
}

// Lookup resolves the credential for the §4.3 line 202
// (tenant, user, provider, environment) four-tuple, decrypting the
// secret before return. A miss is ErrNotFound.
// spec: §4.9 lines 1347-1351.
func (s *Store) Lookup(ctx context.Context, tenantID, userID string, provider credential.Provider, environment string) (credentialstore.Credential, error) {
	var out credentialstore.Credential
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+selectList+` FROM credentials
			 WHERE tenant_id = $1 AND user_id = $2 AND provider = $3 AND environment = $4`,
			tenantID, userID, string(provider), environment)
		c, blob, keyVersion, _, err := scanCredential(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return credentialstore.ErrNotFound
		}
		if err != nil {
			return err
		}
		secret, err := s.openSecret(ctx, tenantID, blob, keyVersion)
		if err != nil {
			return err
		}
		c.Secret = secret
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
		c, blob, keyVersion, _, err := scanCredential(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return credentialstore.ErrNotFound
		}
		if err != nil {
			return err
		}
		secret, err := s.openSecret(ctx, tenantID, blob, keyVersion)
		if err != nil {
			return err
		}
		c.Secret = secret
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
		type pending struct {
			cred       credentialstore.Credential
			blob       []byte
			keyVersion int
		}
		var rowsOut []pending
		for rows.Next() {
			c, blob, keyVersion, _, err := scanCredential(rows)
			if err != nil {
				return err
			}
			rowsOut = append(rowsOut, pending{cred: c, blob: blob, keyVersion: keyVersion})
		}
		if err := rows.Err(); err != nil {
			return err
		}
		// Decrypt after the rows cursor is drained: openSecret must not
		// run while a query is still streaming on the same transaction.
		for _, p := range rowsOut {
			secret, err := s.openSecret(ctx, tenantID, p.blob, p.keyVersion)
			if err != nil {
				return err
			}
			c := p.cred
			c.Secret = secret
			out = append(out, c)
		}
		return nil
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
	// Envelope-encrypt the replacement secret before opening the
	// transaction; the plaintext never reaches a SQL statement.
	secretBlob, keyVersion, err := s.sealSecret(ctx, tenantID, newSecret)
	if err != nil {
		return credentialstore.Credential{}, err
	}
	var out credentialstore.Credential
	txErr := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+selectList+` FROM credentials
			 WHERE tenant_id = $1 AND ref = $2 FOR UPDATE`,
			tenantID, ref)
		c, _, _, prevUpdated, err := scanCredential(row)
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
			secret = $3, secret_key_version = $4, status = $5, rotated_at = $6,
			revoked_at = $7, updated_at = $8
		WHERE tenant_id = $1 AND ref = $2`,
			tenantID, ref, secretBlob, keyVersion, string(c.Status), c.RotatedAt,
			pgtenant.NullTime(c.RevokedAt), updatedAt); err != nil {
			return err
		}
		out = c
		return nil
	})
	if txErr != nil {
		return credentialstore.Credential{}, txErr
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
		c, blob, keyVersion, prevUpdated, err := scanCredential(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return credentialstore.ErrNotFound
		}
		if err != nil {
			return err
		}
		// Revoke does not change the secret; decrypt the stored
		// ciphertext so the returned Credential carries the secret like
		// credentialstore.Memory.Revoke.
		secret, err := s.openSecret(ctx, tenantID, blob, keyVersion)
		if err != nil {
			return err
		}
		c.Secret = secret
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

// MarkUsed records at as the credential's last-used instant. It is a
// no-op (ErrNotFound) for an unknown ref. The §4.9 lease-resolution
// path calls it so the §15.1 GET /v1/credentials response reports
// last_used_at.
//
// spec: §4.9 line 1349, 1365.
func (s *Store) MarkUsed(ctx context.Context, tenantID, ref string, at time.Time) error {
	return pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE credentials SET last_used_at = $3 WHERE tenant_id = $1 AND ref = $2`,
			tenantID, ref, at.UTC())
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return credentialstore.ErrNotFound
		}
		return nil
	})
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

// DeleteByUser implements the §12.1 mandatory-erasure primitive.
// Hard-deletes every credential row owned by (tenantID, userID) — the
// §4.9 user-credential registry is user-keyed, so DeleteByUser is the
// load-bearing path for GDPR erasure of this store.
//
// spec: §12.1 line 5.
func (s *Store) DeleteByUser(ctx context.Context, tenantID, userID string) (int, error) {
	if tenantID == "" || userID == "" {
		return 0, errors.New("credentialstore: DeleteByUser requires non-empty tenant_id and user_id")
	}
	var deleted int64
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`DELETE FROM credentials WHERE tenant_id = $1 AND user_id = $2`,
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
// Hard-deletes every credential row belonging to tenantID.
//
// spec: §12.1 line 5, §12.8 Phase 4.
func (s *Store) DeleteByTenant(ctx context.Context, tenantID string) (int, error) {
	if tenantID == "" {
		return 0, errors.New("credentialstore: DeleteByTenant requires a concrete tenant_id")
	}
	var deleted int64
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`DELETE FROM credentials WHERE tenant_id = $1`, tenantID)
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

// scanCredential reads one row in selectList order into a Credential.
// The secret column holds the envelope ciphertext blob; scanCredential
// returns it and the secret_key_version unchanged rather than the
// plaintext, so the caller decrypts it through openSecret outside any
// streaming query. The Credential's Secret field is left empty by this
// function. The updated_at column has no Credential field; it is
// returned separately so mutations can advance it with
// pgtenant.MonotonicNext.
func scanCredential(row pgx.Row) (cred credentialstore.Credential, secretBlob []byte, keyVersion int, updatedAt time.Time, err error) {
	var (
		c                    credentialstore.Credential
		provider, status     string
		blob                 []byte
		version              int
		rotatedAt, revokedAt *time.Time
		lastUsedAt           *time.Time
		updated              time.Time
	)
	if err := row.Scan(
		&c.TenantID, &c.Ref, &c.UserID, &provider, &c.Environment, &blob, &version, &status,
		&c.CreatedAt, &rotatedAt, &revokedAt, &lastUsedAt, &updated,
	); err != nil {
		return credentialstore.Credential{}, nil, 0, time.Time{}, err
	}
	c.Provider = credential.Provider(provider)
	c.Status = credentialstore.Status(status)
	if rotatedAt != nil {
		c.RotatedAt = *rotatedAt
	}
	if revokedAt != nil {
		c.RevokedAt = *revokedAt
	}
	if lastUsedAt != nil {
		c.LastUsedAt = *lastUsedAt
	}
	return c, blob, version, updated, nil
}

// randomHex returns n random bytes hex-encoded. It mints the opaque
// credential_ref suffix, matching credentialstore.Memory.
func randomHex(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}
