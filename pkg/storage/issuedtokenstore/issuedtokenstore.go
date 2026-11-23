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
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/audit/auditstore"
	"github.com/lennylabs/lenny/pkg/storage/pgtenant"
)

// Sentinel errors.
var (
	ErrNotFound      = errors.New("issuedtokenstore: token not found")
	ErrAlreadyExists = errors.New("issuedtokenstore: jti already recorded")
	// ErrCallerRevoked reports that the caller token of a revocation
	// request is itself revoked; RevokeCascadeByCaller revokes nothing.
	ErrCallerRevoked = errors.New("issuedtokenstore: caller token revoked")
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
	// re-splitting Audience. spec: §4.3, migration 0058.
	Audiences []string
	// DialectCapAppliedSeconds records the §13.3 per-dialect lifetime
	// cap that capped the issued exp. Zero means no per-dialect cap
	// was configured for the audience. spec: §4.3, §13.3
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
	// (§8): the acting subject and the parent token's JTI. A
	// platform-admin impersonation bearer carries ActSubject (the
	// impersonating admin) and no ParentJTI (§13.3 "Platform-admin
	// impersonation" rule 2).
	ActSubject string
	ParentJTI  string
	// ImpersonatorTenantID and ImpersonatorJTI are set only on a
	// platform-admin impersonation row: the tenant and jti of the admin's
	// own bearer that started it. A recursive revocation that reaches that
	// bearer revokes the impersonation row through them
	// (RevokeImpersonationsByImpersonator). They do not enter the
	// impersonation predicate.
	ImpersonatorTenantID string
	ImpersonatorJTI      string
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

// WithSubjectLock acquires a per-subject session-scoped Postgres advisory
// lock, runs fn, and releases the lock on the same pinned connection. It
// serializes the gateway's non-atomic admin-credential rotation
// read-modify-write for one subject so two concurrent rotations cannot
// interleave the Kubernetes Secret patch and the separate issued-token-store
// transactions and drop a successor jti through the Secret's blind
// full-map replace.
//
// The lock is session-scoped (`pg_advisory_lock` / `pg_advisory_unlock` on
// one acquired connection) rather than transaction-scoped
// (`pg_advisory_xact_lock`, used by the §11.7 audit lock and the
// session-admission lock) because the protected sequence is not a single
// Postgres transaction: it interleaves a K8s Secret read-modify-write with
// several independent store transactions (Record, RecordWithAudit,
// RevokeWithAudit), each opening its own pgtenant.InTx. A
// transaction-scoped lock releases at the COMMIT of whichever store
// transaction took it, before the next store or Secret step runs, so it
// cannot span the whole sequence. fn does its own store transactions on
// other pool connections; this connection only holds the mutual exclusion.
//
// The key is derived by Postgres as hashtext("admintoken:"+subject) so it
// is stable across gateway replicas. The advisory lock is database-global
// and reads no tenant-scoped row, so this method does not open a
// pgtenant.InTx or SET app.current_tenant; tenantID names the tenant the
// caller's protected rotation operates on (the §17.6 AdminTenant) and
// keeps the calling convention parallel to the store's other methods.
//
// spec: §13.3,
// §13.3.
func (s *Store) WithSubjectLock(ctx context.Context, tenantID, subject string, fn func(context.Context) error) error {
	_ = tenantID // Advisory lock is database-global; keyed on subject only.
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("issuedtokenstore: acquire connection for subject lock %q: %w", subject, err)
	}
	defer conn.Release()

	// hashtext(text) returns int4; pg_advisory_lock accepts it via the
	// implicit widening to bigint. Computing the key in Postgres keeps the
	// "admintoken:"+subject convention in one place and matches the
	// saltlockpg session-scoped idiom.
	const lockKey = `SELECT pg_advisory_lock(hashtext('admintoken:' || $1))`
	if _, err := conn.Exec(ctx, lockKey, subject); err != nil {
		return fmt.Errorf("issuedtokenstore: acquire advisory lock for subject %q: %w", subject, err)
	}
	defer func() {
		// Release on a background-derived context so a cancelled ctx still
		// frees the session-level lock before the connection returns to the
		// pool; a leaked session lock would deadlock every later rotation
		// for this subject. spec: erasurejob/saltlockpg leak-safe unlock.
		const unlock = `SELECT pg_advisory_unlock(hashtext('admintoken:' || $1))`
		_, _ = conn.Exec(context.WithoutCancel(ctx), unlock, subject)
	}()

	return fn(ctx)
}

// Eraser is the §12.1 mandatory-erasure surface. The compile-time
// assertion below proves the TokenIssuanceStore honors the erasure
// primitives, so a future refactor that drops either method fails to
// build. The full pluggable-backend role interface for the
// interface-less §12.2 stores is tracked separately (F-12.1.3).
//
// spec: §12.1 — "enforced at compile time by Go interface
// satisfaction".
type Eraser interface {
	DeleteByUser(ctx context.Context, tenantID, userID string) (int, error)
	DeleteByTenant(ctx context.Context, tenantID string) (int, error)
}

var _ Eraser = (*Store)(nil)

const selectList = `jti, tenant_id, sub, token_hash, scope, audience,
	audiences, dialect_cap_applied_seconds,
	issued_at, exp, revoked_at, revoked_reason, act_sub, parent_jti,
	impersonator_tenant_id, impersonator_jti`

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
//     lock (auditstore.AcquireLockInTx).
//  3. Step (1a): the issued_tokens rows of the sources are re-read in
//     one SELECT. A source whose row has revoked_at set returns a
//     *SourceRevokedError naming its role; otherwise a source whose row
//     is an impersonation row returns a *SourceImpersonationError naming
//     its role. Either way no issuance statement runs. A source with no
//     row passes.
//  4. The audit_log row is INSERTed under the same advisory lock.
//  5. The issued_tokens row is INSERTed.
//
// sources lists the exchange inputs to re-read: the subject_token jti
// and, on a delegation child mint, the actor_token jti. The gateway's
// in-process admin rotation passes an empty list, because it has no
// subject token to re-read.
//
// Only after COMMIT does the caller surface the signed token to the
// client. If any step fails, the transaction rolls back and neither the
// issued-token row nor the audit row is persisted, and the Token Service
// never returns the minted token to the caller. The auditEventType and
// auditPayload come from the caller (the Token Service handler) because
// the audit payload schema is owned by §16.7 / §25.4 and the store does
// not bake that vocabulary in.
//
// auditPayload is a JSON-encoded object. The sealed audit row is
// returned to the caller for cross-checking and metric labelling; the
// returned row carries the assigned sequence number and prev_hash so a
// caller can trace the audit chain forward.
//
// spec: §13.3 (Credential Flow: Write-before-issue ordering),
// §11.7.
func (s *Store) RecordWithAudit(ctx context.Context, tok IssuedToken, sources []SourceToken, auditEventType string, auditPayload json.RawMessage, auditAt time.Time) (audit.Row, error) {
	var committed audit.Row
	err := pgtenant.InTx(ctx, s.pool, tok.TenantID, func(tx pgx.Tx) error {
		if err := lockAndRecheck(ctx, tx, tok.TenantID, sources); err != nil {
			return err
		}
		// INSERT the audit row first so the chain prev_hash is stable;
		// the issued_tokens INSERT below is bound to the same transaction
		// and rolls back together on any failure.
		row, err := auditstore.AppendInTx(ctx, tx, tok.TenantID, auditEventType, auditPayload, auditAt)
		if err != nil {
			return err
		}
		committed = row
		return insertIssued(ctx, tx, tok)
	})
	if err != nil {
		return audit.Row{}, mapUniqueViolation(err)
	}
	return committed, nil
}

// RecordWithRotationAudit performs the §13.3 atomic
// token-rotation write-before-issue. In one Postgres transaction it
// takes the §11.7 lock, re-reads the sources (step 1a), writes the
// `token.exchanged` audit row, INSERTs the new issued_tokens row, and
// stamps revoked_at/revoked_reason on the previous token (prevJTI). The
// revocation of the previous token and the mint of its replacement
// therefore commit together: there is no window in which both tokens
// validate, and none in which the old token is revoked but the
// replacement was never issued. Only after COMMIT does the Token Service
// hand the new token to the caller.
//
// The Token Service lists the subject token, which is prevJTI, in
// sources. A rotation of a token that is already revoked, which is a
// retried rotation or the loser of two concurrent rotations of one
// subject, therefore fails the step (1a) re-read with a
// *SourceRevokedError naming the subject, and commits no second mint.
// The two concurrent rotations serialize on the §11.7 lock, so the
// second one reads the first one's committed revoked_at stamp.
//
// A revocation that does not take the §11.7 lock (Store.Revoke,
// RevokeBySubject, or RevokeCascade) can commit after the step (1a)
// re-read and before the revoke UPDATE below. The UPDATE then matches no
// live row, so revokePrevious re-reads prevJTI's row and, when it exists
// with revoked_at set, returns a *SourceRevokedError naming the subject.
// The transaction rolls back and no second mint commits.
//
// revoked reports whether a row was revoked, and revokedSub carries that
// row's subject so the caller can emit the §16.7 token.revoked row with
// revocation_reason=rotation_replaced. The rotation commits with revoked
// false only when prevJTI has no issued_tokens row in the tenant, such
// as an upstream IdP token that was never recorded. The §16.7 token.revoked audit row itself
// is written by the caller after COMMIT because its propagation_mode
// field records the post-commit EventBus publish outcome.
//
// spec: §13.3 (Credential Flow: Write-before-issue ordering, Token
// rotation and revocation), §11.7.
func (s *Store) RecordWithRotationAudit(ctx context.Context, tok IssuedToken, sources []SourceToken, prevJTI, revokedReason, exchangeEventType string, exchangePayload json.RawMessage, at time.Time) (revokedSub string, revoked bool, err error) {
	err = pgtenant.InTx(ctx, s.pool, tok.TenantID, func(tx pgx.Tx) error {
		if lerr := lockAndRecheck(ctx, tx, tok.TenantID, sources); lerr != nil {
			return lerr
		}
		// The token.exchanged audit row goes first so the chain prev_hash
		// is stable; the issued_tokens INSERT and the previous-token
		// revocation below are bound to the same transaction and roll
		// back together on any failure.
		if _, aerr := auditstore.AppendInTx(ctx, tx, tok.TenantID, exchangeEventType, exchangePayload, at); aerr != nil {
			return aerr
		}
		if ierr := insertIssued(ctx, tx, tok); ierr != nil {
			return ierr
		}
		sub, ok, rerr := revokePrevious(ctx, tx, tok.TenantID, prevJTI, revokedReason, at)
		revokedSub, revoked = sub, ok
		return rerr
	})
	if err != nil {
		return "", false, mapUniqueViolation(err)
	}
	return revokedSub, revoked, nil
}

// revokePrevious stamps revoked_at/revoked_reason on the rotated token
// when its row exists in the tenant and is live, and returns its subject.
// A missing row reports revoked=false with no error. A row that is
// already revoked returns a *SourceRevokedError naming the subject token,
// so a revocation that committed after the step (1a) re-read still
// refuses the rotation.
//
// spec: §13.3 (Credential Flow: Write-before-issue ordering, Token
// rotation and revocation).
func revokePrevious(ctx context.Context, tx pgx.Tx, tenantID, prevJTI, reason string, at time.Time) (string, bool, error) {
	var sub string
	err := tx.QueryRow(ctx,
		`UPDATE issued_tokens SET revoked_at = $3, revoked_reason = $4
		 WHERE jti = $1 AND tenant_id = $2 AND revoked_at IS NULL
		 RETURNING sub`,
		prevJTI, tenantID, at, pgtenant.NullString(reason)).Scan(&sub)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, previousRevokedError(ctx, tx, tenantID, prevJTI)
	}
	if err != nil {
		return "", false, fmt.Errorf("issuedtokenstore: revoke rotated token: %w", err)
	}
	return sub, true, nil
}

// previousRevokedError re-reads prevJTI's row after the revoke UPDATE
// matched no live row. It returns nil when the tenant has no row for
// prevJTI and a *SourceRevokedError naming the subject token when the row
// exists with revoked_at set.
func previousRevokedError(ctx context.Context, tx pgx.Tx, tenantID, prevJTI string) error {
	var revokedAt *time.Time
	err := tx.QueryRow(ctx,
		`SELECT revoked_at FROM issued_tokens WHERE jti = $1 AND tenant_id = $2`,
		prevJTI, tenantID).Scan(&revokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("issuedtokenstore: re-read rotated token: %w", err)
	}
	if revokedAt == nil {
		return fmt.Errorf("issuedtokenstore: rotated token %q is live but the revoke matched no row", prevJTI)
	}
	return &SourceRevokedError{Role: SourceSubjectToken, JTI: prevJTI, RevokedAt: *revokedAt}
}

// mapUniqueViolation maps a jti primary-key collision to ErrAlreadyExists
// and returns every other error unchanged.
func mapUniqueViolation(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrAlreadyExists
	}
	return err
}

// insertIssued runs the issued_tokens INSERT on tx. It assumes the
// caller has already opened a pgtenant.InTx and acquired the §11.7
// audit advisory lock when binding to an audit row.
//
// spec: §4.3 — audiences and dialect_cap_applied_seconds are
// recorded alongside the legacy single-valued audience column so
// forensic reverse lookups by individual audience and "why was the
// exp this value" both have a durable source.
func insertIssued(ctx context.Context, tx pgx.Tx, tok IssuedToken) error {
	const insertSQL = `INSERT INTO issued_tokens (
		jti, tenant_id, sub, token_hash, scope, audience,
		audiences, dialect_cap_applied_seconds,
		issued_at, exp, revoked_at, revoked_reason, act_sub, parent_jti,
		impersonator_tenant_id, impersonator_jti
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`
	_, err := tx.Exec(ctx, insertSQL,
		tok.JTI, tok.TenantID, tok.Subject, tok.TokenHash, scopeArg(tok.Scope),
		tok.Audience, audienceListArg(tok.Audiences, tok.Audience), tok.DialectCapAppliedSeconds,
		tok.IssuedAt, tok.ExpiresAt, pgtenant.NullTime(tok.RevokedAt),
		pgtenant.NullString(tok.RevokedReason), pgtenant.NullString(tok.ActSubject), pgtenant.NullString(tok.ParentJTI),
		pgtenant.NullString(tok.ImpersonatorTenantID), pgtenant.NullString(tok.ImpersonatorJTI))
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

// RevokeWithAudit durably revokes jti and writes the §16.7 `token.revoked`
// audit row in a single Postgres transaction. The revoked_at/revoked_reason
// stamp and the audit row therefore commit together: a §12.2 rehydration
// (which reads `revoked_at IS NOT NULL`) can never observe a revocation with
// no audit trail, and no audit row can name a token the durable store did
// not revoke. Only after COMMIT should the caller push the jti onto the
// cross-replica revocation cache, so a durable-write failure never leaves
// the cache holding a revocation the authoritative store lacks.
//
// This is the reason-parameterized durable revoke the gateway's in-process
// admin-credential rotation path uses: the standalone Token Service
// revocation request emits `explicit_revoke` and the atomic rotation grant
// uses RecordWithRotationAudit (which revokes the prior token inside the
// mint transaction, before the Secret is patched). The gateway-mediated
// bootstrap rotation must persist the `lenny-admin-token` Secret before
// revoking the prior token, so it mints and records the new token first
// (RecordWithAudit), patches the Secret, then calls RevokeWithAudit with
// `revocation_reason: rotation_replaced` once the patch succeeds. The
// revocation reason (revokedReason) is stamped on revoked_reason; the
// auditEventType and auditPayload carry the §16.7-owned vocabulary
// (`token.revoked` with `revocation_reason`, `propagation_mode`, and the
// revoked jti/sub), so the store does not bake that schema in — the same
// separation of concerns RecordWithAudit uses.
//
// Returns ErrNotFound when the token does not exist or is already revoked,
// so a retried rotation does not double-emit a `token.revoked` row for a
// jti the durable store already stamped. The audit row is returned to the
// caller for cross-checking and metric labelling.
//
// spec: §13.3, §16.7.
func (s *Store) RevokeWithAudit(ctx context.Context, tenantID, jti, revokedReason, auditEventType string, auditPayload json.RawMessage, at time.Time) (audit.Row, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	var committed audit.Row
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		// Stamp revoked_at/revoked_reason only when the token exists and is
		// not already revoked, so a retry after the durable write already
		// committed is a no-op (ErrNotFound) rather than a double-emit.
		tag, err := tx.Exec(ctx,
			`UPDATE issued_tokens SET revoked_at = $3, revoked_reason = $4
			 WHERE jti = $1 AND tenant_id = $2 AND revoked_at IS NULL`,
			jti, tenantID, at, pgtenant.NullString(revokedReason))
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		// Bind the §16.7 token.revoked audit row to the same transaction
		// under the §11.7 per-tenant advisory lock, so the revocation and
		// its audit trail share one COMMIT.
		row, err := auditstore.AppendInTx(ctx, tx, tenantID, auditEventType, auditPayload, at)
		if err != nil {
			return err
		}
		committed = row
		return nil
	})
	if err != nil {
		return audit.Row{}, err
	}
	return committed, nil
}

// RevokeBySubject marks every not-yet-revoked token issued for
// (tenantID, subject), and every not-yet-revoked lineage descendant of a
// token of the subject whatever its own sub, as revoked with the given
// reason and instant, and returns the JTIs it revoked. The walk is seeded
// with every row of the subject, revoked or not, so the descendants of a
// token the subject had already revoked, such as one rotated away from,
// are reached too, and it follows parent_jti inside the tenant. It backs
// the §11.4 full_revoke cached-auth invalidation step: the caller pushes
// the returned JTIs into the in-memory revocation cache so the revoked
// tokens are rejected on the next request without waiting for a
// rehydration. An already-revoked token is left untouched and its JTI is
// not returned, so the operation is idempotent across repeated
// full_revoke calls. A user with no issued tokens yields an empty slice
// and no error.
//
// spec: §11.4 step 5 (partial: TOKX-I19), §13.3 (Credential Flow: Lineage of derived tokens,
// Rotation lineage and leaked tokens).
func (s *Store) RevokeBySubject(ctx context.Context, tenantID, subject, reason string, at time.Time) ([]string, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	var revoked []string
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`WITH RECURSIVE subtree AS (
				SELECT jti, tenant_id FROM issued_tokens WHERE tenant_id = $1 AND sub = $2
				UNION
				-- The walk stays inside the tenant: each child is pinned to
				-- its parent's tenant (R-02 tenant isolation).
				SELECT c.jti, c.tenant_id FROM issued_tokens c
					JOIN subtree p ON c.parent_jti = p.jti AND c.tenant_id = p.tenant_id
					WHERE c.tenant_id = $1
			)
			UPDATE issued_tokens t SET revoked_at = $3, revoked_reason = $4
			FROM subtree s
			WHERE t.jti = s.jti AND t.tenant_id = $1 AND t.revoked_at IS NULL
			RETURNING t.jti`,
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
// token) or a lineage descendant reached through parent_jti.
type RevokedToken struct {
	JTI     string
	Subject string
	IsRoot  bool
}

// RevokeCascade implements the §13.3 / §8.3 recursive
// revocation with no caller check: it is the store-level helper for
// callers that authorize the revocation themselves, and the Token
// Service's revocation request uses RevokeCascadeByCaller. It revokes rootJTI and every lineage descendant
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
// spec: §13.3; §8.3.
func (s *Store) RevokeCascade(ctx context.Context, tenantID, rootJTI, rootReason, childReason string, at time.Time) ([]RevokedToken, error) {
	return s.RevokeCascadeByCaller(ctx, tenantID, "", rootJTI, rootReason, childReason, at)
}

// RevokeCascadeByCaller is RevokeCascade for a revocation request whose
// caller token is callerJTI. It is RevokeLineage for a root that must
// already have a row, and returns only the rows it revoked.
//
// spec: §13.3 (Credential Flow) "Recursive-revocation request
// authorization."
func (s *Store) RevokeCascadeByCaller(ctx context.Context, tenantID, callerJTI, rootJTI, rootReason, childReason string, at time.Time) ([]RevokedToken, error) {
	res, err := s.RevokeLineage(ctx, LineageRevocation{
		TenantID:    tenantID,
		CallerJTI:   callerJTI,
		Root:        IssuedToken{JTI: rootJTI},
		RootReason:  rootReason,
		ChildReason: childReason,
		At:          at,
	})
	return res.Revoked, err
}

// LineageRevocation is one recursive revocation: the tenant, the caller
// token whose revocation refuses the request, the root, and the reasons
// and instant stamped on the revoked rows.
type LineageRevocation struct {
	TenantID  string
	CallerJTI string
	// Root names the root by Root.JTI. When the root has no row in the
	// tenant and Root.TokenHash is set, the revocation records Root as the
	// root's row, which the cascade then stamps revoked; with no
	// TokenHash, a root without a row returns ErrNotFound.
	Root        IssuedToken
	RootReason  string
	ChildReason string
	At          time.Time
}

// CascadeResult reports one recursive revocation: the rows it revoked,
// with IsRoot marking the root, and the jtis of the root's whole lineage
// subtree, including the rows that were revoked before the request. The
// impersonator-link revocation reads Subtree.
type CascadeResult struct {
	Revoked []RevokedToken
	Subtree []string
}

// RevokeLineage revokes the root and every lineage descendant reachable
// through parent_jti inside the tenant, in one transaction. Before the
// cascade it reads the caller's row under FOR SHARE and returns
// ErrCallerRevoked, revoking nothing, when the row is revoked; the share
// lock makes a concurrent revocation of the caller token either commit
// first, which refuses the request, or wait until the request commits. An
// empty CallerJTI, or a caller with no row in the tenant (an upstream OIDC
// ID token), skips that check. The root may be revoked already: the
// cascade then revokes the descendants not yet revoked. A root without a
// row is recorded from Root when Root.TokenHash is set and refused with
// ErrNotFound otherwise. Postgres deadlock victims are retried, because
// two requests that revoke each other's caller token can deadlock on the
// caller-row share locks.
//
// spec: §13.3 (Credential Flow) "Recursive-revocation request
// authorization.", "Lineage of derived tokens."; §8.3.
func (s *Store) RevokeLineage(ctx context.Context, req LineageRevocation) (CascadeResult, error) {
	if req.At.IsZero() {
		req.At = time.Now().UTC()
	}
	var (
		out CascadeResult
		err error
	)
	for attempt := 0; attempt < cascadeDeadlockAttempts; attempt++ {
		out, err = s.revokeLineageOnce(ctx, req)
		if !isDeadlock(err) {
			break
		}
	}
	return out, err
}

// cascadeDeadlockAttempts bounds the transactions RevokeLineage runs when
// Postgres aborts one as a deadlock victim. Two requests that revoke each
// other's caller token deadlock on the caller-row share locks at most
// once: after the survivor commits, the retried request reads its own
// caller row as revoked.
const cascadeDeadlockAttempts = 3

// isDeadlock reports whether err is the Postgres deadlock_detected error.
func isDeadlock(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "40P01"
}

// revokeLineageOnce is one transaction of RevokeLineage.
func (s *Store) revokeLineageOnce(ctx context.Context, req LineageRevocation) (CascadeResult, error) {
	var out CascadeResult
	err := pgtenant.InTx(ctx, s.pool, req.TenantID, func(tx pgx.Tx) error {
		if err := checkCallerLive(ctx, tx, req.TenantID, req.CallerJTI); err != nil {
			return err
		}
		if err := ensureRoot(ctx, tx, req); err != nil {
			return err
		}
		res, err := cascadeFromRoot(ctx, tx, req)
		out = res
		return err
	})
	if err != nil {
		return CascadeResult{}, mapUniqueViolation(err)
	}
	return out, nil
}

// checkCallerLive reads the caller's row under FOR SHARE and returns
// ErrCallerRevoked when it is revoked. An empty jti or a missing row
// passes.
func checkCallerLive(ctx context.Context, tx pgx.Tx, tenantID, callerJTI string) error {
	if callerJTI == "" {
		return nil
	}
	var callerRevoked bool
	err := tx.QueryRow(ctx,
		`SELECT revoked_at IS NOT NULL FROM issued_tokens WHERE jti = $1 AND tenant_id = $2 FOR SHARE`,
		callerJTI, tenantID).Scan(&callerRevoked)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil
	case err != nil:
		return err
	case callerRevoked:
		return ErrCallerRevoked
	}
	return nil
}

// ensureRoot confirms the root has a row in the tenant, so a typo'd jti is
// ErrNotFound rather than a silent no-op, and records the root from
// req.Root when it has none and req.Root.TokenHash is set. The recorded
// row is live; the cascade that follows stamps it revoked. The insert does
// nothing on a jti conflict: a concurrent request that records the same
// root first leaves its row for this one, and a jti that another tenant
// holds leaves the root without a row here, which is ErrNotFound.
//
// spec: §13.3 (Credential Flow) "Recursive-revocation request
// authorization."
func ensureRoot(ctx context.Context, tx pgx.Tx, req LineageRevocation) error {
	exists, err := rootExists(ctx, tx, req)
	if err != nil || exists {
		return err
	}
	if len(req.Root.TokenHash) == 0 {
		return ErrNotFound
	}
	root := req.Root
	root.TenantID = req.TenantID
	if _, err := tx.Exec(ctx,
		`INSERT INTO issued_tokens (
			jti, tenant_id, sub, token_hash, scope, audience, audiences,
			dialect_cap_applied_seconds, issued_at, exp
		) VALUES ($1, $2, $3, $4, $5, $6, $7, 0, $8, $9)
		ON CONFLICT (jti) DO NOTHING`,
		root.JTI, root.TenantID, root.Subject, root.TokenHash, scopeArg(root.Scope),
		root.Audience, audienceListArg(root.Audiences, root.Audience), root.IssuedAt, root.ExpiresAt); err != nil {
		return err
	}
	if exists, err = rootExists(ctx, tx, req); err != nil || exists {
		return err
	}
	return ErrNotFound
}

// rootExists reports whether the root has a row in the request's tenant.
func rootExists(ctx context.Context, tx pgx.Tx, req LineageRevocation) (bool, error) {
	var exists bool
	err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM issued_tokens WHERE jti = $1 AND tenant_id = $2)`,
		req.Root.JTI, req.TenantID).Scan(&exists)
	return exists, err
}

// cascadeFromRoot revokes the not-yet-revoked rows of the root's lineage
// subtree in one statement, and returns them with the jtis of the whole
// subtree. The root is stamped with req.RootReason and each descendant
// with req.ChildReason. Token lineage is acyclic (a child's parent_jti
// always references a pre-existing token), so the recursive walk
// terminates.
func cascadeFromRoot(ctx context.Context, tx pgx.Tx, req LineageRevocation) (CascadeResult, error) {
	rows, err := tx.Query(ctx,
		`WITH RECURSIVE subtree AS (
			SELECT jti, tenant_id FROM issued_tokens WHERE jti = $1 AND tenant_id = $2
			UNION ALL
			-- The recursion stays inside one tenant: the seed binds
			-- tenant_id = $2 and the join pins each child to its parent's
			-- tenant (R-02 tenant-isolation: the descent never crosses a
			-- tenant boundary).
			SELECT c.jti, c.tenant_id FROM issued_tokens c
				JOIN subtree p ON c.parent_jti = p.jti AND c.tenant_id = p.tenant_id
				WHERE c.tenant_id = $2
		), revoked AS (
			UPDATE issued_tokens t
			SET revoked_at = $3,
			    revoked_reason = CASE WHEN t.jti = $1 THEN $4 ELSE $5 END
			FROM subtree s
			WHERE t.jti = s.jti AND t.tenant_id = $2 AND t.revoked_at IS NULL
			RETURNING t.jti, t.tenant_id, t.sub, (t.jti = $1) AS is_root
		)
		SELECT s.jti, r.sub, r.is_root FROM subtree s
			LEFT JOIN revoked r ON r.jti = s.jti AND r.tenant_id = s.tenant_id`,
		req.Root.JTI, req.TenantID, req.At, pgtenant.NullString(req.RootReason), pgtenant.NullString(req.ChildReason))
	if err != nil {
		return CascadeResult{}, err
	}
	defer rows.Close()
	var out CascadeResult
	for rows.Next() {
		var (
			jti    string
			sub    *string
			isRoot *bool
		)
		if err := rows.Scan(&jti, &sub, &isRoot); err != nil {
			return CascadeResult{}, err
		}
		out.Subtree = append(out.Subtree, jti)
		if sub != nil {
			out.Revoked = append(out.Revoked, RevokedToken{JTI: jti, Subject: *sub, IsRoot: isRoot != nil && *isRoot})
		}
	}
	return out, rows.Err()
}

// LineageSubtree returns the jtis of the root's lineage subtree inside the
// tenant: rootJTI itself, whether or not it has a row, and every row whose
// parent_jti chain reaches it, revoked or not. It revokes nothing. In the
// same transaction it reads the caller's row first and returns
// ErrCallerRevoked when the row is revoked; an empty callerJTI or a caller
// with no row skips that check. The recursive-revocation request reads it
// before its cascade so the impersonator-link revocation runs while the
// caller is still live, and a failed or interrupted request can be retried.
//
// spec: §13.3 (Credential Flow) "Lineage of derived tokens.",
// "Platform-admin impersonation." rule 7.
func (s *Store) LineageSubtree(ctx context.Context, tenantID, callerJTI, rootJTI string) ([]string, error) {
	var out []string
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		if err := checkCallerLive(ctx, tx, tenantID, callerJTI); err != nil {
			return err
		}
		rows, err := tx.Query(ctx,
			`WITH RECURSIVE subtree AS (
				SELECT $1::text AS jti, $2::text AS tenant_id
				UNION ALL
				-- The walk stays inside the tenant: each child is pinned to
				-- its parent's tenant (R-02 tenant isolation).
				SELECT c.jti, c.tenant_id FROM issued_tokens c
					JOIN subtree p ON c.parent_jti = p.jti AND c.tenant_id = p.tenant_id
					WHERE c.tenant_id = $2
			)
			SELECT jti FROM subtree`,
			rootJTI, tenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var jti string
			if err := rows.Scan(&jti); err != nil {
				return err
			}
			out = append(out, jti)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// RecordWithSources records an issued-token row in one transaction that
// first takes the §11.7 per-tenant audit advisory lock and re-reads the
// listed source tokens (§13.3 write-before-issue step 1a), and writes no
// audit row. A revoked source returns a *SourceRevokedError and an
// impersonation source a *SourceImpersonationError, and nothing is
// written; a jti collision returns ErrAlreadyExists. The playground mint
// records its bearer through it.
//
// spec: §10.2 (Playground mint invariants, invariant 6), §13.3
// (Credential Flow: Write-before-issue ordering).
func (s *Store) RecordWithSources(ctx context.Context, tok IssuedToken, sources []SourceToken) error {
	err := pgtenant.InTx(ctx, s.pool, tok.TenantID, func(tx pgx.Tx) error {
		if err := lockAndRecheck(ctx, tx, tok.TenantID, sources); err != nil {
			return err
		}
		return insertIssued(ctx, tx, tok)
	})
	return mapUniqueViolation(err)
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
// expired-row GC path. Revoked rows are removed too, so the caller
// passes a cutoff that precedes the §13.3 trailing hour: a row is kept,
// revoked or not, while a buggy client can still present its token.
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
// §12.2 TokenIssuanceStore role. It hard-deletes the issued-token rows whose
// subject is userID within tenantID and whose exp precedes the trailing hour,
// and returns the count removed. A row still inside the hour is kept, revoked
// or not: the token behind it can still be presented, so a later revocation
// or rotation must find its row and a restarted replica must load its
// revocation. The §12.2 expired-row GC deletes the kept rows once they leave
// the hour.
//
// spec: §12.1, §12.8 (Compliance Interfaces: DeleteByUser
// dependency-ordered deletion sequence, step 18), §13.3 (Credential Flow:
// Authoritative durability for revocation).
func (s *Store) DeleteByUser(ctx context.Context, tenantID, userID string) (int, error) {
	if tenantID == "" || userID == "" {
		return 0, errors.New("issuedtokenstore: DeleteByUser requires non-empty tenant_id and user_id")
	}
	var deleted int
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`DELETE FROM issued_tokens
			  WHERE tenant_id = $1 AND sub = $2 AND exp <= now() - interval '1 hour'`,
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

// DeleteByTenant implements the §12.1 mandatory-erasure primitive for the
// §12.8 Phase 4 tenant teardown. It hard-deletes the tenant's issued-token
// rows whose exp precedes the trailing hour and keeps the rest, for the same
// reason DeleteByUser does. The §12.2 expired-row GC, which visits deleted
// tenants too, deletes the kept rows once they leave the hour.
//
// spec: §12.1, §12.8 (Compliance Interfaces: Tenant deletion lifecycle),
// §13.3 (Credential Flow: Authoritative durability for revocation).
func (s *Store) DeleteByTenant(ctx context.Context, tenantID string) (int, error) {
	if tenantID == "" {
		return 0, errors.New("issuedtokenstore: DeleteByTenant requires a concrete tenant_id")
	}
	var deleted int
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`DELETE FROM issued_tokens
			  WHERE tenant_id = $1 AND exp <= now() - interval '1 hour'`, tenantID)
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
// spec: §4.3 — Audiences and DialectCapAppliedSeconds are
// populated alongside the legacy Audience column so callers prefer
// the list form when present.
func scanToken(row pgx.Row) (IssuedToken, error) {
	var (
		t                             IssuedToken
		revokedAt                     *time.Time
		revokedReason, actSub, parent *string
		impTenant, impJTI             *string
	)
	if err := row.Scan(
		&t.JTI, &t.TenantID, &t.Subject, &t.TokenHash, &t.Scope, &t.Audience,
		&t.Audiences, &t.DialectCapAppliedSeconds,
		&t.IssuedAt, &t.ExpiresAt, &revokedAt, &revokedReason, &actSub, &parent,
		&impTenant, &impJTI,
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
	if impTenant != nil {
		t.ImpersonatorTenantID = *impTenant
	}
	if impJTI != nil {
		t.ImpersonatorJTI = *impJTI
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
// spec: §4.3, migration 0058.
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
