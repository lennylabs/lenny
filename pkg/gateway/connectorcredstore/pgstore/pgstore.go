// SPDX-License-Identifier: MIT

// Package pgstore is the §13.3 Postgres-backed encrypted TokenStore
// for §9.3 connector OAuth credentials. Mirrors the §4.9 user-credential
// envelope pattern in pkg/gateway/credentialstore/pgstore: every token
// secret is sealed with a per-record DEK wrapped by the per-tenant KMS
// KEK before it reaches Postgres, and a SHA-256 hash of the access
// token is stored alongside for the §13.3 revocation-lookup hot path.
package pgstore

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/connectorcredstore"
	"github.com/lennylabs/lenny/pkg/gateway/pgtenant"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/kms/envelope"
)

// Store is the Postgres-backed §9.3 connector-credential registry
// with §13.3 KMS-envelope encryption. Construct with New.
type Store struct {
	pool  *pgxpool.Pool
	kms   kms.Provider
	clock func() time.Time
}

// New returns a Store backed by pool, envelope-encrypting connector
// OAuth tokens under KEKs from provider. Pool must point at a
// database with migration 0048 applied. provider must not be nil:
// §13.3 requires T4 token material to be stored under envelope
// encryption; a nil provider cannot satisfy the contract.
//
// clock returns the current time used by Put / RotateAccessToken /
// Revoke. Pass nil to use time.Now in UTC.
func New(pool *pgxpool.Pool, provider kms.Provider, clock func() time.Time) (*Store, error) {
	if pool == nil {
		return nil, errors.New("connectorcredstore/pgstore: nil pool")
	}
	if provider == nil {
		return nil, errors.New("connectorcredstore/pgstore: nil kms provider; §13.3 token secrets require envelope encryption")
	}
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &Store{pool: pool, kms: provider, clock: clock}, nil
}

var _ connectorcredstore.Store = (*Store)(nil)

// kekAlias is the per-tenant KEK alias, matching credentialstore/pgstore.
func kekAlias(tenantID string) string { return "tenant:" + tenantID }

func (s *Store) cipher(tenantID string) (*envelope.Cipher, error) {
	return envelope.New(s.kms, kekAlias(tenantID))
}

// seal envelope-encrypts a plaintext secret for tenantID.
func (s *Store) seal(ctx context.Context, tenantID, plaintext string) ([]byte, int, error) {
	if plaintext == "" {
		return nil, 0, nil
	}
	c, err := s.cipher(tenantID)
	if err != nil {
		return nil, 0, err
	}
	sealed, err := c.Seal(ctx, []byte(plaintext))
	if err != nil {
		return nil, 0, fmt.Errorf("connectorcredstore/pgstore: seal: %w", err)
	}
	blob, err := envelope.Encode(sealed)
	if err != nil {
		return nil, 0, fmt.Errorf("connectorcredstore/pgstore: encode sealed: %w", err)
	}
	return blob, sealed.KEKVersion, nil
}

// open reverses seal. An empty blob decodes to the empty string.
func (s *Store) open(ctx context.Context, tenantID string, blob []byte, keyVersion int) (string, error) {
	if len(blob) == 0 {
		return "", nil
	}
	c, err := s.cipher(tenantID)
	if err != nil {
		return "", err
	}
	sealed, err := envelope.Decode(blob)
	if err != nil {
		return "", fmt.Errorf("connectorcredstore/pgstore: decode sealed: %w", err)
	}
	if sealed.KEKVersion != keyVersion {
		return "", fmt.Errorf("connectorcredstore/pgstore: key_version %d does not match sealed blob version %d",
			keyVersion, sealed.KEKVersion)
	}
	plain, err := c.Open(ctx, sealed)
	if err != nil {
		return "", fmt.Errorf("connectorcredstore/pgstore: open: %w", err)
	}
	return string(plain), nil
}

func accessTokenHash(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// Put implements connectorcredstore.Store. The (tenant, connector,
// user, environment) four-tuple is the upsert key per §4.3 line 202;
// a re-store advances UpdatedAt and rewrites the token blobs and the
// hash.
func (s *Store) Put(ctx context.Context, cred connectorcredstore.ConnectorCredential) error {
	if err := validate(cred); err != nil {
		return err
	}
	accBlob, accVer, err := s.seal(ctx, cred.TenantID, cred.AccessToken)
	if err != nil {
		return err
	}
	var refBlob []byte
	var refVerPtr *int
	if cred.RefreshToken != "" {
		blob, ver, err := s.seal(ctx, cred.TenantID, cred.RefreshToken)
		if err != nil {
			return err
		}
		refBlob = blob
		refVerPtr = &ver
	}
	hash := accessTokenHash(cred.AccessToken)
	scopesJSON, err := json.Marshal(append([]string(nil), cred.Scopes...))
	if err != nil {
		return fmt.Errorf("connectorcredstore/pgstore: marshal scopes: %w", err)
	}

	return pgtenant.InTx(ctx, s.pool, cred.TenantID, func(tx pgx.Tx) error {
		now := s.clock().UTC()
		var prevCreated, prevUpdated time.Time
		row := tx.QueryRow(ctx,
			`SELECT created_at, updated_at FROM connector_credentials
			 WHERE tenant_id = $1 AND connector_id = $2 AND user_id = $3 AND environment = $4`,
			cred.TenantID, cred.ConnectorID, cred.UserID, cred.Environment)
		err := row.Scan(&prevCreated, &prevUpdated)
		var created time.Time
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			created = now
		case err != nil:
			return err
		default:
			created = prevCreated
			now = pgtenant.MonotonicNext(prevUpdated, now)
		}
		expires := pgtenant.NullTime(cred.ExpiresAt)
		_, err = tx.Exec(ctx, `
			INSERT INTO connector_credentials (
				tenant_id, connector_id, user_id, environment,
				access_token_blob, access_token_key_version, access_token_hash,
				refresh_token_blob, refresh_token_key_version,
				token_type, scopes, expires_at,
				created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11::jsonb, $12, $13, $14)
			ON CONFLICT (tenant_id, connector_id, user_id, environment) DO UPDATE SET
				access_token_blob = EXCLUDED.access_token_blob,
				access_token_key_version = EXCLUDED.access_token_key_version,
				access_token_hash = EXCLUDED.access_token_hash,
				refresh_token_blob = EXCLUDED.refresh_token_blob,
				refresh_token_key_version = EXCLUDED.refresh_token_key_version,
				token_type = EXCLUDED.token_type,
				scopes = EXCLUDED.scopes,
				expires_at = EXCLUDED.expires_at,
				updated_at = EXCLUDED.updated_at`,
			cred.TenantID, cred.ConnectorID, cred.UserID, cred.Environment,
			accBlob, accVer, hash,
			refBlob, refVerPtr,
			cred.TokenType, string(scopesJSON), expires,
			created, now)
		return err
	})
}

// Get implements connectorcredstore.Store.
// spec: §4.3 line 202.
func (s *Store) Get(ctx context.Context, tenantID, connectorID, userID, environment string) (connectorcredstore.ConnectorCredential, error) {
	var out connectorcredstore.ConnectorCredential
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		var (
			accBlob, refBlob, hash []byte
			accVer                 int
			refVer                 *int
			scopesJSON             []byte
			expiresAt              *time.Time
		)
		row := tx.QueryRow(ctx, `
			SELECT access_token_blob, access_token_key_version, access_token_hash,
			       refresh_token_blob, refresh_token_key_version,
			       token_type, scopes, expires_at, created_at, updated_at
			FROM connector_credentials
			WHERE tenant_id = $1 AND connector_id = $2 AND user_id = $3 AND environment = $4`,
			tenantID, connectorID, userID, environment)
		err := row.Scan(&accBlob, &accVer, &hash,
			&refBlob, &refVer,
			&out.TokenType, &scopesJSON, &expiresAt, &out.CreatedAt, &out.UpdatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return connectorcredstore.ErrNotFound
		}
		if err != nil {
			return err
		}
		out.TenantID = tenantID
		out.ConnectorID = connectorID
		out.UserID = userID
		out.Environment = environment
		access, err := s.open(ctx, tenantID, accBlob, accVer)
		if err != nil {
			return err
		}
		out.AccessToken = access
		if refBlob != nil && refVer != nil {
			refresh, err := s.open(ctx, tenantID, refBlob, *refVer)
			if err != nil {
				return err
			}
			out.RefreshToken = refresh
		}
		if expiresAt != nil {
			out.ExpiresAt = *expiresAt
		}
		if len(scopesJSON) > 0 {
			if err := json.Unmarshal(scopesJSON, &out.Scopes); err != nil {
				return fmt.Errorf("connectorcredstore/pgstore: unmarshal scopes: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return connectorcredstore.ConnectorCredential{}, err
	}
	return out, nil
}

// Delete implements connectorcredstore.Store.
// spec: §4.3 line 202.
func (s *Store) Delete(ctx context.Context, tenantID, connectorID, userID, environment string) error {
	return pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			DELETE FROM connector_credentials
			WHERE tenant_id = $1 AND connector_id = $2 AND user_id = $3 AND environment = $4`,
			tenantID, connectorID, userID, environment)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return connectorcredstore.ErrNotFound
		}
		return nil
	})
}

// DeleteByUser implements the §12.1 mandatory-erasure primitive.
// Hard-deletes every connector credential row owned by (tenantID,
// userID) across all connectors and environments — the §12.8
// user-erasure path for the §12.2 TokenStore role. Returns the number
// of rows removed.
//
// spec: §12.1 line 5, §12.8 step `TokenStore`.
func (s *Store) DeleteByUser(ctx context.Context, tenantID, userID string) (int, error) {
	if tenantID == "" || userID == "" {
		return 0, errors.New("connectorcredstore/pgstore: DeleteByUser requires non-empty tenant_id and user_id")
	}
	var deleted int64
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`DELETE FROM connector_credentials WHERE tenant_id = $1 AND user_id = $2`,
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
// Hard-deletes every connector credential row owned by tenantID — the
// §12.8 Phase 4 tenant-teardown path for the TokenStore role.
//
// spec: §12.1 line 5, §12.8 Phase 4.
func (s *Store) DeleteByTenant(ctx context.Context, tenantID string) (int, error) {
	if tenantID == "" {
		return 0, errors.New("connectorcredstore/pgstore: DeleteByTenant requires a concrete tenant_id")
	}
	var deleted int64
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`DELETE FROM connector_credentials WHERE tenant_id = $1`, tenantID)
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

// ListByConnector implements connectorcredstore.Store.
// spec: §4.3 line 202.
func (s *Store) ListByConnector(ctx context.Context, tenantID, connectorID string) ([]connectorcredstore.ConnectorCredential, error) {
	var out []connectorcredstore.ConnectorCredential
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT user_id, environment,
			       access_token_blob, access_token_key_version,
			       refresh_token_blob, refresh_token_key_version,
			       token_type, scopes, expires_at, created_at, updated_at
			FROM connector_credentials
			WHERE tenant_id = $1 AND connector_id = $2
			ORDER BY user_id, environment`, tenantID, connectorID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var (
				cred             connectorcredstore.ConnectorCredential
				accBlob, refBlob []byte
				accVer           int
				refVer           *int
				scopesJSON       []byte
				expiresAt        *time.Time
			)
			if err := rows.Scan(&cred.UserID, &cred.Environment,
				&accBlob, &accVer,
				&refBlob, &refVer,
				&cred.TokenType, &scopesJSON, &expiresAt, &cred.CreatedAt, &cred.UpdatedAt); err != nil {
				return err
			}
			cred.TenantID = tenantID
			cred.ConnectorID = connectorID
			access, err := s.open(ctx, tenantID, accBlob, accVer)
			if err != nil {
				return err
			}
			cred.AccessToken = access
			if refBlob != nil && refVer != nil {
				refresh, err := s.open(ctx, tenantID, refBlob, *refVer)
				if err != nil {
					return err
				}
				cred.RefreshToken = refresh
			}
			if expiresAt != nil {
				cred.ExpiresAt = *expiresAt
			}
			if len(scopesJSON) > 0 {
				if err := json.Unmarshal(scopesJSON, &cred.Scopes); err != nil {
					return fmt.Errorf("connectorcredstore/pgstore: unmarshal scopes: %w", err)
				}
			}
			out = append(out, cred)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// RotateAccessToken implements connectorcredstore.Store. The §9.3 /
// RFC 6749 §6 refresh-token grant produces a fresh access token (and
// optionally a rotated refresh token); this method writes both blobs
// back under the same per-tenant KMS KEK, stamps `rotated_at` and
// `updated_at`, and recomputes the SHA-256(access_token) revocation
// index. The (tenant, connector, user) triple must already exist;
// ErrNotFound is returned otherwise.
//
// spec: §4.3 line 200 ("Refresh tokens stored encrypted at rest"),
// §9.3 (connector OAuth token lifecycle), migration 0048's
// `rotated_at` column.
func (s *Store) RotateAccessToken(ctx context.Context, rot connectorcredstore.RotationRecord) error {
	switch {
	case rot.TenantID == "":
		return errors.New("connectorcredstore/pgstore: tenant id is required")
	case rot.ConnectorID == "":
		return errors.New("connectorcredstore/pgstore: connector id is required")
	case rot.UserID == "":
		return errors.New("connectorcredstore/pgstore: user id is required")
	case rot.AccessToken == "":
		return errors.New("connectorcredstore/pgstore: access token is required")
	}
	accBlob, accVer, err := s.seal(ctx, rot.TenantID, rot.AccessToken)
	if err != nil {
		return err
	}
	var refBlob []byte
	var refVerPtr *int
	if rot.RefreshToken != "" {
		blob, ver, err := s.seal(ctx, rot.TenantID, rot.RefreshToken)
		if err != nil {
			return err
		}
		refBlob = blob
		refVerPtr = &ver
	}
	hash := accessTokenHash(rot.AccessToken)
	var scopesJSON []byte
	if len(rot.Scopes) > 0 {
		scopesJSON, err = json.Marshal(append([]string(nil), rot.Scopes...))
		if err != nil {
			return fmt.Errorf("connectorcredstore/pgstore: marshal scopes: %w", err)
		}
	}

	return pgtenant.InTx(ctx, s.pool, rot.TenantID, func(tx pgx.Tx) error {
		now := s.clock().UTC()
		// Read prior updated_at to preserve monotonic ordering across
		// rapid rotations.
		var prevUpdated time.Time
		row := tx.QueryRow(ctx,
			`SELECT updated_at FROM connector_credentials
			 WHERE tenant_id = $1 AND connector_id = $2 AND user_id = $3 AND environment = $4`,
			rot.TenantID, rot.ConnectorID, rot.UserID, rot.Environment)
		err := row.Scan(&prevUpdated)
		if errors.Is(err, pgx.ErrNoRows) {
			return connectorcredstore.ErrNotFound
		}
		if err != nil {
			return err
		}
		now = pgtenant.MonotonicNext(prevUpdated, now)

		// Build the UPDATE with COALESCE-like preservation semantics so
		// unset optional fields leave the prior value intact.
		var (
			tokenType *string
			expires   any
		)
		if rot.TokenType != "" {
			tokenType = &rot.TokenType
		}
		if !rot.ExpiresAt.IsZero() {
			expires = pgtenant.NullTime(rot.ExpiresAt)
		}
		tag, err := tx.Exec(ctx, `
			UPDATE connector_credentials
			SET access_token_blob = $5,
				access_token_key_version = $6,
				access_token_hash = $7,
				refresh_token_blob = COALESCE($8, refresh_token_blob),
				refresh_token_key_version = COALESCE($9, refresh_token_key_version),
				token_type = COALESCE($10, token_type),
				scopes = COALESCE($11::jsonb, scopes),
				expires_at = COALESCE($12, expires_at),
				updated_at = $13,
				rotated_at = $13
			WHERE tenant_id = $1 AND connector_id = $2 AND user_id = $3 AND environment = $4`,
			rot.TenantID, rot.ConnectorID, rot.UserID, rot.Environment,
			accBlob, accVer, hash,
			refBlob, refVerPtr,
			tokenType, scopesJSON, expires,
			now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return connectorcredstore.ErrNotFound
		}
		return nil
	})
}

// FindByAccessTokenHash returns the credential whose access token
// hashes to hash, or ErrNotFound. The §13.3 revocation hot path uses
// it to resolve a presented bearer to a stored row without
// decrypting every credential under the tenant. The matched row's
// Environment is populated on the result so callers can audit the
// scope the credential belongs to. spec: §4.3 line 202.
func (s *Store) FindByAccessTokenHash(ctx context.Context, tenantID string, hash []byte) (connectorcredstore.ConnectorCredential, error) {
	var (
		connectorID, userID  string
		environment          string
		accBlob, refBlob     []byte
		accVer               int
		refVer               *int
		tokenType            string
		scopesJSON           []byte
		expiresAt            *time.Time
		createdAt, updatedAt time.Time
	)
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			SELECT connector_id, user_id, environment,
			       access_token_blob, access_token_key_version,
			       refresh_token_blob, refresh_token_key_version,
			       token_type, scopes, expires_at, created_at, updated_at
			FROM connector_credentials
			WHERE tenant_id = $1 AND access_token_hash = $2
			LIMIT 1`, tenantID, hash)
		err := row.Scan(&connectorID, &userID, &environment,
			&accBlob, &accVer,
			&refBlob, &refVer,
			&tokenType, &scopesJSON, &expiresAt, &createdAt, &updatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return connectorcredstore.ErrNotFound
		}
		return err
	})
	if err != nil {
		return connectorcredstore.ConnectorCredential{}, err
	}
	cred := connectorcredstore.ConnectorCredential{
		TenantID:    tenantID,
		ConnectorID: connectorID,
		UserID:      userID,
		Environment: environment,
		TokenType:   tokenType,
		CreatedAt:   createdAt,
		UpdatedAt:   updatedAt,
	}
	access, err := s.open(ctx, tenantID, accBlob, accVer)
	if err != nil {
		return connectorcredstore.ConnectorCredential{}, err
	}
	cred.AccessToken = access
	if refBlob != nil && refVer != nil {
		refresh, err := s.open(ctx, tenantID, refBlob, *refVer)
		if err != nil {
			return connectorcredstore.ConnectorCredential{}, err
		}
		cred.RefreshToken = refresh
	}
	if expiresAt != nil {
		cred.ExpiresAt = *expiresAt
	}
	if len(scopesJSON) > 0 {
		if err := json.Unmarshal(scopesJSON, &cred.Scopes); err != nil {
			return connectorcredstore.ConnectorCredential{}, err
		}
	}
	return cred, nil
}

// validate mirrors the Memory.Put validation; the inputs the same
// constraints hold on either backend.
func validate(c connectorcredstore.ConnectorCredential) error {
	switch {
	case c.TenantID == "":
		return errors.New("connectorcredstore/pgstore: tenant id is required")
	case c.ConnectorID == "":
		return errors.New("connectorcredstore/pgstore: connector id is required")
	case c.UserID == "":
		return errors.New("connectorcredstore/pgstore: user id is required")
	case c.AccessToken == "":
		return errors.New("connectorcredstore/pgstore: access token is required")
	}
	return nil
}
