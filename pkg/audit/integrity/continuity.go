// SPDX-License-Identifier: MIT

package integrity

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

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

	// gap boundary fields populated by the windowed startup check when
	// Result is ChainBroken (§12.3 line 101 WARN message inputs).
	gapLowSeq  uint64
	gapHighSeq uint64
	gapStart   time.Time
	gapEnd     time.Time
}

// Broken reports whether the tenant's chain verified as broken — the
// §11.7 ChainBroken state that fires the §16.5 AuditChainGap alert.
func (r ChainContinuityResult) Broken() bool {
	return r.Result.Integrity == audit.ChainBroken
}

// GapLowSeq and GapHighSeq bound the detected gap (the last intact
// sequence number and the first sequence number past the break); the
// timestamps bracket the approximate range of the missing entries.
// They are populated only on a broken windowed check and feed the
// §12.3 line 101 WARN message. spec: §12.3 line 101. F-12.3.9.
func (r ChainContinuityResult) GapLowSeq() uint64   { return r.gapLowSeq }
func (r ChainContinuityResult) GapHighSeq() uint64  { return r.gapHighSeq }
func (r ChainContinuityResult) GapStart() time.Time { return r.gapStart }
func (r ChainContinuityResult) GapEnd() time.Time   { return r.gapEnd }

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

// CheckChainContinuityRecent runs the §12.3 line 101 startup chain-
// continuity check over the most recent lastN entries of every tenant's
// audit chain. lastN is the audit.startupChainCheckEntries bound
// (default 1000 at the call site); lastN <= 0 walks each chain in full
// and is equivalent to CheckChainContinuity.
//
// The genesis sentinel is asserted only when the window reaches the
// chain head (sequence_number 1); a partial window is verified by its
// prev_hash links alone. The durable store allocates sequence_number by
// nextval (§11.7), so a rolled-back transaction consumes a value without
// committing a row and leaves a benign interior sequence-number gap. The
// prev_hash linkage is therefore the authoritative tamper signal: an
// intact-link sequence-number gap is detected but not broken, and only a
// non-linking prev_hash (a committed-row tamper or removal) is reported
// as a broken chain with the boundary sequence numbers and timestamp
// range the §12.3 WARN message needs.
func CheckChainContinuityRecent(ctx context.Context, db Querier, lastN int) ([]ChainContinuityResult, error) {
	tenants, err := auditTenants(ctx, db)
	if err != nil {
		return nil, err
	}
	out := make([]ChainContinuityResult, 0, len(tenants))
	for _, tenantID := range tenants {
		rows, err := loadRecentChainRows(ctx, db, tenantID, lastN)
		if err != nil {
			return nil, err
		}
		res, gap := verifyChainWindow(tenantID, rows)
		out = append(out, ChainContinuityResult{
			TenantID:   tenantID,
			Rows:       len(rows),
			Result:     res,
			gapLowSeq:  gap.lowSeq,
			gapHighSeq: gap.highSeq,
			gapStart:   gap.start,
			gapEnd:     gap.end,
		})
	}
	return out, nil
}

// chainGap brackets a detected break for the §12.3 WARN message.
type chainGap struct {
	lowSeq  uint64
	highSeq uint64
	start   time.Time
	end     time.Time
}

// verifyChainWindow walks a sequence-ordered window of a tenant's chain
// and reports its §11.7 integrity state. The genesis sentinel is
// asserted only when the window starts at sequence_number 1, so a
// partial recent-N window is verified by sequence contiguity and
// prev_hash links. The returned chainGap is populated on a break.
//
// The audit sequence_number is allocated by nextval (§11.7), so a
// transaction rollback consumes a value without committing a row and
// leaves a benign interior sequence-number gap, and a swept chain that
// restarts at a non-1 genesis linking to the genesis sentinel is
// likewise benign. Chain integrity is therefore decided by the prev_hash
// linkage rather than by sequence density: a sequence-number gap whose
// prev_hash link is intact (cur.PrevHash == LinkHash(prev)) is detected
// but not a broken chain, and only a non-linking prev_hash — a
// committed-row tamper or removal — returns ChainBroken. This keeps a
// benign nextval-rollback gap and a crash-dropped accepted buffered-T2
// batch (both intact-link) from firing §16.5 AuditChainGap, while a
// committed-row tamper still breaks the chain. spec: §11.7, §12.3.
func verifyChainWindow(tenantID string, rows []audit.Row) (audit.VerifyResult, chainGap) {
	if len(rows) == 0 {
		return audit.VerifyResult{Integrity: audit.ChainVerified}, chainGap{}
	}
	if rows[0].Seq == 1 && rows[0].PrevHash != audit.GenesisPrevHash {
		return audit.VerifyResult{
			Integrity: audit.ChainBroken,
			BreakSeq:  rows[0].Seq,
			Detail:    fmt.Sprintf("genesis row %d prev_hash is not the sentinel", rows[0].Seq),
		}, chainGap{}
	}
	for i := 1; i < len(rows); i++ {
		prev, cur := rows[i-1], rows[i]
		// The prev_hash link is the authoritative tamper signal, so it is
		// evaluated before a sequence-number gap is considered. A
		// non-linking prev_hash is a broken chain whether or not the
		// sequence numbers are contiguous (a committed-row tamper leaves a
		// contiguous non-linking pair; a committed-row removal leaves a
		// sequence gap across a non-linking pair). spec: §11.7, §12.3.
		if cur.PrevHash != audit.LinkHash(prev) {
			detail := fmt.Sprintf("row %d prev_hash does not link to row %d", cur.Seq, prev.Seq)
			if cur.Seq != prev.Seq+1 {
				detail = fmt.Sprintf("prev_hash does not link across sequence gap between %d and %d", prev.Seq, cur.Seq)
			}
			return audit.VerifyResult{
				Integrity: audit.ChainBroken,
				BreakSeq:  cur.Seq,
				Detail:    detail,
			}, chainGap{lowSeq: prev.Seq, highSeq: cur.Seq, start: prev.Timestamp, end: cur.Timestamp}
		}
		// The prev_hash link is intact. A sequence-number gap here is a
		// benign nextval-rollback (or a crash-dropped accepted buffered-T2
		// batch that still linked to the committed tail), detected but not
		// broken, so the walk continues. spec: §11.7, §12.3.
	}
	return audit.VerifyResult{Integrity: audit.ChainVerified}, chainGap{}
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
		SELECT sequence_number, event_type, payload, created_at, prev_hash, id::text, event_schema_version
		FROM audit_log
		WHERE tenant_id = $1
		ORDER BY sequence_number`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("integrity: query chain rows for %q: %w", tenantID, err)
	}
	return scanChainRows(rows, tenantID)
}

// loadRecentChainRows reads the most recent lastN rows of a tenant's
// chain (the §12.3 line 101 audit.startupChainCheckEntries window) and
// returns them in ascending sequence order. lastN <= 0 loads the chain
// in full.
func loadRecentChainRows(ctx context.Context, db Querier, tenantID string, lastN int) ([]audit.Row, error) {
	if lastN <= 0 {
		return loadChainRows(ctx, db, tenantID)
	}
	rows, err := db.Query(ctx, `
		SELECT sequence_number, event_type, payload, created_at, prev_hash, id::text, event_schema_version
		FROM (
			SELECT sequence_number, event_type, payload, created_at, prev_hash, id, event_schema_version
			FROM audit_log
			WHERE tenant_id = $1
			ORDER BY sequence_number DESC
			LIMIT $2
		) recent
		ORDER BY sequence_number ASC`, tenantID, lastN)
	if err != nil {
		return nil, fmt.Errorf("integrity: query recent chain rows for %q: %w", tenantID, err)
	}
	return scanChainRows(rows, tenantID)
}

// scanChainRows scans a sequence-ordered audit_log result set into
// audit.Row values, recomputing each row's content hash (the table
// does not store it). It closes rows before returning.
func scanChainRows(rows pgx.Rows, tenantID string) ([]audit.Row, error) {
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
		if err := rows.Scan(&seq, &r.EventType, &payload, &created, &prevHash, &r.ID, &r.EventSchemaVersion); err != nil {
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
