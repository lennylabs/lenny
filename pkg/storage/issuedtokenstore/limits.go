// SPDX-License-Identifier: MIT

package issuedtokenstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/lennylabs/lenny/pkg/audit/auditstore"
)

// Default values of the two operator-tunable bounds on a token lineage.
const (
	// DefaultLineageMaxDepth is the default lineage depth cap,
	// auth.tokenLineageMaxDepth: an exchange whose parent row records this
	// depth or more is refused.
	//
	// spec: §13.3 (Credential Flow: Write-before-issue ordering, step (1a))
	DefaultLineageMaxDepth = 32
	// DefaultRevocationTimeout is the default statement timeout of a
	// recursive revocation's cascade and of a full revoke's walk,
	// auth.tokenRevocationTimeoutMs.
	//
	// spec: §13.3 (Credential Flow: Recursive-revocation request
	// authorization), §11.4 step 5
	DefaultRevocationTimeout = 5 * time.Second
)

// Limits holds the bounds a Store applies to a token lineage. A zero field
// takes its default, so a caller sets only the bounds an operator changed.
type Limits struct {
	// LineageMaxDepth is auth.tokenLineageMaxDepth: an exchange whose
	// parent row's depth is at or above it is refused in step (1a).
	LineageMaxDepth int
	// RevocationTimeout is auth.tokenRevocationTimeoutMs: the statement
	// timeout of the cascade and of the full revoke's walk, set after they
	// take the per-tenant audit advisory lock.
	RevocationTimeout time.Duration
}

// DefaultLimits returns the default bounds.
func DefaultLimits() Limits {
	return Limits{LineageMaxDepth: DefaultLineageMaxDepth, RevocationTimeout: DefaultRevocationTimeout}
}

// withDefaults replaces every non-positive field with its default.
func (l Limits) withDefaults() Limits {
	d := DefaultLimits()
	if l.LineageMaxDepth <= 0 {
		l.LineageMaxDepth = d.LineageMaxDepth
	}
	if l.RevocationTimeout <= 0 {
		l.RevocationTimeout = d.RevocationTimeout
	}
	return l
}

// WithLimits returns a Store over the same pool that applies l. A zero
// field of l takes its default.
func (s *Store) WithLimits(l Limits) *Store {
	return &Store{pool: s.pool, limits: l.withDefaults()}
}

// Limits returns the bounds the Store applies.
func (s *Store) Limits() Limits { return s.limits }

// ErrRevocationTimeout reports that a recursive revocation's cascade or a
// full revoke's walk did not acquire the per-tenant audit advisory lock
// within audit.lock.acquireTimeoutMs, or reached the statement timeout
// auth.tokenRevocationTimeoutMs. The transaction rolled back, so it revoked
// nothing. RevokeLineage does not retry it.
var ErrRevocationTimeout = errors.New("issuedtokenstore: revocation timed out")

// lockForRevocation takes the per-tenant audit advisory lock on tx and then
// sets the transaction's statement timeout to timeout. The cascade and the
// full revoke's walk call it as their first statement, before they read the
// subtree, so an issuance from a token of the subtree either committed
// before the lock was granted, and its row is in the walk's snapshot, or
// takes the lock after the walk commits and finds its source revoked in
// step (1a).
//
// spec: §13.3 (Credential Flow: Lineage of derived tokens; Recursive-revocation
// request authorization), §11.4 step 5, §11.7
func lockForRevocation(ctx context.Context, tx pgx.Tx, tenantID string, timeout time.Duration) error {
	if err := auditstore.AcquireLockInTx(ctx, tx, tenantID); err != nil {
		var lockTimeout *auditstore.ConcurrencyTimeoutError
		if errors.As(err, &lockTimeout) {
			return fmt.Errorf("%w: %w", ErrRevocationTimeout, err)
		}
		return err
	}
	ms := timeout.Milliseconds()
	if ms < 1 {
		ms = 1
	}
	_, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL statement_timeout = %d", ms))
	return err
}

// revocationTimeoutError maps a statement cancelled by statement_timeout
// (SQLSTATE 57014) to ErrRevocationTimeout and returns every other error
// unchanged. A cancellation that follows the caller's own context ending is
// not a timeout of the statement, so it is returned unchanged.
func revocationTimeoutError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return err
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "57014" {
		return fmt.Errorf("%w: %w", ErrRevocationTimeout, err)
	}
	return err
}
