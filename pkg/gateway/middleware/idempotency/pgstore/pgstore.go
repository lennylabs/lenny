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
	"github.com/lennylabs/lenny/pkg/gateway/storage/pgtenant"
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

// Claim atomically reserves the (tenant_id, idempotency_key) slot. A
// concurrent retry that arrives between Claim and the matching Put
// observes the pending row (response_status = 0) and the middleware
// rejects it with IDEMPOTENCY_KEY_IN_FLIGHT, preventing double
// execution. spec: §11.5 line 277; F-11.5.2.
//
// The implementation uses `INSERT … ON CONFLICT DO UPDATE WHERE expired`
// so an expired pending row (e.g., from a previous request that crashed
// before Put or Release) is overwritten by the fresh claim — the §11.5
// TTL is the upper bound on how long a stuck claim can block a retry.
// When the existing row is still within the TTL window the function
// returns it with claimed=false; the middleware branches on its state.
func (s *Store) Claim(ctx context.Context, tenantID, key, bodyHash string, now time.Time) (idempotency.Record, bool, error) {
	if err := (idempotency.Key{TenantID: tenantID, Value: key}).Validate(); err != nil {
		return idempotency.Record{}, false, err
	}
	cutoff := now.Add(-idempotency.TTL)
	var (
		out     idempotency.Record
		claimed bool
	)
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		// The CTE inserts a fresh pending row; on conflict it overwrites
		// only if the existing row is past the §11.5 TTL (treated as
		// absent per Get's read-time gate). The RETURNING clause yields
		// the inserted/updated row when the claim succeeded; the SELECT
		// fallback fetches the live row when the WHERE guard blocked the
		// update. ROW_TO_JSON is not used; instead a sentinel boolean
		// distinguishes the two branches.
		const sql = `
WITH claim AS (
	INSERT INTO idempotency_keys (
		tenant_id, idempotency_key, body_hash,
		response_status, response_headers, response_body, stored_at
	) VALUES ($1, $2, $3, 0, '{}'::jsonb, '\x'::bytea, $4)
	ON CONFLICT (tenant_id, idempotency_key) DO UPDATE
		SET body_hash = EXCLUDED.body_hash,
		    response_status = 0,
		    response_headers = '{}'::jsonb,
		    response_body = '\x'::bytea,
		    stored_at = EXCLUDED.stored_at
		WHERE idempotency_keys.stored_at < $5
	RETURNING tenant_id, idempotency_key, body_hash,
		response_status, response_headers, response_body, stored_at,
		TRUE AS claimed
)
SELECT tenant_id, idempotency_key, body_hash,
	response_status, response_headers, response_body, stored_at, claimed
FROM claim
UNION ALL
SELECT tenant_id, idempotency_key, body_hash,
	response_status, response_headers, response_body, stored_at, FALSE AS claimed
FROM idempotency_keys
WHERE tenant_id = $1 AND idempotency_key = $2
  AND NOT EXISTS (SELECT 1 FROM claim)
`
		row := tx.QueryRow(ctx, sql, tenantID, key, bodyHash, now, cutoff)
		rec, gotClaimed, scanErr := scanClaimRow(row)
		if scanErr != nil {
			return scanErr
		}
		if gotClaimed {
			out = rec
			claimed = true
		} else {
			out = rec
		}
		return nil
	})
	if err != nil {
		return idempotency.Record{}, false, err
	}
	if claimed {
		// The pending row is the caller's slot; do not return it as
		// "existing" — the middleware uses (existing, claimed=true) to
		// mean "you won the slot, execute now".
		return idempotency.Record{}, true, nil
	}
	return out, false, nil
}

// Release removes the (tenant_id, idempotency_key) row. The middleware
// calls it when the inner handler returned a 5xx (release so a retry
// can re-execute) or when the response was streamed (a frozen snapshot
// is not safely replayable). Releasing a row that no longer exists is
// not an error. spec: §11.5 line 277; F-11.5.2.
func (s *Store) Release(ctx context.Context, tenantID, key string) error {
	return pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`DELETE FROM idempotency_keys WHERE tenant_id = $1 AND idempotency_key = $2`,
			tenantID, key)
		return err
	})
}

// scanClaimRow scans the Claim CTE row, extracting the eight columns
// (the seven record columns plus the `claimed` boolean).
func scanClaimRow(row pgx.Row) (idempotency.Record, bool, error) {
	var (
		rec        idempotency.Record
		headersRaw []byte
		claimed    bool
	)
	if err := row.Scan(
		&rec.Key.TenantID, &rec.Key.Value, &rec.BodyHash,
		&rec.Response.StatusCode, &headersRaw, &rec.Response.Body, &rec.StoredAt,
		&claimed,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Neither the CTE INSERT nor the SELECT fallback produced a
			// row, which can only happen if the row was deleted between
			// the two arms. Treat as "nothing existed"; the caller will
			// claim on the next retry.
			return idempotency.Record{}, false, pgx.ErrNoRows
		}
		return idempotency.Record{}, false, err
	}
	if len(headersRaw) > 0 {
		if err := json.Unmarshal(headersRaw, &rec.Response.Headers); err != nil {
			return idempotency.Record{}, false, fmt.Errorf("idempotencystore: decode headers: %w", err)
		}
	}
	return rec, claimed, nil
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
