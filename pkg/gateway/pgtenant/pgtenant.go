// SPDX-License-Identifier: MIT

// Package pgtenant holds the helpers shared by the Postgres-backed
// store implementations. The central one is InTx: every tenant-scoped
// table carries the §12.3 lenny_tenant_guard trigger, which rejects
// any write whose transaction has not set app.current_tenant, so each
// store operation runs inside a transaction that sets it first.
package pgtenant

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// InTx runs fn inside a transaction that has set app.current_tenant
// to tenantID (transaction-local, per §12.3). fn's error is returned
// verbatim — not wrapped — so callers can errors.Is it against their
// store sentinels. The transaction commits when fn returns nil and
// rolls back otherwise.
func InTx(ctx context.Context, pool *pgxpool.Pool, tenantID string, fn func(pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("pgtenant: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx,
		"SELECT set_config('app.current_tenant', $1, true)", tenantID); err != nil {
		return fmt.Errorf("pgtenant: set tenant context: %w", err)
	}
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("pgtenant: commit: %w", err)
	}
	return nil
}

// NullTime maps a zero time.Time to a SQL NULL, so a nullable
// TIMESTAMPTZ column distinguishes "unset" from a real instant.
func NullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// NullString maps an empty string to a SQL NULL, so a nullable TEXT
// column distinguishes "absent" from "empty".
func NullString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// MonotonicNext returns now (UTC, truncated to the Postgres
// timestamptz microsecond resolution) when it is strictly after prev,
// and prev + 1µs otherwise. Stores use it for updated_at columns so
// the value strictly advances even when two writes land within the
// same microsecond.
func MonotonicNext(prev, now time.Time) time.Time {
	now = now.UTC().Truncate(time.Microsecond)
	if now.After(prev) {
		return now
	}
	return prev.Add(time.Microsecond)
}
