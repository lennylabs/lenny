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
package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credleasestore"
)

// Store is the Postgres-backed credential-lease store. Construct with
// New.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store backed by pool. The pool must point at a
// database that has the migrations/ schema applied.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

var _ credleasestore.LeaseStore = (*Store)(nil)

// Put records a lease, replacing any prior lease with the same lease
// ID. The lease is validated first; an invalid lease is rejected and
// not stored, matching the in-memory store. A proxy-mode lease is
// stored with its lease token so GetByToken can resolve it; a
// direct-mode lease stores a NULL token. Re-issuing a lease ID with a
// rotated token overwrites the token column, so the prior token stops
// resolving and no dangling index row remains.
func (s *Store) Put(lease credential.Lease) error {
	if err := lease.Validate(); err != nil {
		return err
	}
	doc, err := json.Marshal(lease)
	if err != nil {
		return fmt.Errorf("credleasestore: encode lease: %w", err)
	}
	now := time.Now().UTC()
	ctx := context.Background()
	_, err = s.pool.Exec(ctx, `INSERT INTO credential_leases (
		lease_id, lease_token, delivery_mode, lease, created_at, updated_at
	) VALUES ($1, $2, $3, $4::jsonb, $5, $5)
	ON CONFLICT (lease_id) DO UPDATE SET
		lease_token = EXCLUDED.lease_token,
		delivery_mode = EXCLUDED.delivery_mode,
		lease = EXCLUDED.lease,
		updated_at = EXCLUDED.updated_at`,
		lease.LeaseID, leaseToken(lease), string(lease.DeliveryMode), string(doc), now)
	return err
}

// GetByToken resolves a proxy-mode lease by the bearer lease token the
// agent pod presents. ok is false when no lease holds the token. A
// direct-mode lease stores a NULL token, so it is never resolved here,
// and an empty token never matches a stored row.
func (s *Store) GetByToken(token string) (credential.Lease, bool) {
	row := s.pool.QueryRow(context.Background(),
		`SELECT lease FROM credential_leases WHERE lease_token = $1`, token)
	return scanLease(row)
}

// GetByID resolves a lease by its lease ID. ok is false when the store
// holds no lease with the ID.
func (s *Store) GetByID(leaseID string) (credential.Lease, bool) {
	row := s.pool.QueryRow(context.Background(),
		`SELECT lease FROM credential_leases WHERE lease_id = $1`, leaseID)
	return scanLease(row)
}

// Remove drops the lease with the given ID. It is a no-op when the
// store holds no such lease. The lease_token index row is dropped with
// the table row.
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
// credential-lease revocation step. The lease is stored as a JSONB
// document; the query matches on the document's SessionID field, which
// the credential.Lease struct serialises verbatim. An empty sessionIDs
// set yields no leases, and a row whose JSONB does not decode is
// skipped.
func (s *Store) LeasesBySession(sessionIDs []string) []credential.Lease {
	if len(sessionIDs) == 0 {
		return nil
	}
	rows, err := s.pool.Query(context.Background(),
		`SELECT lease FROM credential_leases WHERE lease->>'SessionID' = ANY($1)`,
		sessionIDs)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []credential.Lease
	for rows.Next() {
		if lease, ok := scanLease(rows); ok {
			out = append(out, lease)
		}
	}
	if rows.Err() != nil {
		return nil
	}
	return out
}

// LeasesByCredential returns every lease whose backing credential
// identity equals key, the Postgres-backed counterpart of the in-memory
// store's method. It backs the §4.9 emergency credential revocation
// step. The lease is stored as a JSONB document; the query matches the
// source-aware key fields the credential.Lease struct serialises
// verbatim (Source plus PoolID/CredentialID for a pool key, or
// TenantID/CredentialRef for a user key), mirroring
// credential.Lease.CredentialKey. A row whose JSONB does not decode is
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
		q = `SELECT lease FROM credential_leases
		     WHERE lease->>'Source' = $1 AND lease->>'PoolID' = $2 AND lease->>'CredentialID' = $3`
		args = []any{string(key.Source), key.PoolID, key.CredentialID}
	case credential.SourceUser:
		q = `SELECT lease FROM credential_leases
		     WHERE lease->>'Source' = $1 AND lease->>'TenantID' = $2 AND lease->>'CredentialRef' = $3`
		args = []any{string(key.Source), key.TenantID, key.CredentialRef}
	default:
		return nil
	}
	rows, err := s.pool.Query(context.Background(), q, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []credential.Lease
	for rows.Next() {
		if lease, ok := scanLease(rows); ok {
			out = append(out, lease)
		}
	}
	if rows.Err() != nil {
		return nil
	}
	return out
}

// scanLease decodes the lease JSONB column into a credential.Lease. A
// missing row yields the zero Lease and ok = false, matching the
// in-memory store's lookup miss.
func scanLease(row pgx.Row) (credential.Lease, bool) {
	var doc []byte
	if err := row.Scan(&doc); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return credential.Lease{}, false
		}
		return credential.Lease{}, false
	}
	var lease credential.Lease
	if err := json.Unmarshal(doc, &lease); err != nil {
		return credential.Lease{}, false
	}
	return lease, true
}

// leaseToken returns the lease token to index a proxy-mode lease by,
// or nil for a direct-mode lease so the lease_token column is NULL.
// Lease.Validate has already guaranteed a proxy-mode lease carries a
// non-empty token before Put reaches this point.
func leaseToken(lease credential.Lease) *string {
	if lease.DeliveryMode != credential.DeliveryProxy || lease.Proxy == nil {
		return nil
	}
	t := lease.Proxy.LeaseToken
	return &t
}
