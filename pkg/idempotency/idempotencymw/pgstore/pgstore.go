// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed idempotency middleware Store,
// persisting the §11.5 idempotency-key cache to the idempotency_keys
// table. It is a drop-in alternative to idempotency.MemoryStore.
//
// A row is keyed by (tenant_id, subject, session_id, idempotency_key):
// the scope of the principal that sent the request and the caller's key
// string. session_id holds the empty string for a bearer bound to no
// session, because a primary-key column cannot be NULL.
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

	"github.com/lennylabs/lenny/pkg/idempotency"
	"github.com/lennylabs/lenny/pkg/idempotency/idempotencymw"
	"github.com/lennylabs/lenny/pkg/storage/pgtenant"
)

// Store is the Postgres-backed idempotency-key cache. Construct with New.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store backed by pool. The pool must point at a
// database that has the migrations/ schema applied.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

var _ idempotencymw.Store = (*Store)(nil)

const selectList = `tenant_id, subject, session_id, idempotency_key, body_hash,
	response_status, response_headers, response_body, stored_at`

// Get returns the stored Record for key. A record past the §11.5 TTL is
// reported as absent so the middleware re-executes the operation and
// writes a fresh entry.
func (s *Store) Get(ctx context.Context, key idempotency.Key) (idempotency.Record, bool, error) {
	var rec idempotency.Record
	found := false
	err := pgtenant.InTx(ctx, s.pool, key.TenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+selectList+` FROM idempotency_keys
			 WHERE tenant_id = $1 AND subject = $2 AND session_id = $3 AND idempotency_key = $4`,
			key.TenantID, key.Subject, key.SessionID, key.Value)
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

// Put inserts or replaces the record filed under rec.Key. It validates
// the §11.5 key so the durable backend holds the same invariants the
// request boundary enforces, including a non-empty subject.
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
	k := rec.Key
	return pgtenant.InTx(ctx, s.pool, k.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO idempotency_keys (
			tenant_id, subject, session_id, idempotency_key, body_hash,
			response_status, response_headers, response_body, stored_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8, $9)
		ON CONFLICT (tenant_id, subject, session_id, idempotency_key) DO UPDATE SET
			body_hash = EXCLUDED.body_hash,
			response_status = EXCLUDED.response_status,
			response_headers = EXCLUDED.response_headers,
			response_body = EXCLUDED.response_body,
			stored_at = EXCLUDED.stored_at`,
			k.TenantID, k.Subject, k.SessionID, k.Value, rec.BodyHash,
			rec.Response.StatusCode, string(headers), body, rec.StoredAt)
		return err
	})
}

// claimSQL inserts a fresh pending row for the key's scope. On conflict it
// overwrites only a row past the §11.5 TTL (treated as absent per Get's
// read-time gate). The RETURNING clause yields the inserted or updated row
// when the claim succeeded; the SELECT fallback fetches the live row when
// the WHERE guard blocked the update. A sentinel boolean distinguishes the
// two branches.
const claimSQL = `
WITH claim AS (
	INSERT INTO idempotency_keys (
		tenant_id, subject, session_id, idempotency_key, body_hash,
		response_status, response_headers, response_body, stored_at
	) VALUES ($1, $2, $3, $4, $5, 0, '{}'::jsonb, '\x'::bytea, $6)
	ON CONFLICT (tenant_id, subject, session_id, idempotency_key) DO UPDATE
		SET body_hash = EXCLUDED.body_hash,
		    response_status = 0,
		    response_headers = '{}'::jsonb,
		    response_body = '\x'::bytea,
		    stored_at = EXCLUDED.stored_at
		WHERE idempotency_keys.stored_at < $7
	RETURNING tenant_id, subject, session_id, idempotency_key, body_hash,
		response_status, response_headers, response_body, stored_at,
		TRUE AS claimed
)
SELECT tenant_id, subject, session_id, idempotency_key, body_hash,
	response_status, response_headers, response_body, stored_at, claimed
FROM claim
UNION ALL
SELECT tenant_id, subject, session_id, idempotency_key, body_hash,
	response_status, response_headers, response_body, stored_at, FALSE AS claimed
FROM idempotency_keys
WHERE tenant_id = $1 AND subject = $2 AND session_id = $3 AND idempotency_key = $4
  AND NOT EXISTS (SELECT 1 FROM claim)
`

// Claim atomically reserves the slot of key. A concurrent retry that
// arrives between Claim and the matching Put observes the pending row
// (response_status = 0) and the middleware rejects it with
// IDEMPOTENCY_KEY_IN_FLIGHT, preventing double execution. A claim under
// another principal's scope is a different row and never observes
// it. spec: §11.5; F-11.5.2.
//
// The implementation uses `INSERT … ON CONFLICT DO UPDATE WHERE expired`
// so an expired pending row (e.g., from a previous request that crashed
// before Put or Release) is overwritten by the fresh claim — the §11.5
// TTL is the upper bound on how long a stuck claim can block a retry.
// When the existing row is still within the TTL window the function
// returns it with claimed=false; the middleware branches on its state.
func (s *Store) Claim(ctx context.Context, key idempotency.Key, bodyHash string, now time.Time) (idempotency.Record, bool, error) {
	if err := key.Validate(); err != nil {
		return idempotency.Record{}, false, err
	}
	cutoff := now.Add(-idempotency.TTL)
	var (
		out     idempotency.Record
		claimed bool
	)
	err := pgtenant.InTx(ctx, s.pool, key.TenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, claimSQL,
			key.TenantID, key.Subject, key.SessionID, key.Value, bodyHash, now, cutoff)
		rec, gotClaimed, scanErr := scanClaimRow(row)
		if scanErr != nil {
			return scanErr
		}
		out, claimed = rec, gotClaimed
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

// Release removes the row of key. The middleware calls it when the inner
// handler returned a 5xx (release so a retry can re-execute) or when the
// response was streamed (a frozen snapshot is not safely replayable).
// Releasing a row that no longer exists is not an error. spec: §11.5;
// F-11.5.2.
func (s *Store) Release(ctx context.Context, key idempotency.Key) error {
	return pgtenant.InTx(ctx, s.pool, key.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`DELETE FROM idempotency_keys
			 WHERE tenant_id = $1 AND subject = $2 AND session_id = $3 AND idempotency_key = $4`,
			key.TenantID, key.Subject, key.SessionID, key.Value)
		return err
	})
}

// scanClaimRow scans the Claim CTE row, extracting the record columns
// plus the `claimed` boolean.
func scanClaimRow(row pgx.Row) (idempotency.Record, bool, error) {
	var (
		rec        idempotency.Record
		headersRaw []byte
		claimed    bool
	)
	if err := row.Scan(
		&rec.Key.TenantID, &rec.Key.Subject, &rec.Key.SessionID, &rec.Key.Value, &rec.BodyHash,
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

// DeleteByUser deletes every record of one user in one tenant: the rows
// whose subject is the user, under every bound session. User erasure runs
// it as its idempotency-records step. The DELETE names tenant_id so it
// never spans tenants. It returns the number of rows deleted.
//
// spec: §12.8 (Compliance Interfaces)
func (s *Store) DeleteByUser(ctx context.Context, tenantID, subject string) (int, error) {
	if tenantID == "" || subject == "" {
		return 0, fmt.Errorf("idempotencystore: DeleteByUser needs a tenant and a subject")
	}
	var deleted int64
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`DELETE FROM idempotency_keys WHERE tenant_id = $1 AND subject = $2`, tenantID, subject)
		if err != nil {
			return err
		}
		deleted = tag.RowsAffected()
		return nil
	})
	return int(deleted), err
}

// DeleteByTenant deletes every record of one tenant. Tenant deletion
// Phase 4 runs it as its idempotency-records step. It returns the number
// of rows deleted.
//
// spec: §12.8 (Compliance Interfaces)
func (s *Store) DeleteByTenant(ctx context.Context, tenantID string) (int, error) {
	if tenantID == "" {
		return 0, fmt.Errorf("idempotencystore: DeleteByTenant needs a tenant")
	}
	var deleted int64
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM idempotency_keys WHERE tenant_id = $1`, tenantID)
		if err != nil {
			return err
		}
		deleted = tag.RowsAffected()
		return nil
	})
	return int(deleted), err
}

// scanRecord reads one row in selectList order into a Record.
func scanRecord(row pgx.Row) (idempotency.Record, error) {
	var (
		rec        idempotency.Record
		headersRaw []byte
	)
	if err := row.Scan(
		&rec.Key.TenantID, &rec.Key.Subject, &rec.Key.SessionID, &rec.Key.Value, &rec.BodyHash,
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
