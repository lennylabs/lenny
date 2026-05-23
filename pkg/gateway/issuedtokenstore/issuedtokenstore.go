// SPDX-License-Identifier: MIT

// Package issuedtokenstore is the §12.2 TokenIssuanceStore: the
// Postgres-backed index of issued-token metadata over the
// issued_tokens table. The Token Service (§13.3) writes a row here
// before it returns a minted token to the caller, so a token can
// always be matched against its revocation state ("write-before-issue
// atomicity"). The store never holds the raw token — only the
// SHA-256 digest of the token bytes.
//
// Every operation runs inside a transaction that sets
// app.current_tenant, which the §12.3 lenny_tenant_guard trigger
// requires on writes to issued_tokens.
package issuedtokenstore

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/gateway/auditstore"
	"github.com/lennylabs/lenny/pkg/gateway/pgtenant"
)

// Sentinel errors.
var (
	ErrNotFound      = errors.New("issuedtokenstore: token not found")
	ErrAlreadyExists = errors.New("issuedtokenstore: jti already recorded")
)

// IssuedToken is one row of issued-token metadata. The §12.2 schema
// pins the column set; this struct mirrors it.
type IssuedToken struct {
	// JTI is the RFC 7519 token identifier and the primary key.
	JTI string
	// TenantID is the tenant the token was issued for.
	TenantID string
	// Subject is the OIDC `sub` the token authenticates.
	Subject string
	// TokenHash is the SHA-256 digest of the raw token bytes. The raw
	// token is never stored.
	TokenHash []byte
	// Scope is the granted scope set.
	Scope []string
	// Audience is the token's intended audience.
	Audience string
	// IssuedAt and ExpiresAt bound the token's validity window.
	IssuedAt  time.Time
	ExpiresAt time.Time
	// RevokedAt is the revocation instant; zero when the token has
	// not been revoked. RevokedReason carries the operator-supplied
	// cause.
	RevokedAt     time.Time
	RevokedReason string
	// ActSubject and ParentJTI are populated for delegation children
	// (§8): the acting subject and the parent token's JTI.
	ActSubject string
	ParentJTI  string
}

// Revoked reports whether the token has been revoked.
func (t IssuedToken) Revoked() bool { return !t.RevokedAt.IsZero() }

// Store is the Postgres-backed TokenIssuanceStore. Construct with New.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store backed by pool. The pool must point at a
// database that has the migrations/ schema applied.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const selectList = `jti, tenant_id, sub, token_hash, scope, audience,
	issued_at, exp, revoked_at, revoked_reason, act_sub, parent_jti`

// Record persists issued-token metadata. It returns ErrAlreadyExists
// when the JTI is already recorded — the JTI primary key is what
// makes write-before-issue idempotent under retry.
func (s *Store) Record(ctx context.Context, tok IssuedToken) error {
	err := pgtenant.InTx(ctx, s.pool, tok.TenantID, func(tx pgx.Tx) error {
		return insertIssued(ctx, tx, tok)
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrAlreadyExists
	}
	return err
}

// RecordWithAudit persists issued-token metadata and writes the §13.3
// `token.exchanged` audit row in a single Postgres transaction.
//
// The §13.3 write-before-issue ordering is implemented here:
//
//  1. pgtenant.InTx opens one transaction and SETs app.current_tenant.
//  2. The transaction acquires the §11.7 per-tenant audit advisory
//     lock (via auditstore.AppendInTx).
//  3. The issued_tokens row is INSERTed.
//  4. The audit_log row is INSERTed under the same advisory lock.
//
// Only after COMMIT does the caller surface the signed token to the
// client. If any step fails, the transaction rolls back and neither the
// issued-token row nor the audit row is persisted — the Token Service
// then never returns the minted token to the caller. The auditEventType
// and auditPayload come from the caller (the Token Service handler)
// because the audit payload schema is owned by §16.7 / §25.4 and the
// store should not bake that vocabulary in.
//
// auditPayload is a JSON-encoded object. The sealed audit row is
// returned to the caller for cross-checking and metric labelling; the
// returned row carries the assigned sequence number and prev_hash so a
// caller can trace the audit chain forward.
//
// spec: §13.3 line 589 — single Postgres transaction binding the
// issued-token INSERT and the token.exchanged audit INSERT.
func (s *Store) RecordWithAudit(ctx context.Context, tok IssuedToken, auditEventType string, auditPayload json.RawMessage, auditAt time.Time) (audit.Row, error) {
	var committed audit.Row
	err := pgtenant.InTx(ctx, s.pool, tok.TenantID, func(tx pgx.Tx) error {
		// Step 1: acquire the §11.7 advisory lock + INSERT the audit
		// row first so the chain prev_hash is stable; the issued_tokens
		// INSERT below is bound to the same transaction and rolls back
		// together on any failure.
		row, err := auditstore.AppendInTx(ctx, tx, tok.TenantID, auditEventType, auditPayload, auditAt)
		if err != nil {
			return err
		}
		committed = row
		// Step 2: INSERT the issued_tokens row.
		if err := insertIssued(ctx, tx, tok); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return audit.Row{}, ErrAlreadyExists
		}
		return audit.Row{}, err
	}
	return committed, nil
}

// insertIssued runs the issued_tokens INSERT on tx. It assumes the
// caller has already opened a pgtenant.InTx and acquired the §11.7
// audit advisory lock when binding to an audit row.
func insertIssued(ctx context.Context, tx pgx.Tx, tok IssuedToken) error {
	const insertSQL = `INSERT INTO issued_tokens (
		jti, tenant_id, sub, token_hash, scope, audience,
		issued_at, exp, revoked_at, revoked_reason, act_sub, parent_jti
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`
	_, err := tx.Exec(ctx, insertSQL,
		tok.JTI, tok.TenantID, tok.Subject, tok.TokenHash, scopeArg(tok.Scope),
		tok.Audience, tok.IssuedAt, tok.ExpiresAt, pgtenant.NullTime(tok.RevokedAt),
		pgtenant.NullString(tok.RevokedReason), pgtenant.NullString(tok.ActSubject), pgtenant.NullString(tok.ParentJTI))
	return err
}

// Get returns the issued-token metadata for jti within tenantID.
// A cross-tenant miss is indistinguishable from a missing row.
func (s *Store) Get(ctx context.Context, tenantID, jti string) (IssuedToken, error) {
	var out IssuedToken
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+selectList+` FROM issued_tokens WHERE jti = $1 AND tenant_id = $2`,
			jti, tenantID)
		tok, err := scanToken(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		out = tok
		return nil
	})
	if err != nil {
		return IssuedToken{}, err
	}
	return out, nil
}

// Revoke marks jti revoked with the given reason and instant. It is
// idempotent: revoking an already-revoked token overwrites the reason
// and timestamp. Returns ErrNotFound when the token does not exist.
func (s *Store) Revoke(ctx context.Context, tenantID, jti, reason string, at time.Time) error {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	return pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE issued_tokens SET revoked_at = $3, revoked_reason = $4
			 WHERE jti = $1 AND tenant_id = $2`,
			jti, tenantID, at, pgtenant.NullString(reason))
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// RevokeBySubject marks every not-yet-revoked token issued for
// (tenantID, subject) as revoked with the given reason and instant, and
// returns the JTIs it revoked. It backs the §11.4 full_revoke
// cached-auth invalidation step: the caller pushes the returned JTIs
// into the in-memory revocation cache so the user's issued tokens are
// rejected on the next request without waiting for a rehydration. An
// already-revoked token is left untouched and its JTI is not returned,
// so the operation is idempotent across repeated full_revoke calls. A
// user with no issued tokens yields an empty slice and no error.
func (s *Store) RevokeBySubject(ctx context.Context, tenantID, subject, reason string, at time.Time) ([]string, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	var revoked []string
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`UPDATE issued_tokens SET revoked_at = $3, revoked_reason = $4
			 WHERE tenant_id = $1 AND sub = $2 AND revoked_at IS NULL
			 RETURNING jti`,
			tenantID, subject, at, pgtenant.NullString(reason))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var jti string
			if err := rows.Scan(&jti); err != nil {
				return err
			}
			revoked = append(revoked, jti)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return revoked, nil
}

// ListRevoked returns every revoked token for the tenant, supporting
// the §12.2 revocation-cache rehydration path. The result is ordered
// by revocation instant.
func (s *Store) ListRevoked(ctx context.Context, tenantID string) ([]IssuedToken, error) {
	var out []IssuedToken
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT `+selectList+` FROM issued_tokens
			 WHERE tenant_id = $1 AND revoked_at IS NOT NULL
			 ORDER BY revoked_at`, tenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			tok, err := scanToken(rows)
			if err != nil {
				return err
			}
			out = append(out, tok)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteExpired removes the tenant's tokens whose exp is at or before
// cutoff and returns the number of rows deleted. This is the §12.2
// expired-row GC path; revoked rows past expiry are removed too,
// since an expired token cannot be presented regardless.
func (s *Store) DeleteExpired(ctx context.Context, tenantID string, cutoff time.Time) (int, error) {
	var deleted int
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`DELETE FROM issued_tokens WHERE tenant_id = $1 AND exp <= $2`,
			tenantID, cutoff)
		if err != nil {
			return err
		}
		deleted = int(tag.RowsAffected())
		return nil
	})
	if err != nil {
		return 0, err
	}
	return deleted, nil
}

// scanToken reads one row in selectList order into an IssuedToken.
func scanToken(row pgx.Row) (IssuedToken, error) {
	var (
		t                             IssuedToken
		revokedAt                     *time.Time
		revokedReason, actSub, parent *string
	)
	if err := row.Scan(
		&t.JTI, &t.TenantID, &t.Subject, &t.TokenHash, &t.Scope, &t.Audience,
		&t.IssuedAt, &t.ExpiresAt, &revokedAt, &revokedReason, &actSub, &parent,
	); err != nil {
		return IssuedToken{}, err
	}
	if revokedAt != nil {
		t.RevokedAt = *revokedAt
	}
	if revokedReason != nil {
		t.RevokedReason = *revokedReason
	}
	if actSub != nil {
		t.ActSubject = *actSub
	}
	if parent != nil {
		t.ParentJTI = *parent
	}
	return t, nil
}

// scopeArg normalizes a nil scope slice to a non-nil empty slice so
// the text[] column is never written NULL.
func scopeArg(scope []string) []string {
	if scope == nil {
		return []string{}
	}
	return scope
}
