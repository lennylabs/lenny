// SPDX-License-Identifier: MIT

// Package denialpg is the Postgres-backed leasecontrol.DenialStore: the
// handoff-safe durable home for the §8.6 extension-denied flag, the
// rejection cool-off expiry, and the lease-extension grant counters.
//
// §8.6 lines 730-733 forbid a user's rejection of a budget elicitation
// from being bypassed by a gateway restart, rolling update, or
// coordinator handoff. This store satisfies the three normative rules:
//
//   - Durability (line 730): Deny persists the flag and the cool-off
//     expiry to the delegation_tree_budget table, keyed by
//     (tenant_id, root_session_id), so a newly elected replica reads the
//     denial on its first extension request.
//   - Read-from-store (line 731): Denied reads the flag from Postgres,
//     never from an in-memory cache, and compares cool_off_expiry against
//     the database clock (NOW()) rather than a replica's Go wall clock.
//   - In-flight atomic re-check (line 732): Grant locks the tree's row,
//     re-evaluates the denial inside the same transaction that increments
//     the durable extension counters, and rolls the increment back —
//     returning ErrExtensionDenied — when the flag is found set. The flag
//     read and the counter write serialize under the row lock, closing
//     the race where a REJECTED outcome is persisted between an earlier
//     flag read and a later grant commit.
//
// All cool-off comparisons use the database clock per §8.6 line 733; the
// store never compares an expiry against time.Now() in Go. The §11.2
// consumption-counter checkpoint (delegationbudget/pgstore) and this
// store write disjoint column sets on the same row, so neither clobbers
// the other.
//
// spec: §8.6 lines 730-733.
package denialpg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/delegationtree/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pgtenant"
)

// Store is the Postgres-backed leasecontrol.DenialStore. Construct with New.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store backed by pool.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

var _ leasecontrol.DenialStore = (*Store)(nil)

// Deny persists the tree's extension-denied flag and sets the cool-off
// expiry coolOff into the future from the database clock (§8.6 line 733),
// keyed by (tenantID, rootSessionID). It upserts so a tree with no prior
// row (no checkpoint yet) is created denied. The §11.2 consumption
// columns are left at their defaults.
func (s *Store) Deny(ctx context.Context, tenantID, rootSessionID string, coolOff time.Duration) error {
	secs := int64(coolOff / time.Second)
	if secs < 0 {
		secs = 0
	}
	return pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO delegation_tree_budget
			     (tenant_id, root_session_id, extension_denied, cool_off_expiry, updated_at)
			 VALUES ($1, $2, TRUE, NOW() + ($3 * INTERVAL '1 second'), clock_timestamp())
			 ON CONFLICT (tenant_id, root_session_id) DO UPDATE
			     SET extension_denied = TRUE,
			         cool_off_expiry  = NOW() + ($3 * INTERVAL '1 second'),
			         updated_at       = clock_timestamp()`,
			tenantID, rootSessionID, secs); err != nil {
			return fmt.Errorf("denialpg: deny %q/%q: %w", tenantID, rootSessionID, err)
		}
		return nil
	})
}

// Denied reports whether the tree is currently extension-denied. The
// flag-set AND cool_off_expiry > NOW() comparison is evaluated in SQL
// against the database clock (§8.6 line 733). A tree with no persisted
// row is reported not denied with a zero expiry.
func (s *Store) Denied(ctx context.Context, tenantID, rootSessionID string) (bool, time.Time, error) {
	var denied bool
	var expiry *time.Time
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT extension_denied
			          AND cool_off_expiry IS NOT NULL
			          AND cool_off_expiry > NOW(),
			        cool_off_expiry
			   FROM delegation_tree_budget
			  WHERE tenant_id = $1 AND root_session_id = $2`,
			tenantID, rootSessionID).Scan(&denied, &expiry)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, time.Time{}, nil
	}
	if err != nil {
		return false, time.Time{}, fmt.Errorf("denialpg: denied %q/%q: %w", tenantID, rootSessionID, err)
	}
	if expiry == nil {
		return denied, time.Time{}, nil
	}
	return denied, expiry.UTC(), nil
}

// Grant runs the §8.6 line 732 in-flight atomic re-check. The single
// upsert acquires the tree row's lock (via ON CONFLICT) and either:
//
//   - increments the durable per-tree extension counters and returns the
//     row (the grant committed) when the tree is not denied; or
//   - suppresses the increment via the conditional WHERE and returns no
//     row when the flag is set and the cool-off has not expired, which
//     this method maps to ErrExtensionDenied.
//
// On the fresh-insert path no denial can exist, so the row is always
// returned. The denial comparison uses NOW() inside the transaction so
// the flag read and the counter write are serialized under the row lock.
func (s *Store) Grant(ctx context.Context, tenantID, rootSessionID string, granted leasecontrol.Dimensions) error {
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		var ok bool
		scanErr := tx.QueryRow(
			ctx,
			`INSERT INTO delegation_tree_budget
			     (tenant_id, root_session_id,
			      ext_tokens, ext_seconds, ext_children, ext_parallel_children,
			      ext_tree_size, ext_file_export_files, ext_file_export_bytes, updated_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, clock_timestamp())
			 ON CONFLICT (tenant_id, root_session_id) DO UPDATE
			     SET ext_tokens            = delegation_tree_budget.ext_tokens            + EXCLUDED.ext_tokens,
			         ext_seconds           = delegation_tree_budget.ext_seconds           + EXCLUDED.ext_seconds,
			         ext_children          = delegation_tree_budget.ext_children          + EXCLUDED.ext_children,
			         ext_parallel_children = delegation_tree_budget.ext_parallel_children + EXCLUDED.ext_parallel_children,
			         ext_tree_size         = delegation_tree_budget.ext_tree_size         + EXCLUDED.ext_tree_size,
			         ext_file_export_files = delegation_tree_budget.ext_file_export_files + EXCLUDED.ext_file_export_files,
			         ext_file_export_bytes = delegation_tree_budget.ext_file_export_bytes + EXCLUDED.ext_file_export_bytes,
			         updated_at            = clock_timestamp()
			     WHERE NOT (delegation_tree_budget.extension_denied
			                AND delegation_tree_budget.cool_off_expiry IS NOT NULL
			                AND delegation_tree_budget.cool_off_expiry > NOW())
			 RETURNING TRUE`,
			tenantID, rootSessionID,
			granted.Tokens, granted.Seconds, granted.Children, granted.ParallelChildren,
			granted.TreeSize, granted.FileExportFiles, granted.FileExportBytes,
		).Scan(&ok)
		if errors.Is(scanErr, pgx.ErrNoRows) {
			// The conflict-update WHERE was false: the tree is denied and
			// in cool-off, so no row was updated or returned. Roll back.
			return leasecontrol.ErrExtensionDenied
		}
		return scanErr
	})
	if err != nil {
		if errors.Is(err, leasecontrol.ErrExtensionDenied) {
			return leasecontrol.ErrExtensionDenied
		}
		return fmt.Errorf("denialpg: grant %q/%q: %w", tenantID, rootSessionID, err)
	}
	return nil
}

// Clear clears the tree's extension-denied flag and cool-off, backing the
// §15.1 line 868 admin extension-denial clear endpoint. Clearing a tree
// with no persisted denial row is a no-op (the caller has already
// established the tree is known).
func (s *Store) Clear(ctx context.Context, tenantID, rootSessionID string) error {
	return pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`UPDATE delegation_tree_budget
			     SET extension_denied = FALSE,
			         cool_off_expiry  = NULL,
			         updated_at       = clock_timestamp()
			   WHERE tenant_id = $1 AND root_session_id = $2`,
			tenantID, rootSessionID); err != nil {
			return fmt.Errorf("denialpg: clear %q/%q: %w", tenantID, rootSessionID, err)
		}
		return nil
	})
}
