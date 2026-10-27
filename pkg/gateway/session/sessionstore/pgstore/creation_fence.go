// SPDX-License-Identifier: MIT

package pgstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pgtenant"
)

// TranscriptCopier copies the transcript of sourceSessionID into
// destSessionID on tx, the insert transaction of CreateWithTranscript. The
// transcript store's CopySessionTx has this signature.
type TranscriptCopier func(ctx context.Context, tx pgx.Tx, tenantID, sourceSessionID, destSessionID string) error

// WithTranscriptCopier wires the copy CreateWithTranscript runs in its
// insert transaction. The session store and the transcript store share one
// pool and no transaction, so the session store takes the copy as a
// function over its own transaction rather than holding a second store.
// spec: §12.8 (Lifecycle fence and drain), §15.1 (REST API).
func WithTranscriptCopier(copyFn TranscriptCopier) Option {
	return func(s *Store) { s.copyTranscript = copyFn }
}

// readTenantMarkersSQL and readUserMarkersSQL take FOR SHARE rather than
// FOR KEY SHARE. FOR SHARE conflicts with the FOR UPDATE that the tenant and
// user stores' Update, SetErasureRestrictionTx, and
// ClearProcessingRestrictionTx take, and also with the FOR NO KEY UPDATE of
// a plain UPDATE, which SoftDelete issues. FOR KEY SHARE conflicts with
// neither of the latter, so SoftDelete could commit between the fence's read
// and the insert's commit. Every marker writer locks exactly one of the two
// rows and the fence locks the tenant row first, so the order admits no lock
// cycle.
const (
	readTenantMarkersSQL = `SELECT suspended, state FROM tenants WHERE id = $1 FOR SHARE`
	readUserMarkersSQL   = `SELECT disabled, deleted_at IS NOT NULL, processing_restricted, erasure_job_id
		FROM users WHERE tenant_id = $1 AND subject = $2 FOR SHARE`
	readParentStateSQL = `SELECT state FROM sessions WHERE tenant_id = $1 AND id = $2::uuid FOR SHARE`
)

// readCreationMarkers reads the tenant row, the owner's user row for a
// non-empty owner, and a delegated child's parent session row FOR SHARE on
// tx, in that order. A missing tenant or user row leaves the matching Found
// field false; any other read error is returned, so the caller's insert
// fails closed. An empty parentSessionID reads no parent row, which is how
// an ID reservation runs only the tenant and user rules.
// spec: §12.8 (Lifecycle fence and drain).
func readCreationMarkers(ctx context.Context, tx pgx.Tx, tenantID, ownerUserID string, delegatedChild bool, parentSessionID string) (sessionstore.CreationMarkers, error) {
	m := sessionstore.CreationMarkers{IsDelegatedChild: delegatedChild, ParentSessionID: parentSessionID}
	err := tx.QueryRow(ctx, readTenantMarkersSQL, tenantID).Scan(&m.TenantSuspended, &m.TenantState)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return m, fmt.Errorf("creation fence: read tenant %s: %w", tenantID, err)
	default:
		m.TenantFound = true
	}
	if ownerUserID != "" {
		err = tx.QueryRow(ctx, readUserMarkersSQL, tenantID, ownerUserID).
			Scan(&m.UserDisabled, &m.UserDeleted, &m.ProcessingRestricted, &m.ErasureJobID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return m, fmt.Errorf("creation fence: read user %s/%s: %w", tenantID, ownerUserID, err)
		default:
			m.UserFound = true
		}
	}
	if delegatedChild && parentSessionID != "" {
		terminal, err := readParentTerminal(ctx, tx, tenantID, parentSessionID)
		if err != nil {
			return m, err
		}
		m.ParentTerminal = terminal
	}
	return m, nil
}

// readParentTerminal reads a delegated child's parent session row FOR SHARE
// on tx and reports whether the parent is terminal or has no row. A missing
// row counts as terminal, so the refusal comes before the
// parent_session_id foreign key's error. Any other read error is returned
// wrapped, so the insert fails closed.
//
// Lock order: the parent_session_id foreign key already takes FOR KEY SHARE
// on the parent row when the INSERT runs, so this FOR SHARE read keeps the
// existing order of tenant row, user row, then parent row. The terminal
// writer's SELECT ... FOR UPDATE in Update conflicts with FOR SHARE, so the
// child insert and the parent's terminal write serialize: a child that
// commits first is visible to the parent's cascade listing from the
// primary, and a child whose read runs after the terminal commit is refused.
// spec: §12.8 (Lifecycle fence and drain), §8.2 (Parent freshness).
func readParentTerminal(ctx context.Context, tx pgx.Tx, tenantID, parentSessionID string) (bool, error) {
	var state string
	err := tx.QueryRow(ctx, readParentStateSQL, tenantID, parentSessionID).Scan(&state)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return true, nil
	case err != nil:
		return false, fmt.Errorf("creation fence: read parent session %s/%s: %w", tenantID, parentSessionID, err)
	}
	return session.IsTerminal(session.State(state)), nil
}

// fenceInsert runs the creation fence on tx: it reads the marker rows and
// returns *sessionstore.CreationRefusal when they refuse a row ownerUserID
// owns. The fence has no exemption: a row created terminal, such as the
// derive-failure audit row, is fenced like any other. A non-empty
// parentSessionID on a delegated child adds the parent rule; a reservation
// passes an empty one.
// spec: §12.8 (Lifecycle fence and drain).
func fenceInsert(ctx context.Context, tx pgx.Tx, tenantID, ownerUserID string, delegatedChild bool, parentSessionID string) error {
	m, err := readCreationMarkers(ctx, tx, tenantID, ownerUserID, delegatedChild, parentSessionID)
	if err != nil {
		return err
	}
	if ref := m.Refusal(tenantID, ownerUserID); ref != nil {
		return ref
	}
	return nil
}

// deleteOwnReservation deletes the reservation whose ID and owner match the
// inserted row and reports whether it deleted one. A row with no owner has
// no reservation, because Reserve records nothing for an empty owner.
// spec: §12.8 (Lifecycle fence and drain).
func deleteOwnReservation(ctx context.Context, tx pgx.Tx, sess sessionstore.Session) (bool, error) {
	if sess.UserID == "" {
		return false, nil
	}
	tag, err := tx.Exec(ctx,
		`DELETE FROM session_reservations WHERE tenant_id = $1 AND session_id = $2::uuid AND user_id = $3`,
		sess.TenantID, sess.ID, sess.UserID)
	if err != nil {
		return false, fmt.Errorf("delete reservation %s: %w", sess.ID, err)
	}
	return tag.RowsAffected() > 0, nil
}

// requireReservation is CreateReserved's afterInsert: a row with an owner
// whose reservation was already gone is refused. The DELETE that found it
// gone conflicts with the DELETE of the same row by DeleteByUser or
// DeleteByTenant, so the insert either removed the reservation or observes
// that the erasure removed it.
func requireReservation(_ context.Context, _ pgx.Tx, sess sessionstore.Session, deleted bool) error {
	if sess.UserID != "" && !deleted {
		return sessionstore.ReservationEndedRefusal(sess.UserID)
	}
	return nil
}

// CreateReserved implements sessionstore.Store.
// spec: §12.8 (Lifecycle fence and drain).
func (s *Store) CreateReserved(ctx context.Context, sess sessionstore.Session) error {
	return s.create(ctx, sess, nil, requireReservation)
}

// CreateWithTranscript implements sessionstore.Store: the copy runs on the
// insert transaction after the fence, the INSERT, and the reservation
// DELETE, so the copied rows commit only with the session row and the fence
// holds its FOR SHARE locks until the copy ends.
// spec: §12.8 (Lifecycle fence and drain), §15.1 (REST API).
func (s *Store) CreateWithTranscript(ctx context.Context, sess sessionstore.Session, transcriptSourceID string) error {
	if s.copyTranscript == nil {
		return sessionstore.ErrTranscriptCopierUnwired
	}
	return s.create(ctx, sess, nil, func(ctx context.Context, tx pgx.Tx, sess sessionstore.Session, _ bool) error {
		if err := s.copyTranscript(ctx, tx, sess.TenantID, transcriptSourceID, sess.ID); err != nil {
			return fmt.Errorf("copy transcript %s to %s: %w", transcriptSourceID, sess.ID, err)
		}
		return nil
	})
}

// Reserve implements sessionstore.Store.
// spec: §12.8 (Lifecycle fence and drain).
func (s *Store) Reserve(ctx context.Context, tenantID, ownerUserID, sessionID string, delegatedChild bool) error {
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		// spec: §12.8 (Lifecycle fence and drain) — a reservation runs only
		// the tenant and user rules, so it reads no parent row.
		if err := fenceInsert(ctx, tx, tenantID, ownerUserID, delegatedChild, ""); err != nil {
			return err
		}
		if ownerUserID == "" {
			return nil
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO session_reservations (tenant_id, session_id, user_id) VALUES ($1, $2::uuid, $3)`,
			tenantID, sessionID, ownerUserID)
		return err
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return sessionstore.ErrAlreadyExists
	}
	return err
}

// ReleaseReservation implements sessionstore.Store.
// spec: §12.8 (Lifecycle fence and drain).
func (s *Store) ReleaseReservation(ctx context.Context, tenantID, sessionID string) error {
	return pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`DELETE FROM session_reservations WHERE tenant_id = $1 AND session_id = $2::uuid`,
			tenantID, sessionID)
		return err
	})
}

// ReservedSessionIDs implements sessionstore.Store. It reads the primary
// pool, because a reservation the read pool has not replicated must still
// reach the owner's erasure.
// spec: §12.8 (Lifecycle fence and drain).
func (s *Store) ReservedSessionIDs(ctx context.Context, tenantID, userID string) ([]string, error) {
	var out []string
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		ids, err := collectStrings(ctx, tx,
			`SELECT session_id::text FROM session_reservations
			 WHERE tenant_id = $1 AND user_id = $2 ORDER BY created_at, session_id`,
			tenantID, userID)
		out = ids
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
