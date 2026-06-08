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
	// Audience is the token's intended audience as a single
	// space-joined string. Preserved for legacy readers; new code
	// should prefer Audiences (the list form) when available.
	Audience string
	// Audiences is the multi-value audience set captured per RFC 8693.
	// The writer always populates this from the original list so a
	// forensic reverse lookup can match individual audiences without
	// re-splitting Audience. spec: §4.3 line 193, migration 0058.
	Audiences []string
	// DialectCapAppliedSeconds records the §13.3 per-dialect lifetime
	// cap that capped the issued exp. Zero means no per-dialect cap
	// was configured for the audience. spec: §4.3 line 193, §13.3
	// dialect-cap discipline.
	DialectCapAppliedSeconds int
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

// Eraser is the §12.1 mandatory-erasure surface. The compile-time
// assertion below proves the TokenIssuanceStore honors the erasure
// primitives, so a future refactor that drops either method fails to
// build. The full pluggable-backend role interface for the
// interface-less §12.2 stores is tracked separately (F-12.1.3).
//
// spec: §12.1 line 5 — "enforced at compile time by Go interface
// satisfaction".
type Eraser interface {
	DeleteByUser(ctx context.Context, tenantID, userID string) (int, error)
	DeleteByTenant(ctx context.Context, tenantID string) (int, error)
}

var _ Eraser = (*Store)(nil)

const selectList = `jti, tenant_id, sub, token_hash, scope, audience,
	audiences, dialect_cap_applied_seconds,
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
//
// spec: §4.3 line 193 — audiences and dialect_cap_applied_seconds are
// recorded alongside the legacy single-valued audience column so
// forensic reverse lookups by individual audience and "why was the
// exp this value" both have a durable source.
func insertIssued(ctx context.Context, tx pgx.Tx, tok IssuedToken) error {
	const insertSQL = `INSERT INTO issued_tokens (
		jti, tenant_id, sub, token_hash, scope, audience,
		audiences, dialect_cap_applied_seconds,
		issued_at, exp, revoked_at, revoked_reason, act_sub, parent_jti
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`
	_, err := tx.Exec(ctx, insertSQL,
		tok.JTI, tok.TenantID, tok.Subject, tok.TokenHash, scopeArg(tok.Scope),
		tok.Audience, audienceListArg(tok.Audiences, tok.Audience), tok.DialectCapAppliedSeconds,
		tok.IssuedAt, tok.ExpiresAt, pgtenant.NullTime(tok.RevokedAt),
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

// RevokedToken identifies one token revoked by a cascade: its JTI,
// subject, and whether it is the cascade root (the explicitly-targeted
// token) or a delegation descendant reached through parent_jti.
type RevokedToken struct {
	JTI     string
	Subject string
	IsRoot  bool
}

// RevokeCascade implements the §13.3 line 603 / §8.3 recursive
// revocation: it revokes rootJTI and every delegation descendant
// reachable through the parent_jti edge, in one statement via a
// recursive CTE. The root row is stamped with rootReason; each
// descendant with childReason. Already-revoked rows in the subtree are
// left untouched (idempotent) and excluded from the result, so a repeat
// call after a partial revocation only revokes the remainder. The
// returned slice lists every row this call revoked, with IsRoot marking
// the root. ErrNotFound is returned only when rootJTI does not exist in
// the tenant. Token lineage is acyclic (a child's parent_jti always
// references a pre-existing token), so the recursive walk terminates.
//
// spec: §13.3 line 603 ("recursive child revocation"); §8.3.
func (s *Store) RevokeCascade(ctx context.Context, tenantID, rootJTI, rootReason, childReason string, at time.Time) ([]RevokedToken, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	var out []RevokedToken
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		// Confirm the root exists so a typo'd jti is ErrNotFound rather
		// than a silent no-op; an already-revoked root still cascades to
		// any not-yet-revoked descendant.
		var exists bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM issued_tokens WHERE jti = $1 AND tenant_id = $2)`,
			rootJTI, tenantID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		rows, err := tx.Query(ctx,
			`WITH RECURSIVE subtree AS (
				SELECT jti FROM issued_tokens WHERE jti = $1 AND tenant_id = $2
				UNION ALL
				SELECT c.jti FROM issued_tokens c
					JOIN subtree p ON c.parent_jti = p.jti
					WHERE c.tenant_id = $2
			)
			UPDATE issued_tokens t
			SET revoked_at = $3,
			    revoked_reason = CASE WHEN t.jti = $1 THEN $4 ELSE $5 END
			FROM subtree s
			WHERE t.jti = s.jti AND t.tenant_id = $2 AND t.revoked_at IS NULL
			RETURNING t.jti, t.sub, (t.jti = $1) AS is_root`,
			rootJTI, tenantID, at, pgtenant.NullString(rootReason), pgtenant.NullString(childReason))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var rt RevokedToken
			if err := rows.Scan(&rt.JTI, &rt.Subject, &rt.IsRoot); err != nil {
				return err
			}
			out = append(out, rt)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
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

// DeleteByUser implements the §12.1 mandatory-erasure primitive for the
// §12.2 TokenIssuanceStore role. It hard-deletes every issued-token row
// whose subject is userID within tenantID and returns the count
// removed. RevokeBySubject only marks rows revoked (the row, including
// its token hash, survives); a GDPR erasure must remove the row
// outright, so the §12.8 user-erasure path calls DeleteByUser rather
// than RevokeBySubject. Deleting an erased user's tokens is safe
// because a deleted token can no longer be presented for validation.
//
// spec: §12.1 line 5, §12.8 step `TokenStore`.
func (s *Store) DeleteByUser(ctx context.Context, tenantID, userID string) (int, error) {
	if tenantID == "" || userID == "" {
		return 0, errors.New("issuedtokenstore: DeleteByUser requires non-empty tenant_id and user_id")
	}
	var deleted int
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`DELETE FROM issued_tokens WHERE tenant_id = $1 AND sub = $2`,
			tenantID, userID)
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

// DeleteByTenant implements the §12.1 mandatory-erasure primitive. It
// hard-deletes every issued-token row owned by tenantID — the §12.8
// Phase 4 tenant-teardown path for the TokenIssuanceStore role.
//
// spec: §12.1 line 5, §12.8 Phase 4.
func (s *Store) DeleteByTenant(ctx context.Context, tenantID string) (int, error) {
	if tenantID == "" {
		return 0, errors.New("issuedtokenstore: DeleteByTenant requires a concrete tenant_id")
	}
	var deleted int
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`DELETE FROM issued_tokens WHERE tenant_id = $1`, tenantID)
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
//
// spec: §4.3 line 193 — Audiences and DialectCapAppliedSeconds are
// populated alongside the legacy Audience column so callers prefer
// the list form when present.
func scanToken(row pgx.Row) (IssuedToken, error) {
	var (
		t                             IssuedToken
		revokedAt                     *time.Time
		revokedReason, actSub, parent *string
	)
	if err := row.Scan(
		&t.JTI, &t.TenantID, &t.Subject, &t.TokenHash, &t.Scope, &t.Audience,
		&t.Audiences, &t.DialectCapAppliedSeconds,
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

// audienceListArg normalizes the audience list for the audiences
// text[] column. When the caller passes an explicit list it is used
// verbatim. When the list is empty but the legacy single-valued
// `audience` string is set, split on whitespace so a legacy single
// audience like "lenny-gateway" arrives as ["lenny-gateway"] and a
// joined value like "lenny-gateway lenny-ops" arrives as the matching
// list. The text[] column is never written NULL.
// spec: §4.3 line 193, migration 0058.
func audienceListArg(audiences []string, legacy string) []string {
	if len(audiences) > 0 {
		out := make([]string, 0, len(audiences))
		for _, a := range audiences {
			if a == "" {
				continue
			}
			out = append(out, a)
		}
		return out
	}
	if legacy == "" {
		return []string{}
	}
	out := []string{}
	for _, a := range splitAudienceFallback(legacy) {
		if a == "" {
			continue
		}
		out = append(out, a)
	}
	return out
}

// splitAudienceFallback splits a legacy space-joined audience string
// into its constituent values. It exists so callers that only set
// IssuedToken.Audience (the pre-0058 form) still populate the
// audiences[] column on the way to Postgres.
func splitAudienceFallback(s string) []string {
	out := []string{}
	cur := ""
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
