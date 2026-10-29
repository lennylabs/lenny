// SPDX-License-Identifier: MIT

// Package denialpg is the Postgres-backed leasecontrol.DenialStore: the
// handoff-safe durable home for the §8.6 extension-denied flag, the
// rejection cool-off expiry, and the lease-extension grant counters.
//
// §8.6 forbid a user's rejection of a budget elicitation
// from being bypassed by a gateway restart, rolling update, or
// coordinator handoff. This store satisfies the three normative rules:
//
//   - Durability: Deny persists the flag and the cool-off
//     expiry to the delegation_tree_budget table, keyed by
//     (tenant_id, root_session_id), so a newly elected replica reads the
//     denial on its first extension request.
//   - Read-from-store: Denied reads the flag from Postgres,
//     never from an in-memory cache, and compares cool_off_expiry against
//     the database clock (NOW()) rather than a replica's Go wall clock.
//   - In-flight atomic re-check: Grant locks the tree's row,
//     re-evaluates the denial inside the same transaction that adds the
//     clamped grant to the cumulative ext_* columns, and rolls back,
//     returning ErrExtensionDenied, when the flag is found set. The flag
//     read, the clamp, and the counter write serialize under the row
//     lock, closing the race where a REJECTED outcome is persisted
//     between an earlier flag read and a later grant commit, and keeping
//     concurrent grants from different replicas within the limit.
//
// All cool-off comparisons use the database clock per §8.6; the
// store never compares an expiry against time.Now() in Go. The §11.2
// consumption-counter checkpoint (delegationbudget/pgstore) and this
// store write disjoint column sets on the same row, so neither clobbers
// the other.
//
// spec: §8.6.
package denialpg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
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
// expiry coolOff into the future from the database clock (§8.6),
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
// against the database clock (§8.6). A tree with no persisted
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

// Grant runs the §8.6 in-flight atomic re-check. In one transaction it
// ensures the tree's row exists, locks it with SELECT ... FOR UPDATE,
// re-evaluates the denial against the database clock, clamps requested
// so each cumulative ext_* column stays within limit
// (leasecontrol.ClampGrant), and adds the clamped amount. A denied tree
// returns leasecontrol.ErrExtensionDenied and the transaction rolls back.
// The flag read, the clamp, and the counter write serialize under the
// row lock, so grants served by different replicas never exceed the
// limit together.
// spec: §8.6 (In-flight requests), §8.6 (Extension state)
func (s *Store) Grant(ctx context.Context, tenantID, rootSessionID string, requested, limit leasecontrol.Dimensions) (leasecontrol.GrantResult, error) {
	var res leasecontrol.GrantResult
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO delegation_tree_budget (tenant_id, root_session_id, updated_at)
			 VALUES ($1, $2, clock_timestamp())
			 ON CONFLICT (tenant_id, root_session_id) DO NOTHING`,
			tenantID, rootSessionID); err != nil {
			return err
		}
		var denied bool
		var cur leasecontrol.Dimensions
		if err := tx.QueryRow(ctx,
			`SELECT extension_denied
			          AND cool_off_expiry IS NOT NULL
			          AND cool_off_expiry > NOW(),
			        `+extColumns+`
			   FROM delegation_tree_budget
			  WHERE tenant_id = $1 AND root_session_id = $2
			    FOR UPDATE`,
			tenantID, rootSessionID).Scan(append([]any{&denied}, extTargets(&cur)...)...); err != nil {
			return err
		}
		if denied {
			return leasecontrol.ErrExtensionDenied
		}
		applied := leasecontrol.ClampGrant(cur, requested, limit)
		var total leasecontrol.Dimensions
		if err := tx.QueryRow(
			ctx,
			`UPDATE delegation_tree_budget
			     SET ext_tokens            = ext_tokens            + $3,
			         ext_seconds           = ext_seconds           + $4,
			         ext_children          = ext_children          + $5,
			         ext_parallel_children = ext_parallel_children + $6,
			         ext_tree_size         = ext_tree_size         + $7,
			         ext_file_export_files = ext_file_export_files + $8,
			         ext_file_export_bytes = ext_file_export_bytes + $9,
			         updated_at            = clock_timestamp()
			   WHERE tenant_id = $1 AND root_session_id = $2
			 RETURNING `+extColumns,
			tenantID, rootSessionID,
			applied.Tokens, applied.Seconds, applied.Children, applied.ParallelChildren,
			applied.TreeSize, applied.FileExportFiles, applied.FileExportBytes,
		).Scan(extTargets(&total)...); err != nil {
			return err
		}
		res = leasecontrol.GrantResult{Applied: applied, Total: total}
		return nil
	})
	if errors.Is(err, leasecontrol.ErrExtensionDenied) {
		return leasecontrol.GrantResult{}, leasecontrol.ErrExtensionDenied
	}
	if err != nil {
		return leasecontrol.GrantResult{}, fmt.Errorf("denialpg: grant %q/%q: %w", tenantID, rootSessionID, err)
	}
	return res, nil
}

// Grants returns the tree's cumulative extension grants with a
// primary-key SELECT of the ext_* columns, and zero for a tree with no
// row.
// spec: §8.6 (Extension state)
func (s *Store) Grants(ctx context.Context, tenantID, rootSessionID string) (leasecontrol.Dimensions, error) {
	var d leasecontrol.Dimensions
	err := pgtenant.InTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT `+extColumns+`
			   FROM delegation_tree_budget
			  WHERE tenant_id = $1 AND root_session_id = $2`,
			tenantID, rootSessionID).Scan(extTargets(&d)...)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return leasecontrol.Dimensions{}, nil
	}
	if err != nil {
		return leasecontrol.Dimensions{}, fmt.Errorf("denialpg: grants %q/%q: %w", tenantID, rootSessionID, err)
	}
	return d, nil
}

// extColumns lists the cumulative grant columns in the order extTargets
// scans them.
const extColumns = `ext_tokens, ext_seconds, ext_children, ext_parallel_children,
			        ext_tree_size, ext_file_export_files, ext_file_export_bytes`

// extTargets returns scan targets for extColumns.
func extTargets(d *leasecontrol.Dimensions) []any {
	return []any{
		&d.Tokens, &d.Seconds, &d.Children, &d.ParallelChildren,
		&d.TreeSize, &d.FileExportFiles, &d.FileExportBytes,
	}
}

// Clear clears the tree's extension-denied flag and cool-off, backing the
// §15.1 admin extension-denial clear endpoint. Clearing a tree
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
