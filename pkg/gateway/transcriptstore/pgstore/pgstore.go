// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed transcriptstore.Store,
// persisting session transcripts to the session_messages table. It
// is a drop-in alternative to the in-memory transcriptstore.Memory.
//
// Every operation runs inside a tenant-scoped transaction (§12.3).
// Append additionally takes a per-session transaction advisory lock
// so concurrent appends to one session allocate disjoint sequence
// numbers rather than colliding on the (session_id, seq) unique
// constraint.
package pgstore

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/pgtenant"
	"github.com/lennylabs/lenny/pkg/gateway/transcriptstore"
)

// Store is the Postgres-backed transcript registry. Construct with New.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store backed by pool. The pool must point at a
// database that has the migrations/ schema applied.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

var _ transcriptstore.Store = (*Store)(nil)

// Append adds entries to a session's transcript, assigning each the
// next per-session monotonic seq. The session row must already exist
// (session_messages.session_id is a foreign key to sessions.id).
func (s *Store) Append(ctx context.Context, tenantID, sessionID string, entries ...transcriptstore.Entry) error {
	if len(entries) == 0 {
		return nil
	}
	return pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		// Serialize appends to this session so concurrent writers
		// allocate disjoint seq values.
		if _, err := tx.Exec(ctx,
			`SELECT pg_advisory_xact_lock(hashtext($1))`, "transcript:"+sessionID); err != nil {
			return err
		}
		var maxSeq int64
		if err := tx.QueryRow(ctx,
			`SELECT COALESCE(MAX(seq), 0) FROM session_messages
			 WHERE session_id = $1::uuid AND tenant_id = $2`,
			sessionID, tenantID).Scan(&maxSeq); err != nil {
			return err
		}
		for _, e := range entries {
			maxSeq++
			ts := e.Timestamp
			if ts.IsZero() {
				ts = time.Now().UTC()
			}
			// The gateway owns schema_version per §15.4.1 line 1694;
			// normalize a zero-value caller field to the v1 baseline.
			schemaVer := e.SchemaVersion
			if schemaVer == 0 {
				schemaVer = transcriptstore.SchemaVersion
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO session_messages
				 (id, session_id, tenant_id, seq, role, content, created_at, schema_version)
				 VALUES (gen_random_uuid(), $1::uuid, $2, $3, $4, $5, $6, $7)`,
				sessionID, tenantID, maxSeq, e.Role, e.Content, ts, schemaVer); err != nil {
				return err
			}
		}
		return nil
	})
}

// Get returns the full ordered transcript for a session, or
// ErrNotFound when the session has no recorded entries.
func (s *Store) Get(ctx context.Context, tenantID, sessionID string) ([]transcriptstore.Entry, error) {
	var out []transcriptstore.Entry
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT id::text, seq, role, content, created_at, schema_version FROM session_messages
			 WHERE session_id = $1::uuid AND tenant_id = $2 ORDER BY seq`,
			sessionID, tenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			e, err := scanEntry(rows)
			if err != nil {
				return err
			}
			out = append(out, e)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, normalizeMiss(err)
	}
	if len(out) == 0 {
		return nil, transcriptstore.ErrNotFound
	}
	return out, nil
}

// Page returns up to limit entries after afterSeq. It returns
// ErrNotFound when the session has no transcript at all, and an empty
// slice when the transcript exists but afterSeq is past its end.
func (s *Store) Page(ctx context.Context, tenantID, sessionID string, afterSeq uint64, limit int) ([]transcriptstore.Entry, error) {
	if limit <= 0 {
		limit = 100
	}
	var out []transcriptstore.Entry
	var exists bool
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT id::text, seq, role, content, created_at, schema_version FROM session_messages
			 WHERE session_id = $1::uuid AND tenant_id = $2 AND seq > $3
			 ORDER BY seq LIMIT $4`,
			sessionID, tenantID, int64(afterSeq), limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			e, err := scanEntry(rows)
			if err != nil {
				return err
			}
			out = append(out, e)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(out) > 0 {
			exists = true
			return nil
		}
		// Empty page: distinguish "no transcript" from "afterSeq past end".
		return tx.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM session_messages
			 WHERE session_id = $1::uuid AND tenant_id = $2)`,
			sessionID, tenantID).Scan(&exists)
	})
	if err != nil {
		return nil, normalizeMiss(err)
	}
	if !exists {
		return nil, transcriptstore.ErrNotFound
	}
	return out, nil
}

// DeleteByUser implements the §12.1 mandatory-erasure primitive.
// Transcripts are session-scoped, so the §12.8 orchestrator walks the
// user's sessions and calls DeleteBySession. DeleteByUser at this
// layer returns (0, nil).
//
// spec: §12.1 line 5.
func (s *Store) DeleteByUser(_ context.Context, _, _ string) (int, error) {
	return 0, nil
}

// DeleteByTenant implements the §12.1 mandatory-erasure primitive.
// Removes every session_messages row belonging to the tenant by
// joining the sessions parent. session_messages also cascades on
// session deletion via the FK, so this is a defensive direct delete
// for the §12.8 Phase 4 path.
//
// spec: §12.1 line 5, §12.8 Phase 4.
func (s *Store) DeleteByTenant(ctx context.Context, tenantID string) (int, error) {
	if tenantID == "" {
		return 0, errors.New("transcriptstore: DeleteByTenant requires a concrete tenant_id")
	}
	var deleted int64
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`DELETE FROM session_messages
			   WHERE session_id IN (SELECT id FROM sessions WHERE tenant_id = $1)`, tenantID)
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

// scanEntry reads one row in (id, seq, role, content, created_at,
// schema_version) order.
func scanEntry(row pgx.Row) (transcriptstore.Entry, error) {
	var (
		e   transcriptstore.Entry
		seq int64
	)
	if err := row.Scan(&e.ID, &seq, &e.Role, &e.Content, &e.Timestamp, &e.SchemaVersion); err != nil {
		return transcriptstore.Entry{}, err
	}
	e.Seq = uint64(seq)
	return e, nil
}

// normalizeMiss maps the invalid-UUID-text error to ErrNotFound: a
// malformed session id can have no transcript.
func normalizeMiss(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "22P02" {
		return transcriptstore.ErrNotFound
	}
	return err
}
