// SPDX-License-Identifier: MIT

package integrity

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
)

// ChainContinuityResult is the §11.7 startup chain-continuity check
// outcome for one tenant. The gateway runs CheckChainContinuity at
// startup and the periodic background integrity check reuses it; a
// broken chain triggers the §16.5 critical alert path.
type ChainContinuityResult struct {
	// TenantID is the tenant whose chain was walked.
	TenantID string

	// Rows is the number of audit rows in the tenant chain.
	Rows int

	// Result is the verifier's §11.7 chain state.
	Result audit.VerifyResult
}

// Broken reports whether the tenant's chain verified as broken — the
// §11.7 ChainBroken state that fires the §16.5 AuditChainGap alert.
func (r ChainContinuityResult) Broken() bool {
	return r.Result.Integrity == audit.ChainBroken
}

// CheckChainContinuity walks every tenant's audit hash chain on a live
// Postgres connection and reports each chain's §11.7 integrity state.
// It is the startup chain-continuity check: a gateway runs it before
// accepting traffic, and the periodic background integrity check runs
// the same walk on a timer.
//
// The walk reconstructs each tenant chain by reading rows ordered by
// sequence_number — the §11.7 authoritative total order — and recomputes
// the hash links with the shared pkg/audit verification logic. A
// broken chain does not abort the check; every tenant is walked so the
// caller sees the full picture.
func CheckChainContinuity(ctx context.Context, db Querier) ([]ChainContinuityResult, error) {
	tenants, err := auditTenants(ctx, db)
	if err != nil {
		return nil, err
	}
	out := make([]ChainContinuityResult, 0, len(tenants))
	for _, tenantID := range tenants {
		rows, err := loadChainRows(ctx, db, tenantID)
		if err != nil {
			return nil, err
		}
		res := audit.ChainFromRows(tenantID, rows, nil).Verify()
		out = append(out, ChainContinuityResult{
			TenantID: tenantID,
			Rows:     len(rows),
			Result:   res,
		})
	}
	return out, nil
}

// FirstBroken returns the first broken-chain result, or nil when every
// chain verified. The gateway startup path uses it to decide whether
// to fire the §16.5 AuditChainGap alert.
func FirstBroken(results []ChainContinuityResult) *ChainContinuityResult {
	for i := range results {
		if results[i].Broken() {
			return &results[i]
		}
	}
	return nil
}

// auditTenants returns the distinct tenant ids that have at least one
// audit_log row, sorted for deterministic iteration.
func auditTenants(ctx context.Context, db Querier) ([]string, error) {
	rows, err := db.Query(ctx, `SELECT DISTINCT tenant_id FROM audit_log`)
	if err != nil {
		return nil, fmt.Errorf("integrity: query audit tenants: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, fmt.Errorf("integrity: scan audit tenant: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("integrity: iterate audit tenants: %w", err)
	}
	sort.Strings(out)
	return out, nil
}

// loadChainRows reads a tenant's audit rows in sequence order and
// recomputes each row's content hash, exactly as the in-memory chain
// derives it (the audit_log table does not store the per-row hash).
func loadChainRows(ctx context.Context, db Querier, tenantID string) ([]audit.Row, error) {
	rows, err := db.Query(ctx, `
		SELECT sequence_number, event_type, payload, created_at, prev_hash
		FROM audit_log
		WHERE tenant_id = $1
		ORDER BY sequence_number`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("integrity: query chain rows for %q: %w", tenantID, err)
	}
	defer rows.Close()
	var out []audit.Row
	for rows.Next() {
		var (
			seq      int64
			r        audit.Row
			payload  []byte
			prevHash []byte
			created  time.Time
		)
		if err := rows.Scan(&seq, &r.EventType, &payload, &created, &prevHash); err != nil {
			return nil, fmt.Errorf("integrity: scan chain row: %w", err)
		}
		r.Seq = uint64(seq)
		r.TenantID = tenantID
		r.Payload = json.RawMessage(payload)
		r.Timestamp = created.UTC()
		r.PrevHash = hex.EncodeToString(prevHash)
		r.Hash = audit.ComputeHash(r)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("integrity: iterate chain rows: %w", err)
	}
	return out, nil
}
