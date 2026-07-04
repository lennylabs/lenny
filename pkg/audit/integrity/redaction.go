// SPDX-License-Identifier: MIT

package integrity

import (
	"context"
	"fmt"
	"sort"
)

// RedactionReceiptResult reports, for one tenant, how many audit_log rows
// carry the §12.8 in-place GDPR redaction marker but have no
// signature-verifying RedactionReceipt in audit_redaction_receipts. The
// §11.7 periodic background integrity check turns each non-zero count into
// a lenny_audit_redaction_receipt_missing_total increment, which fires the
// §16.5 AuditRedactionReceiptMissing critical alert. spec: §12.8 lines
// 810-827; §16.5 line 467. F-11.7.15.
type RedactionReceiptResult struct {
	// TenantID is the tenant whose audit chain carries the orphaned
	// redaction(s).
	TenantID string
	// Missing is the number of redaction-marked rows lacking a valid
	// receipt for the tenant.
	Missing int
}

// CheckRedactionReceipts finds audit_log rows that were rewritten in place
// by the §12.8 step-14 OCSF dead-letter PII redaction (their payload
// carries the `{"redacted": true, ...}` marker) but for which no
// signature-bearing RedactionReceipt exists at the matching
// (tenant_id, sequence_number). The receipt is the sole provenance token
// that distinguishes an authorized GDPR redaction from a tamper that
// merely cleared `payload`; a redaction-marked row without one MUST be
// treated as a compliance incident (§12.8 line 810, §16.5 line 467).
//
// A receipt counts as missing when the row exists but has no receipt, or
// the receipt carries no signature (the absent / signature-invalid cases
// the §16.5 alert enumerates). The full KMS signature verification and the
// match of the receipt's original_hash against the redacted row's
// preserved pre-redaction hash are performed by external verifiers that
// hold the audit public key; this in-process detector
// covers the absent-receipt and unsigned-receipt cases that a gateway can
// observe without the signing key.
//
// The scan is filtered to redaction-marked rows, which are exceptional
// (steady-state zero), so it does not walk the full ledger. A query error
// (for example, the audit_redaction_receipts relation not yet existing on
// a partially-migrated deployment) is returned to the caller, which treats
// it as a non-fatal skipped cycle rather than drift. spec: §11.7 line 370;
// §12.8 lines 810-827. F-11.7.15.
func CheckRedactionReceipts(ctx context.Context, db Querier) ([]RedactionReceiptResult, error) {
	rows, err := db.Query(ctx, `
		SELECT a.tenant_id, COUNT(*)
		FROM audit_log a
		LEFT JOIN audit_redaction_receipts r
			ON r.tenant_id = a.tenant_id
			AND r.sequence_number = a.sequence_number
		WHERE (a.payload->>'redacted') = 'true'
			AND (r.receipt_id IS NULL
				OR r.signature IS NULL
				OR octet_length(r.signature) = 0)
		GROUP BY a.tenant_id`)
	if err != nil {
		return nil, fmt.Errorf("integrity: scan orphaned redaction rows: %w", err)
	}
	defer rows.Close()
	var out []RedactionReceiptResult
	for rows.Next() {
		var (
			tenant  string
			missing int64
		)
		if err := rows.Scan(&tenant, &missing); err != nil {
			return nil, fmt.Errorf("integrity: scan redaction receipt count: %w", err)
		}
		if missing > 0 {
			out = append(out, RedactionReceiptResult{TenantID: tenant, Missing: int(missing)})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("integrity: iterate redaction receipt counts: %w", err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TenantID < out[j].TenantID })
	return out, nil
}
