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
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

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
	const insertSQL = `INSERT INTO issued_tokens (
		jti, tenant_id, sub, token_hash, scope, audience,
		issued_at, exp, revoked_at, revoked_reason, act_sub, parent_jti
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`

	err := pgtenant.InTx(ctx, s.pool, tok.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, insertSQL,
			tok.JTI, tok.TenantID, tok.Subject, tok.TokenHash, scopeArg(tok.Scope),
			tok.Audience, tok.IssuedAt, tok.ExpiresAt, pgtenant.NullTime(tok.RevokedAt),
			pgtenant.NullString(tok.RevokedReason), pgtenant.NullString(tok.ActSubject), pgtenant.NullString(tok.ParentJTI))
		return err
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrAlreadyExists
	}
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
