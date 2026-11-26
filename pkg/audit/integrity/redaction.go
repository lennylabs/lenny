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

// RedactionReceiptResult reports, for one tenant, how many audit_log rows
// carry the §12.8 in-place GDPR redaction marker but have no
// signature-verifying RedactionReceipt in audit_redaction_receipts. The
// §11.7 periodic background integrity check turns each non-zero count into
// a lenny_audit_redaction_receipt_missing_total increment, which fires the
// §16.5 AuditRedactionReceiptMissing critical alert. spec: §12.8; §16.5. F-11.7.15.
type RedactionReceiptResult struct {
	// TenantID is the tenant whose audit chain carries the orphaned
	// redaction(s).
	TenantID string
	// Missing is the number of redaction-marked rows lacking a valid
	// receipt for the tenant.
	Missing int
}

// CheckRedactionReceipts finds audit_log rows that the §12.8 step-14
// OCSF dead-letter PII redaction rewrote in place (their payload carries
// the `{"redacted": true, ...}` marker) and that have no signature-verifying
// RedactionReceipt at the matching (tenant_id, sequence_number). Only the
// receipt tells an authorized GDPR redaction apart from a rewrite of the
// payload by someone else, so a redaction-marked row without a verifying
// receipt is a compliance incident.
//
// A receipt counts as missing when the row has none, when the receipt
// names another audit row or tenant, when the row's content hash
// recomputed from its stored columns differs from the receipt's new_hash,
// or when verifier does not verify its signature: an unsigned receipt, a
// key identifier outside the trusted set, and a signature that does not
// cover the stored tuple all count. The new_hash match catches a redacted
// row whose payload was rewritten again after its receipt was signed. A
// nil verifier trusts no receipt, so every redaction-marked row counts.
// The chain walk and the match of the receipt's original_hash against the
// redacted row's preserved pre-redaction hash are performed by external
// verifiers and by the audit chain verifier, which read the successor
// row's prev_hash.
//
// The scan reads only redaction-marked rows and their receipts, so it does
// not walk the full ledger. A query error (for example, the
// audit_redaction_receipts relation not yet existing on a partially-
// migrated deployment) is returned to the caller, which treats it as a
// non-fatal skipped cycle rather than drift.
//
// spec: §12.8 (Compliance Interfaces), §11.7 (Audit Logging), §16.5
// (Alerting Rules and SLOs)
func CheckRedactionReceipts(ctx context.Context, db Querier, verifier audit.ReceiptVerifier) ([]RedactionReceiptResult, error) {
	rows, err := db.Query(ctx, `
		SELECT a.tenant_id, a.id::text, a.sequence_number, a.event_type,
			a.event_schema_version, a.payload, a.created_at, a.prev_hash,
			COALESCE(r.receipt_id::text, ''), COALESCE(r.audit_event_id::text, ''),
			COALESCE(r.tenant_id, ''), COALESCE(r.sequence_number, 0),
			COALESCE(r.original_hash, ''::bytea), COALESCE(r.new_hash, ''::bytea),
			COALESCE(r.erasure_job_id::text, ''), COALESCE(r.legal_basis, ''),
			COALESCE(r.redactor_identity, ''),
			COALESCE(r."timestamp", 'epoch'::timestamptz),
			COALESCE(r.signature, ''::bytea), COALESCE(r.signature_kms_key_id, '')
		FROM audit_log a
		LEFT JOIN audit_redaction_receipts r
			ON r.tenant_id = a.tenant_id
			AND r.sequence_number = a.sequence_number
		WHERE (a.payload->>'redacted') = 'true'`)
	if err != nil {
		return nil, fmt.Errorf("integrity: scan redacted rows and receipts: %w", err)
	}
	defer rows.Close()
	missing := map[string]int{}
	for rows.Next() {
		row, rcpt, err := scanRedactedRow(rows)
		if err != nil {
			return nil, err
		}
		tenant := row.TenantID
		if !receiptVerifies(verifier, row, rcpt) {
			missing[tenant]++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("integrity: iterate redacted rows: %w", err)
	}
	out := make([]RedactionReceiptResult, 0, len(missing))
	for tenant, n := range missing {
		out = append(out, RedactionReceiptResult{TenantID: tenant, Missing: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TenantID < out[j].TenantID })
	return out, nil
}

// scanRedactedRow reads one row of the CheckRedactionReceipts scan: the
// redacted audit row with the columns its content hash covers, and its
// receipt, whose ReceiptID is empty when the row has none.
func scanRedactedRow(rows pgx.Rows) (audit.Row, audit.RedactionReceipt, error) {
	var (
		row               audit.Row
		rowSeq, seq       int64
		payload, prevHash []byte
		r                 audit.RedactionReceipt
		origHash, newHash []byte
		ts                time.Time
	)
	if err := rows.Scan(&row.TenantID, &row.ID, &rowSeq, &row.EventType,
		&row.EventSchemaVersion, &payload, &row.Timestamp, &prevHash,
		&r.ReceiptID, &r.AuditEventID, &r.TenantID, &seq,
		&origHash, &newHash, &r.ErasureJobID, &r.LegalBasis, &r.RedactorIdentity,
		&ts, &r.Signature, &r.KeyID); err != nil {
		return audit.Row{}, audit.RedactionReceipt{}, fmt.Errorf("integrity: scan redacted row: %w", err)
	}
	row.Seq = uint64(rowSeq)
	row.Payload = json.RawMessage(payload)
	row.Timestamp = row.Timestamp.UTC()
	row.PrevHash = hex.EncodeToString(prevHash)
	r.RedactedSeq = uint64(seq)
	r.OriginalHash = hex.EncodeToString(origHash)
	r.NewHash = hex.EncodeToString(newHash)
	r.Timestamp = ts.UTC()
	return row, r, nil
}

// receiptVerifies reports whether rcpt is a receipt for the redacted row
// whose new_hash equals the row's recomputed content hash and whose
// signature verifier accepts. It fails closed.
//
// spec: §12.8 (Compliance Interfaces)
func receiptVerifies(verifier audit.ReceiptVerifier, row audit.Row, rcpt audit.RedactionReceipt) bool {
	if verifier == nil || rcpt.ReceiptID == "" || rcpt.TenantID != row.TenantID || rcpt.AuditEventID != row.ID {
		return false
	}
	if rcpt.NewHash != audit.ComputeHash(row) {
		return false
	}
	return verifier.VerifyReceipt(rcpt) == nil
}
