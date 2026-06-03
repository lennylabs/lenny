// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed credleasestore.LeaseStore,
// persisting the §4.9 gateway-replica credential-lease working set to
// the credential_leases table. It is a drop-in alternative to the
// in-memory credleasestore.Store.
//
// credential_leases is platform-global (§4.9): the store is keyed by
// lease id alone and a pool-backed proxy-mode lease carries no
// tenant_id, so operations run as plain queries without an
// app.current_tenant context. Put runs Lease.Validate so an invalid
// lease is rejected regardless of backend, matching the in-memory
// store.
//
// Envelope encryption. §12.9 classifies a credential lease as T4 —
// Restricted: the persisted body carries the §4.9 proxy-mode bearer
// lease token, the capability a runtime presents to the LLM reverse
// proxy. The store envelope-encrypts the body on write and decrypts it
// on read (migration 0129): the lease column holds the
// pkg/kms/envelope-encoded ciphertext blob and lease_key_version
// records the §4.9.1 KEK version that wrapped the row's DEK. The KEK
// alias is platform-scoped ("platform:credential-leases") because a
// pool-backed lease has no owning tenant. The plaintext bearer token
// never reaches Postgres: GetByToken resolves a presented token through
// its SHA-256 hash (lease_token_hash), and the non-secret routing
// identifiers the §11.4 / §7.1 lookups query by live in dedicated
// plaintext columns so those lookups stay indexed without decrypting
// every row.
package pgstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credleasestore"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/kms/envelope"
)

// leasesKEKAlias is the platform-scoped KEK alias the credential-lease
// bodies are envelope-encrypted under. A pool-backed lease carries no
// owning tenant, so the leases share one platform KEK rather than the
// per-tenant aliases the credential-secret registry uses.
//
// spec: §12.9 line 1048 (T4 — Restricted envelope encryption).
const leasesKEKAlias = "platform:credential-leases"

// Store is the Postgres-backed credential-lease store. Construct with
// New.
type Store struct {
	pool   *pgxpool.Pool
	cipher *envelope.Cipher
}

// New returns a Store backed by pool, envelope-encrypting lease bodies
// under the platform KEK from provider. The pool must point at a
// database that has the migrations/ schema applied (migration 0129 or
// later for the envelope columns). provider must not be nil: §12.9
// classifies a credential lease as T4 — Restricted, so a Store with no
// KEK provider cannot satisfy the encryption-at-rest contract.
func New(pool *pgxpool.Pool, provider kms.Provider) (*Store, error) {
	if pool == nil {
		return nil, errors.New("credleasestore/pgstore: nil pool")
	}
	if provider == nil {
		return nil, errors.New("credleasestore/pgstore: nil kms provider; §12.9 T4 credential leases require envelope encryption")
	}
	cipher, err := envelope.New(provider, leasesKEKAlias)
	if err != nil {
		return nil, fmt.Errorf("credleasestore/pgstore: build envelope cipher: %w", err)
	}
	return &Store{pool: pool, cipher: cipher}, nil
}

var _ credleasestore.LeaseStore = (*Store)(nil)

// sealLease envelope-encrypts the marshalled lease body, returning the
// encoded ciphertext blob for the lease column and the §4.9.1 KEK
// version for the lease_key_version column.
func (s *Store) sealLease(ctx context.Context, plaintext []byte) ([]byte, int, error) {
	sealed, err := s.cipher.Seal(ctx, plaintext)
	if err != nil {
		return nil, 0, fmt.Errorf("credleasestore/pgstore: seal lease: %w", err)
	}
	blob, err := envelope.Encode(sealed)
	if err != nil {
		return nil, 0, fmt.Errorf("credleasestore/pgstore: encode sealed lease: %w", err)
	}
	return blob, sealed.KEKVersion, nil
}

// openLease reverses sealLease: it decodes the lease column blob and
// decrypts it under the platform KEK. It fails closed when the column
// version and the version embedded in the blob disagree rather than
// decrypt under a guessed version.
func (s *Store) openLease(ctx context.Context, blob []byte, keyVersion int) ([]byte, error) {
	sealed, err := envelope.Decode(blob)
	if err != nil {
		return nil, fmt.Errorf("credleasestore/pgstore: decode sealed lease: %w", err)
	}
	if sealed.KEKVersion != keyVersion {
		return nil, fmt.Errorf("credleasestore/pgstore: lease_key_version %d does not match sealed blob version %d",
			keyVersion, sealed.KEKVersion)
	}
	plain, err := s.cipher.Open(ctx, sealed)
	if err != nil {
		return nil, fmt.Errorf("credleasestore/pgstore: open lease: %w", err)
	}
	return plain, nil
}

// Put records a lease, replacing any prior lease with the same lease
// ID. The lease is validated first; an invalid lease is rejected and
// not stored, matching the in-memory store. The body is envelope-
// encrypted before it reaches Postgres. A proxy-mode lease stores the
// SHA-256 hash of its bearer token so GetByToken can resolve it; a
// direct-mode lease stores a NULL hash. Re-issuing a lease ID with a
// rotated token overwrites the hash column, so the prior token stops
// resolving and no dangling index row remains.
func (s *Store) Put(lease credential.Lease) error {
	if err := lease.Validate(); err != nil {
		return err
	}
	doc, err := json.Marshal(lease)
	if err != nil {
		return fmt.Errorf("credleasestore: encode lease: %w", err)
	}
	ctx := context.Background()
	blob, keyVersion, err := s.sealLease(ctx, doc)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	_, err = s.pool.Exec(ctx, `INSERT INTO credential_leases (
		lease_id, lease_token_hash, delivery_mode, lease, lease_key_version,
		session_id, cred_source, pool_id, credential_id, cred_tenant_id, credential_ref,
		created_at, updated_at
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $12)
	ON CONFLICT (lease_id) DO UPDATE SET
		lease_token_hash = EXCLUDED.lease_token_hash,
		delivery_mode = EXCLUDED.delivery_mode,
		lease = EXCLUDED.lease,
		lease_key_version = EXCLUDED.lease_key_version,
		session_id = EXCLUDED.session_id,
		cred_source = EXCLUDED.cred_source,
		pool_id = EXCLUDED.pool_id,
		credential_id = EXCLUDED.credential_id,
		cred_tenant_id = EXCLUDED.cred_tenant_id,
		credential_ref = EXCLUDED.credential_ref,
		updated_at = EXCLUDED.updated_at`,
		lease.LeaseID, leaseTokenHash(lease), string(lease.DeliveryMode), blob, keyVersion,
		lease.SessionID, string(lease.Source), lease.PoolID, lease.CredentialID,
		lease.TenantID, lease.CredentialRef, now)
	return err
}

// GetByToken resolves a proxy-mode lease by the bearer lease token the
// agent pod presents. The token is hashed and matched against
// lease_token_hash so the plaintext token is never compared against a
// stored plaintext. ok is false when no lease holds the token. A
// direct-mode lease stores a NULL hash, so it is never resolved here,
// and an empty token never matches a stored row.
func (s *Store) GetByToken(token string) (credential.Lease, bool) {
	row := s.pool.QueryRow(context.Background(),
		`SELECT lease, lease_key_version FROM credential_leases WHERE lease_token_hash = $1`,
		tokenHash(token))
	return s.scanLease(row)
}

// GetByID resolves a lease by its lease ID. ok is false when the store
// holds no lease with the ID.
func (s *Store) GetByID(leaseID string) (credential.Lease, bool) {
	row := s.pool.QueryRow(context.Background(),
		`SELECT lease, lease_key_version FROM credential_leases WHERE lease_id = $1`, leaseID)
	return s.scanLease(row)
}

// Remove drops the lease with the given ID. It is a no-op when the
// store holds no such lease. The lease_token_hash index row is dropped
// with the table row.
func (s *Store) Remove(leaseID string) {
	_, _ = s.pool.Exec(context.Background(),
		`DELETE FROM credential_leases WHERE lease_id = $1`, leaseID)
}

// Len reports how many leases the store holds.
func (s *Store) Len() int {
	var n int
	if err := s.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM credential_leases`).Scan(&n); err != nil {
		return 0
	}
	return n
}

// LeasesBySession returns every lease the store holds whose SessionID
// is one of sessionIDs, the Postgres-backed counterpart of the
// in-memory store's method. It backs the §11.4 full_revoke
// credential-lease revocation step. The query matches the dedicated
// session_id column (the §12.9 body is encrypted, so the prior
// JSONB-field match is no longer available). An empty sessionIDs set
// yields no leases, and a row whose body does not decrypt is skipped.
func (s *Store) LeasesBySession(sessionIDs []string) []credential.Lease {
	if len(sessionIDs) == 0 {
		return nil
	}
	rows, err := s.pool.Query(context.Background(),
		`SELECT lease, lease_key_version FROM credential_leases WHERE session_id = ANY($1)`,
		sessionIDs)
	if err != nil {
		return nil
	}
	return s.collect(rows)
}

// LeasesByCredential returns every lease whose backing credential
// identity equals key, the Postgres-backed counterpart of the in-memory
// store's method. It backs the §4.9 emergency credential revocation
// step. The query matches the dedicated source-aware credential-key
// columns (cred_source plus pool_id/credential_id for a pool key, or
// cred_tenant_id/credential_ref for a user key), mirroring
// credential.Lease.CredentialKey. A row whose body does not decrypt is
// skipped.
//
// spec: §4.9 lines 1640-1652 — look up all active leases backed by the
// revoked credential.
func (s *Store) LeasesByCredential(key credential.CredentialKey) []credential.Lease {
	var (
		q    string
		args []any
	)
	switch key.Source {
	case credential.SourcePool:
		q = `SELECT lease, lease_key_version FROM credential_leases
		     WHERE cred_source = $1 AND pool_id = $2 AND credential_id = $3`
		args = []any{string(key.Source), key.PoolID, key.CredentialID}
	case credential.SourceUser:
		q = `SELECT lease, lease_key_version FROM credential_leases
		     WHERE cred_source = $1 AND cred_tenant_id = $2 AND credential_ref = $3`
		args = []any{string(key.Source), key.TenantID, key.CredentialRef}
	default:
		return nil
	}
	rows, err := s.pool.Query(context.Background(), q, args...)
	if err != nil {
		return nil
	}
	return s.collect(rows)
}

// collect decrypts and decodes every (lease, lease_key_version) row in
// rows, skipping any row whose body does not decrypt or decode. It
// closes rows before returning.
func (s *Store) collect(rows pgx.Rows) []credential.Lease {
	defer rows.Close()
	var out []credential.Lease
	for rows.Next() {
		if lease, ok := s.scanLease(rows); ok {
			out = append(out, lease)
		}
	}
	if rows.Err() != nil {
		return nil
	}
	return out
}

// scanLease scans the lease ciphertext blob and its KEK version from
// row, decrypts the body, and decodes it into a credential.Lease. A
// missing row, an undecryptable blob, or an undecodable body yields the
// zero Lease and ok = false, matching the in-memory store's lookup
// miss.
func (s *Store) scanLease(row pgx.Row) (credential.Lease, bool) {
	var (
		blob       []byte
		keyVersion int
	)
	if err := row.Scan(&blob, &keyVersion); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return credential.Lease{}, false
		}
		return credential.Lease{}, false
	}
	doc, err := s.openLease(context.Background(), blob, keyVersion)
	if err != nil {
		return credential.Lease{}, false
	}
	var lease credential.Lease
	if err := json.Unmarshal(doc, &lease); err != nil {
		return credential.Lease{}, false
	}
	return lease, true
}

// leaseTokenHash returns the SHA-256 hex digest of a proxy-mode lease's
// bearer token to index it by, or nil for a direct-mode lease so the
// lease_token_hash column is NULL. Lease.Validate has already
// guaranteed a proxy-mode lease carries a non-empty token before Put
// reaches this point.
func leaseTokenHash(lease credential.Lease) *string {
	if lease.DeliveryMode != credential.DeliveryProxy || lease.Proxy == nil {
		return nil
	}
	h := tokenHash(lease.Proxy.LeaseToken)
	return &h
}

// tokenHash returns the SHA-256 hex digest GetByToken matches a
// presented token against. The digest is a deterministic lookup key for
// the high-entropy bearer token; the token itself is never persisted.
func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
