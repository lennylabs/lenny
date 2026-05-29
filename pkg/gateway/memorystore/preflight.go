// SPDX-License-Identifier: MIT

package memorystore

import (
	"context"
	"fmt"
)

// Reserved preflight scope. The §12.8 MemoryStore erasure preflight
// seeds a known agent-memory row under this synthetic (tenant, user)
// scope, erases it, and asserts the row does not survive. The ids are
// reserved: no production tenant or user uses them. The Postgres
// default backend satisfies the agent_memory → tenants(id) foreign key
// via the migration that seeds this reserved tenant.
//
// spec: §12.8 line 746.
const (
	// PreflightTenantID is the reserved tenant the erasure preflight
	// writes its probe row under.
	PreflightTenantID = "__preflight__"
	// PreflightUserID is the reserved user the erasure preflight writes
	// its probe row under.
	PreflightUserID = "__preflight_user__"
)

// ValidateMemoryStoreErasure is the §12.8 "MemoryStore erasure
// preflight" stub-detector. It seeds a known memory under the reserved
// (PreflightTenantID, PreflightUserID) scope, invokes DeleteByUser, and
// asserts a re-query returns zero rows; it then repeats the cycle for
// DeleteByTenant. A backend whose erasure primitive satisfies the
// interface signature but silently no-ops leaves the seeded row in
// place, which this check catches: the gateway refuses to start (and
// the per-job preflight aborts the job) rather than reporting a
// successful GDPR erasure while memories persist. The compile-time
// `var _ Store = (*Backend)(nil)` assertion is the primary defense;
// this is the runtime catch-net for a no-op implementation.
//
// The check is self-cleaning: it removes its probe row on every path,
// and verifies the §9.4 idempotency contract by re-invoking DeleteByUser
// after a successful deletion (which MUST return nil). It returns a
// non-nil error naming the failing primitive when the seeded row
// survives or any operation errors; the caller composes the §12.8 fatal
// startup message.
//
// spec: §12.8 lines 743-758; §9.4 lines 196, 200.
func ValidateMemoryStoreErasure(ctx context.Context, store Store) error {
	if store == nil {
		return fmt.Errorf("memorystore: erasure preflight: store is nil")
	}
	scope := MemoryScope{TenantID: PreflightTenantID, UserID: PreflightUserID}

	// Start from a clean slate so a prior crashed preflight cannot make
	// this run pass or fail spuriously.
	if err := store.DeleteByTenant(ctx, PreflightTenantID); err != nil {
		return fmt.Errorf("memorystore: erasure preflight: initial cleanup DeleteByTenant: %w", err)
	}

	// Layer 1 — DeleteByUser must remove the seeded row.
	if err := seedPreflightRow(ctx, store, scope); err != nil {
		return err
	}
	if err := store.DeleteByUser(ctx, PreflightTenantID, PreflightUserID); err != nil {
		return fmt.Errorf("memorystore: erasure preflight: DeleteByUser: %w", err)
	}
	if n, err := preflightSurvivors(ctx, store, scope); err != nil {
		return err
	} else if n > 0 {
		return fmt.Errorf("memorystore: erasure preflight: DeleteByUser is a silent no-op — %d probe row(s) survived erasure", n)
	}
	// §9.4 idempotency: a repeated DeleteByUser after successful
	// deletion MUST return nil.
	if err := store.DeleteByUser(ctx, PreflightTenantID, PreflightUserID); err != nil {
		return fmt.Errorf("memorystore: erasure preflight: DeleteByUser is not idempotent: %w", err)
	}

	// Layer 2 — DeleteByTenant must remove the seeded row.
	if err := seedPreflightRow(ctx, store, scope); err != nil {
		return err
	}
	if err := store.DeleteByTenant(ctx, PreflightTenantID); err != nil {
		return fmt.Errorf("memorystore: erasure preflight: DeleteByTenant: %w", err)
	}
	if n, err := preflightSurvivors(ctx, store, scope); err != nil {
		return err
	} else if n > 0 {
		return fmt.Errorf("memorystore: erasure preflight: DeleteByTenant is a silent no-op — %d probe row(s) survived erasure", n)
	}
	return nil
}

// seedPreflightRow writes the probe memory and confirms it persisted, so
// a Write that silently drops the row cannot make the subsequent delete
// check pass vacuously.
func seedPreflightRow(ctx context.Context, store Store, scope MemoryScope) error {
	if err := store.Write(ctx, scope, []Memory{{Content: "memorystore erasure preflight probe"}}); err != nil {
		return fmt.Errorf("memorystore: erasure preflight: seed Write: %w", err)
	}
	n, err := preflightSurvivors(ctx, store, scope)
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("memorystore: erasure preflight: seed Write did not persist the probe row")
	}
	return nil
}

// preflightSurvivors counts the probe rows still readable under the
// reserved scope.
func preflightSurvivors(ctx context.Context, store Store, scope MemoryScope) (int, error) {
	rows, err := store.Query(ctx, scope, "", 0)
	if err != nil {
		return 0, fmt.Errorf("memorystore: erasure preflight: Query: %w", err)
	}
	return len(rows), nil
}
