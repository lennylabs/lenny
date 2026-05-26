// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed idempotency middleware Store,
// persisting the §11.5 idempotency-key cache to the idempotency_keys
// table. It is a drop-in alternative to idempotency.MemoryStore.
//
// idempotency_keys is tenant-scoped, so every operation runs inside a
// transaction that sets app.current_tenant for the §12.3
// lenny_tenant_guard trigger and the RLS policy.
//
// Get treats a record past the §11.5 24-hour TTL as absent, mirroring
// idempotency.MemoryStore, so the middleware writes a fresh entry.
// Expired rows are reclaimed by the DeleteExpired garbage-collection
// sweep rather than at read time.
package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	idemmw "github.com/lennylabs/lenny/pkg/gateway/middleware/idempotency"
	"github.com/lennylabs/lenny/pkg/gateway/pgtenant"
	"github.com/lennylabs/lenny/pkg/idempotency"
)

// Store is the Postgres-backed idempotency-key cache. Construct with New.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store backed by pool. The pool must point at a
// database that has the migrations/ schema applied.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

var _ idemmw.Store = (*Store)(nil)

const selectList = `tenant_id, idempotency_key, body_hash,
	response_status, response_headers, response_body, stored_at`

// Get returns the stored Record for (tenantID, key). A record past the
// §11.5 TTL is reported as absent so the middleware re-executes the
// operation and writes a fresh entry.
func (s *Store) Get(ctx context.Context, tenantID, key string) (idempotency.Record, bool, error) {
	var rec idempotency.Record
	found := false
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+selectList+` FROM idempotency_keys
			 WHERE tenant_id = $1 AND idempotency_key = $2`, tenantID, key)
		r, err := scanRecord(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		rec, found = r, true
		return nil
	})
	if err != nil {
		return idempotency.Record{}, false, err
	}
	if !found || rec.IsExpired(time.Now().UTC()) {
		return idempotency.Record{}, false, nil
	}
	return rec, true, nil
}

// Put inserts or replaces the record for (tenant, key). It validates
// the §11.5 key format so the durable backend holds the same
// invariants the request boundary enforces.
func (s *Store) Put(ctx context.Context, rec idempotency.Record) error {
	if err := rec.Key.Validate(); err != nil {
		return err
	}
	if rec.StoredAt.IsZero() {
		rec.StoredAt = time.Now().UTC()
	}
	headers, err := json.Marshal(headerMap(rec.Response.Headers))
	if err != nil {
		return fmt.Errorf("idempotencystore: encode headers: %w", err)
	}
	body := rec.Response.Body
	if body == nil {
		body = []byte{}
	}
	return pgtenant.InTx(ctx, s.pool, rec.Key.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO idempotency_keys (
			tenant_id, idempotency_key, body_hash,
			response_status, response_headers, response_body, stored_at
		) VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7)
		ON CONFLICT (tenant_id, idempotency_key) DO UPDATE SET
			body_hash = EXCLUDED.body_hash,
			response_status = EXCLUDED.response_status,
			response_headers = EXCLUDED.response_headers,
			response_body = EXCLUDED.response_body,
			stored_at = EXCLUDED.stored_at`,
			rec.Key.TenantID, rec.Key.Value, rec.BodyHash,
			rec.Response.StatusCode, string(headers), body, rec.StoredAt)
		return err
	})
}

// DeleteExpired removes the records for one tenant whose stored_at
// predates cutoff, the §11.5 24-hour TTL garbage-collection sweep.
// The sweep is tenant-scoped: the DELETE filters on tenant_id
// explicitly so it never spans tenants even when the connecting role
// bypasses RLS, and the lenny_tenant_guard trigger fires for every
// deleted row. The caller iterates the tenant set and calls
// DeleteExpired once per tenant. Returns the number of rows reclaimed.
func (s *Store) DeleteExpired(ctx context.Context, tenantID string, cutoff time.Time) (int64, error) {
	var reclaimed int64
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`DELETE FROM idempotency_keys WHERE tenant_id = $1 AND stored_at < $2`,
			tenantID, cutoff)
		if err != nil {
			return err
		}
		reclaimed = tag.RowsAffected()
		return nil
	})
	return reclaimed, err
}

// scanRecord reads one row in selectList order into a Record.
func scanRecord(row pgx.Row) (idempotency.Record, error) {
	var (
		rec        idempotency.Record
		headersRaw []byte
	)
	if err := row.Scan(
		&rec.Key.TenantID, &rec.Key.Value, &rec.BodyHash,
		&rec.Response.StatusCode, &headersRaw, &rec.Response.Body, &rec.StoredAt,
	); err != nil {
		return idempotency.Record{}, err
	}
	if len(headersRaw) > 0 {
		if err := json.Unmarshal(headersRaw, &rec.Response.Headers); err != nil {
			return idempotency.Record{}, fmt.Errorf("idempotencystore: decode headers: %w", err)
		}
	}
	return rec, nil
}

// headerMap returns a non-nil map so json.Marshal yields {} rather
// than null for the NOT NULL response_headers column. The map value
// is a string slice so multi-valued headers (Set-Cookie, Vary,
// WWW-Authenticate, …) round-trip without flattening. spec: §11.5.
func headerMap(m map[string][]string) map[string][]string {
	if m == nil {
		return map[string][]string{}
	}
	return m
}
