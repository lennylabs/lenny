// SPDX-License-Identifier: MIT

// Package pgstore is the Postgres-backed interactionstore.Store,
// persisting the §6 / §9.2 pending interactive-session prompt registry
// to the interactions table. It is a drop-in alternative to
// interactionstore.Memory.
//
// interactions is tenant-scoped, so every operation runs inside a
// transaction that sets app.current_tenant for the §12.3
// lenny_tenant_guard trigger and the RLS policy.
//
// The scalar fields (kind, the §15.1 session/user authorization
// triple, phase, reason, the audit timestamps) are typed columns. The
// opaque nested fields — the interaction detail map and the client's
// response — are stored as jsonb documents, mirroring how the session
// store persists its workspace plan.
package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/pgtenant"
)

// Store is the Postgres-backed pending-interaction registry. Construct
// with New.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store backed by pool. The pool must point at a
// database that has the migrations/ schema applied.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

var _ interactionstore.Store = (*Store)(nil)

// selectList is the column projection for reads.
const selectList = `tenant_id, session_id, id, kind, user_id, phase,
	detail, response, reason, created_at, resolved_at`

// Put records a new pending interaction. It mirrors
// interactionstore.Memory: a missing Phase defaults to pending, a
// missing CreatedAt defaults to now, and re-recording the same
// (tenant, session, id) overwrites the prior row.
func (s *Store) Put(ctx context.Context, in interactionstore.Interaction) error {
	if in.Phase == "" {
		in.Phase = interactionstore.PhasePending
	}
	if in.CreatedAt.IsZero() {
		in.CreatedAt = time.Now().UTC()
	}
	detail, err := encodeDetail(in.Detail)
	if err != nil {
		return err
	}
	response, err := encodeResponse(in.Response)
	if err != nil {
		return err
	}
	return pgtenant.InTx(ctx, s.pool, in.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO interactions (
			tenant_id, session_id, id, kind, user_id, phase,
			detail, response, reason, created_at, resolved_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8::jsonb, $9, $10, $11)
		ON CONFLICT (tenant_id, session_id, id) DO UPDATE SET
			kind = EXCLUDED.kind, user_id = EXCLUDED.user_id,
			phase = EXCLUDED.phase, detail = EXCLUDED.detail,
			response = EXCLUDED.response, reason = EXCLUDED.reason,
			created_at = EXCLUDED.created_at, resolved_at = EXCLUDED.resolved_at`,
			in.TenantID, in.SessionID, in.ID, string(in.Kind), in.UserID,
			string(in.Phase), detail, response, in.Reason, in.CreatedAt,
			pgtenant.NullTime(in.ResolvedAt))
		return err
	})
}

// Get returns the interaction, scoped to the §15.1
// (tenant, session, user) authorization triple. A mismatch on any
// component is indistinguishable from a missing row, so the existence
// of another session's interactions is never leaked.
func (s *Store) Get(ctx context.Context, tenantID, sessionID, userID, id string) (interactionstore.Interaction, error) {
	var out interactionstore.Interaction
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+selectList+` FROM interactions
			 WHERE tenant_id = $1 AND session_id = $2 AND id = $3 AND user_id = $4`,
			tenantID, sessionID, id, userID)
		in, err := scanInteraction(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return interactionstore.ErrNotFound
		}
		if err != nil {
			return err
		}
		out = in
		return nil
	})
	if err != nil {
		return interactionstore.Interaction{}, err
	}
	return out, nil
}

// Resolve applies mutate to a pending interaction under
// SELECT ... FOR UPDATE. The caller's (tenant, session, user) must
// match the stored triple or ErrNotFound is returned; an interaction
// that is no longer pending returns ErrAlreadyResolved. The mutate
// error is propagated verbatim and aborts the write. ResolvedAt is
// stamped with the current time, mirroring interactionstore.Memory.
func (s *Store) Resolve(ctx context.Context, tenantID, sessionID, userID, id string, mutate func(*interactionstore.Interaction) error) (interactionstore.Interaction, error) {
	var out interactionstore.Interaction
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+selectList+` FROM interactions
			 WHERE tenant_id = $1 AND session_id = $2 AND id = $3 AND user_id = $4
			 FOR UPDATE`,
			tenantID, sessionID, id, userID)
		in, err := scanInteraction(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return interactionstore.ErrNotFound
		}
		if err != nil {
			return err
		}
		if in.Phase != interactionstore.PhasePending {
			return interactionstore.ErrAlreadyResolved
		}
		if err := mutate(&in); err != nil {
			return err
		}
		in.ResolvedAt = time.Now().UTC().Truncate(time.Microsecond)
		detail, err := encodeDetail(in.Detail)
		if err != nil {
			return err
		}
		response, err := encodeResponse(in.Response)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE interactions SET
			kind = $5, phase = $6, detail = $7::jsonb, response = $8::jsonb,
			reason = $9, resolved_at = $10
		WHERE tenant_id = $1 AND session_id = $2 AND id = $3 AND user_id = $4`,
			tenantID, sessionID, id, userID, string(in.Kind), string(in.Phase),
			detail, response, in.Reason, in.ResolvedAt); err != nil {
			return err
		}
		out = in
		return nil
	})
	if err != nil {
		return interactionstore.Interaction{}, err
	}
	return out, nil
}

// CountElicitations returns the number of §9.2 elicitation
// interactions recorded for the session across every resolution
// phase. The §9.1 maxElicitationsPerSession budget is a per-session
// lifetime cap, so already-resolved elicitations count toward it.
func (s *Store) CountElicitations(ctx context.Context, tenantID, sessionID string) (int, error) {
	var n int
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM interactions
			 WHERE tenant_id = $1 AND session_id = $2 AND kind = $3`,
			tenantID, sessionID, string(interactionstore.KindElicitation)).Scan(&n)
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

// ListPending returns every pending interaction recorded for the
// (tenant, session) tuple, ordered oldest first. The §7.2 line 153
// `ReattachedChild.pending_request_id` surface calls this so a
// resumed parent learns which request its child is waiting on.
func (s *Store) ListPending(ctx context.Context, tenantID, sessionID string) ([]interactionstore.Interaction, error) {
	var out []interactionstore.Interaction
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT `+selectList+` FROM interactions
			 WHERE tenant_id = $1 AND session_id = $2 AND phase = $3
			 ORDER BY created_at ASC`,
			tenantID, sessionID, string(interactionstore.PhasePending))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			in, err := scanInteraction(rows)
			if err != nil {
				return err
			}
			out = append(out, in)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteByUser removes every interaction directed at userID within
// tenantID and returns the count deleted — the §12.8 GDPR-erasure
// per-store adapter. Erasing a user with no interactions is a no-op
// returning (0, nil).
func (s *Store) DeleteByUser(ctx context.Context, tenantID, userID string) (int, error) {
	deleted := 0
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`DELETE FROM interactions WHERE tenant_id = $1 AND user_id = $2`,
			tenantID, userID)
		if err != nil {
			return err
		}
		deleted = int(tag.RowsAffected())
		return nil
	})
	return deleted, err
}

// DeleteByTenant removes every interaction belonging to tenantID and
// returns the count deleted — the §12.8 Phase-4 tenant-deletion adapter.
// A tenant with no interactions is a no-op returning (0, nil).
//
// spec: §12.1 line 5 (mandatory primitive); §12.8 Phase 4.
func (s *Store) DeleteByTenant(ctx context.Context, tenantID string) (int, error) {
	deleted := 0
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`DELETE FROM interactions WHERE tenant_id = $1`, tenantID)
		if err != nil {
			return err
		}
		deleted = int(tag.RowsAffected())
		return nil
	})
	return deleted, err
}

// DismissByUser sets every pending elicitation directed at userID
// within tenantID to dismissed and returns the count dismissed — the
// §11.4 full_revoke step that clears a revoked user's pending
// elicitations. Pending tool-use interactions are left untouched;
// §11.4 step 7 scopes the dismissal to elicitations.
func (s *Store) DismissByUser(ctx context.Context, tenantID, userID string) (int, error) {
	dismissed := 0
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE interactions SET phase = $3, resolved_at = $4
			 WHERE tenant_id = $1 AND user_id = $2 AND kind = $5 AND phase = $6`,
			tenantID, userID, string(interactionstore.PhaseDismissed),
			time.Now().UTC().Truncate(time.Microsecond),
			string(interactionstore.KindElicitation),
			string(interactionstore.PhasePending))
		if err != nil {
			return err
		}
		dismissed = int(tag.RowsAffected())
		return nil
	})
	return dismissed, err
}

// scanInteraction reads one row in selectList order into an
// Interaction.
func scanInteraction(row pgx.Row) (interactionstore.Interaction, error) {
	var (
		in               interactionstore.Interaction
		kind, phase      string
		detail, response []byte
		resolvedAt       *time.Time
	)
	if err := row.Scan(
		&in.TenantID, &in.SessionID, &in.ID, &kind, &in.UserID, &phase,
		&detail, &response, &in.Reason, &in.CreatedAt, &resolvedAt,
	); err != nil {
		return interactionstore.Interaction{}, err
	}
	in.Kind = interactionstore.Kind(kind)
	in.Phase = interactionstore.Phase(phase)
	if resolvedAt != nil {
		in.ResolvedAt = *resolvedAt
	}
	if len(detail) > 0 {
		var d map[string]any
		if err := json.Unmarshal(detail, &d); err != nil {
			return interactionstore.Interaction{}, fmt.Errorf("interactionstore: decode detail: %w", err)
		}
		in.Detail = d
	}
	if len(response) > 0 {
		var r any
		if err := json.Unmarshal(response, &r); err != nil {
			return interactionstore.Interaction{}, fmt.Errorf("interactionstore: decode response: %w", err)
		}
		in.Response = r
	}
	return in, nil
}

// encodeDetail marshals the opaque interaction detail map into the
// JSON document persisted in the detail jsonb column. An empty or nil
// map becomes a SQL NULL so the column stays null, and the scan path
// reads it back as a nil map — matching interactionstore.Memory, whose
// zero Interaction carries a nil Detail.
func encodeDetail(d map[string]any) ([]byte, error) {
	if len(d) == 0 {
		return nil, nil
	}
	b, err := json.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("interactionstore: encode detail: %w", err)
	}
	return b, nil
}

// encodeResponse marshals the client's response into the JSON document
// persisted in the response jsonb column. A nil response (an
// unresolved interaction) becomes a SQL NULL so the column stays null,
// and the scan path reads it back as a nil response.
func encodeResponse(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("interactionstore: encode response: %w", err)
	}
	return b, nil
}
