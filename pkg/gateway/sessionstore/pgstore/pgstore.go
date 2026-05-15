// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed §4.2 SessionStore. It
// satisfies the same sessionstore.Store contract as the in-memory
// memstore, so the gateway swaps backends without changing callers.
//
// Every operation runs inside a transaction that sets
// app.current_tenant: the §12.3 lenny_tenant_guard trigger rejects
// writes without it, and the row-level security policies filter reads
// by it. Reads additionally carry an explicit tenant_id predicate so
// the store behaves identically whether the connection role is the
// non-superuser lenny_app (RLS-filtered) or a superuser (RLS bypassed).
package pgstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/pgtenant"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// Store is the Postgres-backed SessionStore. Construct with New.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store backed by pool. The pool must point at a
// database that has the migrations/ schema applied.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

var _ sessionstore.Store = (*Store)(nil)

// selectList is the column projection for reads. UUID columns are
// rendered as text so they scan into the Session struct's string
// fields; parent_session_id collapses NULL to the empty string.
const selectList = `id::text, tenant_id, user_id, state, runtime_ref, pool_ref,
	isolation_profile, COALESCE(parent_session_id::text, ''), failure_class,
	failure_reason, workspace_snapshot_ref, workspace_snapshot_source,
	workspace_snapshot_at, parent_workspace_ref, retention_expires_at,
	upload_token_digest, upload_token_expiry, created_at, updated_at`

// Create persists a fresh session row. root_session_id is set to the
// session's own id: a standalone session is the root of its own tree.
func (s *Store) Create(ctx context.Context, sess sessionstore.Session) error {
	now := time.Now().UTC()
	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = now
	}
	if sess.UpdatedAt.IsZero() {
		sess.UpdatedAt = sess.CreatedAt
	}
	ref, src, at := snapshotCols(sess.WorkspaceSnapshot)

	const insertSQL = `INSERT INTO sessions (
		id, tenant_id, user_id, state, runtime_ref, pool_ref, isolation_profile,
		parent_session_id, root_session_id, failure_class, failure_reason,
		workspace_snapshot_ref, workspace_snapshot_source, workspace_snapshot_at,
		parent_workspace_ref, retention_expires_at, upload_token_digest,
		upload_token_expiry, created_at, updated_at
	) VALUES (
		$1::uuid, $2, $3, $4, $5, $6, $7,
		NULLIF($8, '')::uuid, $1::uuid, $9, $10,
		$11, $12, $13, $14, $15, $16, $17, $18, $19
	)`

	err := pgtenant.InTx(ctx, s.pool, sess.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, insertSQL,
			sess.ID, sess.TenantID, sess.UserID, string(sess.State), sess.RuntimeRef,
			sess.PoolRef, string(sess.IsolationProfile), sess.ParentSessionID,
			string(sess.FailureClass), sess.FailureReason,
			ref, src, pgtenant.NullTime(at), sess.ParentWorkspaceRef,
			pgtenant.NullTime(sess.RetentionExpiresAt), sess.UploadTokenDigest,
			pgtenant.NullTime(sess.UploadTokenExpiry), sess.CreatedAt, sess.UpdatedAt)
		return err
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return sessionstore.ErrAlreadyExists
	}
	return err
}

// Get returns the session row keyed by (tenantID, id). A cross-tenant
// miss is indistinguishable from a missing row (§4.2 isolation).
func (s *Store) Get(ctx context.Context, tenantID, id string) (sessionstore.Session, error) {
	var out sessionstore.Session
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+selectList+` FROM sessions WHERE id = $1::uuid AND tenant_id = $2`,
			id, tenantID)
		sess, err := scanSession(row)
		if err != nil {
			return err
		}
		out = sess
		return nil
	})
	if err != nil {
		return sessionstore.Session{}, normalizeMiss(err)
	}
	return out, nil
}

// Update applies mutate to the row under SELECT ... FOR UPDATE, then
// writes it back. UpdatedAt strictly advances on every successful
// Update (clamped to the prior value + 1µs, the Postgres timestamptz
// resolution) so callers can rely on monotonicity.
func (s *Store) Update(ctx context.Context, tenantID, id string, mutate func(*sessionstore.Session) error) (sessionstore.Session, error) {
	const updateSQL = `UPDATE sessions SET
		user_id = $3, state = $4, runtime_ref = $5, pool_ref = $6,
		isolation_profile = $7, parent_session_id = NULLIF($8, '')::uuid,
		failure_class = $9, failure_reason = $10, workspace_snapshot_ref = $11,
		workspace_snapshot_source = $12, workspace_snapshot_at = $13,
		parent_workspace_ref = $14, retention_expires_at = $15,
		upload_token_digest = $16, upload_token_expiry = $17, updated_at = $18
	WHERE id = $1::uuid AND tenant_id = $2`

	var out sessionstore.Session
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+selectList+` FROM sessions WHERE id = $1::uuid AND tenant_id = $2 FOR UPDATE`,
			id, tenantID)
		sess, err := scanSession(row)
		if err != nil {
			return err
		}
		prevUpdated := sess.UpdatedAt
		if err := mutate(&sess); err != nil {
			return err
		}
		sess.UpdatedAt = pgtenant.MonotonicNext(prevUpdated, time.Now())
		ref, src, at := snapshotCols(sess.WorkspaceSnapshot)
		if _, err := tx.Exec(ctx, updateSQL,
			id, tenantID, sess.UserID, string(sess.State), sess.RuntimeRef,
			sess.PoolRef, string(sess.IsolationProfile), sess.ParentSessionID,
			string(sess.FailureClass), sess.FailureReason, ref, src, pgtenant.NullTime(at),
			sess.ParentWorkspaceRef, pgtenant.NullTime(sess.RetentionExpiresAt),
			sess.UploadTokenDigest, pgtenant.NullTime(sess.UploadTokenExpiry), sess.UpdatedAt,
		); err != nil {
			return err
		}
		out = sess
		return nil
	})
	if err != nil {
		return sessionstore.Session{}, normalizeMiss(err)
	}
	return out, nil
}

// List returns the tenant's sessions newest-first, narrowed by filter.
func (s *Store) List(ctx context.Context, tenantID string, filter sessionstore.ListFilter) ([]sessionstore.Session, error) {
	q := `SELECT ` + selectList + ` FROM sessions WHERE tenant_id = $1`
	args := []any{tenantID}
	if filter.State != "" {
		args = append(args, string(filter.State))
		q += fmt.Sprintf(" AND state = $%d", len(args))
	}
	if filter.RuntimeRef != "" {
		args = append(args, filter.RuntimeRef)
		q += fmt.Sprintf(" AND runtime_ref = $%d", len(args))
	}
	if filter.FailureClass != "" {
		args = append(args, string(filter.FailureClass))
		q += fmt.Sprintf(" AND failure_class = $%d", len(args))
	}
	q += ` ORDER BY created_at DESC, id`

	var out []sessionstore.Session
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			sess, err := scanSession(rows)
			if err != nil {
				return err
			}
			out = append(out, sess)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Delete removes the session row. session_messages rows cascade.
func (s *Store) Delete(ctx context.Context, tenantID, id string) error {
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`DELETE FROM sessions WHERE id = $1::uuid AND tenant_id = $2`, id, tenantID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return sessionstore.ErrNotFound
		}
		return nil
	})
	return normalizeMiss(err)
}

// scanSession reads one row in selectList order into a Session.
func scanSession(row pgx.Row) (sessionstore.Session, error) {
	var (
		s                       sessionstore.Session
		state, isoProf, failCls string
		wsRef, wsSrc            string
		wsAt, retAt, upExp      *time.Time
	)
	if err := row.Scan(
		&s.ID, &s.TenantID, &s.UserID, &state, &s.RuntimeRef, &s.PoolRef,
		&isoProf, &s.ParentSessionID, &failCls, &s.FailureReason,
		&wsRef, &wsSrc, &wsAt, &s.ParentWorkspaceRef, &retAt,
		&s.UploadTokenDigest, &upExp, &s.CreatedAt, &s.UpdatedAt,
	); err != nil {
		return sessionstore.Session{}, err
	}
	s.State = session.State(state)
	s.IsolationProfile = isolation.Profile(isoProf)
	s.FailureClass = session.FailureClass(failCls)
	if retAt != nil {
		s.RetentionExpiresAt = *retAt
	}
	if upExp != nil {
		s.UploadTokenExpiry = *upExp
	}
	if wsRef != "" || wsSrc != "" || wsAt != nil {
		ws := &sessionstore.WorkspaceSnapshot{
			Ref:    wsRef,
			Source: sessionstore.WorkspaceSnapshotSource(wsSrc),
		}
		if wsAt != nil {
			ws.Timestamp = *wsAt
		}
		s.WorkspaceSnapshot = ws
	}
	return s, nil
}

// snapshotCols flattens an optional WorkspaceSnapshot into its three
// column values.
func snapshotCols(ws *sessionstore.WorkspaceSnapshot) (ref, src string, at time.Time) {
	if ws == nil {
		return "", "", time.Time{}
	}
	return ws.Ref, string(ws.Source), ws.Timestamp
}

// normalizeMiss maps pgx.ErrNoRows and the invalid-UUID-text error to
// the store's ErrNotFound: a malformed or absent id is, from the
// caller's view, simply "no such session".
func normalizeMiss(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return sessionstore.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "22P02" {
		return sessionstore.ErrNotFound
	}
	return err
}
