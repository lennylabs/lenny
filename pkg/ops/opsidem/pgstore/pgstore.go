// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed §25.4 opsidem.Store. It persists
// idempotency records to the ops_idempotency_keys table (migration 0116)
// so required-key endpoints survive a lenny-ops restart and coordinate
// across replicas. A Postgres outage surfaces as opsidem.ErrStoreUnavailable
// so the middleware fails required-key endpoints closed (503) rather than
// silently proceeding.
//
// The ops_idempotency_keys table is platform-scoped (the §25 control
// plane is not multi-tenanted at this boundary), so the store does not
// run inside a tenant-scoped transaction; the table has no RLS policy
// and no tenant column.
//
// spec: §25.4 lines 2011-2130.
package pgstore

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/ops/opsidem"
)

// Store is the Postgres-backed §25.4 idempotency registry. Construct with New.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store backed by pool. The pool must point at a database
// with the migrations/ schema applied.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// responseEnvelope is the JSONB-encoded cached response. The body is
// base64-encoded so an arbitrary (non-JSON) response is still valid JSONB.
type responseEnvelope struct {
	StatusCode int    `json:"statusCode"`
	BodyB64    string `json:"bodyB64"`
}

// classifyErr maps a pgx error to opsidem.ErrStoreUnavailable for a
// connection/transport failure (the §25.4 Postgres-outage case), leaving
// a server-side query error (PgError — the database responded) intact for
// the caller to surface as a 500.
func classifyErr(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return err
	}
	return opsidem.ErrStoreUnavailable
}

// Claim implements opsidem.Store. It runs the lazy-cleanup, owned-by-other
// probe, and conditional insert in one transaction so concurrent claimers
// resolve deterministically.
func (s *Store) Claim(ctx context.Context, key, callerID, endpoint string, ttl time.Duration, now time.Time) (opsidem.Record, opsidem.ClaimResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return opsidem.Record{}, 0, classifyErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Lazy cleanup of this key's own expired row so a fresh claim inserts.
	if _, err := tx.Exec(ctx,
		`DELETE FROM ops_idempotency_keys WHERE key=$1 AND caller_id=$2 AND expires_at <= $3`,
		key, callerID, now); err != nil {
		return opsidem.Record{}, 0, classifyErr(err)
	}

	// §25.4 IDEMPOTENCY_KEY_OWNED_BY_OTHER_CALLER: a live row under a
	// different caller_id.
	var dummy int
	err = tx.QueryRow(ctx,
		`SELECT 1 FROM ops_idempotency_keys WHERE key=$1 AND caller_id<>$2 AND expires_at > $3 LIMIT 1`,
		key, callerID, now).Scan(&dummy)
	if err == nil {
		return opsidem.Record{}, opsidem.ClaimOwnedByOther, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return opsidem.Record{}, 0, classifyErr(err)
	}

	// Conditional insert: a returned row means we claimed it.
	var inserted bool
	err = tx.QueryRow(ctx,
		`INSERT INTO ops_idempotency_keys (key, caller_id, endpoint, status, created_at, expires_at)
		 VALUES ($1, $2, $3, 'in_progress', $4, $5)
		 ON CONFLICT (key, caller_id) DO NOTHING
		 RETURNING true`,
		key, callerID, endpoint, now, now.Add(ttl)).Scan(&inserted)
	if err == nil && inserted {
		if cerr := tx.Commit(ctx); cerr != nil {
			return opsidem.Record{}, 0, classifyErr(cerr)
		}
		return opsidem.Record{
			Key: key, CallerID: callerID, Endpoint: endpoint,
			Status: opsidem.StatusInProgress, CreatedAt: now, ExpiresAt: now.Add(ttl),
		}, opsidem.ClaimInserted, nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return opsidem.Record{}, 0, classifyErr(err)
	}

	// Conflict: an existing live row. Read it to decide replay vs in-progress.
	rec, rerr := scanRow(tx.QueryRow(ctx,
		`SELECT key, caller_id, endpoint, status, response, created_at, expires_at
		 FROM ops_idempotency_keys WHERE key=$1 AND caller_id=$2`, key, callerID))
	if rerr != nil {
		return opsidem.Record{}, 0, classifyErr(rerr)
	}
	if cerr := tx.Commit(ctx); cerr != nil {
		return opsidem.Record{}, 0, classifyErr(cerr)
	}
	if rec.Status == opsidem.StatusInProgress {
		return rec, opsidem.ClaimInProgress, nil
	}
	return rec, opsidem.ClaimReplay, nil
}

// Complete implements opsidem.Store.
func (s *Store) Complete(ctx context.Context, key, callerID string, statusCode int, response []byte, _ time.Time) error {
	env, err := json.Marshal(responseEnvelope{
		StatusCode: statusCode,
		BodyB64:    base64.StdEncoding.EncodeToString(response),
	})
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx,
		`UPDATE ops_idempotency_keys SET status='completed', response=$3 WHERE key=$1 AND caller_id=$2`,
		key, callerID, env)
	return classifyErr(err)
}

// Fail implements opsidem.Store. A server-error mutation must be
// retryable, so the row is deleted rather than left as a terminal failed
// record that would replay the error for the TTL window.
func (s *Store) Fail(ctx context.Context, key, callerID string, _ time.Time) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM ops_idempotency_keys WHERE key=$1 AND caller_id=$2`, key, callerID)
	return classifyErr(err)
}

// PruneExpired implements opsidem.Store.
func (s *Store) PruneExpired(ctx context.Context, now time.Time) (int, error) {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM ops_idempotency_keys WHERE expires_at <= $1`, now)
	if err != nil {
		return 0, classifyErr(err)
	}
	return int(tag.RowsAffected()), nil
}

// scanRow decodes one ops_idempotency_keys row, unpacking the response
// envelope back into StatusCode + Response.
func scanRow(row pgx.Row) (opsidem.Record, error) {
	var (
		rec opsidem.Record
		env []byte
	)
	if err := row.Scan(&rec.Key, &rec.CallerID, &rec.Endpoint, &rec.Status, &env, &rec.CreatedAt, &rec.ExpiresAt); err != nil {
		return opsidem.Record{}, err
	}
	if len(env) > 0 {
		var e responseEnvelope
		if err := json.Unmarshal(env, &e); err == nil {
			rec.StatusCode = e.StatusCode
			if body, derr := base64.StdEncoding.DecodeString(e.BodyB64); derr == nil {
				rec.Response = body
			}
		}
	}
	return rec, nil
}

// Compile-time guard.
var _ opsidem.Store = (*Store)(nil)
