// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// aliasPrimaryDDLToBillingAudit reports whether the primary-instance DDL
// pool should alias the billing/audit DDL pool rather than open its own
// connection. This is true only in the single-instance topology: no separate
// billing/audit instance is configured (billingAuditDSN empty) and no distinct
// primary DDL DSN is supplied (primaryDDLDSN empty), so the primary and the
// billing/audit instance are one and the single billing/audit DDL pool
// addresses both. When a separate billing/audit instance is configured the
// operator must supply LENNY_PG_PRIMARY_DDL_DSN, because the primary's
// lenny_app pool holds no CREATE ON SCHEMA and the §13.3 issued-token
// write-before-issue path seals its per-tenant audit row on the primary.
//
// spec: §12.3, §15.1. F-11.2.10.
func aliasPrimaryDDLToBillingAudit(billingAuditDSN, primaryDDLDSN string) bool {
	return billingAuditDSN == "" && primaryDDLDSN == ""
}

// distinctDDLPools returns the CREATE-privileged DDL pools that must be closed
// at shutdown, deduplicated by pointer identity. In the single-instance
// topology primaryDDLPool aliases billingAuditDDLPool (aliasPrimaryDDLToBillingAudit),
// so closing each distinct pool once avoids a double Close on the shared pool.
// A nil pool contributes nothing. The shutdown path (runServers) iterates the
// result and calls Close on each, so the dedup decision has a single canonical
// implementation the tier-1 test pins.
//
// spec: §12.3, §15.1. F-11.2.10.
func distinctDDLPools(billingAuditDDLPool, primaryDDLPool *pgxpool.Pool) []*pgxpool.Pool {
	pools := make([]*pgxpool.Pool, 0, 2)
	if billingAuditDDLPool != nil {
		pools = append(pools, billingAuditDDLPool)
	}
	if primaryDDLPool != nil && primaryDDLPool != billingAuditDDLPool {
		pools = append(pools, primaryDDLPool)
	}
	return pools
}

// closeDDLPools closes each CREATE-privileged DDL pool exactly once at
// shutdown, deduplicating the pool shared between the billing/audit and
// primary limbs in the single-instance topology through distinctDDLPools. The
// shutdown path (runServers) delegates here so the close loop is a single
// canonical implementation the tier-1 test pins, rather than an inline loop
// that runs only during a real process shutdown.
//
// spec: §12.3, §15.1. F-11.2.10.
func closeDDLPools(billingAuditDDLPool, primaryDDLPool *pgxpool.Pool) {
	for _, pool := range distinctDDLPools(billingAuditDDLPool, primaryDDLPool) {
		pool.Close()
	}
}

// ddlPoolOpener opens a Postgres connection pool from a DSN. It is the injected
// seam over pgxpool.New that lets openVerifiedDDLPool be exercised at tier 1
// without a live Postgres, and lets buildPersistenceStores pass the real
// pgxpool.New at startup.
type ddlPoolOpener func(ctx context.Context, dsn string) (*pgxpool.Pool, error)

// ddlSchemaVerifier verifies that an opened pool addresses an instance carrying
// the migrations/ schema. It is the injected seam over verifyPostgresSchema.
type ddlSchemaVerifier func(ctx context.Context, pool *pgxpool.Pool) error

// openVerifiedDDLPool opens a CREATE-privileged DDL pool from dsn and verifies
// its schema, returning the pool for the caller to retain. Both the
// billing/audit and the primary per-tenant sequence-provisioning pools are
// opened this way, so the open-then-verify-then-fail-closed sequence has a
// single canonical implementation the tier-1 test pins rather than two
// near-identical inline blocks. An empty dsn yields a nil pool and no error
// (the topology does not use this DDL pool). A verify failure closes the pool
// it opened before returning the error, so a failed startup leaks no
// connection pool. The caller escalates a non-nil error to log.Fatalf, keeping
// the DDL-provisioning path fail-closed at startup: a gateway that cannot open
// a CREATE-privileged pool cannot provision per-tenant sequences and must not
// start.
//
// spec: §12.3, §15.1. F-11.2.10.
func openVerifiedDDLPool(
	ctx context.Context,
	dsn string,
	open ddlPoolOpener,
	verify ddlSchemaVerifier,
) (*pgxpool.Pool, error) {
	if dsn == "" {
		return nil, nil
	}
	pool, err := open(ctx, dsn)
	if err != nil {
		return nil, err
	}
	if err := verify(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// ddlPoolDSNs are the three DSNs that determine the §12.3 R-03 per-tenant DDL
// pool topology: the billing/audit instance DSN (billingAudit, whose emptiness
// distinguishes the single-instance from the separate-instance topology), and
// the two CREATE-privileged DDL DSNs (billingAuditDDL, primaryDDL).
type ddlPoolDSNs struct {
	billingAudit    string
	billingAuditDDL string
	primaryDDL      string
}

// resolveDDLPools opens the CREATE-privileged per-tenant DDL pools for the
// configured topology, returning the billing/audit DDL pool and the primary DDL
// pool. It centralizes the topology decision the two pools share so the alias
// branch and the separate-instance branch have a single canonical
// implementation the tier-1 test pins, rather than two inline blocks in the
// startup path. The rules:
//
//   - billingAuditDDL opens the billing/audit DDL pool (empty DSN -> nil).
//   - primaryDDL, when set, opens a distinct primary DDL pool.
//   - when primaryDDL is empty and the topology is single-instance
//     (aliasPrimaryDDLToBillingAudit), the primary DDL pool aliases the
//     billing/audit DDL pool, because the primary and the billing/audit
//     instance are one and the single pool addresses both.
//   - when primaryDDL is empty but a separate billing/audit instance is
//     configured, the primary DDL pool is nil; the operator must supply
//     LENNY_PG_PRIMARY_DDL_DSN for the separate-instance topology, and its
//     absence is surfaced as a nil primary pool the caller acts on.
//
// A failed open or schema-verify on either pool closes any pool already opened
// and returns the error, so a failed startup leaks no connection pool and the
// caller escalates to log.Fatalf (fail-closed DDL provisioning).
//
// spec: §12.3, §15.1. F-11.2.10.
func resolveDDLPools(
	ctx context.Context,
	dsns ddlPoolDSNs,
	open ddlPoolOpener,
	verify ddlSchemaVerifier,
) (billingAuditDDLPool, primaryDDLPool *pgxpool.Pool, err error) {
	billingAuditDDLPool, err = openVerifiedDDLPool(ctx, dsns.billingAuditDDL, open, verify)
	if err != nil {
		return nil, nil, fmt.Errorf("billing/audit DDL pool: %w", err)
	}
	if dsns.primaryDDL != "" {
		primaryDDLPool, err = openVerifiedDDLPool(ctx, dsns.primaryDDL, open, verify)
		if err != nil {
			closeDDLPools(billingAuditDDLPool, nil)
			return nil, nil, fmt.Errorf("primary DDL pool: %w", err)
		}
		return billingAuditDDLPool, primaryDDLPool, nil
	}
	// No explicit primary DDL DSN. In the single-instance topology the primary
	// is the billing/audit instance, so the billing/audit DDL pool addresses
	// the primary too; in the separate-instance topology the primary pool stays
	// nil until the operator supplies LENNY_PG_PRIMARY_DDL_DSN.
	if aliasPrimaryDDLToBillingAudit(dsns.billingAudit, dsns.primaryDDL) {
		primaryDDLPool = billingAuditDDLPool
	}
	return billingAuditDDLPool, primaryDDLPool, nil
}
