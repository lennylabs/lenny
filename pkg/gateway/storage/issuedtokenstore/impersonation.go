// SPDX-License-Identifier: MIT

package issuedtokenstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/lennylabs/lenny/pkg/gateway/storage/pgtenant"
)

// impersonationPredicate selects the impersonation rows of issued_tokens:
// a platform-admin impersonation bearer records the impersonating admin in
// act_sub and has no parent, while a delegation child always carries a
// parent_jti. Every query that reads or revokes impersonation sessions
// matches it, so none of them reads or revokes another token's row.
//
// spec: §13.3 (Credential Flow: Platform-admin impersonation, rule 2).
const impersonationPredicate = "act_sub IS NOT NULL AND parent_jti IS NULL"

// revokedReasonImpersonationEnded is the revoked_reason the impersonation
// end stamps on the row.
//
// spec: §13.3 (Credential Flow: Platform-admin impersonation, rule 3).
const revokedReasonImpersonationEnded = "impersonation_ended"

// Impersonation states a NotActiveError reports.
const (
	// StateRevoked reports an impersonation row whose revoked_at is set.
	StateRevoked = "revoked"
	// StateExpired reports an impersonation row whose exp has passed.
	StateExpired = "expired"
)

// ErrNotActive matches every *NotActiveError under errors.Is.
var ErrNotActive = errors.New("issuedtokenstore: impersonation session not active")

// NotActiveError reports that an impersonation row exists but is no longer
// active, so the end revoked nothing. State is StateRevoked or
// StateExpired.
//
// spec: §13.3 (Credential Flow: Platform-admin impersonation, rules 2 and 3).
type NotActiveError struct {
	State string
}

// Error implements error.
func (e *NotActiveError) Error() string {
	return "issuedtokenstore: impersonation session is " + e.State
}

// Is reports whether target is ErrNotActive.
func (e *NotActiveError) Is(target error) bool { return target == ErrNotActive }

// UserInvalidatedError reports that the impersonation record fence found a
// user row whose invalidation level is hard disable or full revoke, both
// of which set deleted_at. The record wrote nothing.
//
// spec: §13.3 (Credential Flow: Platform-admin impersonation, rule 1 (c)),
// §11.4.
type UserInvalidatedError struct {
	TenantID string
	Subject  string
}

// Error implements error.
func (e *UserInvalidatedError) Error() string {
	return fmt.Sprintf("issuedtokenstore: user %q in tenant %q is invalidated", e.Subject, e.TenantID)
}

// readUserInvalidatedSQL reads whether a user row records a hard disable
// or a full revoke. It takes FOR SHARE for the reason the session creation
// fence gives: FOR SHARE conflicts with the plain UPDATE that writes the
// invalidation level, so a full revoke cannot commit between this read and
// the commit of the insert that follows it.
const readUserInvalidatedSQL = `SELECT deleted_at IS NOT NULL FROM users
	WHERE tenant_id = $1 AND subject = $2 FOR SHARE`

// RecordImpersonation records a platform-admin impersonation bearer under
// the user-row fence. In a first transaction under the caller's tenant it
// reads the impersonating admin's user row FOR SHARE. Inside that
// transaction, a second transaction under the target tenant reads the
// target's user row FOR SHARE and inserts the bearer's row, and commits
// before the first one does. Each user row is read under its own tenant's
// RLS context, because under another tenant's context it reads as absent.
//
// A user row that records an invalidation returns *UserInvalidatedError and
// writes nothing. A missing user row passes. A jti collision returns
// ErrAlreadyExists. The method returns nil only once the first transaction
// commits, so a full revoke that commits its invalidation level before
// step 1 of §11.4 is either observed here or finds the row at its step 5.
// It never uses the __all__ sentinel.
//
// tok carries the target's tenant and subject, ActSubject set to the
// admin's subject, and no ParentJTI. callerTenantID is the admin's tenant
// and must not be empty.
//
// spec: §13.3 (Credential Flow: Platform-admin impersonation, rule 1 (c)),
// §12.8 (Lifecycle fence and drain), §12.6.
func (s *Store) RecordImpersonation(ctx context.Context, tok IssuedToken, callerTenantID string) error {
	if callerTenantID == "" {
		return errors.New("issuedtokenstore: RecordImpersonation requires the caller's tenant_id")
	}
	// A row without act_sub, or with a parent, is not an impersonation row,
	// so no impersonation query would ever end or list it.
	if tok.ActSubject == "" || tok.ParentJTI != "" {
		return errors.New("issuedtokenstore: RecordImpersonation requires act_sub and no parent_jti")
	}
	err := pgtenant.InTx(ctx, s.pool, callerTenantID, func(callerTx pgx.Tx) error {
		if err := checkUserNotInvalidated(ctx, callerTx, callerTenantID, tok.ActSubject); err != nil {
			return err
		}
		return pgtenant.InTx(ctx, s.pool, tok.TenantID, func(targetTx pgx.Tx) error {
			if err := checkUserNotInvalidated(ctx, targetTx, tok.TenantID, tok.Subject); err != nil {
				return err
			}
			return insertIssued(ctx, targetTx, tok)
		})
	})
	return mapUniqueViolation(err)
}

// checkUserNotInvalidated reads the user row of (tenantID, subject) FOR
// SHARE on tx and returns *UserInvalidatedError when it records an
// invalidation. A missing row passes.
func checkUserNotInvalidated(ctx context.Context, tx pgx.Tx, tenantID, subject string) error {
	var invalidated bool
	err := tx.QueryRow(ctx, readUserInvalidatedSQL, tenantID, subject).Scan(&invalidated)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("issuedtokenstore: read user row %q for impersonation fence: %w", subject, err)
	}
	if invalidated {
		return &UserInvalidatedError{TenantID: tenantID, Subject: subject}
	}
	return nil
}

// EndImpersonation revokes the active impersonation row whose jti is jti,
// stamping revoked_reason impersonation_ended. The update is conditional on
// the row being active, so exactly one of any number of concurrent calls
// on any replicas revokes it. When the update matches no row, the same
// transaction re-reads the row and returns ErrNotFound when there is no
// impersonation row for jti, or a *NotActiveError naming StateRevoked or
// StateExpired.
//
// The caller has confirmed that it holds platform-admin and emits the
// cross_tenant_read event; the store does neither.
//
// spec: §13.3 (Credential Flow: Platform-admin impersonation, rules 2, 3,
// and 7).
func (s *Store) EndImpersonation(ctx context.Context, jti string, at time.Time) (IssuedToken, error) {
	var out IssuedToken
	err := pgtenant.InAllTenants(ctx, s.pool, func(tx pgx.Tx) error {
		tok, err := scanToken(tx.QueryRow(ctx,
			`UPDATE issued_tokens
			 -- platform-admin-cross-tenant-allowed
			 -- platform-admin-cross-tenant-justification: §13.3 impersonation end; the session id is a jti in the target tenant, which the platform-admin caller does not name, and the predicate confines the update to impersonation rows.
			 SET revoked_at = $2, revoked_reason = $3
			 WHERE jti = $1 AND `+impersonationPredicate+`
			   AND revoked_at IS NULL AND exp > $2
			 RETURNING `+selectList,
			jti, at, revokedReasonImpersonationEnded))
		if errors.Is(err, pgx.ErrNoRows) {
			return inactiveImpersonationError(ctx, tx, jti)
		}
		if err != nil {
			return fmt.Errorf("issuedtokenstore: end impersonation %q: %w", jti, err)
		}
		out = tok
		return nil
	})
	if err != nil {
		return IssuedToken{}, err
	}
	return out, nil
}

// inactiveImpersonationError re-reads the impersonation row for jti after
// the end's update matched no row and classifies the miss.
func inactiveImpersonationError(ctx context.Context, tx pgx.Tx, jti string) error {
	var revokedAt *time.Time
	err := tx.QueryRow(ctx,
		`SELECT revoked_at FROM issued_tokens
		 -- platform-admin-cross-tenant-allowed
		 -- platform-admin-cross-tenant-justification: §13.3 impersonation end; re-reads the same impersonation row the end's update missed to report why.
		 WHERE jti = $1 AND `+impersonationPredicate,
		jti).Scan(&revokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("issuedtokenstore: re-read impersonation %q: %w", jti, err)
	}
	if revokedAt != nil {
		return &NotActiveError{State: StateRevoked}
	}
	return &NotActiveError{State: StateExpired}
}

// ListActiveImpersonations returns every active impersonation row in every
// tenant, ordered by issued_at. The caller has confirmed that it holds
// platform-admin and emits the cross_tenant_read event before returning
// the rows.
//
// spec: §13.3 (Credential Flow: Platform-admin impersonation, rules 2 and 7).
func (s *Store) ListActiveImpersonations(ctx context.Context, now time.Time) ([]IssuedToken, error) {
	var out []IssuedToken
	err := pgtenant.InAllTenants(ctx, s.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT `+selectList+` FROM issued_tokens
			 -- platform-admin-cross-tenant-allowed
			 -- platform-admin-cross-tenant-justification: §13.3 impersonation listing; a platform-admin lists the active impersonation sessions of every tenant, and the predicate confines the read to impersonation rows.
			 WHERE `+impersonationPredicate+` AND revoked_at IS NULL AND exp > $1
			 ORDER BY issued_at`, now)
		if err != nil {
			return fmt.Errorf("issuedtokenstore: list impersonations: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			tok, err := scanToken(rows)
			if err != nil {
				return fmt.Errorf("issuedtokenstore: scan impersonation: %w", err)
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

// RevokeImpersonationsByActor revokes every active impersonation row whose
// act_sub is actorSub, in every tenant, and returns the revoked jtis. It
// backs the full-revoke cascade on a platform-admin. The caller has
// confirmed that it holds platform-admin and emits the cross_tenant_read
// event after the revocation commits.
//
// spec: §13.3 (Credential Flow: Platform-admin impersonation, rules 2, 6,
// and 7), §11.4.
func (s *Store) RevokeImpersonationsByActor(ctx context.Context, actorSub, reason string, at time.Time) ([]string, error) {
	var revoked []string
	err := pgtenant.InAllTenants(ctx, s.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`UPDATE issued_tokens
			 -- platform-admin-cross-tenant-allowed
			 -- platform-admin-cross-tenant-justification: §13.3 full-revoke cascade; the bearers a platform-admin minted live in the target tenants, and the act_sub and parent_jti terms confine the update to impersonation rows.
			 SET revoked_at = $3, revoked_reason = $2
			 WHERE act_sub = $1 AND parent_jti IS NULL
			   AND revoked_at IS NULL AND exp > $3
			 RETURNING jti`,
			actorSub, pgtenant.NullString(reason), at)
		if err != nil {
			return fmt.Errorf("issuedtokenstore: revoke impersonations by actor: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var jti string
			if err := rows.Scan(&jti); err != nil {
				return fmt.Errorf("issuedtokenstore: scan revoked impersonation: %w", err)
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
